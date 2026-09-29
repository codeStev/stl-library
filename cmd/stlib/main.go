// Command stlib is the stl-library CLI. It only wires adapters into the
// app's use cases.
//
//	stlib check <library-root>                 report models and the folders
//	                                           that don't follow the convention
//	stlib scan --db <index.db> <library-root>  bring the index up to date
//	stlib search --db <index.db> [--creator C] [words…]
//	stlib import --db <index.db> --source <downloads> <library-root> [--dry-run]
//	                                           copy complete new downloads in
//	stlib printer [--host addr] <command>      control a network resin printer
//	                                           (status, files, send, print, rm, …)
//	stlib serve                                serve the API; configured by
//	                                           LIBRARY_ROOT, DATA_DIR,
//	                                           LISTEN_ADDR, SCAN_INTERVAL
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/codeStev/stl-library/internal/adapters/disk"
	"github.com/codeStev/stl-library/internal/adapters/sqlite"
	"github.com/codeStev/stl-library/internal/app"
	"github.com/codeStev/stl-library/internal/platform/lowprio"
)

// version is set at build time (-ldflags "-X main.version=…").
var version = "dev"

const usage = `usage:
  stlib check <library-root>
  stlib scan --db <index.db> <library-root>
  stlib search --db <index.db> [--creator <name>] [--limit N] [words...]
  stlib import --db <index.db> --source <downloads> [--settle 1h] [--delete] [--adopt] [--dry-run] <library-root>
  stlib printer [--host <addr>[:<port>]] <status|files|send|print|rm|pause|resume|stop|watch> …
  stlib serve [--root <library-root>] [--data <dir>] [--listen <addr>] [--scan-interval <duration>]
      (defaults from LIBRARY_ROOT, DATA_DIR, LISTEN_ADDR, SCAN_INTERVAL)`

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "stlib:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	db := fs.String("db", "", "index database")
	creator := fs.String("creator", "", "only this creator")
	limit := fs.Int("limit", 50, "maximum hits")
	root := fs.String("root", os.Getenv("LIBRARY_ROOT"), "library root")
	data := fs.String("data", envOr("DATA_DIR", "./data"), "data directory")
	listen := fs.String("listen", envOr("LISTEN_ADDR", "127.0.0.1:8080"), "listen address")
	interval := fs.String("scan-interval", envOr("SCAN_INTERVAL", "1h"), "time between library scans")
	source := fs.String("source", os.Getenv("IMPORT_SOURCE"), "downloads folder to import from")
	settle := fs.String("settle", envOr("IMPORT_SETTLE", "1h"), "how long a download folder must be unchanged")
	importEvery := fs.String("import-interval", envOr("IMPORT_INTERVAL", "1h"), "time between imports")
	importAdopt := fs.Bool("adopt", false, "import the folders already in the downloads on the first run too (instead of only recording them)")
	importDelete := fs.Bool("delete", os.Getenv("IMPORT_DELETE") == "true", "remove imported folders from the downloads once verified in the library")
	dryRun := fs.Bool("dry-run", false, "only show where files would go")
	host := fs.String("host", "", "printer address (printer command)")
	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("%v\n%s", err, usage)
	}
	rest := fs.Args()
	switch args[0] {
	case "version":
		fmt.Fprintln(out, version)
	case "printer":
		return runPrinter(ctx, *host, *db, rest, out)
	case "check":
		if len(rest) != 1 {
			return errors.New(usage)
		}
		lowPriority()
		r, err := app.Check(ctx, disk.Lister{Root: rest[0]})
		if err != nil {
			return err
		}
		printReport(out, r)
	case "scan":
		if len(rest) != 1 || *db == "" {
			return errors.New(usage)
		}
		lowPriority()
		s, err := sqlite.Open(*db)
		if err != nil {
			return err
		}
		defer s.Close()
		start := time.Now()
		st, err := app.Scan(ctx, disk.Lister{Root: rest[0]}, s)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "scanned in %s: %d added, %d updated, %d removed, %d unchanged; %d folders don't follow the convention\n",
			time.Since(start).Round(time.Millisecond), st.Added, st.Updated, st.Removed, st.Unchanged, st.Issues)
	case "search":
		if *db == "" {
			return errors.New(usage)
		}
		s, err := sqlite.Open(*db)
		if err != nil {
			return err
		}
		defer s.Close()
		hits, err := app.Search(ctx, s, app.Query{Text: strings.Join(rest, " "), Creator: *creator, Limit: *limit})
		if err != nil {
			return err
		}
		for _, h := range hits {
			fmt.Fprintf(out, "%6d  %-40s %d variants, %d parts, %.1f MB  %s\n",
				h.ID, h.Name, h.Variants, h.Parts, float64(h.Bytes)/1e6, h.Dir)
		}
	case "import":
		if len(rest) != 1 || *db == "" || *source == "" {
			return errors.New(usage)
		}
		settleFor, err := time.ParseDuration(*settle)
		if err != nil {
			return fmt.Errorf("--settle: %v", err)
		}
		if err := separateFolders(*source, rest[0]); err != nil {
			return err
		}
		lowPriority()
		s, err := sqlite.Open(*db)
		if err != nil {
			return err
		}
		defer s.Close()
		im := &app.Importer{Downloads: newDownloads(*source), Library: disk.LibraryWriter{Root: rest[0]}, Log: s, Settle: settleFor,
			DeleteImported: *importDelete, AdoptExisting: *importAdopt}
		if *dryRun {
			return printPreview(ctx, out, im)
		}
		sum, err := im.Run(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "imported %d folders (%d files, %d removed from the downloads); %d waiting, %d failed; %d recorded as already there\n",
			sum.Imported, sum.Files, sum.Removed, sum.Waiting, sum.Failed, sum.Baselined)
	case "serve":
		if *root == "" {
			return errors.New("serve: no library root (set LIBRARY_ROOT or --root)\n" + usage)
		}
		every, err := time.ParseDuration(*interval)
		if err != nil || every < time.Minute {
			return fmt.Errorf("serve: scan interval %q: must be a duration of at least 1m", *interval)
		}
		lowPriority()
		var imp *importConfig
		if *source != "" {
			settleFor, err := time.ParseDuration(*settle)
			if err != nil {
				return fmt.Errorf("IMPORT_SETTLE: %v", err)
			}
			importFor, err := time.ParseDuration(*importEvery)
			if err != nil || importFor < time.Minute {
				return fmt.Errorf("IMPORT_INTERVAL %q: must be a duration of at least 1m", *importEvery)
			}
			if err := separateFolders(*source, *root); err != nil {
				return fmt.Errorf("IMPORT_SOURCE: %v", err)
			}
			imp = &importConfig{source: *source, settle: settleFor, every: importFor, delete: *importDelete}
		}
		return serve(ctx, *root, *data, *listen, every, imp)
	default:
		return errors.New(usage)
	}
	return nil
}

