// Package importer decides how new downloads enter the library: which
// download folders are complete, and where each of their files goes so
// that the result follows the folder convention. Pure: it works on
// listings, the app does the copying.
package importer

import (
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	convention "github.com/codeStev/stl-convention"
)

// File is a file of a download folder, possibly inside a zip archive.
type File struct {
	// Rel is the logical path below the unit: for a file inside
	// "foo_pre_supported.zip" it is "foo_pre_supported/<entry path>".
	Rel     string
	Archive string // the archive's path below the unit, "" for plain files
	Entry   string // the entry inside the archive
	Size    int64
	ModUnix int64
	// ChangeUnix is the file system's change time (ctime): unlike the
	// modification time, which downloaders set to the original's, it moves
	// on every write and rename. 0 if unknown.
	ChangeUnix int64
	// Hidden files (".name…") are never imported; other than known junk
	// they are a downloader's temporary files.
	Hidden bool
}

// Unit is one download folder that becomes one model:
// <downloads>/<creator>/<model>, or a model inside a container folder
// (<creator>/<container>/<model>).
type Unit struct {
	Creator string // the creator folder's name in the downloads
	Path    string // below the downloads root
	Name    string // the model folder's name
	Files   []File // plain files only (archives not expanded)
	// Bust: the unit is a model's bust, shipped separately in a Busts
	// folder (<creator>/Busts/<model>, <creator>/<container>/Busts/<model>).
	// It belongs to the model of that name as its Bust scale.
	Bust bool
}

// temporary download files: a unit containing one is still being written.
var reTemp = regexp.MustCompile(`(?i)\.(partial|part|crdownload|tmp|download|!qb|aria2)$|\.part\d+$`)

// Settled reports whether a download folder looks complete: no temporary
// download files, and nothing changed within the settle window. A reason
// is returned when it is not.
func Settled(u Unit, now time.Time, window time.Duration) (bool, string) {
	var newest int64
	visible := 0
	for _, f := range u.Files {
		name := path.Base(f.Rel)
		if reTemp.MatchString(f.Rel) || f.Hidden && !hiddenJunk(name) {
			return false, "still downloading (" + name + ")"
		}
		newest = max(newest, f.ModUnix, f.ChangeUnix)
		if !f.Hidden {
			visible++
		}
	}
	if visible == 0 {
		return false, "empty"
	}
	if age := now.Sub(time.Unix(newest, 0)); age < window {
		return false, "changed " + age.Round(time.Minute).String() + " ago, waiting until nothing changed for " + window.String()
	}
	return true, ""
}

func hiddenJunk(name string) bool { return name == ".DS_Store" || strings.HasPrefix(name, "._") }

// Signature changes whenever files of the unit are added, removed or
// changed.
func Signature(files []File) string {
	lines := make([]string, 0, len(files))
	for _, f := range files {
		if f.Hidden {
			continue
		}
		lines = append(lines, f.Rel+"|"+strconv.FormatInt(f.Size, 10)+"|"+strconv.FormatInt(f.ModUnix, 10))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// Units groups a listing of the downloads folder (paths relative to it)
// into units. A folder directly below a creator folder is a model, unless
// it holds no files itself and at least two subfolders that don't look
// like variant folders - then it is a container of models ("Last month's
// models/<model>").
func Units(files []File) []Unit {
	byTop := map[string][]File{} // "<creator>/<folder>" -> files with Rel below it
	for _, f := range files {
		parts := strings.SplitN(f.Rel, "/", 3)
		if len(parts) < 3 || strings.HasPrefix(parts[0], ".") || strings.HasPrefix(parts[1], ".") {
			continue // files directly in the root or a creator folder are not models
		}
		key := parts[0] + "/" + parts[1]
		g := f
		g.Rel = parts[2]
		byTop[key] = append(byTop[key], g)
	}
	var units []Unit
	for key, fs := range byTop {
		creator, name := splitFirst(key)
		if isBustsFolder(name) {
			if busts := bustChildren(fs); busts != nil {
				for child, cfs := range busts {
					units = append(units, Unit{Creator: creator, Path: key + "/" + child, Name: child, Files: cfs, Bust: true})
				}
				continue
			}
		}
		if children := containerChildren(fs); children != nil {
			for child, cfs := range children {
				if isBustsFolder(child) {
					if busts := bustChildren(cfs); busts != nil {
						for m, mfs := range busts {
							units = append(units, Unit{Creator: creator, Path: key + "/" + child + "/" + m, Name: m, Files: mfs, Bust: true})
						}
						continue
					}
				}
				units = append(units, Unit{Creator: creator, Path: key + "/" + child, Name: child, Files: cfs})
			}
			continue
		}
		units = append(units, Unit{Creator: creator, Path: key, Name: name, Files: fs})
	}
	sort.Slice(units, func(i, j int) bool { return units[i].Path < units[j].Path })
	return units
}

func containerChildren(fs []File) map[string][]File {
	children := map[string][]File{}
	for _, f := range fs {
		child, rest := splitFirst(f.Rel)
		if rest == "" {
			return nil // a file directly in the folder: it is a model
		}
		g := f
		g.Rel = rest
		children[child] = append(children[child], g)
	}
	named := 0
	for child := range children {
		if !looksLikeVariantOrExtra(child) {
			named++
		}
	}
	if named < 2 {
		return nil
	}
	return children
}

// isBustsFolder: a folder of busts ("Busts", "Bust").
func isBustsFolder(name string) bool {
	ws := words(name)
	return len(ws) == 1 && (ws[0] == "busts" || ws[0] == "bust")
}

// bustChildren splits a Busts folder into its models (one folder each);
// nil if it has loose files (then it is a bust model itself).
func bustChildren(fs []File) map[string][]File {
	out := map[string][]File{}
	for _, f := range fs {
		child, rest := splitFirst(f.Rel)
		if rest == "" {
			return nil
		}
		g := f
		g.Rel = rest
		out[child] = append(out[child], g)
	}
	return out
}

func splitFirst(p string) (string, string) {
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i], p[i+1:]
	}
	return p, ""
}

