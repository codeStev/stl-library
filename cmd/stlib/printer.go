package main

import (
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

// parsePrinterAddr: "192.168.2.35" or "host:3030" (the control port).
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
	doPrint := false
	var rest []string
	for _, a := range args {
		if a == "--print" {
			doPrint = true
		} else {
			rest = append(rest, a)
		}
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
				fmt.Fprintf(out, "  %s  (%s)\n", path.Base(f.Path), size(f.Size))
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
		start := time.Now()
		last := -1
		err = p.Upload(ctx, path.Base(file), info.Size(), open, func(sent int64) {
			if pct := int(sent * 100 / max(info.Size(), 1)); pct != last {
				last = pct
				fmt.Fprintf(out, "\r  uploading %s: %3d%%", path.Base(file), pct)
			}
		})
		fmt.Fprintln(out)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "uploaded in %s\n", time.Since(start).Round(time.Second))
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
