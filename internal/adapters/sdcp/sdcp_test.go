package sdcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/codeStev/stl-library/internal/adapters/sdcp/sdcptest"
	"github.com/codeStev/stl-library/internal/core/printer"
)

// mockPrinter starts a mock printer on free local ports. Tests never talk
// to a real printer.
func mockPrinter(t *testing.T) (*sdcptest.Mock, *Printer) {
	t.Helper()
	m := sdcptest.New()
	h, u, err := m.Listen("127.0.0.1:0", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	port := func(addr string) int {
		_, p, _ := net.SplitHostPort(addr)
		n, _ := strconv.Atoi(p)
		return n
	}
	return m, &Printer{Host: "127.0.0.1", ControlPort: port(h), DiscoveryPort: port(u), Timeout: 3 * time.Second}
}

func opener(data []byte) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }
}

func TestStatusOfAnIdlePrinter(t *testing.T) {
	_, p := mockPrinter(t)
	st, err := p.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Machine != printer.Idle || st.Name != "Saturn 4 Ultra (mock)" || st.Firmware != "V1.4.8" || st.Job != nil {
		t.Errorf("status %+v", st)
	}
}

func TestUploadInChunksWithTheQuirkyResponses(t *testing.T) {
	m, p := mockPrinter(t)
	data := make([]byte, 2*ChunkSize+12345) // three chunks
	rand.Read(data)
	var last int64
	if err := p.Upload(context.Background(), "panther.ctb", int64(len(data)), opener(data), func(n int64) { last = n }); err != nil {
		t.Fatal(err)
	}
	if got := m.Files()["/local/panther.ctb"]; got != len(data) || last != int64(len(data)) {
		t.Errorf("stored %d bytes, progress %d, want %d", got, last, len(data))
	}
}

func TestAChangedFileFailsTheChecksum(t *testing.T) {
	m, p := mockPrinter(t)
	data := make([]byte, ChunkSize+10)
	first := true
	// The first pass (checksum) and the second (upload) see different data.
	open := func() (io.ReadCloser, error) {
		d := append([]byte(nil), data...)
		if !first {
			d[len(d)-1] ^= 0xff
		}
		first = false
		return io.NopCloser(bytes.NewReader(d)), nil
	}
	err := p.Upload(context.Background(), "x.ctb", int64(len(data)), open, func(int64) {})
	if err == nil || !strings.Contains(err.Error(), "md5") {
		t.Errorf("err = %v", err)
	}
	if len(m.Files()) != 0 {
		t.Errorf("stored despite a bad checksum: %v", m.Files())
	}
}

func TestPrintPauseResumeStopAndComplete(t *testing.T) {
	m, p := mockPrinter(t)
	ctx := context.Background()
	if err := p.Start(ctx, "missing.ctb"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("start of a missing file: %v", err)
	}
	if err := p.Upload(ctx, "a.ctb", 3, opener([]byte("abc")), func(int64) {}); err != nil {
		t.Fatal(err)
	}
	if err := p.Start(ctx, "a.ctb"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	st, _ := p.Status(ctx)
	if st.Machine != printer.Printing || st.Job == nil || st.Job.File != "a.ctb" || st.Job.Layer == 0 || st.Job.Layers != 40 {
		t.Fatalf("printing: %+v %+v", st, st.Job)
	}
	if err := p.Start(ctx, "a.ctb"); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Errorf("second start: %v", err)
	}
	p.Pause(ctx)
	st, _ = p.Status(ctx)
	layer := st.Job.Layer
	time.Sleep(150 * time.Millisecond)
	st, _ = p.Status(ctx)
	if st.Job.State != printer.JobPaused || st.Job.Layer != layer {
		t.Errorf("paused: %+v", st.Job)
	}
	p.Resume(ctx)
	time.Sleep(100 * time.Millisecond)
	if st, _ = p.Status(ctx); st.Job.State != printer.JobPrinting || st.Job.Layer <= layer {
		t.Errorf("resumed: %+v", st.Job)
	}
	p.Stop(ctx)
	if st, _ = p.Status(ctx); st.Job.State != printer.JobStopped || st.Machine != printer.Idle {
		t.Errorf("stopped: %+v %s", st.Job, st.Machine)
	}
	// Start again and let it finish.
	if err := p.Start(ctx, "a.ctb"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2500 * time.Millisecond)
	if st, _ = p.Status(ctx); st.Job.State != printer.JobComplete || st.Job.Layer != 40 {
		t.Errorf("complete: %+v", st.Job)
	}
	if got := strings.Join(m.Log(), ","); got != "start /local/missing.ctb,start /local/a.ctb,start /local/a.ctb,pause,resume,stop,start /local/a.ctb" {
		t.Errorf("commands: %s", got)
	}
}

