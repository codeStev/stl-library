package authcrypto

import (
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/codeStev/stl-library/internal/app"
)

func TestBcrypt(t *testing.T) {
	b := Bcrypt{Cost: 4}
	h, err := b.Hash("correct horse battery")
	if err != nil || !b.Compare(h, "correct horse battery") || b.Compare(h, "wrong") {
		t.Errorf("bcrypt: %v", err)
	}
}

func TestJWTRoundTripAndTampering(t *testing.T) {
	j := JWT{Key: []byte("0123456789abcdef0123456789abcdef"), Issuer: "stl-library"}
	c := app.Claims{Subject: "acc-1", Role: "ADMIN", Version: 3, Factors: []string{"PASSWORD", "MFA"}, ID: "jti-1", Expires: time.Now().Add(time.Hour).Unix()}
	tok, err := j.Issue(c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := j.Parse(tok)
	if err != nil || got.Subject != "acc-1" || got.Version != 3 || !got.Full() || got.ID != "jti-1" {
		t.Errorf("parsed %+v %v", got, err)
	}
	parts := strings.Split(tok, ".")
	if _, err := j.Parse(parts[0] + "." + parts[1] + ".AAAA"); err == nil {
		t.Error("bad signature accepted")
	}
	other := JWT{Key: []byte("another-key-another-key-another-k"), Issuer: "stl-library"}
	if _, err := other.Parse(tok); err == nil {
		t.Error("other key accepted")
	}
	c.Expires = time.Now().Add(-time.Minute).Unix()
	old, _ := j.Issue(c)
	if _, err := j.Parse(old); err == nil {
		t.Error("expired token accepted")
	}
	// "alg: none" must never pass.
	none := "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0." + parts[1] + "."
	if _, err := j.Parse(none); err == nil {
		t.Error("alg none accepted")
	}
}

func TestTOTP(t *testing.T) {
	tt := TOTP{Issuer: "STL Library"}
	secret, url, qr, err := tt.NewSecret("me@example.org")
	if err != nil || !strings.HasPrefix(url, "otpauth://totp/") || !strings.HasPrefix(qr, "data:image/png;base64,") {
		t.Fatalf("%q %q %v", url, qr[:30], err)
	}
	now := time.Now()
	code, _ := totp.GenerateCode(secret, now)
	if !tt.Validate(secret, code, now) || !tt.Validate(secret, code, now.Add(25*time.Second)) {
		t.Error("valid code refused")
	}
	if tt.Validate(secret, code, now.Add(5*time.Minute)) || tt.Validate(secret, "000000", now) && code != "000000" {
		t.Error("wrong code accepted")
	}
}
