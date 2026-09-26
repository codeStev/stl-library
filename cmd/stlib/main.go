// Command stlib is the stl-library CLI. It only wires adapters into the
// app's use cases.
//
//	stlib check <library-root>                 report models and the folders
//	                                           that don't follow the convention
//	stlib scan --db <index.db> <library-root>  bring the index up to date
//	stlib search --db <index.db> [--creator C] [words…]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/codeStev/stl-library/internal/adapters/disk"
	"github.com/codeStev/stl-library/internal/adapters/sqlite"
	"github.com/codeStev/stl-library/internal/app"
	"github.com/codeStev/stl-library/internal/platform/lowprio"
)

const usage = `usage:
  stlib check <library-root>
  stlib scan --db <index.db> <library-root>
  stlib search --db <index.db> [--creator <name>] [--limit N] [words...]`

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
	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("%v\n%s", err, usage)
	}
	rest := fs.Args()
	switch args[0] {
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
	default:
		return errors.New(usage)
	}
	return nil
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
