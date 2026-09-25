package convention

import (
	"reflect"
	"testing"
)

func TestCanonicalSegments(t *testing.T) {
	cases := []struct {
		d    Dims
		want []string
	}{
		{Dims{}, nil},
		{Dims{Scale: "75mm", Supports: "Supported", Fill: "Hollow"}, []string{"75mm", "Supported", "Hollow"}},
		{Dims{Scale: "32mm", Supports: "Supported", Format: "Lychee"}, []string{"32mm", "Supported Lychee"}},
		{Dims{Supports: "Supported", Density: "Beefed", Format: "Lychee"}, []string{"Supported Beefed Lychee"}},
		{Dims{Supports: "No Supports", Split: "Combined"}, []string{"No Supports Combined"}},
		{Dims{Supports: "No Supports", Split: "Parts"}, []string{"No Supports"}},
		{Dims{Supports: "No Supports", Extra: "Repaired"}, []string{"No Supports Repaired"}},
		{Dims{Scale: "178mm", Tech: "FDM"}, []string{"178mm", "FDM"}},
	}
	for _, c := range cases {
		if got := CanonicalSegments(c.d); !reflect.DeepEqual(got, c.want) {
			t.Errorf("CanonicalSegments(%+v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestParseSegmentRoundTripsEveryCanonicalSpelling(t *testing.T) {
	variants := []Dims{
		{Scale: "75mm", Supports: "Supported", Density: "Beefed", Format: "Lychee", Fill: "Solid", Tech: "Resin"},
		{Scale: "1-12", Supports: "No Supports", Split: "Combined", Extra: "Repaired"},
		{Scale: "Bust", Format: "STL"},
		{Supports: "Supported", Format: "Chitubox"},
	}
	for _, d := range variants {
		var levels []Dims
		for _, seg := range CanonicalSegments(d) {
			got, ok := ParseSegment(seg)
			if !ok {
				t.Fatalf("canonical segment %q did not parse", seg)
			}
			levels = append(levels, got)
		}
		if got := Merge(levels...); got != d {
			t.Errorf("round trip: got %+v, want %+v", got, d)
		}
	}
}

func TestParseSegmentIsStrict(t *testing.T) {
	for _, name := range []string{"Bell Head", "supported", "Pre-Supported", "32 mm", "Supported Lyche", "Supported  Lychee", "Hollowed", ""} {
		if d, ok := ParseSegment(name); ok {
			t.Errorf("ParseSegment(%q) accepted a non-canonical name: %+v", name, d)
		}
	}
}

func TestExtensions(t *testing.T) {
	if FormatOfExtension("LYS") != "Lychee" || FormatOfExtension("jpg") != "" {
		t.Error("FormatOfExtension")
	}
	if !Printable("STL") || Printable("jpg") {
		t.Error("Printable")
	}
	if !IsCategory("Enemies") || IsCategory("enemies") {
		t.Error("IsCategory is case-sensitive: only the canonical spelling counts")
	}
}
