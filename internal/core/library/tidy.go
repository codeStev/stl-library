package library

import (
	"regexp"
	"strings"
)

// IsJunkFile reports whether a file is operating-system litter that belongs to no model: Finder's
// .DS_Store, the "._name" resource forks of macOS archives, Windows' Thumbs.db and desktop.ini.
func IsJunkFile(name string) bool {
	switch strings.ToLower(name) {
	case ".ds_store", "thumbs.db", "desktop.ini":
		return true
	}
	return strings.HasPrefix(name, "._") && len(name) > 2
}

// IsJunkFolder reports whether a folder is litter that archives made on a Mac carry along.
func IsJunkFolder(name string) bool { return name == "__MACOSX" }

var reCopy = regexp.MustCompile(`^(.*\S) \((?:imported|\d+)\)(\.[^./]*)?$`)

// CopyOf recognises the name of a file that was written next to an existing one of the same name:
// "Head (imported).stl", "Head (2).stl". It returns the name the original would have ("Head.stl").
func CopyOf(name string) (original string, ok bool) {
	m := reCopy.FindStringSubmatch(name)
	if m == nil {
		return "", false
	}
	return m[1] + m[2], true
}

var stemNoise = map[string]bool{
	"sup": true, "sups": true, "support": true, "supports": true, "supported": true, "presupported": true, "presup": true,
	"pre": true, "unsupported": true, "nosup": true, "nosupport": true, "nosupports": true, "unsup": true, "no": true,
	"lychee": true, "lys": true, "lyt": true, "chitubox": true, "ctb": true, "goo": true, "stl": true, "hollow": true,
	"hollowed": true, "solid": true, "fdm": true, "resin": true, "combined": true,
}

var (
	reStemSplit = regexp.MustCompile(`[^\p{L}\p{N}]+`)
	reStemScale = regexp.MustCompile(`^\d+(mm|cm)$`)
)

// StemKey reduces a file name to what identifies the model part behind it: the extension, the words of
// supports and slicers ("Supported", "SUP", "Lychee"), hollow/solid and a scale ("32mm") are dropped, so
// "Arm_left_SUP.lys" and "Arm_left.stl" give the same key. "" when nothing is left.
func StemKey(name string) string {
	if i := strings.LastIndex(name, "."); i > 0 && len(name)-i <= 9 {
		name = name[:i]
	}
	var keep []string
	for _, w := range reStemSplit.Split(strings.ToLower(name), -1) {
		if w == "" || stemNoise[w] || reStemScale.MatchString(w) {
			continue
		}
		keep = append(keep, w)
	}
	return strings.Join(keep, "")
}
