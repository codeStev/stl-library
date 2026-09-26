// The dependency rule, enforced (hexagonal):
//   - convention: the pure, public definition of the folder convention
//   - internal/core: the domain, pure (no I/O), may use convention
//   - internal/app: use cases and the ports they need; no I/O, no adapters
//   - internal/adapters: implement the ports (disk, database, HTTP)
//   - cmd: wires adapters into use cases
package architecture_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/codeStev/stl-library/"

// ioPackages touch the outside world.
var ioPackages = []string{"os", "io/fs", "path/filepath", "database/sql", "encoding/csv", "net/", "log", "modernc.org/", "golang.org/x/sys"}

// pureParsers are packages under a forbidden prefix that only parse
// strings and never touch the outside world.
var pureParsers = map[string]bool{"net/url": true, "net/mail": true}

var rules = []struct {
	dir       string
	forbidden []string
}{
	{"../convention", append(append([]string{}, ioPackages...), module+"internal")},
	{"core", append(append([]string{}, ioPackages...), module+"internal/app", module+"internal/adapters", module+"cmd")},
	{"app", append(append([]string{}, ioPackages...), module+"internal/adapters", module+"cmd")},
	{"adapters", []string{module + "cmd"}},
}

func TestLayersOnlyDependInward(t *testing.T) {
	for _, r := range rules {
		err := filepath.WalkDir(r.dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			f, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range f.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				if pureParsers[path] {
					continue
				}
				for _, bad := range r.forbidden {
					if path == strings.TrimSuffix(bad, "/") || strings.HasPrefix(path, strings.TrimSuffix(bad, "/")+"/") {
						t.Errorf("%s imports %s - not allowed there", p, path)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
