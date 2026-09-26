package library

import (
	"path"
	"regexp"
	"sort"
	"strings"

	convention "github.com/codeStev/stl-convention"
)

// Read groups a listing into models following the convention:
//
//	<Creator>/[<Release>/][<Category>/]<Model>/[<variant levels>/][<Option>/]<parts>
//
// Variant levels are canonical folder names (convention.ParseSegment) in
// canonical order. Without variant levels, a level below
// Creator/Release/[Category]/Model is an option of the model
// ("Stonewurm Riders/Pose01"). Folders that can't be read this way are
// returned as issues, and their files are left out.
func Read(files []File) ([]*Model, []Issue) {
	r := reader{models: map[string]*Model{}, variants: map[string]*Variant{}, issues: map[string]string{}}
	var extras []File
	for _, f := range files {
		dir, name := path.Split(f.Path)
		dir = strings.TrimSuffix(dir, "/")
		if !convention.Printable(ext(name)) {
			extras = append(extras, f)
			continue
		}
		r.addPart(dir, f)
	}
	for _, f := range extras {
		r.addExtra(f)
	}
	return r.result()
}

type reader struct {
	models   map[string]*Model   // by model dir
	variants map[string]*Variant // by variant dir
	issues   map[string]string   // dir -> reason
}

// placement is where a folder of part files sits in the convention.
type placement struct {
	creator, release, category, model string
	modelDir, option                  string
	dims                              convention.Dims
}

func (r *reader) addPart(dir string, f File) {
	p, reason := place(dir)
	if reason != "" {
		r.issue(dir, reason)
		return
	}
	m := r.models[p.modelDir]
	if m == nil {
		m = &Model{Creator: p.creator, Release: p.release, Category: p.category, Name: p.model, Dir: p.modelDir}
		r.models[p.modelDir] = m
	}
	v := r.variants[dir]
	if v == nil {
		v = &Variant{Dims: p.dims, Option: p.option, Dir: dir}
		r.variants[dir] = v
		m.Variants = append(m.Variants, v)
	}
	v.Parts = append(v.Parts, f)
}

// addExtra files an image or document with the model whose folder holds
// it (at any depth). Extras outside any model are ignored.
func (r *reader) addExtra(f File) {
	for d := path.Dir(f.Path); d != "." && d != ""; d = path.Dir(d) {
		if m, ok := r.models[d]; ok {
			m.Images = append(m.Images, f)
			return
		}
	}
}

func (r *reader) issue(dir, reason string) {
	if _, ok := r.issues[dir]; !ok {
		r.issues[dir] = reason
	}
}

func (r *reader) result() ([]*Model, []Issue) {
	models := make([]*Model, 0, len(r.models))
	for _, m := range r.models {
		sort.Slice(m.Variants, func(i, j int) bool { return m.Variants[i].Dir < m.Variants[j].Dir })
		for _, v := range m.Variants {
			sort.Slice(v.Parts, func(i, j int) bool { return v.Parts[i].Path < v.Parts[j].Path })
		}
		sort.Slice(m.Images, func(i, j int) bool { return m.Images[i].Path < m.Images[j].Path })
		models = append(models, m)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Dir < models[j].Dir })
	issues := make([]Issue, 0, len(r.issues))
	for d, why := range r.issues {
		issues = append(issues, Issue{Dir: d, Reason: why})
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Dir < issues[j].Dir })
	return models, issues
}

// place reads the folder levels of a folder holding part files.
func place(dir string) (placement, string) {
	if dir == "" {
		return placement{}, "print files directly in the library root"
	}
	segs := strings.Split(dir, "/")
	// Trailing variant levels, or variant levels followed by one option
	// ("<Model>/Supported/Helmet Version").
	levels, start := variantRun(segs, len(segs))
	option := ""
	if len(levels) == 0 && len(segs) >= 3 {
		if lv, st := variantRun(segs, len(segs)-1); len(lv) > 0 {
			levels, start, option = lv, st, segs[len(segs)-1]
		}
	}
	if reason := checkOrder(levels); reason != "" {
		return placement{}, reason
	}
	p := placement{option: option, dims: convention.Merge(levels...)}
	names := segs[:start]
	if option == "" && len(levels) == 0 {
		// No variant levels: the last level is an option only if the path
		// is deeper than Creator/Release/[Category]/Model.
		max := 3
		if len(names) >= 3 && convention.IsCategory(names[2]) || len(names) >= 2 && convention.IsCategory(names[1]) {
			max = 4
		}
		if len(names) > max {
			p.option, names = names[len(names)-1], names[:len(names)-1]
		}
	}
	for _, n := range append(append([]string{}, names...), p.option) {
		if looksLikeVariant(n) {
			return placement{}, "folder " + quote(n) + " looks like a variant level but isn't spelled canonically"
		}
	}
	switch {
	case len(names) < 2:
		return placement{}, "print files directly in a creator folder"
	case len(names) == 2:
		p.creator, p.model = names[0], names[1]
	case len(names) == 3 && convention.IsCategory(names[1]):
		p.creator, p.category, p.model = names[0], names[1], names[2]
	case len(names) == 3:
		p.creator, p.release, p.model = names[0], names[1], names[2]
	case len(names) == 4 && convention.IsCategory(names[2]):
		p.creator, p.release, p.category, p.model = names[0], names[1], names[2], names[3]
	default:
		return placement{}, "too many folder levels above the model (expected <Creator>/<Release>/[<Category>]/<Model>)"
	}
	p.modelDir = strings.Join(names, "/")
	return p, ""
}

// variantRun returns the canonical variant levels ending just before
// segs[end], and the index where they start. Level 0 (the creator) never
// counts.
func variantRun(segs []string, end int) ([]convention.Dims, int) {
	var levels []convention.Dims
	start := end
	for i := end - 1; i >= 1; i-- {
		d, ok := convention.ParseSegment(segs[i])
		if !ok {
			break
		}
		levels = append([]convention.Dims{d}, levels...)
		start = i
	}
	return levels, start
}

// checkOrder enforces the canonical order scale -> supports -> fill -> tech,
// each at most once.
func checkOrder(levels []convention.Dims) string {
	rank := func(d convention.Dims) int {
		switch {
		case d.Scale != "":
			return 0
		case d.Fill != "":
			return 2
		case d.Tech != "":
			return 3
		}
		return 1
	}
	last := -1
	for _, d := range levels {
		r := rank(d)
		if r <= last {
			return "variant levels out of order or repeated (expected scale, supports, fill, tech)"
		}
		last = r
	}
	return ""
}

// reVariantish matches names that describe a variant in a non-canonical
// spelling ("Presupported", "32mm_Supported", "LYS", "Unsupported").
var reVariantish = regexp.MustCompile(`(?i)(^|[\s_\-])(pre-?supported|presupports?|un-?supported|supports?|supported|lys|lychee|chitubox|stl|\d+\s?mm|hollowed?|solid)($|[\s_\-])`)

func looksLikeVariant(name string) bool { return reVariantish.MatchString(name) }

func ext(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return ""
}

func quote(s string) string { return `"` + s + `"` }
