package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/codeStev/stl-library/internal/adapters/sdcp"
	"github.com/codeStev/stl-library/internal/adapters/sqlite"
	"github.com/codeStev/stl-library/internal/app"
	"github.com/codeStev/stl-library/internal/core/printer"
)

const printerUsage = `usage: stlib printer [--host <addr>[:<port>]] [--db <index.db>] <command> [args]

Commands:
  status               printer state and the current/last print
  files [dir]          list the printer's storage (default /local; "/" for volumes)
  send <file> [--print]
                       upload a sliced file (.ctb/.goo), optionally start it
  send <file> --trace [--max-chunks N] [--no-check] [--chunk-kb N]
                       upload only (never prints) and show where the time
                       goes per chunk: network vs. the printer. --max-chunks
                       stops early (and removes the partial file);
                       --no-check and --chunk-kb try variants (default
                       128 KB chunks; the protocol's own size is 1024)
  print <name>         start a file already on the printer
  rm <name>...         delete files from the printer
  pause | resume | stop
  watch                show the status every few seconds

The printer is --host, else PRINTER_ADDR, else the settings saved in the
app (--db, default DATA_DIR/index.db).`

// printerSettings resolves the printer: the --host flag, PRINTER_ADDR, or
// the app's saved settings.
func printerSettings(ctx context.Context, host, db string) (app.PrinterSettings, error) {
	if host == "" {
		host = os.Getenv("PRINTER_ADDR")
	}
	if host != "" {
		return parsePrinterAddr(host), nil
	}
	if db == "" {
		db = path.Join(envOr("DATA_DIR", "./data"), "index.db")
	}
	if _, err := os.Stat(db); err == nil {
		s, err := sqlite.Open(db)
		if err != nil {
			return app.PrinterSettings{}, err
		}
		defer s.Close()
		ps, ok, err := s.PrinterSettings(ctx)
		if err != nil || ok && ps.Host != "" {
			return ps, err
		}
	}
	return app.PrinterSettings{}, errors.New("no printer given: use --host, PRINTER_ADDR, or save one in the app's settings")
}

// parsePrinterAddr: "192.168.1.50" or "host:3030" (the control port).
func parsePrinterAddr(addr string) app.PrinterSettings {
	s := app.PrinterSettings{Host: addr}
	if h, p, err := net.SplitHostPort(addr); err == nil {
		if n, err := strconv.Atoi(p); err == nil {
			s.Host, s.ControlPort = h, n
		}
	}
	if n, err := strconv.Atoi(os.Getenv("PRINTER_DISCOVERY_PORT")); err == nil {
		s.DiscoveryPort = n
	}
	return s
}

func newPrinter(s app.PrinterSettings) app.Printer {
	return &sdcp.Printer{Host: s.Host, ControlPort: s.ControlPort, DiscoveryPort: s.DiscoveryPort}
}

