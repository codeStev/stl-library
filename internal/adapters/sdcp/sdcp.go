// Package sdcp controls ELEGOO resin printers over the network with their
// SDCP V3 protocol (Saturn 4 Ultra and similar): UDP discovery (port
// 3000), a WebSocket control channel and a chunked HTTP upload (port 3030).
package sdcp

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/codeStev/stl-library/internal/app"
	"github.com/codeStev/stl-library/internal/core/printer"
)

// Printer is one SDCP printer.
type Printer struct {
	Host          string // IP or host name
	ControlPort   int    // 3030 when 0
	DiscoveryPort int    // 3000 when 0
	Timeout       time.Duration

	// Upload experiments (see `stlib printer send --trace`). The zero
	// values are what the protocol specifies.
	UploadChunk   int  // bytes per chunk; ChunkSize when 0
	UploadNoCheck bool // send Check=0 (no MD5 verification by the printer)
	// UploadMaxChunks stops after that many chunks (0 = all), for timing
	// experiments; the partial file is removed.
	UploadMaxChunks int
	// UploadTrace, if set, receives the timing of every chunk. On Linux
	// the kernel tells when the printer has acknowledged all of a chunk,
	// so Send measures the network and Wait the printer.
	UploadTrace func(ChunkTiming)

	mu          sync.Mutex
	id          string
	mainboardID string
	name        string
	firmware    string
}

var _ app.Printer = (*Printer)(nil)

// ChunkSize of uploads, as the protocol specifies. The printer wants
// strictly sequential chunks.
const ChunkSize = 1 << 20

// ErrStoppedEarly: the upload stopped after UploadMaxChunks, on purpose.
var ErrStoppedEarly = errors.New("upload stopped early (--max-chunks)")

// ChunkTiming is where the time of one uploaded chunk went.
type ChunkTiming struct {
	Offset, Bytes int64
	Connect       time.Duration // TCP connection set up
	Send          time.Duration // until the printer acknowledged the whole request (Linux; elsewhere: until written)
	Wait          time.Duration // from then until the printer's answer starts
	RTT           time.Duration // the connection's smoothed round-trip time (Linux)
	TCP           TCPStats      // what the kernel saw while sending (Linux)
	Transfer      string        // the transfer's Uuid
}

// TCPStats of one chunk's connection, from the kernel (Linux).
type TCPStats struct {
	Busy, RwndLimited, SndbufLimited time.Duration // time sending; of it, limited by the printer's receive window / our buffer
	MinWindow, MaxWindow             uint32        // the receive window the printer advertised (bytes)
	MSS                              uint32
	Retransmits                      uint32
}

func (p *Printer) ports() (int, int) {
	c, d := p.ControlPort, p.DiscoveryPort
	if c == 0 {
		c = 3030
	}
	if d == 0 {
		d = 3000
	}
	return c, d
}

func (p *Printer) timeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return 10 * time.Second
}

