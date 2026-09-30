package library

import "testing"

func TestJunkNames(t *testing.T) {
	for n, want := range map[string]bool{
		".DS_Store": true, "Thumbs.db": true, "THUMBS.DB": true, "desktop.ini": true, "._Head.stl": true,
		"._": false, "Head.stl": false, ".gitignore": false, "notes.txt": false,
	} {
		if IsJunkFile(n) != want {
			t.Errorf("IsJunkFile(%q) != %v", n, want)
		}
	}
	if !IsJunkFolder("__MACOSX") || IsJunkFolder("_duplicates") {
		t.Error("junk folder")
	}
}

func TestCopyOf(t *testing.T) {
	for in, want := range map[string]string{
		"Head (imported).stl": "Head.stl", "Head (2).stl": "Head.stl", "Head (12).ctb": "Head.ctb",
		"Head (imported)": "Head", "Base Full (3).stl": "Base Full.stl",
		"Head.stl": "", "Head (Supported).stl": "", "(2).stl": "",
	} {
		got, ok := CopyOf(in)
		if got != want || ok != (want != "") {
			t.Errorf("CopyOf(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}
