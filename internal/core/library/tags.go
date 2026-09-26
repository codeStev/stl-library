package library

import (
	"strings"
	"unicode/utf8"
)

// Limits for user tags.
const (
	MaxTags      = 30
	MaxTagLength = 40
)

// NormalizeTags trims tags, collapses inner whitespace, drops empty and
// over-long ones and duplicates (case-insensitive, the first spelling
// wins), keeping the order given, at most MaxTags.
func NormalizeTags(tags []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tags {
		t = strings.Join(strings.Fields(t), " ")
		k := strings.ToLower(t)
		if t == "" || utf8.RuneCountInString(t) > MaxTagLength || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, t)
		if len(out) == MaxTags {
			break
		}
	}
	return out
}
