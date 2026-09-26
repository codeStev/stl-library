package main

import (
	"log/slog"
	"os"
	"strings"

	"github.com/codeStev/stl-library/internal/adapters/authcrypto"
	"github.com/codeStev/stl-library/internal/adapters/google"
	"github.com/codeStev/stl-library/internal/adapters/passkey"
	"github.com/codeStev/stl-library/internal/adapters/secrets"
	"github.com/codeStev/stl-library/internal/adapters/sqlite"
	"github.com/codeStev/stl-library/internal/app"
)

// newAuth wires sign-in. PUBLIC_URL (the address users open the app at)
// turns on passkeys, and with GOOGLE_CLIENT_ID/GOOGLE_CLIENT_SECRET also
// Google sign-in.
func newAuth(store *sqlite.Store, keys *secrets.Keys) (*app.Auth, error) {
	public := strings.TrimRight(os.Getenv("PUBLIC_URL"), "/")
	pk, err := passkey.New(public, "STL Library")
	if err != nil {
		return nil, err
	}
	g := google.New(os.Getenv("GOOGLE_CLIENT_ID"), os.Getenv("GOOGLE_CLIENT_SECRET"), public)
	slog.Info("sign-in", "passkeys", pk.Enabled(), "google", g.Enabled())
	return &app.Auth{
		Accounts: store,
		Hasher:   authcrypto.Bcrypt{},
		Tokens:   authcrypto.JWT{Key: keys.Derive("stlib jwt v1", 32), Issuer: "stl-library"},
		TOTP:     authcrypto.TOTP{Issuer: "STL Library"},
		Sealer:   keys,
		Passkeys: pk,
		Google:   g,
	}, nil
}