func runPrinter(ctx context.Context, host, db string, args []string, out io.Writer) error {
	doPrint, trace, noCheck, chunkKB, maxChunks := false, false, false, 0, 0
	var rest []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--print":
			doPrint = true
		case "--trace":
			trace = true
		case "--no-check":
			noCheck = true
		case "--max-chunks":
			if i+1 >= len(args) {
				return errors.New("--max-chunks needs a number")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 {
				return errors.New("--max-chunks must be a positive number")
			}
			maxChunks = n
		case "--chunk-kb":
			if i+1 >= len(args) {
				return errors.New("--chunk-kb needs a number")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 16 || n > 16<<10 {
				return errors.New("--chunk-kb must be between 16 and 16384")
			}
			chunkKB = n
		default:
			rest = append(rest, a)
		}
	}
	if trace && doPrint {
		return errors.New("--trace only uploads; it never starts a print (leave out --print)")
	}
	if (noCheck || chunkKB > 0 || maxChunks > 0) && !trace {
		return errors.New("--no-check, --chunk-kb and --max-chunks are experiments; use them with --trace")
	}
	if len(rest) == 0 || rest[0] == "help" {
		fmt.Fprintln(out, printerUsage)
		return nil
	}
	s, err := printerSettings(ctx, host, db)
	if err != nil {
		return err
	}
	p := newPrinter(s)
	cmd, args := rest[0], rest[1:]
	switch cmd {
	case "status":
		st, err := p.Status(ctx)
		if err != nil {
			return err
		}
		printStatus(out, s.Host, st)
	case "watch":
		for {
			st, err := p.Status(ctx)
			if err != nil {
				return err
			}
			printStatus(out, s.Host, st)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(3 * time.Second):
			}
		}
	case "files", "ls":
		dir := "/local"
		if len(args) > 0 {
			dir = args[0]
		}
		files, err := p.Files(ctx, dir)
		if err != nil {
			return err
		}
		sort.Slice(files, func(i, j int) bool {
			if files[i].Folder != files[j].Folder {
				return files[i].Folder
			}
			return strings.ToLower(files[i].Path) < strings.ToLower(files[j].Path)
		})
		fmt.Fprintf(out, "%s  (%d entries)  @ %s\n", dir, len(files), s.Host)
		for _, f := range files {
			if f.Folder {
				fmt.Fprintf(out, "  %s/  (%s / %s used)\n", path.Base(f.Path), size(f.Used), size(f.Total))
			} else {
				if f.Size > 0 {
					fmt.Fprintf(out, "  %s  (%s)\n", path.Base(f.Path), size(f.Size))
				} else { // the printer doesn't report file sizes
					fmt.Fprintf(out, "  %s\n", path.Base(f.Path))
				}
			}
		}
	case "send":
		if len(args) != 1 {
			return errors.New("usage: stlib printer send <file> [--print]")
		}
		file := args[0]
		if !printer.Printable(file) {
			return fmt.Errorf("%s is not a sliced printer file (.ctb or .goo)", path.Base(file))
		}
		info, err := os.Stat(file)
		if err != nil {
			return err
		}
		open := func() (io.ReadCloser, error) { return os.Open(file) }
		var tr *uploadTrace
		if trace {
			sp := p.(*sdcp.Printer)
			sp.UploadNoCheck, sp.UploadChunk, sp.UploadMaxChunks = noCheck, chunkKB<<10, maxChunks
			tr = &uploadTrace{out: out}
			sp.UploadTrace = tr.chunk
			fmt.Fprintf(out, "tracing the upload of %s (%s), chunk %s, Check=%v - no print is started\n",
				path.Base(file), size(info.Size()), size(int64(cmp.Or(chunkKB<<10, sdcp.ChunkSize))), !noCheck)
			fmt.Fprintln(out, "  chunk  offset      connect    send     wait    network     rtt   rwnd-limited  window(min-max)  mss   retrans")
		}
		start := time.Now()
		last := -1
		err = p.Upload(ctx, path.Base(file), info.Size(), open, func(sent int64) {
			if trace {
				return
			}
			if pct := int(sent * 100 / max(info.Size(), 1)); pct != last {
				last = pct
				fmt.Fprintf(out, "\r  uploading %s: %3d%%", path.Base(file), pct)
			}
		})
		fmt.Fprintln(out)
		if errors.Is(err, sdcp.ErrStoppedEarly) {
			fmt.Fprintf(out, "stopped after %d chunks, as asked; removed the partial file from the printer\n", maxChunks)
			tr.summary(time.Since(start))
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "uploaded in %s; waiting for the printer to check the file ...\n", time.Since(start).Round(time.Second))
		upload := time.Since(start)
		checkStart := time.Now()
		if err := app.WaitForFile(ctx, p, path.Base(file), app.FinalizeTimeout(info.Size()), 0); err != nil {
			return err
		}
		fmt.Fprintf(out, "the printer accepted %s after %s\n", path.Base(file), time.Since(checkStart).Round(time.Second))
		if tr != nil {
			tr.summary(upload)
		}
		if doPrint {
			if err := p.Start(ctx, path.Base(file)); err != nil {
				return err
			}
			fmt.Fprintf(out, "print started: %s\n", path.Base(file))
		}
	case "print":
		if len(args) != 1 || !printer.Printable(args[0]) {
			return errors.New("usage: stlib printer print <name.ctb|name.goo>  (see 'stlib printer files')")
		}
		if err := p.Start(ctx, args[0]); err != nil {
			return err
		}
		fmt.Fprintf(out, "print started: %s\n", printer.RemotePath(args[0]))
	case "rm", "delete":
		if len(args) == 0 {
			return errors.New("usage: stlib printer rm <name>...")
		}
		if err := p.Delete(ctx, args); err != nil {
			return err
		}
		for _, a := range args {
			fmt.Fprintf(out, "deleted %s\n", printer.RemotePath(a))
		}
	case "pause":
		return p.Pause(ctx)
	case "resume":
		return p.Resume(ctx)
	case "stop":
		return p.Stop(ctx)
	default:
		return fmt.Errorf("unknown printer command %q\n%s", cmd, printerUsage)
	}
	return nil
}

func printStatus(out io.Writer, host string, st printer.Status) {
	fmt.Fprintf(out, "%s @ %s  (firmware %s): %s\n", st.Name, host, st.Firmware, st.Machine)
	if j := st.Job; j != nil {
		fmt.Fprintf(out, "  %s: %s, layer %d/%d (%.0f%%)", j.File, j.State, j.Layer, j.Layers, j.Progress()*100)
		if r := time.Duration(j.RemainingMs()) * time.Millisecond; r > 0 {
			step := time.Minute
			if r < time.Minute {
				step = time.Second
			}
			fmt.Fprintf(out, ", %s left", r.Round(step))
		}
		if j.Error != "" {
			fmt.Fprintf(out, ", error: %s", j.Error)
		}
		fmt.Fprintln(out)
	}
}

