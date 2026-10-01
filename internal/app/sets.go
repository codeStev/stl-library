package app

import (
	"context"
	"path"
	"regexp"
	"sort"
	"strings"
)

// SetVariant is one variant with the model files in it, for comparing the variants of a model.
type SetVariant struct {
	ModelID   int64
	ModelName string
	Dir       string
	Key       string // the variant's dimensions without the supports, so variants differing only there share it
	Supports  string
	Label     string // what to show for the variant
	Files     []string
}

// SetReader is implemented by stores that can list every variant with its model files.
type SetReader interface {
	SetVariants(ctx context.Context) ([]SetVariant, error)
}

// SetSide is one variant of a gap.
type SetSide struct {
	Label string
	Dir   string
	Files int
}

// SetGap is a model whose variants that should hold the same parts (they differ only in the
// supports) hold a different number of them: probably a download that lost files.
type SetGap struct {
	ModelID   int64
	ModelName string
	Sides     []SetSide
	// Missing are files of the fullest variant that no file of the others matches by name.
	Missing []string
}

// maxMissingShown bounds the file names listed per gap.
const maxMissingShown = 8

var supportWords = regexp.MustCompile(`(?i)(non|no|un|pre)?[ _\-]?supports?(ed)?|(^|[^a-z0-9])sup([^a-z0-9]|$)`)

// partKey is a file name made comparable across supports variants: "Head_Supported.stl" and
// "head.stl" are the same part.
func partKey(file string) string {
	name := strings.TrimSuffix(path.Base(file), path.Ext(file))
	name = supportWords.ReplaceAllString(name, " ")
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// FindSetGaps compares, inside each model, the variants that differ only in their supports.
func FindSetGaps(vs []SetVariant) []SetGap {
	type groupKey struct {
		model int64
		key   string
	}
	groups := map[groupKey][]SetVariant{}
	var order []groupKey
	for _, v := range vs {
		k := groupKey{v.ModelID, v.Key}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], v)
	}
	var out []SetGap
	for _, k := range order {
		g := groups[k]
		supports := map[string]bool{}
		most, least := 0, int(^uint(0)>>1)
		for _, v := range g {
			supports[v.Supports] = true
			most, least = max(most, len(v.Files)), min(least, len(v.Files))
		}
		if len(g) < 2 || len(supports) < 2 || most == least {
			continue
		}
		sort.SliceStable(g, func(i, j int) bool { return len(g[i].Files) > len(g[j].Files) })
		gap := SetGap{ModelID: g[0].ModelID, ModelName: g[0].ModelName}
		for _, v := range g {
			gap.Sides = append(gap.Sides, SetSide{Label: v.Label, Dir: v.Dir, Files: len(v.Files)})
		}
		have := map[string]bool{}
		for _, v := range g[1:] {
			for _, f := range v.Files {
				have[partKey(f)] = true
			}
		}
		for _, f := range g[0].Files {
			if !have[partKey(f)] && len(gap.Missing) < maxMissingShown {
				gap.Missing = append(gap.Missing, path.Base(f))
			}
		}
		out = append(out, gap)
	}
	sort.SliceStable(out, func(i, j int) bool {
		di := out[i].Sides[0].Files - out[i].Sides[len(out[i].Sides)-1].Files
		dj := out[j].Sides[0].Files - out[j].Sides[len(out[j].Sides)-1].Files
		return di > dj
	})
	return out
}