// ---- where files go ----

// Placement is where one file of a unit goes, below the library root.
type Placement struct {
	File   File
	Target string
}

// Placements maps every file of a unit (archives expanded: pass the
// entries as Files) to its place in the library:
//
//	<Creator>/<Model>/<variant levels>/[<Option>/]<file>   print files
//	<Creator>/<Model>/<folders>/<file>                     images, documents
//
// creatorDir is the creator's folder name in the library.
func Placements(u Unit, files []File, creatorDir string) []Placement {
	model := ModelName(u.Name, u.Creator)
	type item struct {
		f      File
		dims   convention.Dims
		option string
		extras []string // non-variant folder names, for images/docs
		print  bool
	}
	items := make([]item, 0, len(files))
	anySupports, splitScales := false, map[string]bool{}
	for _, f := range files {
		if f.Hidden {
			continue
		}
		dir, name := path.Split(f.Rel)
		it := item{f: f, print: convention.Printable(ext(name))}
		for _, seg := range strings.Split(strings.Trim(dir, "/"), "/") {
			if seg == "" {
				continue
			}
			d, option, isDims := classify(seg, model, u.Creator)
			it.dims = convention.Merge(it.dims, d)
			if option != "" {
				it.option = option
			}
			if !isDims && option == "" && !sameWords(seg, model) {
				// "Images/Images/…" (an Images archive holding an Images
				// folder): one level is enough.
				if e := extraFolder(seg); len(it.extras) == 0 || !strings.EqualFold(it.extras[len(it.extras)-1], e) {
					it.extras = append(it.extras, e)
				}
			}
		}
		if it.print {
			if u.Bust {
				it.dims.Scale = "Bust" // a separately shipped bust: the model's Bust scale
			}
			if fmt := convention.FormatOfExtension(ext(name)); fmt != "" && fmt != "STL" {
				it.dims.Format = fmt
			}
			anySupports = anySupports || it.dims.Supports != ""
			if it.dims.Split == "Parts" && it.option == "" {
				splitScales[it.dims.Scale] = true
			}
		}
		items = append(items, it)
	}
	base := cleanFolder(creatorDir) + "/" + model
	out := make([]Placement, 0, len(items))
	for _, it := range items {
		name := path.Base(it.f.Rel)
		var target string
		if it.print {
			d := it.dims
			// Next to supported files, unmarked ones are the unsupported
			// version; next to split files, unmarked ones are one piece.
			if anySupports && d.Supports == "" {
				d.Supports = "No Supports"
			}
			if d.Split == "" && splitScales[d.Scale] {
				d.Split = "Combined"
			}
			segs := append([]string{base}, convention.CanonicalSegments(d)...)
			if it.option != "" {
				segs = append(segs, cleanFolder(it.option))
			}
			target = path.Join(append(segs, name)...)
		} else {
			target = path.Join(append(append([]string{base}, it.extras...), name)...)
		}
		out = append(out, Placement{File: it.f, Target: target})
	}
	return out
}