func size(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(b)/(1<<10))
	}
	return fmt.Sprintf("%d B", b)
}

// uploadTrace prints per-chunk timings and a verdict.
type uploadTrace struct {
	out    io.Writer
	chunks []sdcp.ChunkTiming
}

func (t *uploadTrace) chunk(c sdcp.ChunkTiming) {
	if len(t.chunks) == 0 {
		fmt.Fprintf(t.out, "  (transfer %s; an interrupted one stays on the printer as %s)\n", c.Transfer, sdcp.PartialName(c.Transfer, "<name>"))
	}
	t.chunks = append(t.chunks, c)
	fmt.Fprintf(t.out, "  %5d  %-10s  %7s  %7s  %7s  %-10s  %5s  %12s  %15s  %4d  %d\n", len(t.chunks), size(c.Offset),
		ms(c.Connect), ms(c.Send), ms(c.Wait), rate(c.Bytes, c.Send), ms(c.RTT), pct(c.TCP.RwndLimited, c.TCP.Busy),
		fmt.Sprintf("%d-%d", c.TCP.MinWindow, c.TCP.MaxWindow), c.TCP.MSS, c.TCP.Retransmits)
}

func (t *uploadTrace) summary(total time.Duration) {
	if len(t.chunks) == 0 {
		return
	}
	var connect, send, wait time.Duration
	var bytes int64
	for _, c := range t.chunks {
		connect, send, wait, bytes = connect+c.Connect, send+c.Send, wait+c.Wait, bytes+c.Bytes
	}
	n := time.Duration(len(t.chunks))
	fmt.Fprintf(t.out, "\n%d chunks, %s in %s = %s overall\n", len(t.chunks), size(bytes), total.Round(time.Second), rate(bytes, total))
	fmt.Fprintf(t.out, "  connecting: %s total (%s per chunk)\n", connect.Round(time.Millisecond), ms(connect/n))
	fmt.Fprintf(t.out, "  sending:    %s total (%s per chunk) - until the printer's network stack had it all, %s\n", send.Round(time.Millisecond), ms(send/n), rate(bytes, send))
	var rwnd, busy time.Duration
	var retrans uint32
	for _, c := range t.chunks {
		rwnd, busy, retrans = rwnd+c.TCP.RwndLimited, busy+c.TCP.Busy, retrans+c.TCP.Retransmits
	}
	if busy > 0 {
		fmt.Fprintf(t.out, "              of that, %s limited by the printer's receive window; %d retransmits\n", pct(rwnd, busy), retrans)
	}
	fmt.Fprintf(t.out, "  waiting:    %s total (%s per chunk) - the printer handling a chunk\n", wait.Round(time.Millisecond), ms(wait/n))
	if len(t.chunks) >= 10 {
		first, last := avgWait(t.chunks[:5]), avgWait(t.chunks[len(t.chunks)-5:])
		fmt.Fprintf(t.out, "  waiting per chunk, first 5: %s, last 5: %s\n", ms(first), ms(last))
		if last > 2*first && last-first > 200*time.Millisecond {
			fmt.Fprintln(t.out, "  -> the printer gets slower as the file grows (e.g. re-checking everything received so far); try --no-check")
		}
	}
	switch {
	case (send+wait)/n < 100*time.Millisecond:
		fmt.Fprintln(t.out, "  -> fast: under 100 ms per chunk, nothing to improve here")
	case send > 2*wait && busy > 0 && rwnd*2 > busy:
		fmt.Fprintln(t.out, "  -> mostly sending, and the printer's receive window was full most of that time: the printer reads the data slowly - not the network")
	case send > 2*wait:
		fmt.Fprintln(t.out, "  -> mostly the network: the data travels slowly (WiFi signal/band). Compare placing the router closer, or a wired connection")
	case wait > 2*send:
		fmt.Fprintln(t.out, "  -> mostly the printer handling each chunk (storage writes, checks): the client can't speed that up, except by avoiding per-chunk work (try --no-check)")
	default:
		fmt.Fprintln(t.out, "  -> network and printer both matter")
	}
}

func avgWait(cs []sdcp.ChunkTiming) time.Duration {
	var d time.Duration
	for _, c := range cs {
		d += c.Wait
	}
	return d / time.Duration(len(cs))
}

func ms(d time.Duration) string { return fmt.Sprintf("%dms", d.Milliseconds()) }

func rate(b int64, d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f MB/s", float64(b)/1e6/d.Seconds())
}

func pct(part, whole time.Duration) string {
	if whole <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d%%", part*100/whole)
}