func TestUnreachablePrinterIsOffline(t *testing.T) {
	p := &Printer{Host: "127.0.0.1", ControlPort: 1, DiscoveryPort: 1, Timeout: 300 * time.Millisecond}
	st, err := p.Status(context.Background())
	if err != nil || st.Machine != printer.Offline {
		t.Errorf("status %+v err %v", st, err)
	}
}

func TestListAndDeleteFiles(t *testing.T) {
	m, p := mockPrinter(t)
	ctx := context.Background()
	m.AddFile("/local/a.ctb", []byte("aaa"))
	m.AddFile("/local/b.goo", []byte("bb"))
	files, err := p.Files(ctx, "")
	if err != nil || len(files) != 2 {
		t.Fatalf("files: %+v %v", files, err)
	}
	vols, _ := p.Files(ctx, "/")
	if len(vols) != 1 || !vols[0].Folder || vols[0].Used != 5 {
		t.Errorf("volumes: %+v", vols)
	}
	if err := p.Delete(ctx, []string{"a.ctb"}); err != nil {
		t.Fatal(err)
	}
	if files, _ := p.Files(ctx, "/local"); len(files) != 1 || files[0].Path != "/local/b.goo" || files[0].Size != 2 {
		t.Errorf("after delete: %+v", files)
	}
}

func TestUploadTraceSeparatesNetworkFromPrinter(t *testing.T) {
	m, p := mockPrinter(t)
	m.ChunkDelay = 150 * time.Millisecond // a printer slow to handle chunks
	var chunks []ChunkTiming
	p.UploadTrace = func(c ChunkTiming) { chunks = append(chunks, c) }
	data := make([]byte, ChunkSize+100)
	rand.Read(data)
	if err := p.Upload(context.Background(), "trace.ctb", int64(len(data)), opener(data), func(int64) {}); err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 || chunks[0].Bytes != ChunkSize || chunks[1].Offset != ChunkSize {
		t.Fatalf("chunks: %+v", chunks)
	}
	for _, c := range chunks {
		if c.Wait < 150*time.Millisecond || c.Send > 100*time.Millisecond {
			t.Errorf("printer time counted as network: %+v", c)
		}
	}
}

func TestUploadVariants(t *testing.T) {
	m, p := mockPrinter(t)
	m.CheckDelay = time.Second // would make Check=1 slow
	p.UploadNoCheck, p.UploadChunk = true, 256<<10
	var chunks int
	p.UploadTrace = func(ChunkTiming) { chunks++ }
	data := make([]byte, 3*ChunkSize)
	rand.Read(data)
	start := time.Now()
	if err := p.Upload(context.Background(), "v.ctb", int64(len(data)), opener(data), func(int64) {}); err != nil {
		t.Fatal(err)
	}
	if chunks != 12 || time.Since(start) > time.Second || m.Files()["/local/v.ctb"] != len(data) {
		t.Errorf("%d chunks in %s, stored %d", chunks, time.Since(start), m.Files()["/local/v.ctb"])
	}
}