// ModelName cleans a download folder name into a model folder name:
// surrounding space, a leading "<creator> - " and a Google Drive export
// suffix go; characters Windows can't store become "_".
func ModelName(name, creator string) string {
	n := reDriveSuffix.ReplaceAllString(strings.TrimSpace(name), "")
	if p := strings.SplitN(n, " - ", 2); len(p) == 2 && strings.EqualFold(strings.TrimSpace(p[0]), creator) {
		n = p[1]
	}
	return cleanFolder(n)
}

var reDriveSuffix = regexp.MustCompile(`-\d{8}T\d{6}Z(?:-\d{1,3})+$`)

func cleanFolder(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 0x20 {
			return '_'
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimRight(s, ". ")
}

// ---- reading variant information from names ----

var (
	reWords = regexp.MustCompile(`[A-Za-z]+|\d+(?:\.\d+)?`)
	reMM    = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s?mm\b`)
	// 1:9, 1-9, 1_12 - also with "scale" glued on ("1-9scale").
	reRatio  = regexp.MustCompile(`(?i)\b1\s?[-_/:]\s?(\d{1,2})(?:\b|scale\b)`)
	reParens = regexp.MustCompile(`\(([^()]*)\)`)
)

// phrases per dimension, matched on the lower-case words of a name.
var phrases = []struct {
	words []string
	apply func(*convention.Dims)
}{
	{[]string{"no", "supports"}, func(d *convention.Dims) { d.Supports = "No Supports" }},
	{[]string{"no", "support"}, func(d *convention.Dims) { d.Supports = "No Supports" }},
	{[]string{"non", "supported"}, func(d *convention.Dims) { d.Supports = "No Supports" }},
	{[]string{"unsupported"}, func(d *convention.Dims) { d.Supports = "No Supports" }},
	{[]string{"nosupports"}, func(d *convention.Dims) { d.Supports = "No Supports" }},
	{[]string{"pre", "supported"}, func(d *convention.Dims) { d.Supports = "Supported" }},
	{[]string{"pre", "supports"}, func(d *convention.Dims) { d.Supports = "Supported" }},
	{[]string{"presupported"}, func(d *convention.Dims) { d.Supports = "Supported" }},
	{[]string{"presupports"}, func(d *convention.Dims) { d.Supports = "Supported" }},
	{[]string{"presupport"}, func(d *convention.Dims) { d.Supports = "Supported" }},
	{[]string{"supported"}, func(d *convention.Dims) { d.Supports = "Supported" }},
	// typos seen in real downloads
	{[]string{"pre", "suppotted"}, func(d *convention.Dims) { d.Supports = "Supported" }},
	{[]string{"pre", "suported"}, func(d *convention.Dims) { d.Supports = "Supported" }},
	{[]string{"pre", "suppoted"}, func(d *convention.Dims) { d.Supports = "Supported" }},
	{[]string{"suppotted"}, func(d *convention.Dims) { d.Supports = "Supported" }},
	{[]string{"suported"}, func(d *convention.Dims) { d.Supports = "Supported" }},
	{[]string{"suppoted"}, func(d *convention.Dims) { d.Supports = "Supported" }},
	{[]string{"one", "piece"}, func(d *convention.Dims) { d.Split = "Combined" }},
	{[]string{"onepiece"}, func(d *convention.Dims) { d.Split = "Combined" }},
	{[]string{"uncut"}, func(d *convention.Dims) { d.Split = "Combined" }},
	{[]string{"unsplit"}, func(d *convention.Dims) { d.Split = "Combined" }},
	{[]string{"combined"}, func(d *convention.Dims) { d.Split = "Combined" }},
	{[]string{"split"}, func(d *convention.Dims) { d.Split = "Parts" }},
	{[]string{"hollow"}, func(d *convention.Dims) { d.Fill = "Hollow" }},
	{[]string{"hollowed"}, func(d *convention.Dims) { d.Fill = "Hollow" }},
	{[]string{"solid"}, func(d *convention.Dims) { d.Fill = "Solid" }},
	{[]string{"bust"}, func(d *convention.Dims) { d.Scale = "Bust" }},
	{[]string{"busts"}, func(d *convention.Dims) { d.Scale = "Bust" }},
	{[]string{"lys"}, func(d *convention.Dims) {}}, // format comes from the file extension
	{[]string{"lychee"}, func(d *convention.Dims) {}},
	{[]string{"chitubox"}, func(d *convention.Dims) {}},
	{[]string{"stl"}, func(d *convention.Dims) {}},
	{[]string{"stls"}, func(d *convention.Dims) {}},
	{[]string{"files"}, func(d *convention.Dims) {}},
	{[]string{"scale"}, func(d *convention.Dims) {}},
	{[]string{"miniature"}, func(d *convention.Dims) {}},
}

// extra folder names that hold images or documents.
var extraWords = map[string]bool{"renders": true, "render": true, "images": true, "image": true, "pictures": true,
	"photos": true, "test": true, "print": true, "size": true, "printing": true, "information": true, "info": true}

// classify reads a folder or archive name below the model: its
// dimensions, an option (text in parentheses that isn't a dimension, the
// way Wicked names its archives: "… (X Pose)"), and whether the name is
// nothing but variant information (plus words of the model's own name,
// "miyamoto_musashi_pre_supported_lys").
func classify(seg, model, creator string) (convention.Dims, string, bool) {
	// Words of the model's and creator's own name say nothing about the
	// variant ("Black Panther Portrait Bust (Non Supported)" is no bust
	// scale).
	known := map[string]bool{}
	for _, w := range words(model + " " + creator) {
		if !phraseWords[w] { // "Sousou no Frieren": "no" still counts in "no supports"
			known[w] = true
		}
	}
	// "alternative_split", "Helmet Version", "Pose 2": an alternative of the
	// model; words like "split" in it describe the alternative, not the
	// variant.
	if ws := words(strip2(seg, known)); hasOptionWord(ws) {
		return convention.Dims{}, optionName(seg, known), false
	}
	strip := func(s string) string {
		var keep []string
		for _, w := range reWords.FindAllString(s, -1) {
			if !known[strings.ToLower(w)] {
				keep = append(keep, w)
			}
		}
		return strings.Join(keep, " ")
	}
	var d convention.Dims
	if m := reMM.FindStringSubmatch(seg); m != nil {
		d.Scale = m[1] + "mm"
	} else if m := reRatio.FindStringSubmatch(seg); m != nil {
		d.Scale = "1-" + m[1]
	}
	option := ""
	for _, p := range reParens.FindAllStringSubmatch(seg, -1) {
		pd, rest := dims(strip(p[1]))
		d = convention.Merge(d, pd)
		if len(rest) > 0 && !allExtra(rest) {
			option = strings.TrimSpace(p[1])
		}
	}
	outside := reParens.ReplaceAllString(seg, " ")
	od, rest := dims(strip(outside))
	d = convention.Merge(d, od)
	// Words left that are neither dimensions nor the model/creator name.
	var unknown []string
	for _, w := range rest {
		if !reNumber.MatchString(w) {
			unknown = append(unknown, w)
		}
	}
	isDims := d.Any() && len(unknown) == 0
	// "miyamoto_musashi_no_supports" for model "Musashi": one stray word
	// next to clear variant information is part of the model's name.
	if d.Any() && len(unknown) <= 1 && od.Any() {
		isDims = true
	}
	return d, option, isDims
}

var reNumber = regexp.MustCompile(`^\d+(\.\d+)?$`)

// phraseWords are the words that only occur as part of multi-word
// dimension phrases ("no", "pre", "one" …): they keep counting even when
// the model's name contains them ("Sousou no Frieren"), unlike words that
// are a dimension on their own ("Portrait Bust" is no bust scale).
var phraseWords = func() map[string]bool {
	single := map[string]bool{}
	for _, p := range phrases {
		if len(p.words) == 1 {
			single[p.words[0]] = true
		}
	}
	m := map[string]bool{}
	for _, p := range phrases {
		if len(p.words) > 1 {
			for _, w := range p.words {
				if !single[w] {
					m[w] = true
				}
			}
		}
	}
	return m
}()

var optionWords = map[string]bool{"alternative": true, "alternate": true, "alt": true, "version": true,
	"variant": true, "option": true, "pose": true, "kit": true, "nsfw": true, "sfw": true}

func hasOptionWord(ws []string) bool {
	for _, w := range ws {
		if optionWords[w] {
			return true
		}
	}
	return false
}

// strip2 removes known words (the model's and creator's name) from s.
func strip2(s string, known map[string]bool) string {
	var keep []string
	for _, w := range reWords.FindAllString(s, -1) {
		if !known[strings.ToLower(w)] {
			keep = append(keep, w)
		}
	}
	return strings.Join(keep, " ")
}

// optionName: the option's own words, readable ("alternative_split" ->
// "alternative split"); text in parentheses if there is some.
func optionName(seg string, known map[string]bool) string {
	if m := reParens.FindAllStringSubmatch(seg, -1); len(m) > 0 {
		return cleanFolder(m[len(m)-1][1])
	}
	return cleanFolder(strip2(seg, known))
}

func dims(s string) (convention.Dims, []string) {
	var d convention.Dims
	ws := words(s)
	used := make([]bool, len(ws))
	for _, p := range phrases {
		for i := 0; i+len(p.words) <= len(ws); i++ {
			match := true
			for j, pw := range p.words {
				if used[i+j] || ws[i+j] != pw {
					match = false
					break
				}
			}
			if match {
				p.apply(&d)
				for j := range p.words {
					used[i+j] = true
				}
			}
		}
	}
	var rest []string
	for i, w := range ws {
		if !used[i] && w != "mm" {
			rest = append(rest, w)
		}
	}
	return d, rest
}

func words(s string) []string {
	var out []string
	for _, w := range reWords.FindAllString(s, -1) {
		out = append(out, strings.ToLower(w))
	}
	return out
}

func allExtra(ws []string) bool {
	for _, w := range ws {
		if !extraWords[w] && w != "and" {
			return false
		}
	}
	return true
}

func looksLikeVariantOrExtra(name string) bool {
	d, rest := dims(name)
	return d.Any() || len(rest) == 0 || allExtra(rest) || reMM.MatchString(name) || reRatio.MatchString(name)
}

// extraFolder names the folder for images and documents from a folder or
// archive: the part in parentheses if there is one ("… (Images)").
func extraFolder(seg string) string {
	if m := reParens.FindAllStringSubmatch(seg, -1); len(m) > 0 {
		return cleanFolder(m[len(m)-1][1])
	}
	return cleanFolder(seg)
}

func sameWords(a, b string) bool { return strings.Join(words(a), " ") == strings.Join(words(b), " ") }

func ext(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return strings.ToLower(name[i+1:])
	}
	return ""
}

// NotAModel reports a download folder whose name is only variant
// information ("Presupports", "STL" directly in a creator folder): a
// model's files that lost their model folder. Those are left for a person.
func NotAModel(u Unit) bool {
	_, rest := dims(u.Name)
	var named []string
	for _, w := range rest {
		if !reNumber.MatchString(w) {
			named = append(named, w)
		}
	}
	return len(named) == 0 || allExtra(named)
}

// IsArchive reports archives that are expanded on import (see ArchiveKind).
func IsArchive(name string) bool { return ArchiveKind(name) != "" }

var (
	reRarPart   = regexp.MustCompile(`(?i)\.part0*(\d+)\.rar$`)
	reOldVolume = regexp.MustCompile(`(?i)\.r\d{2,3}$`)
	re7zVolume  = regexp.MustCompile(`(?i)\.7z\.0*(\d+)$`)
)

// ArchiveKind names the archive format of a file by its name: "zip",
// "7z" or "rar" (the first volume of a split archive included), else "".
func ArchiveKind(name string) string {
	lower := strings.ToLower(name)
	switch {
	case IsVolumePart(name):
		return ""
	case strings.HasSuffix(lower, ".zip"):
		return "zip"
	case strings.HasSuffix(lower, ".7z"), re7zVolume.MatchString(lower):
		return "7z"
	case strings.HasSuffix(lower, ".rar"):
		return "rar"
	}
	return ""
}

// IsVolumePart reports a later volume of a split archive ("x.part2.rar",
// "x.r00", "x.7z.002"): the first volume's extraction reads it, it is no
// archive of its own.
func IsVolumePart(name string) bool {
	if m := reRarPart.FindStringSubmatch(name); m != nil {
		return m[1] != "1"
	}
	if m := re7zVolume.FindStringSubmatch(name); m != nil {
		return m[1] != "1"
	}
	return reOldVolume.MatchString(name)
}

// ArchiveBase is an archive's path without its extension and volume
// marker: "a/x.part1.rar" -> "a/x", "a/x.7z.001" -> "a/x".
func ArchiveBase(rel string) string {
	if b := re7zVolume.ReplaceAllString(rel, ""); b != rel {
		return b
	}
	if b := reRarPart.ReplaceAllString(rel, ""); b != rel {
		return b
	}
	return strings.TrimSuffix(rel, path.Ext(rel))
}
