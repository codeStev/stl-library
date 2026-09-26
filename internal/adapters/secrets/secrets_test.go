package secrets

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestGeneratedOnceThenReused(t *testing.T) {
	dir := t.TempDir()
	a, err := Load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(filepath.Join(dir, "secret.key"))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("secret file mode %v", info.Mode())
	}
	b, _ := Load("", dir)
	if !bytes.Equal(a.Derive("x", 32), b.Derive("x", 32)) {
		t.Error("second load derived different keys")
	}
	if bytes.Equal(a.Derive("x", 32), a.Derive("y", 32)) {
		t.Error("purposes share a key")
	}
	if _, err := Load("too short", dir); err == nil {
		t.Error("short APP_SECRET accepted")
	}
}

func TestSealAndOpen(t *testing.T) {
	k, _ := New([]byte("0123456789abcdef0123456789abcdef"))
	s1, _ := k.Seal("JBSWY3DPEHPK3PXP")
	s2, _ := k.Seal("JBSWY3DPEHPK3PXP")
	if s1 == s2 || s1 == "JBSWY3DPEHPK3PXP" {
		t.Errorf("sealing is not randomized: %s %s", s1, s2)
	}
	if p, err := k.Open(s1); err != nil || p != "JBSWY3DPEHPK3PXP" {
		t.Errorf("open: %q %v", p, err)
	}
	other, _ := New([]byte("another-secret-another-secret-xx"))
	if _, err := other.Open(s1); err == nil {
		t.Error("opened with another secret")
	}
	if e, _ := k.Seal(""); e != "" {
		t.Error("empty value sealed")
	}
}
