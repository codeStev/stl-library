package disk

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/codeStev/stl-library/internal/core/importer"
)

// Downloads reads a downloads folder, looking into zip archives. It only
// reads, except for Remove (imported folders, when asked to).
type Downloads struct {
	Root string
	// SevenZip and Unrar are the programs that read 7z and rar archives
	// (empty: those stay plain files); TempDir is where an archive is
	// unpacked (default: the system's temp folder) - it needs room for
	// the largest archive.
	SevenZip, Unrar string
	TempDir         string
}

func (d Downloads) List(ctx context.Context) ([]importer.File, error) {
	var out []importer.File
	err := filepath.WalkDir(d.Root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		hidden := p != d.Root && strings.HasPrefix(e.Name(), ".")
		if !hidden && (e.IsDir() || !e.Type().IsRegular()) {
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(d.Root, p)
		// Hidden files and folders are reported (a downloader's temporary
		// files show a download in progress) but never imported.
		out = append(out, importer.File{Rel: filepath.ToSlash(rel), Size: info.Size(), ModUnix: info.ModTime().Unix(),
			ChangeUnix: changeTime(info), Hidden: hidden})
		if hidden && e.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	return out, err
}

func (d Downloads) unitPath(unit, rel string) string {
	return filepath.Join(d.Root, filepath.FromSlash(unit), filepath.FromSlash(rel))
}

// Expand replaces zip archives by their entries: an entry "foo\bar.stl"
// of "sub/x.zip" becomes "sub/x/foo/bar.stl".
func (d Downloads) Expand(ctx context.Context, unit string, files []importer.File) ([]importer.File, error) {
	var out []importer.File
	for _, f := range files {
		if f.Hidden {
			continue
		}
		kind := importer.ArchiveKind(f.Rel)
		if kind != "" && kind != "zip" && d.tool(kind) != "" {
			entries, err := d.expandExternal(ctx, unit, f, kind)
			if err != nil {
				return nil, err
			}
			out = append(out, entries...)
			continue
		}
		if importer.IsVolumePart(f.Rel) && (d.tool("rar") != "" || d.tool("7z") != "") {
			continue // read together with its first volume
		}
		if kind != "zip" {
			out = append(out, f) // no reader for it: a plain file
			continue
		}
		zr, err := zip.OpenReader(d.unitPath(unit, f.Rel))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Rel, err)
		}
		base := importer.ArchiveBase(f.Rel)
		for _, e := range zr.File {
			if e.FileInfo().IsDir() {
				continue
			}
			name, ok := entryPath(decodeName(e.Name))
			if !ok {
				zr.Close()
				return nil, fmt.Errorf("%s: unsafe entry %q", f.Rel, e.Name)
			}
			out = append(out, importer.File{Rel: base + "/" + portableName(name), Archive: f.Rel, Entry: e.Name,
				Size: int64(e.UncompressedSize64), ModUnix: f.ModUnix})
		}
		zr.Close()
	}
	return out, nil
}

// decodeName makes an entry name valid UTF-8. Zips written by other
// tools store names in the local code page without saying so: a name that
// is no valid UTF-8 is read as GBK if that yields clean Chinese text, else
// as CP437 (the zip format's own default).
func decodeName(name string) string {
	if utf8.ValidString(name) {
		return name
	}
	if s, err := simplifiedchinese.GBK.NewDecoder().String(name); err == nil && utf8.ValidString(s) && hasHan(s) {
		return s
	}
	if s, err := charmap.CodePage437.NewDecoder().String(name); err == nil {
		return s
	}
	return strings.ToValidUTF8(name, "_")
}

func hasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// entryPath cleans a zip entry name (Windows zips use backslashes) and
// refuses names that would leave the folder.
func entryPath(name string) (string, bool) {
	name = strings.ReplaceAll(name, `\`, "/")
	clean := path.Clean("/" + name)[1:]
	if clean == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "../") || strings.HasPrefix(name, "..") || strings.Contains(clean, ":") {
		return "", false
	}
	return clean, true
}

// portableName replaces what common file systems (NTFS, exFAT) refuse in a
// file name - replacement characters, control characters, non-characters,
// "<>\"|?*" - and trailing dots or spaces of each level.
func portableName(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		s = strings.Map(func(r rune) rune {
			switch {
			case r == utf8.RuneError, r < 0x20, r == 0x7f, r == 0xfffe, r == 0xffff,
				strings.ContainsRune(`<>"|?*`, r):
				return '_'
			}
			return r
		}, s)
		s = strings.TrimRight(s, ". ")
		if s == "" {
			s = "_"
		}
		segs[i] = s
	}
	return strings.Join(segs, "/")
}