// discover asks the printer for its mainboard ID (needed in every
// command) with the SDCP discovery message, sent straight to its address.
func (p *Printer) discover(ctx context.Context) (string, string, error) {
	p.mu.Lock()
	if p.mainboardID != "" {
		defer p.mu.Unlock()
		return p.id, p.mainboardID, nil
	}
	p.mu.Unlock()
	_, dport := p.ports()
	conn, err := net.Dial("udp", net.JoinHostPort(p.Host, strconv.Itoa(dport)))
	if err != nil {
		return "", "", err
	}
	defer conn.Close()
	deadline := time.Now().Add(p.timeout())
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline)
	buf := make([]byte, 4096)
	for attempt := 0; attempt < 3; attempt++ { // UDP: ask again if lost
		if _, err := conn.Write([]byte("M99999")); err != nil {
			return "", "", err
		}
		conn.SetReadDeadline(time.Now().Add(p.timeout() / 3))
		n, err := conn.Read(buf)
		if err != nil {
			continue
		}
		var reply struct {
			Id   string `json:"Id"`
			Data struct {
				Name            string `json:"Name"`
				MachineName     string `json:"MachineName"`
				MainboardID     string `json:"MainboardID"`
				FirmwareVersion string `json:"FirmwareVersion"`
				Attributes      *struct {
					MainboardID     string `json:"MainboardID"`
					MachineName     string `json:"MachineName"`
					FirmwareVersion string `json:"FirmwareVersion"`
				} `json:"Attributes"`
			} `json:"Data"`
		}
		if json.Unmarshal(buf[:n], &reply) != nil {
			continue
		}
		d := reply.Data
		mb, machine, fw := d.MainboardID, d.MachineName, d.FirmwareVersion
		if a := d.Attributes; a != nil && mb == "" { // older firmware nests them
			mb, machine, fw = a.MainboardID, a.MachineName, a.FirmwareVersion
		}
		if mb == "" {
			continue
		}
		p.mu.Lock()
		p.id, p.mainboardID, p.name, p.firmware = reply.Id, mb, machine, fw
		p.mu.Unlock()
		return reply.Id, mb, nil
	}
	return "", "", fmt.Errorf("printer at %s did not answer discovery", p.Host)
}

type message struct {
	Topic string `json:"Topic"`
	Data  struct {
		Cmd       int             `json:"Cmd"`
		Data      json.RawMessage `json:"Data"`
		RequestID string          `json:"RequestID"`
	} `json:"Data"`
	Status json.RawMessage `json:"Status"`
}

// command sends one request over a fresh WebSocket connection and waits
// for its acknowledgement; with wantStatus also for the status message
// that follows.
func (p *Printer) command(ctx context.Context, cmd int, data map[string]any, wantStatus bool) (json.RawMessage, json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout())
	defer cancel()
	id, mb, err := p.discover(ctx)
	if err != nil {
		return nil, nil, err
	}
	cport, _ := p.ports()
	c, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://%s/websocket", net.JoinHostPort(p.Host, strconv.Itoa(cport))), nil)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to the printer: %w", err)
	}
	defer c.CloseNow()
	c.SetReadLimit(4 << 20)
	if data == nil {
		data = map[string]any{}
	}
	reqID := randomHex(16)
	req, _ := json.Marshal(map[string]any{
		"Id":    id,
		"Topic": "sdcp/request/" + mb,
		"Data": map[string]any{"Cmd": cmd, "Data": data, "RequestID": reqID, "MainboardID": mb,
			"TimeStamp": time.Now().Unix(), "From": 0},
	})
	if err := c.Write(ctx, websocket.MessageText, req); err != nil {
		return nil, nil, err
	}
	var ack, status json.RawMessage
	for ack == nil || (wantStatus && status == nil) {
		_, msg, err := c.Read(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("waiting for the printer: %w", err)
		}
		var m message
		if json.Unmarshal(msg, &m) != nil {
			continue
		}
		switch {
		case m.Topic == "sdcp/response/"+mb && m.Data.RequestID == reqID:
			ack = m.Data.Data
		case m.Topic == "sdcp/status/"+mb && wantStatus:
			status = m.Status
		}
	}
	c.Close(websocket.StatusNormalClosure, "")
	return ack, status, nil
}

