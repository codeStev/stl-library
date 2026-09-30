package disk

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/codeStev/stl-library/internal/core/importer"
)

// 7z and rar archives are read with the 7z and unrar programs (Downloads.SevenZip
// and Downloads.Unrar; unset means such archives stay plain files). An
// archive is unpacked once, as a whole, into a temporary folder - reading
// entry by entry would decompress a solid archive again for every entry.

// tool returns the program for an archive kind, "" if it isn't available.
func (d Downloads) tool(kind string) string {
	switch kind {
	case "7z":
		return d.SevenZip
	case "rar":
		return d.Unrar
	}
	return ""
}

// listExternal lists an archive's files (folders, links and unsafe names left out).
func (d Downloads) listExternal(ctx context.Context, kind, archive string) ([]archiveEntry, error) {
	var out []byte
	var err error
	switch kind {
	case "7z":
		out, err = run(ctx, d.SevenZip, "l", "-slt", "-ba", "-sccUTF-8", "--", archive)
	case "rar":
		out, err = run(ctx, d.Unrar, "lt", "-idq", "--", archive)
	}
	if err != nil {
		return nil, err
	}
	if kind == "7z" {
		return parse7zList(out)
	}
	return parseUnrarList(out)
}

type archiveEntry struct {
	Name    string // as stored (safe: cleaned by entryPath)
	Size    int64
	ModUnix int64
}

func run(ctx context.Context, prog string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, prog, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[len(msg)-300:]
		}
		return nil, fmt.Errorf("%s: %w: %s", filepath.Base(prog), err, msg)
	}
	return out, nil
}

// blocks splits "Key = value" technical listings at blank lines.
func blocks(out []byte, sep string) []map[string]string {
	var all []map[string]string
	cur := map[string]string{}
	flush := func() {
		if len(cur) > 0 {
			all = append(all, cur)
			cur = map[string]string{}
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if k, v, ok := strings.Cut(line, sep); ok {
			cur[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	flush()
	return all
}

func parseTime(layout, s string) int64 {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, ",."); i > 10 { // fractions of a second
		s = s[:i]
	}
	if t, err := time.Parse(layout, s); err == nil {
		return t.Unix()
	}
	return 0
}

// parse7zList reads "7z l -slt -ba" output.
func parse7zList(out []byte) ([]archiveEntry, error) {
	var res []archiveEntry
	for _, b := range blocks(out, " = ") {
		name, ok := b["Path"]
		if !ok || b["Folder"] == "+" || strings.HasPrefix(b["Attributes"], "D") {
			continue
		}
		if _, link := b["Symbolic Link"]; link {
			continue
		}
		if _, link := b["Hard Link"]; link {
			continue
		}
		clean, ok := entryPath(name)
		if !ok {
			return nil, fmt.Errorf("unsafe entry %q", name)
		}
		size, _ := strconv.ParseInt(b["Size"], 10, 64)
		res = append(res, archiveEntry{Name: clean, Size: size, ModUnix: parseTime("2006-01-02 15:04:05", b["Modified"])})
	}
	return res, nil
}

// parseUnrarList reads "unrar lt" output ("Name: …", "Type: File", "Size: …").
func parseUnrarList(out []byte) ([]archiveEntry, error) {
	var res []archiveEntry
	for _, b := range blocks(out, ": ") {
		name, ok := b["Name"]
		if !ok || b["Type"] != "File" {
			continue
		}
		clean, ok := entryPath(name)
		if !ok {
			return nil, fmt.Errorf("unsafe entry %q", name)
		}
		size, _ := strconv.ParseInt(b["Size"], 10, 64)
		res = append(res, archiveEntry{Name: clean, Size: size, ModUnix: parseTime("2006-01-02 15:04:05", b["mtime"])})
	}
	return res, nil
}

// expandExternal turns a 7z/rar archive into entries, like the zip case.
func (d Downloads) expandExternal(ctx context.Context, unit string, f importer.File, kind string) ([]importer.File, error) {
	entries, err := d.listExternal(ctx, kind, d.unitPath(unit, f.Rel))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Rel, err)
	}
	base := importer.ArchiveBase(f.Rel)
	var out []importer.File
	for _, e := range entries {
		out = append(out, importer.File{Rel: base + "/" + portableName(e.Name), Archive: f.Rel, Entry: e.Name, Size: e.Size, ModUnix: f.ModUnix})
	}
	return out, nil
}

// eachExternal unpacks the archive once and streams the wanted entries.
func (d Downloads) eachExternal(ctx context.Context, unit, archive string, entries []importer.File, fn func(importer.File, io.Reader) error) error {
	kind := importer.ArchiveKind(archive)
	src := d.unitPath(unit, archive)
	tmp, err := os.MkdirTemp(d.TempDir, "stlib-unpack-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	switch kind {
	case "7z":
		_, err = run(ctx, d.SevenZip, "x", "-y", "-bd", "-o"+tmp, "--", src)
	case "rar":
		_, err = run(ctx, d.Unrar, "x", "-y", "-idq", "-o+", "--", src, tmp+string(os.PathSeparator))
	default:
		err = fmt.Errorf("not an external archive: %s", archive)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", archive, err)
	}
	for _, f := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		clean, ok := entryPath(f.Entry)
		if !ok {
			return fmt.Errorf("%s: unsafe entry %q", archive, f.Entry)
		}
		p := filepath.Join(tmp, filepath.FromSlash(path.Clean(clean)))
		if err := func() error {
			info, err := os.Lstat(p) // a link the archive planted is no file
			if err != nil {
				return fmt.Errorf("%s: entry %q was not extracted", archive, f.Entry)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("%s: entry %q is not a plain file", archive, f.Entry)
			}
			if f.Size > 0 && info.Size() != f.Size {
				return fmt.Errorf("%s: entry %q has %d bytes, the listing said %d", archive, f.Entry, info.Size(), f.Size)
			}
			r, err := os.Open(p)
			if err != nil {
				return err
			}
			defer r.Close()
			return fn(f, r)
		}(); err != nil {
			return err
		}
		os.Remove(p) // free the space as we go
	}
	return nil
}
