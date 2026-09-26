// Command stlib is the stl-library CLI. It only wires adapters into the
// app's use cases.
//
//	stlib check <library-root>   read the library, report models and the
//	                             folders that don't follow the convention
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"

	"github.com/codeStev/stl-library/internal/adapters/disk"
	"github.com/codeStev/stl-library/internal/app"
	"github.com/codeStev/stl-library/internal/platform/lowprio"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "stlib:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) != 2 || args[0] != "check" {
		return fmt.Errorf("usage: stlib check <library-root>")
	}
	if err := lowprio.Apply(); err != nil {
		slog.Warn("could not lower process priority", "err", err)
	}
	r, err := app.Check(context.Background(), disk.Lister{Root: args[1]})
	if err != nil {
		return err
	}
	printReport(out, r)
	return nil
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