// Status reads the printer's state. An unreachable printer is reported as
// offline, not as an error.
func (p *Printer) Status(ctx context.Context) (printer.Status, error) {
	_, raw, err := p.command(ctx, 0, nil, true)
	p.mu.Lock()
	st := printer.Status{Name: p.name, Firmware: p.firmware, Machine: printer.Offline}
	p.mu.Unlock()
	if err != nil {
		return st, nil
	}
	var s struct {
		CurrentStatus []int   `json:"CurrentStatus"`
		TempOfUVLED   float64 `json:"TempOfUVLED"`
		PrintInfo     *struct {
			Status       int    `json:"Status"`
			CurrentLayer int    `json:"CurrentLayer"`
			TotalLayer   int    `json:"TotalLayer"`
			CurrentTicks int64  `json:"CurrentTicks"`
			TotalTicks   int64  `json:"TotalTicks"`
			ErrorNumber  int    `json:"ErrorNumber"`
			Filename     string `json:"Filename"`
		} `json:"PrintInfo"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return st, fmt.Errorf("reading the printer's status: %w", err)
	}
	st.UVTemp = s.TempOfUVLED
	st.Machine = machine(s.CurrentStatus)
	if pi := s.PrintInfo; pi != nil && (pi.Filename != "" || pi.Status != 0) {
		st.Job = &printer.Job{File: pi.Filename, State: jobState(pi.Status), Layer: pi.CurrentLayer, Layers: pi.TotalLayer,
			ElapsedMs: pi.CurrentTicks, TotalMs: pi.TotalTicks, Error: taskError(pi.ErrorNumber)}
	}
	return st, nil
}

func machine(codes []int) printer.Machine {
	for _, c := range codes {
		switch c {
		case 1:
			return printer.Printing
		case 2:
			return printer.Transferring
		case 3, 4:
			return printer.Testing
		}
	}
	return printer.Idle
}

func jobState(code int) printer.JobState {
	switch code {
	case 1, 2, 10:
		return printer.JobPreparing
	case 3, 4:
		return printer.JobPrinting
	case 5:
		return printer.JobPausing
	case 6:
		return printer.JobPaused
	case 7:
		return printer.JobStopping
	case 8:
		return printer.JobStopped
	case 9:
		return printer.JobComplete
	}
	return printer.JobIdle
}

// taskError: the printer's error numbers (SDCP_PRINT_TASKERROR), the
// ones worth telling apart; others get their number.
func taskError(n int) string {
	texts := map[int]string{
		1: "over-temperature", 2: "strain gauge calibration failed", 3: "resin level low", 4: "model needs more resin than the vat holds",
		5: "no resin detected", 6: "foreign object detected", 7: "auto-leveling failed", 8: "model detachment detected",
		12: "USB drive removed", 17: "home position calibration failed", 18: "a model is on the platform - clean it and restart",
		19: "printing exception", 20: "motor movement abnormal", 21: "no model detected", 22: "model warping detected", 24: "file error",
	}
	if n == 0 {
		return ""
	}
	if t, ok := texts[n]; ok {
		return t
	}
	return fmt.Sprintf("printer error %d", n)
}

// startAcks: answers to the start command.
var startAcks = map[int]string{1: "the printer is busy", 2: "file not found on the printer", 3: "file checksum failed",
	4: "file could not be read", 5: "the file was sliced for a different resolution", 6: "unknown file format",
	7: "the file was sliced for a different printer model"}

func ackError(what string, raw json.RawMessage) error {
	var a struct {
		Ack int `json:"Ack"`
	}
	json.Unmarshal(raw, &a)
	if a.Ack == 0 {
		return nil
	}
	if t, ok := startAcks[a.Ack]; ok && what == "start" {
		return fmt.Errorf("the printer refused to start: %s", t)
	}
	return fmt.Errorf("the printer refused to %s (code %d)", what, a.Ack)
}

// Start prints a file from the printer's internal storage.
func (p *Printer) Start(ctx context.Context, name string) error {
	ack, _, err := p.command(ctx, 128, map[string]any{"Filename": printer.RemotePath(name), "StartLayer": 0}, false)
	if err != nil {
		return err
	}
	return ackError("start", ack)
}

// Files lists a folder of the printer's storage.
func (p *Printer) Files(ctx context.Context, dir string) ([]printer.File, error) {
	if dir == "" {
		dir = "/local"
	}
	ack, _, err := p.command(ctx, 258, map[string]any{"Url": dir}, false)
	if err != nil {
		return nil, err
	}
	if err := ackError("list "+dir, ack); err != nil {
		return nil, err
	}
	var d struct {
		FileList []struct {
			Name      string `json:"name"`
			Type      int    `json:"type"`
			Size      int64  `json:"size"`
			UsedSize  int64  `json:"usedSize"`
			TotalSize int64  `json:"totalSize"`
		} `json:"FileList"`
	}
	if err := json.Unmarshal(ack, &d); err != nil {
		return nil, fmt.Errorf("reading the file list: %w", err)
	}
	out := make([]printer.File, 0, len(d.FileList))
	for _, f := range d.FileList {
		out = append(out, printer.File{Path: f.Name, Folder: f.Type == 0, Size: f.Size, Used: f.UsedSize, Total: f.TotalSize})
	}
	return out, nil
}

// Delete removes files from the printer's storage (bare names: internal
// storage).
func (p *Printer) Delete(ctx context.Context, paths []string) error {
	full := make([]string, 0, len(paths))
	for _, n := range paths {
		full = append(full, printer.RemotePath(n))
	}
	ack, _, err := p.command(ctx, 259, map[string]any{"FileList": full, "FolderList": []string{}}, false)
	if err != nil {
		return err
	}
	return ackError("delete", ack)
}

func (p *Printer) simple(ctx context.Context, cmd int, what string) error {
	ack, _, err := p.command(ctx, cmd, nil, false)
	if err != nil {
		return err
	}
	return ackError(what, ack)
}

func (p *Printer) Pause(ctx context.Context) error  { return p.simple(ctx, 129, "pause") }
func (p *Printer) Stop(ctx context.Context) error   { return p.simple(ctx, 130, "stop") }
func (p *Printer) Resume(ctx context.Context) error { return p.simple(ctx, 131, "resume") }

// Upload sends a file in sequential 1 MiB chunks with its MD5 (read in a
// first pass). The printer answers with broken HTTP responses (announcing
// more bytes than it sends), so a short body is accepted.
func (p *Printer) Upload(ctx context.Context, name string, size int64, open func() (io.ReadCloser, error), progress func(int64)) error {
	sum, err := md5Of(open)
	if err != nil {
		return err
	}
	r, err := open()
	if err != nil {
		return err
	}
	defer r.Close()
	cport, _ := p.ports()
	url := fmt.Sprintf("http://%s/uploadFile/upload", net.JoinHostPort(p.Host, strconv.Itoa(cport)))
	uuid := randomHex(32)
	transport := &http.Transport{DisableKeepAlives: true}
	var conn func() net.Conn // the current chunk's connection, when tracing
	if p.UploadTrace != nil {
		var mu sync.Mutex
		var last net.Conn
		d := &net.Dialer{Timeout: 30 * time.Second}
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := d.DialContext(ctx, network, addr)
			mu.Lock()
			last = c
			mu.Unlock()
			return c, err
		}
		conn = func() net.Conn { mu.Lock(); defer mu.Unlock(); return last }
	}
	client := &http.Client{Timeout: 5 * time.Minute, Transport: transport}
	chunk := ChunkSize
	if p.UploadChunk > 0 {
		chunk = p.UploadChunk
	}
	check := "1"
	if p.UploadNoCheck {
		check = "0"
	}
	buf := make([]byte, chunk)
	var offset int64
	err = p.sendChunks(ctx, client, conn, url, name, uuid, sum, check, r, buf, size, &offset, progress)
	if err != nil && offset > 0 {
		// The printer keeps an interrupted transfer as "<id>_<name>";
		// remove ours (and only ours). Also after a cancel, so without ctx.
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		if derr := p.Delete(dctx, []string{PartialName(uuid, name)}); derr != nil {
			err = fmt.Errorf("%w (and the partial file %s could not be removed: %v)", err, PartialName(uuid, name), derr)
		}
	}
	return err
}

// PartialName is what the printer calls an interrupted transfer's file.
func PartialName(uuid, name string) string { return uuid[:32] + "_" + name }

func (p *Printer) sendChunks(ctx context.Context, client *http.Client, conn func() net.Conn, url, name, uuid, sum, check string,
	r io.Reader, buf []byte, size int64, offsetp *int64, progress func(int64)) error {
	chunk := len(buf)
	offset := *offsetp
	defer func() { *offsetp = offset }()
	for offset < size || offset == 0 {
		n, err := io.ReadFull(r, buf)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !(errors.Is(err, io.EOF) && size == 0) {
			return fmt.Errorf("reading %s: %w", name, err)
		}
		ok, msg, t, err := postChunk(ctx, client, conn, url, name, uuid, sum, check, offset, size, buf[:n])
		if p.UploadTrace != nil && err == nil {
			t.Offset, t.Bytes, t.Transfer = offset, int64(n), uuid
			p.UploadTrace(t)
		}
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("the printer rejected the upload: %s", msg)
		}
		offset += int64(n)
		progress(offset)
		if p.UploadMaxChunks > 0 && offset >= int64(p.UploadMaxChunks)*int64(chunk) {
			return ErrStoppedEarly
		}
		if n == 0 {
			break
		}
	}
	return nil
}

func postChunk(ctx context.Context, client *http.Client, conn func() net.Conn, url, name, uuid, sum, check string, offset, total int64, data []byte) (bool, string, ChunkTiming, error) {
	var t ChunkTiming
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, f := range [][2]string{{"Uuid", uuid}, {"Offset", strconv.FormatInt(offset, 10)},
		{"TotalSize", strconv.FormatInt(total, 10)}, {"Check", check}, {"S-File-MD5", sum}} {
		mw.WriteField(f[0], f[1])
	}
	fw, _ := mw.CreateFormFile("File", name)
	fw.Write(data)
	mw.Close()
	// The hooks run on the transport's goroutines.
	var mu sync.Mutex
	var start, connected, wrote, acked, answered time.Time
	var rtt time.Duration
	var tcp TCPStats
	at := func(p *time.Time) { mu.Lock(); *p = time.Now(); mu.Unlock() }
	ackDone := make(chan struct{})
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		ConnectStart: func(string, string) { at(&start) },
		ConnectDone:  func(string, string, error) { at(&connected) },
		WroteRequest: func(httptrace.WroteRequestInfo) {
			at(&wrote)
			go func() { // written into the kernel; now wait for the printer's ACKs
				defer close(ackDone)
				if conn == nil {
					return
				}
				if c := conn(); c != nil {
					if r, st, ok := waitAcked(ctx, c); ok {
						at(&acked)
						mu.Lock()
						rtt, tcp = r, st
						mu.Unlock()
					}
				}
			}()
		},
		GotFirstResponseByte: func() { at(&answered) },
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return false, "", t, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		return false, "", t, fmt.Errorf("uploading to the printer: %w", err)
	}
	if conn != nil {
		select { // the connection closes after the answer; the ACKs came before it
		case <-ackDone:
		case <-time.After(time.Second):
		}
	}
	mu.Lock()
	sent := wrote
	if !acked.IsZero() && acked.Before(answered) {
		sent = acked
	}
	t.Connect, t.Send, t.Wait, t.RTT, t.TCP = connected.Sub(start), sent.Sub(connected), answered.Sub(sent), rtt, tcp
	mu.Unlock()
	t.Wait = max(t.Wait, 0) // an early answer, before the body was written
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, "", t, fmt.Errorf("reading the printer's answer: %w", err)
	}
	var r struct {
		Success  bool `json:"success"`
		Messages []struct {
			Message string `json:"message"`
		} `json:"messages"`
	}
	if json.Unmarshal(b, &r) != nil {
		return false, "", t, fmt.Errorf("unexpected answer from the printer (HTTP %d): %.200s", resp.StatusCode, b)
	}
	var msgs []string
	for _, m := range r.Messages {
		msgs = append(msgs, m.Message)
	}
	if resp.StatusCode >= 400 && r.Success {
		r.Success = false
	}
	return r.Success, strings.Join(msgs, "; "), t, nil
}

func md5Of(open func() (io.ReadCloser, error)) (string, error) {
	r, err := open()
	if err != nil {
		return "", err
	}
	defer r.Close()
	h := md5.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
