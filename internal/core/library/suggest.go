package library

import (
	"regexp"
	"sort"
	"strings"

	convention "github.com/codeStev/stl-convention"
)

// Fix is a suggested rename of one folder to its canonical spelling ("Unsupported" ->
// "No Supports"). From and To are paths relative to the library root; To differs from From in the last
// level only.
type Fix struct {
	From, To string
	Issues   int // how many folders of the issue list it would clear
}

var (
	reWords = regexp.MustCompile(`[^a-z0-9]+`)
	reMM    = regexp.MustCompile(`^\d+(?:\.\d+)?mm$`)
)

// Words that say something about the variant, per phrase of one or two words (in the form the words
// take after lowercasing and splitting at anything that is not a letter or digit).
var (
	supported   = map[string]bool{"presupported": true, "presupports": true, "presupport": true, "pre supported": true, "pre supports": true, "pre support": true, "supported": true, "supports": true, "support": true}
	unsupported = map[string]bool{"unsupported": true, "nosupport": true, "nosupports": true, "nonsupported": true, "no supports": true, "no support": true, "no supported": true, "non supported": true, "un supported": true, "unsupports": true}
	formats     = map[string]string{"lys": "Lychee", "lychee": "Lychee", "chitubox": "Chitubox", "stl": "STL", "stls": "STL", "obj": "OBJ", "3mf": "3MF"}
	fills       = map[string]string{"hollow": "Hollow", "hollowed": "Hollow", "solid": "Solid"}
	fillers     = map[string]bool{"files": true, "file": true}
)

// suggestSegment spells one folder name the canonical way - but only when every word of it is a
// known variant word and the result is exactly one folder level.
func suggestSegment(name string) (string, bool) {
	if _, ok := convention.ParseSegment(name); ok {
		return "", false // already canonical
	}
	words := strings.Fields(reWords.ReplaceAllString(strings.ToLower(name), " "))
	if len(words) == 0 {
		return "", false
	}
	var d convention.Dims
	setSupports := func(v string) bool {
		if d.Supports != "" && d.Supports != v {
			return false
		}
		d.Supports = v
		return true
	}
	for i := 0; i < len(words); i++ {
		w := words[i]
		two := ""
		if i+1 < len(words) {
			two = w + " " + words[i+1]
		}
		switch {
		case two != "" && supported[two]:
			if !setSupports("Supported") {
				return "", false
			}
			i++
		case two != "" && unsupported[two]:
			if !setSupports("No Supports") {
				return "", false
			}
			i++
		case supported[w]:
			if !setSupports("Supported") {
				return "", false
			}
		case unsupported[w]:
			if !setSupports("No Supports") {
				return "", false
			}
		case formats[w] != "":
			if d.Format != "" && d.Format != formats[w] {
				return "", false
			}
			d.Format = formats[w]
		case fills[w] != "":
			if d.Fill != "" && d.Fill != fills[w] {
				return "", false
			}
			d.Fill = fills[w]
		case reMM.MatchString(w):
			if d.Scale != "" && d.Scale != w {
				return "", false
			}
			d.Scale = w
		case fillers[w]:
		default:
			return "", false // a word that is not about the variant: probably a name
		}
	}
	segs := convention.CanonicalSegments(d)
	if len(segs) != 1 || segs[0] == name {
		return "", false
	}
	return segs[0], true
}

// Suggest proposes renames that would bring the folders of the issue list into line: each folder
// level of an issue directory that has a one-level canonical spelling. The most helpful first.
func Suggest(issueDirs []string) []Fix {
	found := map[string]*Fix{}
	for _, dir := range issueDirs {
		segs := strings.Split(dir, "/")
		for i := 1; i < len(segs); i++ { // the creator (level 0) is never renamed
			to, ok := suggestSegment(segs[i])
			if !ok {
				continue
			}
			from := strings.Join(segs[:i+1], "/")
			f := found[from]
			if f == nil {
				f = &Fix{From: from, To: strings.Join(segs[:i], "/") + "/" + to}
				found[from] = f
			}
			f.Issues++
		}
	}
	out := make([]Fix, 0, len(found))
	for _, f := range found {
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Issues != out[j].Issues {
			return out[i].Issues > out[j].Issues
		}
		return out[i].From < out[j].From
	})
	return out
}
