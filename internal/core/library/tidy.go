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
