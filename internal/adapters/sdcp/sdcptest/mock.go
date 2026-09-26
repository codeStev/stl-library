// Package sdcptest is a mock ELEGOO SDCP V3 printer (Saturn 4 Ultra
// style) for tests and local development: UDP discovery, the WebSocket
// control channel, the chunked HTTP upload, and a simulated print that
// advances layer by layer. It follows the real printer's quirks where
// they matter: uploads must arrive strictly in order (HTTP 500 "offset not
// match" otherwise), the file's MD5 is checked on the last chunk, and
// upload responses announce more bytes than they send.
package sdcptest

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Printer states as the protocol numbers them.
const (
	machineIdle         = 0
	machinePrinting     = 1
	machineTransferring = 2

	printIdle     = 0
	printHoming   = 1
	printExposing = 3
	printPaused   = 6
	printStopped  = 8
	printComplete = 9
)

// Mock is a simulated printer.
type Mock struct {
	MainboardID string
	Name        string // "3D Printer"
	MachineName string // "Saturn 4 Ultra"
	Firmware    string
	LayerTime   time.Duration // how long a simulated layer takes
	Layers      int           // layers of every simulated print
	// Quirky sends upload responses with a Content-Length larger than the
	// body, as the real printer does.
	Quirky bool

	mu        sync.Mutex
	files     map[string][]byte // "/local/<name>" -> content
	upload    *upload
	machine   int
	print     printInfo
	startedAt time.Time
	paused    time.Duration // accumulated pause time of the current print
	pausedAt  time.Time
	log       []string // commands received, for tests

	http *http.Server
	udp  net.PacketConn
}

type upload struct {
	uuid, name, md5 string
	total, offset   int64
	data            []byte
}

type printInfo struct {
	Status       int
	CurrentLayer int
	TotalLayer   int
	CurrentTicks int64
	TotalTicks   int64
	ErrorNumber  int
	Filename     string
	TaskId       string
}

// New returns a mock with sensible defaults (fast layers).
func New() *Mock {
	return &Mock{
		MainboardID: "mock0000000000000000000000000001",
		Name:        "3D Printer",
		MachineName: "Saturn 4 Ultra (mock)",
		Firmware:    "V1.4.8",
		LayerTime:   50 * time.Millisecond,
		Layers:      40,
		Quirky:      true,
		files:       map[string][]byte{},
	}
}

// Listen serves the control channel and uploads on httpAddr (the real
// printer uses port 3030) and discovery on udpAddr (port 3000). Use
// "127.0.0.1:0" for free ports; the bound addresses are returned.
func (m *Mock) Listen(httpAddr, udpAddr string) (httpBound, udpBound string, err error) {
	ln, err := net.Listen("tcp", httpAddr)
	if err != nil {
		return "", "", err
	}
	m.udp, err = net.ListenPacket("udp", udpAddr)
	if err != nil {
		ln.Close()
		return "", "", err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/websocket", m.serveWS)
	mux.HandleFunc("/uploadFile/upload", m.serveUpload)
	m.http = &http.Server{Handler: mux}
	go m.http.Serve(ln)
	go m.serveDiscovery()
	return ln.Addr().String(), m.udp.LocalAddr().String(), nil
}

// Close stops the mock.
func (m *Mock) Close() {
	if m.http != nil {
		m.http.Close()
	}
	if m.udp != nil {
		m.udp.Close()
	}
}

// Files lists stored files with their sizes.
func (m *Mock) Files() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]int{}
	for k, v := range m.files {
		out[k] = len(v)
	}
	return out
}

// AddFile puts a file on the simulated storage ("/local/a.ctb").
func (m *Mock) AddFile(name string, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[name] = data
}

// Log returns the commands received ("start /local/a.ctb", "pause", …).
func (m *Mock) Log() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.log...)
}

// ---- discovery ----

func (m *Mock) serveDiscovery() {
	buf := make([]byte, 1500)
	for {
		n, from, err := m.udp.ReadFrom(buf)
		if err != nil {
			return
		}
		if strings.TrimSpace(string(buf[:n])) != "M99999" {
			continue
		}
		reply, _ := json.Marshal(map[string]any{
			"Id": "mock-id",
			"Data": map[string]any{
				"Name": m.Name, "MachineName": m.MachineName, "BrandName": "ELEGOO",
				"MainboardIP": "127.0.0.1", "MainboardID": m.MainboardID,
				"ProtocolVersion": "V3.0.0", "FirmwareVersion": m.Firmware,
			},
		})
		m.udp.WriteTo(reply, from)
	}
}

