// The dependency rule, enforced: the convention package is the pure
// definition of the folder convention and must never touch I/O. (The app's
// own hexagon - core, app/ports, adapters, cmd - is added with the app and
// gets its rules here.)
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

var rules = []struct {
	dir       string
	forbidden []string
}{
	{"../convention", append(append([]string{}, ioPackages...), module+"internal")},
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
