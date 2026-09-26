// Package google signs users in with Google (OpenID Connect, authorization
// code flow with PKCE).
package google

import (
	"context"
	"errors"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/codeStev/stl-library/internal/app"
)

const issuer = "https://accounts.google.com"

// endpoint is Google's (as published in its discovery document; hard-coded
// so building the auth URL never needs the network).
var endpoint = oauth2.Endpoint{
	AuthURL:   "https://accounts.google.com/o/oauth2/v2/auth",
	TokenURL:  "https://oauth2.googleapis.com/token",
	AuthStyle: oauth2.AuthStyleInParams,
}

// CallbackPath is where Google sends the browser back to.
const CallbackPath = "/login/oauth2/code/google"

// Google implements app.OIDC. It is disabled unless a client id, secret
// and the app's public URL are configured.
type Google struct {
	conf oauth2.Config

	once     sync.Once
	verifier *oidc.IDTokenVerifier
	err      error
}

var _ app.OIDC = (*Google)(nil)

// New configures Google sign-in; any empty value disables it.
func New(clientID, clientSecret, publicURL string) *Google {
	if clientID == "" || clientSecret == "" || publicURL == "" {
		return &Google{}
	}
	return &Google{conf: oauth2.Config{
		ClientID: clientID, ClientSecret: clientSecret, Endpoint: endpoint,
		RedirectURL: publicURL + CallbackPath, Scopes: []string{oidc.ScopeOpenID, "email"},
	}}
}

func (g *Google) Enabled() bool { return g != nil && g.conf.ClientID != "" }

func (g *Google) AuthURL(state, verifier string) string {
	return g.conf.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
}

// idVerifier discovers Google's keys once (lazily, so startup never needs
// the network).
func (g *Google) idVerifier(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	g.once.Do(func() {
		p, err := oidc.NewProvider(context.WithoutCancel(ctx), issuer)
		if err != nil {
			g.err = err
			return
		}
		g.verifier = p.Verifier(&oidc.Config{ClientID: g.conf.ClientID})
	})
	if g.err != nil {
		err := g.err
		g.once, g.err = sync.Once{}, nil // try again next time
		return nil, err
	}
	return g.verifier, nil
}

func (g *Google) Exchange(ctx context.Context, code, verifier string) (app.Identity, error) {
	v, err := g.idVerifier(ctx)
	if err != nil {
		return app.Identity{}, err
	}
	tok, err := g.conf.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return app.Identity{}, err
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		return app.Identity{}, errors.New("google returned no id token")
	}
	idt, err := v.Verify(ctx, raw)
	if err != nil {
		return app.Identity{}, err
	}
	var c struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := idt.Claims(&c); err != nil {
		return app.Identity{}, err
	}
	return app.Identity{Subject: idt.Subject, Email: c.Email, EmailVerified: c.EmailVerified}, nil
}
