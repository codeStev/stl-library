package passkey

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/fxamacker/cbor/v2"

	"github.com/codeStev/stl-library/internal/app"
	"github.com/codeStev/stl-library/internal/core/account"
)

var b64 = base64.RawURLEncoding

// authenticator is a minimal software passkey: an ES256 key, "none"
// attestation, and a signature counter the test controls.
type authenticator struct {
	key    *ecdsa.PrivateKey
	id     []byte
	origin string
	rpID   string
}

func newAuthenticator(t *testing.T, origin, rpID string) *authenticator {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &authenticator{key: k, id: []byte("credential-1"), origin: origin, rpID: rpID}
}

type publicKeyOptions struct {
	PublicKey struct {
		Challenge string `json:"challenge"`
	} `json:"publicKey"`
}

func (a *authenticator) clientData(t *testing.T, typ string, options []byte) []byte {
	var o publicKeyOptions
	if err := json.Unmarshal(options, &o); err != nil {
		t.Fatal(err)
	}
	cd, _ := json.Marshal(map[string]string{"type": typ, "challenge": o.PublicKey.Challenge, "origin": a.origin})
	return cd
}

func (a *authenticator) authData(flags byte, counter uint32, attested []byte) []byte {
	rp := sha256.Sum256([]byte(a.rpID))
	d := append(rp[:], flags)
	d = binary.BigEndian.AppendUint32(d, counter)
	return append(d, attested...)
}

// create answers navigator.credentials.create().
func (a *authenticator) create(t *testing.T, options []byte) []byte {
	pub, err := a.key.PublicKey.Bytes() // 0x04 | X | Y
	if err != nil {
		t.Fatal(err)
	}
	x, y := pub[1:33], pub[33:]
	cose, _ := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
	attested := make([]byte, 16) // AAGUID
	attested = binary.BigEndian.AppendUint16(attested, uint16(len(a.id)))
	attested = append(append(attested, a.id...), cose...)
	att, _ := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{},
		"authData": a.authData(0x45, 0, attested)}) // UP | UV | AT
	resp, _ := json.Marshal(map[string]any{"id": b64.EncodeToString(a.id), "rawId": b64.EncodeToString(a.id), "type": "public-key",
		"response": map[string]string{"attestationObject": b64.EncodeToString(att),
			"clientDataJSON": b64.EncodeToString(a.clientData(t, "webauthn.create", options))}})
	return resp
}

// get answers navigator.credentials.get() with the given counter.
func (a *authenticator) get(t *testing.T, options []byte, counter uint32) []byte {
	cd := a.clientData(t, "webauthn.get", options)
	ad := a.authData(0x05, counter, nil) // UP | UV
	h := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), h[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	resp, _ := json.Marshal(map[string]any{"id": b64.EncodeToString(a.id), "rawId": b64.EncodeToString(a.id), "type": "public-key",
		"response": map[string]string{"authenticatorData": b64.EncodeToString(ad), "clientDataJSON": b64.EncodeToString(cd),
			"signature": b64.EncodeToString(sig)}})
	return resp
}

func TestDisabledWithoutPublicURL(t *testing.T) {
	p, err := New("", "STL Library")
	if err != nil || p.Enabled() {
		t.Errorf("enabled without a URL: %v", err)
	}
	if _, err := New("stl.example.org", "x"); err == nil {
		t.Error("URL without scheme accepted")
	}
}

func TestRegisterAndSignIn(t *testing.T) {
	p, err := New("https://stl.example.org", "STL Library")
	if err != nil || !p.Enabled() {
		t.Fatal(err)
	}
	acc := account.Account{ID: "a1", Email: "me@example.org"}
	auth := newAuthenticator(t, "https://stl.example.org", "stl.example.org")

	opts, session, err := p.BeginRegistration(acc, nil)
	if err != nil {
		t.Fatal(err)
	}
	cred, err := p.FinishRegistration(acc, nil, session, auth.create(t, opts))
	if err != nil {
		t.Fatal(err)
	}
	if string(cred.ID) != "credential-1" {
		t.Errorf("credential id %q", cred.ID)
	}
	stored := []app.Credential{cred}

	// Registering the same authenticator again is excluded.
	opts, _, _ = p.BeginRegistration(acc, stored)
	var o struct {
		PublicKey struct {
			Exclude []struct{ ID string } `json:"excludeCredentials"`
		} `json:"publicKey"`
	}
	json.Unmarshal(opts, &o)
	if len(o.PublicKey.Exclude) != 1 {
		t.Errorf("exclusions: %s", opts)
	}

	opts, session, _ = p.BeginLogin(acc, stored)
	id, data, err := p.FinishLogin(acc, stored, session, auth.get(t, opts, 5))
	if err != nil {
		t.Fatal(err)
	}
	if string(id) != "credential-1" {
		t.Errorf("used %q", id)
	}
	stored[0].Data = data // the counter is now 5

	// A counter that goes backwards means a cloned authenticator.
	opts, session, _ = p.BeginLogin(acc, stored)
	if _, _, err := p.FinishLogin(acc, stored, session, auth.get(t, opts, 3)); err == nil {
		t.Error("cloned authenticator accepted")
	}
	opts, session, _ = p.BeginLogin(acc, stored)
	if _, _, err := p.FinishLogin(acc, stored, session, auth.get(t, opts, 6)); err != nil {
		t.Errorf("a higher counter is fine: %v", err)
	}

	// A response for another site's challenge doesn't count.
	evil := *auth
	evil.origin = "https://evil.example"
	opts, session, _ = p.BeginLogin(acc, stored)
	if _, _, err := p.FinishLogin(acc, stored, session, evil.get(t, opts, 9)); err == nil {
		t.Error("wrong origin accepted")
	}
	// Nor does a replayed response for an old challenge.
	old, _, _ := p.BeginLogin(acc, stored)
	_, session, _ = p.BeginLogin(acc, stored)
	if _, _, err := p.FinishLogin(acc, stored, session, auth.get(t, old, 10)); err == nil {
		t.Error("stale challenge accepted")
	}
}