// Each streams files, opening every archive once.
func (d Downloads) Each(ctx context.Context, unit string, files []importer.File, fn func(importer.File, io.Reader) error) error {
	byArchive := map[string][]importer.File{}
	var plain []importer.File
	for _, f := range files {
		if f.Archive == "" {
			plain = append(plain, f)
		} else {
			byArchive[f.Archive] = append(byArchive[f.Archive], f)
		}
	}
	for _, f := range plain {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := func() error {
			r, err := os.Open(d.unitPath(unit, f.Rel))
			if err != nil {
				return err
			}
			defer r.Close()
			return fn(f, r)
		}(); err != nil {
			return err
		}
	}
	for archive, entries := range byArchive {
		var err error
		if kind := importer.ArchiveKind(archive); kind == "7z" || kind == "rar" {
			err = d.eachExternal(ctx, unit, archive, entries, fn)
		} else {
			err = d.eachEntry(ctx, unit, archive, entries, fn)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (d Downloads) eachEntry(ctx context.Context, unit, archive string, entries []importer.File, fn func(importer.File, io.Reader) error) error {
	zr, err := zip.OpenReader(d.unitPath(unit, archive))
	if err != nil {
		return err
	}
	defer zr.Close()
	byName := map[string]*zip.File{}
	for _, e := range zr.File {
		byName[e.Name] = e
	}
	for _, f := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		e := byName[f.Entry]
		if e == nil {
			return fmt.Errorf("%s: entry %q vanished", archive, f.Entry)
		}
		if err := func() error {
			r, err := e.Open()
			if err != nil {
				return err
			}
			defer r.Close()
			return fn(f, r)
		}(); err != nil {
			return fmt.Errorf("%s: %w", archive, err)
		}
	}
	return nil
}

// Remove deletes the given files of a unit and then the folders that are
// left empty, deepest first, up to and including the unit's folder -
// never above it, and never a folder that still holds anything.
func (d Downloads) Remove(_ context.Context, unit string, rels []string) error {
	unitDir := filepath.Join(d.Root, filepath.FromSlash(unit))
	if rel, err := filepath.Rel(d.Root, unitDir); err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("%q is not a folder inside the downloads", unit)
	}
	dirs := map[string]bool{unitDir: true}
	for _, r := range rels {
		p := d.unitPath(unit, r)
		if rel, err := filepath.Rel(unitDir, p); err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("%q is outside %q", r, unit)
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		for dir := filepath.Dir(p); dir != unitDir && strings.HasPrefix(dir, unitDir+string(filepath.Separator)); dir = filepath.Dir(dir) {
			dirs[dir] = true
		}
	}
	ordered := make([]string, 0, len(dirs))
	for dir := range dirs {
		ordered = append(ordered, dir)
	}
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, dir := range ordered {
		if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
			os.Remove(dir) // a folder that isn't empty stays
		}
	}
	return nil
}

// LibraryWriter adds files to the library. It never overwrites.
type LibraryWriter struct {
	Root string
}

// ErrExists is returned when writing would overwrite a file.
var ErrExists = errors.New("file exists")

func (w LibraryWriter) full(rel string) (string, error) {
	clean := path.Clean("/" + rel)
	if rel == "" || clean != "/"+rel {
		return "", ErrOutsideRoot
	}
	return filepath.Join(w.Root, filepath.FromSlash(clean[1:])), nil
}

func (w LibraryWriter) Stat(_ context.Context, rel string) (int64, bool, error) {
	p, err := w.full(rel)
	if err != nil {
		return 0, false, err
	}
	info, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return info.Size(), true, nil
}

// Rename renames a folder within its parent folder, and refuses to replace anything. It is the one
// change the app ever makes to existing library folders (a fix the user confirmed).
func (w LibraryWriter) Rename(_ context.Context, from, to string) error {
	src, err := w.full(from)
	if err != nil {
		return err
	}
	dst, err := w.full(to)
	if err != nil {
		return err
	}
	if path.Dir(from) != path.Dir(to) || from == to {
		return ErrOutsideRoot
	}
	if info, err := os.Lstat(src); err != nil || !info.IsDir() {
		if err == nil {
			err = errors.New("not a folder")
		}
		return err
	}
	if _, err := os.Lstat(dst); err == nil {
		return ErrExists
	}
	return os.Rename(src, dst)
}

// Move moves a folder to another place in the library (another parent, another name), creating the
// parent folders, and refuses to replace anything or to move a folder into itself. Like Rename it is
// only used for changes the user confirmed.
func (w LibraryWriter) Move(_ context.Context, from, to string) error {
	src, err := w.full(from)
	if err != nil {
		return err
	}
	dst, err := w.full(to)
	if err != nil {
		return err
	}
	if from == to || strings.HasPrefix(to, from+"/") {
		return ErrOutsideRoot
	}
	if info, err := os.Lstat(src); err != nil || !info.IsDir() {
		if err == nil {
			err = errors.New("not a folder")
		}
		return err
	}
	if _, err := os.Lstat(dst); err == nil {
		return ErrExists
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// Write copies r into a hidden temporary file next to the target and
// renames it when complete, so a half-written file never appears under
// its real name (the library listing skips hidden files).
func (w LibraryWriter) Write(_ context.Context, rel string, r io.Reader, modUnix int64) error {
	p, err := w.full(rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".stlib-import-*")
	if err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if _, err := io.Copy(tmp, r); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if _, err := os.Stat(p); err == nil {
		return ErrExists
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return err
	}
	done = true
	if modUnix > 0 {
		t := timeUnix(modUnix)
		_ = os.Chtimes(p, t, t)
	}
	return nil
}

// FindSame walks dir for files with that name and size.
func (w LibraryWriter) FindSame(_ context.Context, dir, name string, size int64) ([]string, error) {
	root, err := w.full(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	err = filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if e.IsDir() || e.Name() != name {
			return nil
		}
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() && info.Size() == size {
			rel, _ := filepath.Rel(w.Root, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out, err
}

func (w LibraryWriter) Hash(_ context.Context, rel string) (string, error) {
	p, err := w.full(rel)
	if err != nil {
		return "", err
	}
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (w LibraryWriter) Creators(context.Context) ([]string, error) {
	entries, err := os.ReadDir(w.Root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && !strings.HasPrefix(e.Name(), "_") {
			out = append(out, e.Name())
		}
	}
	return out, nil
}
