// Package convention defines the folder convention a library must follow
// to be read by stl-library:
//
//	<Creator>/<Release>/[<Category>]/<Model>/[<Scale>]/<Supports…>/[Hollow|Solid]/[<Option>/]<parts>
//
// It is the single source of truth for how a variant is spelled as folder
// levels. The package is pure (no I/O) and public, so other tools - for
// example a migration script for an existing library - can produce folders
// the app will recognize.
package convention

import (
	"regexp"
	"strings"
)

// Dims are the variant dimensions of a model. "" means not present.
type Dims struct {
	Scale    string // "32mm", "75mm", "Bust", "1-12", "Freescale", "Heroic"
	Supports string // "Supported", "No Supports"
	Density  string // "Beefed", "Light"
	Format   string // "Lychee", "Chitubox", "STL", "OBJ", "3MF"
	Fill     string // "Hollow", "Solid"
	Split    string // "Parts", "Combined"
	Tech     string // "FDM", "Resin"
	Extra    string // "Repaired", "Original"
}

func (d Dims) Any() bool { return d != Dims{} }

// Canonical values per dimension, in the spelling used for folder names.
var (
	Supports  = []string{"Supported", "No Supports"}
	Densities = []string{"Beefed", "Light"}
	Formats   = []string{"Lychee", "Chitubox", "STL", "OBJ", "3MF"}
	Fills     = []string{"Hollow", "Solid"}
	Techs     = []string{"FDM", "Resin"}
	Extras    = []string{"Repaired", "Original"}
	// NamedScales are the scales that aren't a number.
	NamedScales = []string{"Bust", "Freescale", "Heroic"}
	// Categories group models within a release (optional level).
	Categories = []string{"Heroes", "Enemies", "Busts", "Environments", "Objects", "Terrain", "Monsters",
		"NPCs", "Props", "Bases", "Scenery", "Minions", "Characters", "Creatures", "Villains", "Allies",
		"Bosses", "Weapons", "Accessories", "Mounts", "Maps"}
)

var (
	reMMScale    = regexp.MustCompile(`^\d+(\.\d+)?mm$`)
	reRatioScale = regexp.MustCompile(`^1-\d{1,2}$`)
)

// CanonicalSegments spells out a variant as folder levels: scale, then
// supports (with density, format, "Combined" and extras appended), then
// fill, then tech. Empty dimensions are left out - a model with no
// variant information has no variant levels at all.
func CanonicalSegments(d Dims) []string {
	var segs []string
	if d.Scale != "" {
		segs = append(segs, d.Scale)
	}
	var sup []string
	for _, v := range []string{d.Supports, d.Density, d.Format, combined(d.Split), d.Extra} {
		if v != "" {
			sup = append(sup, v)
		}
	}
	if len(sup) > 0 {
		segs = append(segs, strings.Join(sup, " "))
	}
	if d.Fill != "" {
		segs = append(segs, d.Fill)
	}
	if d.Tech != "" {
		segs = append(segs, d.Tech)
	}
	return segs
}

// Parts is the default and needs no folder of its own; only combined
// (one-piece) files are set apart.
func combined(split string) string {
	if split == "Combined" {
		return "Combined"
	}
	return ""
}

// ParseSegment reads one canonical variant folder name back into the
// dimensions it sets. It is strict: only exact canonical spellings are
// accepted, anything else (a model name, a misspelling) returns ok=false.
func ParseSegment(name string) (Dims, bool) {
	var d Dims
	switch {
	case reMMScale.MatchString(name), reRatioScale.MatchString(name), contains(NamedScales, name):
		d.Scale = name
		return d, true
	case contains(Fills, name):
		d.Fill = name
		return d, true
	case contains(Techs, name):
		d.Tech = name
		return d, true
	}
	// "<Supports>[ <Density>][ <Format>][ Combined][ <Extra>]", or a lone
	// format/extra/"Combined" where the supports aren't known.
	rest := name
	for _, s := range Supports {
		if rest == s || strings.HasPrefix(rest, s+" ") {
			d.Supports, rest = s, strings.TrimPrefix(strings.TrimPrefix(rest, s), " ")
			break
		}
	}
	take := func(options []string, set func(string)) {
		for _, o := range options {
			if rest == o || strings.HasPrefix(rest, o+" ") {
				set(o)
				rest = strings.TrimPrefix(strings.TrimPrefix(rest, o), " ")
				return
			}
		}
	}
	take(Densities, func(v string) { d.Density = v })
	take(Formats, func(v string) { d.Format = v })
	take([]string{"Combined"}, func(v string) { d.Split = v })
	take(Extras, func(v string) { d.Extra = v })
	if rest != "" || !d.Any() {
		return Dims{}, false
	}
	return d, true
}

// Merge combines the dimensions of several levels; a later (deeper) level
// wins where both set the same dimension.
func Merge(levels ...Dims) Dims {
	var out Dims
	for _, d := range levels {
		set := func(dst *string, v string) {
			if v != "" {
				*dst = v
			}
		}
		set(&out.Scale, d.Scale)
		set(&out.Supports, d.Supports)
		set(&out.Density, d.Density)
		set(&out.Format, d.Format)
		set(&out.Fill, d.Fill)
		set(&out.Split, d.Split)
		set(&out.Tech, d.Tech)
		set(&out.Extra, d.Extra)
	}
	return out
}

// IsCategory reports whether a folder name is a canonical category.
func IsCategory(name string) bool { return contains(Categories, name) }

// FormatOfExtension maps a file extension (without dot, any case) to the
// format dimension, or "" for extensions that don't define one.
func FormatOfExtension(ext string) string {
	switch strings.ToLower(ext) {
	case "lys", "lyt":
		return "Lychee"
	case "chitubox", "ctb", "cbddlp":
		return "Chitubox"
	case "stl":
		return "STL"
	case "obj":
		return "OBJ"
	case "3mf":
		return "3MF"
	}
	return ""
}

// Printable reports model and sliced print files - the files that make a
// folder a model or variant folder.
func Printable(ext string) bool {
	switch strings.ToLower(ext) {
	case "stl", "lys", "lyt", "chitubox", "ctb", "cbddlp", "obj", "3mf", "goo", "pwmx", "photon":
		return true
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