func printPreview(ctx context.Context, out io.Writer, im *app.Importer) error {
	previews, err := im.Preview(ctx)
	if err != nil {
		return err
	}
	for _, p := range previews {
		state := "complete"
		if !p.Settled {
			state = "waiting: " + p.Why
		}
		fmt.Fprintf(out, "== %s  ->  %s  (%s)\n", p.Source, p.Target, state)
		if p.Err != nil {
			fmt.Fprintf(out, "   cannot read: %v\n", p.Err)
			continue
		}
		// Group files by target folder.
		dirs := map[string]int{}
		var order []string
		for _, pl := range p.Placements {
			d := path.Dir(pl.Target)
			if dirs[d] == 0 {
				order = append(order, d)
			}
			dirs[d]++
		}
		sort.Strings(order)
		for _, d := range order {
			fmt.Fprintf(out, "   %4d  %s\n", dirs[d], strings.TrimPrefix(d, p.Target))
		}
	}
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func lowPriority() {
	if err := lowprio.Apply(); err != nil {
		slog.Warn("could not lower process priority", "err", err)
	}
}

func printReport(out io.Writer, r app.Report) {
	creators := map[string]int{}
	variants, parts := 0, 0
	for _, m := range r.Models {
		creators[m.Creator]++
		variants += len(m.Variants)
		for _, v := range m.Variants {
			parts += len(v.Parts)
		}
	}
	fmt.Fprintf(out, "%d files: %d models (%d variants, %d part files) from %d creators\n",
		r.Files, len(r.Models), variants, parts, len(creators))
	names := make([]string, 0, len(creators))
	for c := range creators {
		names = append(names, c)
	}
	sort.Slice(names, func(i, j int) bool { return creators[names[i]] > creators[names[j]] })
	for _, c := range names {
		fmt.Fprintf(out, "  %6d  %s\n", creators[c], c)
	}
	fmt.Fprintf(out, "\n%d folders don't follow the convention:\n", len(r.Issues))
	for _, i := range r.Issues {
		fmt.Fprintf(out, "  %s\n      %s\n", i.Dir, i.Reason)
	}
}