// ---- control channel ----

type request struct {
	Id   string `json:"Id"`
	Data struct {
		Cmd         int             `json:"Cmd"`
		Data        json.RawMessage `json:"Data"`
		RequestID   string          `json:"RequestID"`
		MainboardID string          `json:"MainboardID"`
	} `json:"Data"`
	Topic string `json:"Topic"`
}

func (m *Mock) serveWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx := r.Context()
	for {
		_, msg, err := c.Read(ctx)
		if err != nil {
			return
		}
		var req request
		if json.Unmarshal(msg, &req) != nil || req.Data.MainboardID != m.MainboardID {
			continue // the real printer ignores requests for other boards
		}
		for _, out := range m.handle(req) {
			b, _ := json.Marshal(out)
			if err := c.Write(ctx, websocket.MessageText, b); err != nil {
				return
			}
		}
	}
}

// handle answers a request: an acknowledgement, plus a status or
// attributes message for the queries.
func (m *Mock) handle(req request) []any {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().Unix()
	ack := func(data map[string]any) any {
		return map[string]any{
			"Id": req.Id, "Topic": "sdcp/response/" + m.MainboardID,
			"Data": map[string]any{"Cmd": req.Data.Cmd, "Data": data, "RequestID": req.Data.RequestID,
				"MainboardID": m.MainboardID, "TimeStamp": now},
		}
	}
	switch req.Data.Cmd {
	case 0:
		return []any{ack(map[string]any{"Ack": 0}), m.statusMessage()}
	case 1:
		return []any{ack(map[string]any{"Ack": 0}), map[string]any{
			"Topic": "sdcp/attributes/" + m.MainboardID, "MainboardID": m.MainboardID, "TimeStamp": now,
			"Attributes": map[string]any{"Name": m.Name, "MachineName": m.MachineName, "FirmwareVersion": m.Firmware,
				"ProtocolVersion": "V3.0.0", "Resolution": "11520x5120", "MainboardID": m.MainboardID},
		}}
	case 128:
		var d struct {
			Filename   string `json:"Filename"`
			StartLayer int    `json:"StartLayer"`
		}
		json.Unmarshal(req.Data.Data, &d)
		m.log = append(m.log, "start "+d.Filename)
		name := d.Filename
		if !strings.HasPrefix(name, "/") {
			name = "/local/" + name
		}
		switch {
		case m.machine != machineIdle:
			return []any{ack(map[string]any{"Ack": 1})} // busy
		case m.files[name] == nil:
			return []any{ack(map[string]any{"Ack": 2})} // file not found
		}
		m.machine, m.startedAt, m.paused = machinePrinting, time.Now(), 0
		m.print = printInfo{Status: printHoming, TotalLayer: m.Layers, Filename: path.Base(name),
			TotalTicks: int64(m.Layers) * m.LayerTime.Milliseconds(), TaskId: fmt.Sprintf("task-%d", time.Now().UnixNano())}
		return []any{ack(map[string]any{"Ack": 0})}
	case 129:
		m.log = append(m.log, "pause")
		if m.machine == machinePrinting && m.print.Status != printPaused {
			m.advance()
			m.print.Status, m.pausedAt = printPaused, time.Now()
		}
		return []any{ack(map[string]any{"Ack": 0})}
	case 131:
		m.log = append(m.log, "resume")
		if m.print.Status == printPaused {
			m.paused += time.Since(m.pausedAt)
			m.print.Status = printExposing
		}
		return []any{ack(map[string]any{"Ack": 0})}
	case 130:
		m.log = append(m.log, "stop")
		if m.machine == machinePrinting {
			m.advance()
			m.print.Status, m.machine = printStopped, machineIdle
		}
		return []any{ack(map[string]any{"Ack": 0})}
	case 259:
		var d struct {
			FileList []string `json:"FileList"`
		}
		json.Unmarshal(req.Data.Data, &d)
		for _, f := range d.FileList {
			m.log = append(m.log, "delete "+f)
			delete(m.files, f)
		}
		return []any{ack(map[string]any{"Ack": 0})}
	case 258:
		var d struct {
			Url string `json:"Url"`
		}
		json.Unmarshal(req.Data.Data, &d)
		list := []map[string]any{}
		if d.Url == "/" {
			used := 0
			for _, data := range m.files {
				used += len(data)
			}
			list = append(list, map[string]any{"name": "/local", "type": 0, "usedSize": used, "totalSize": 8 << 30})
		}
		for name, data := range m.files {
			if path.Dir(name) == strings.TrimSuffix(d.Url, "/") {
				list = append(list, map[string]any{"name": name, "type": 1, "size": len(data)})
			}
		}
		return []any{ack(map[string]any{"Ack": 0, "FileList": list})}
	}
	return []any{ack(map[string]any{"Ack": 0})}
}

