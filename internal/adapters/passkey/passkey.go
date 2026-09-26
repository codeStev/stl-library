// Package passkey runs WebAuthn (passkey) ceremonies with go-webauthn.
package passkey

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/codeStev/stl-library/internal/app"
	"github.com/codeStev/stl-library/internal/core/account"
)

// Passkeys implements app.Passkeys. The zero value (no public URL) is
// disabled.
type Passkeys struct {
	w *webauthn.WebAuthn
}

var _ app.Passkeys = (*Passkeys)(nil)

// New configures passkeys for the app's public URL (e.g.
// "https://stl.example.org"): the relying party is its host name, and
// only that origin is accepted. An empty URL disables passkeys.
func New(publicURL, displayName string) (*Passkeys, error) {
	if publicURL == "" {
		return &Passkeys{}, nil
	}
	u, err := url.Parse(publicURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("PUBLIC_URL %q must be like https://stl.example.org", publicURL)
	}
	w, err := webauthn.New(&webauthn.Config{
		RPID: u.Hostname(), RPDisplayName: displayName, RPOrigins: []string{u.Scheme + "://" + u.Host},
	})
	if err != nil {
		return nil, err
	}
	return &Passkeys{w: w}, nil
}

func (p *Passkeys) Enabled() bool { return p != nil && p.w != nil }

// user adapts an account to go-webauthn.
type user struct {
	a     account.Account
	creds []webauthn.Credential
}

// WebAuthnID: an opaque handle derived from the account id (never the
// email).
func (u user) WebAuthnID() []byte {
	h := sha256.Sum256([]byte("stlib-user:" + u.a.ID))
	return h[:]
}
func (u user) WebAuthnName() string                       { return u.a.Email }
func (u user) WebAuthnDisplayName() string                { return u.a.Email }
func (u user) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func decode(creds []app.Credential) ([]webauthn.Credential, error) {
	out := make([]webauthn.Credential, 0, len(creds))
	for _, c := range creds {
		var wc webauthn.Credential
		if err := json.Unmarshal(c.Data, &wc); err != nil {
			return nil, fmt.Errorf("stored passkey: %w", err)
		}
		out = append(out, wc)
	}
	return out, nil
}

func (p *Passkeys) BeginRegistration(a account.Account, existing []app.Credential) ([]byte, []byte, error) {
	creds, err := decode(existing)
	if err != nil {
		return nil, nil, err
	}
	u := user{a: a, creds: creds}
	var exclude []protocol.CredentialDescriptor
	for _, c := range creds {
		exclude = append(exclude, c.Descriptor())
	}
	creation, session, err := p.w.BeginRegistration(u, webauthn.WithExclusions(exclude),
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred))
	if err != nil {
		return nil, nil, err
	}
	opts, err := json.Marshal(creation)
	if err != nil {
		return nil, nil, err
	}
	s, err := json.Marshal(session)
	return opts, s, err
}

func (p *Passkeys) FinishRegistration(a account.Account, existing []app.Credential, session, response []byte) (app.Credential, error) {
	creds, err := decode(existing)
	if err != nil {
		return app.Credential{}, err
	}
	var sd webauthn.SessionData
	if err := json.Unmarshal(session, &sd); err != nil {
		return app.Credential{}, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(response)
	if err != nil {
		return app.Credential{}, err
	}
	cred, err := p.w.CreateCredential(user{a: a, creds: creds}, sd, parsed)
	if err != nil {
		return app.Credential{}, err
	}
	data, err := json.Marshal(cred)
	return app.Credential{ID: cred.ID, Data: data}, err
}

func (p *Passkeys) BeginLogin(a account.Account, stored []app.Credential) ([]byte, []byte, error) {
	creds, err := decode(stored)
	if err != nil {
		return nil, nil, err
	}
	assertion, session, err := p.w.BeginLogin(user{a: a, creds: creds})
	if err != nil {
		return nil, nil, err
	}
	opts, err := json.Marshal(assertion)
	if err != nil {
		return nil, nil, err
	}
	s, err := json.Marshal(session)
	return opts, s, err
}

// FinishLogin verifies the assertion and returns the used credential with
// its updated signature counter (clone detection).
func (p *Passkeys) FinishLogin(a account.Account, stored []app.Credential, session, response []byte) ([]byte, []byte, error) {
	creds, err := decode(stored)
	if err != nil {
		return nil, nil, err
	}
	var sd webauthn.SessionData
	if err := json.Unmarshal(session, &sd); err != nil {
		return nil, nil, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(response)
	if err != nil {
		return nil, nil, err
	}
	cred, err := p.w.ValidateLogin(user{a: a, creds: creds}, sd, parsed)
	if err != nil {
		return nil, nil, err
	}
	if cred.Authenticator.CloneWarning {
		return nil, nil, fmt.Errorf("this passkey's signature counter went backwards - possibly a cloned authenticator")
	}
	data, err := json.Marshal(cred)
	return cred.ID, data, err
}