// advance moves the simulated print on to "now".
func (m *Mock) advance() {
	if m.machine != machinePrinting || m.print.Status == printPaused {
		return
	}
	elapsed := time.Since(m.startedAt) - m.paused
	layer := int(elapsed / m.LayerTime)
	if layer >= m.print.TotalLayer {
		m.print.CurrentLayer, m.print.Status, m.machine = m.print.TotalLayer, printComplete, machineIdle
		m.print.CurrentTicks = m.print.TotalTicks
		return
	}
	m.print.CurrentLayer = layer
	m.print.CurrentTicks = elapsed.Milliseconds()
	if layer > 0 {
		m.print.Status = printExposing
	}
}

func (m *Mock) statusMessage() any {
	m.advance()
	machine := m.machine
	transfer := map[string]any{"Status": 0}
	if m.upload != nil {
		machine = machineTransferring
		transfer = map[string]any{"Status": 0, "DownloadOffset": m.upload.offset, "FileTotalSize": m.upload.total, "Filename": m.upload.name}
	}
	return map[string]any{
		"Topic": "sdcp/status/" + m.MainboardID, "MainboardID": m.MainboardID, "TimeStamp": time.Now().Unix(),
		"Status": map[string]any{
			"CurrentStatus":    []int{machine},
			"PrintInfo":        m.print,
			"FileTransferInfo": transfer,
			"TempOfUVLED":      26.1,
			"ReleaseFilm":      1234,
		},
	}
}

// ---- upload ----

func (m *Mock) serveUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		m.reply(w, 400, false, "bad form")
		return
	}
	f, hdr, err := r.FormFile("File")
	if err != nil {
		m.reply(w, 400, false, "no file")
		return
	}
	chunk, _ := io.ReadAll(f)
	f.Close()
	offset, _ := strconv.ParseInt(r.FormValue("Offset"), 10, 64)
	total, _ := strconv.ParseInt(r.FormValue("TotalSize"), 10, 64)
	uuid := r.FormValue("Uuid")

	m.mu.Lock()
	defer m.mu.Unlock()
	if offset == 0 {
		if m.machine != machineIdle {
			m.reply(w, 500, false, "printer busy")
			return
		}
		m.upload = &upload{uuid: uuid, name: hdr.Filename, md5: r.FormValue("S-File-MD5"), total: total}
	}
	u := m.upload
	if u == nil || u.uuid != uuid || offset != u.offset {
		m.reply(w, 500, false, "offset not match")
		return
	}
	u.data = append(u.data, chunk...)
	u.offset += int64(len(chunk))
	if u.offset < u.total {
		m.reply(w, 200, true, "")
		return
	}
	m.upload = nil
	sum := md5.Sum(u.data)
	if hex.EncodeToString(sum[:]) != u.md5 || u.offset != u.total {
		m.reply(w, 200, false, "md5 check failed")
		return
	}
	m.files["/local/"+u.name] = u.data
	m.reply(w, 200, true, "")
}

// reply writes the printer's JSON answer; with Quirky, the announced
// Content-Length is larger than the body (the real printer's habit).
func (m *Mock) reply(w http.ResponseWriter, status int, success bool, message string) {
	msgs := "null"
	if message != "" {
		msgs = fmt.Sprintf(`[{"field":"common_field","message":%q}]`, message)
	}
	body := fmt.Sprintf(`{"code":"000000","messages":%s,"data":null,"success":%v}`, msgs, success)
	if !m.Quirky {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		log.Print("sdcptest: cannot hijack connection")
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	fmt.Fprintf(buf, "HTTP/1.1 %d %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		status, http.StatusText(status), len(body)+16, body)
	buf.Flush()
}

// Run serves until ctx is done (for the standalone mock).
func (m *Mock) Run(ctx context.Context, httpAddr, udpAddr string) error {
	h, u, err := m.Listen(httpAddr, udpAddr)
	if err != nil {
		return err
	}
	log.Printf("mock SDCP printer %q: control+upload on %s, discovery on %s", m.MachineName, h, u)
	<-ctx.Done()
	m.Close()
	return nil
}
