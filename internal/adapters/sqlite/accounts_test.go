package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/codeStev/stl-library/internal/app"
	"github.com/codeStev/stl-library/internal/core/account"
)

func TestAccountStore(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	first, err := s.CreateAccount(ctx, account.Account{ID: "a1", Email: "a@x.org", Role: account.User, Enabled: true})
	if err != nil || first.Role != account.Admin {
		t.Fatalf("first account: %+v %v", first, err)
	}
	second, _ := s.CreateAccount(ctx, account.Account{ID: "a2", Email: "b@x.org", Role: account.User, Enabled: true})
	if second.Role != account.User {
		t.Errorf("second account is %s", second.Role)
	}
	if _, err := s.CreateAccount(ctx, account.Account{ID: "a3", Email: "a@x.org"}); err != app.ErrExists {
		t.Errorf("duplicate email: %v", err)
	}
	if _, err := s.CreateFirstAccount(ctx, account.Account{ID: "a4", Email: "c@x.org"}); err != app.ErrExists {
		t.Errorf("second 'first' account: %v", err)
	}
	// Google accounts added by an admin wait without a subject.
	for _, id := range []string{"p1", "p2"} {
		if _, err := s.CreateAccount(ctx, account.Account{ID: id, Email: id + "@x.org", AuthProvider: "google"}); err != nil {
			t.Fatalf("placeholder %s: %v", id, err)
		}
	}
	g1 := account.Account{ID: "g1", Email: "g@x.org", AuthProvider: "google", ExternalSubject: "sub", Enabled: true}
	if _, err := s.CreateAccount(ctx, g1); err != nil {
		t.Fatal(err)
	}
	if a, err := s.AccountByExternal(ctx, "google", "sub"); err != nil || a.ID != "g1" {
		t.Errorf("by external: %+v %v", a, err)
	}
	if _, err := s.AccountByEmail(ctx, "nobody@x.org"); err != app.ErrNotFound {
		t.Errorf("missing: %v", err)
	}

	first.MFA, first.TokenVersion, first.TOTPSecret = account.MFATOTP, 3, "v1:sealed"
	if err := s.SaveAccount(ctx, first); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.AccountByID(ctx, "a1"); a.MFA != account.MFATOTP || a.TokenVersion != 3 || a.Role != account.Admin {
		t.Errorf("saved: %+v", a)
	}
	if all, _ := s.ListAccounts(ctx); len(all) != 5 {
		t.Errorf("roster: %d", len(all))
	}

	s.ReplaceRecoveryCodes(ctx, "a1", []string{"h1", "h2"})
	codes, _ := s.RecoveryCodes(ctx, "a1")
	if len(codes) != 2 {
		t.Fatalf("codes: %v", codes)
	}
	if ok, _ := s.UseRecoveryCode(ctx, codes[0].ID); !ok {
		t.Error("use failed")
	}
	if ok, _ := s.UseRecoveryCode(ctx, codes[0].ID); ok {
		t.Error("used twice")
	}
	if codes, _ = s.RecoveryCodes(ctx, "a1"); len(codes) != 1 {
		t.Errorf("unused: %v", codes)
	}

	s.AddSession(ctx, app.Session{ID: "s1", AccountID: "a1", CreatedUnix: 100, ExpiresUnix: 200})
	s.AddSession(ctx, app.Session{ID: "s2", AccountID: "a1", CreatedUnix: 110, ExpiresUnix: 210})
	if err := s.RevokeSession(ctx, "a2", "s1", 120); err != app.ErrNotFound {
		t.Errorf("revoked someone else's session: %v", err)
	}
	s.RevokeSession(ctx, "a1", "s1", 120)
	if list, _ := s.Sessions(ctx, "a1", 150); len(list) != 1 || list[0].ID != "s2" {
		t.Errorf("sessions: %+v", list)
	}
	if x, _ := s.Session(ctx, "s1"); x.RevokedUnix != 120 {
		t.Errorf("revoked: %+v", x)
	}

	s.AddCredential(ctx, "a1", app.Credential{ID: []byte{1, 2}, Data: []byte("{}"), Name: "Phone", CreatedUnix: 5})
	if err := s.UpdateCredential(ctx, "a2", []byte{1, 2}, []byte("x")); err != app.ErrNotFound {
		t.Errorf("updated someone else's passkey: %v", err)
	}
	s.UpdateCredential(ctx, "a1", []byte{1, 2}, []byte(`{"n":1}`))
	if c, _ := s.Credentials(ctx, "a1"); len(c) != 1 || string(c[0].Data) != `{"n":1}` {
		t.Errorf("credentials: %+v", c)
	}
	if err := s.DeleteCredential(ctx, "a1", []byte{1, 2}); err != nil {
		t.Error(err)
	}

	s.PutEphemeral(ctx, "k", []byte("v"), 100)
	s.PutEphemeral(ctx, "old", []byte("v"), 10)
	if v, ok, err := s.TakeEphemeral(ctx, "k", 50); !ok || string(v) != "v" || err != nil {
		t.Errorf("take: %q %v %v", v, ok, err)
	}
	if _, ok, _ := s.TakeEphemeral(ctx, "k", 50); ok {
		t.Error("taken twice")
	}
	if _, ok, _ := s.TakeEphemeral(ctx, "old", 50); ok {
		t.Error("expired value returned")
	}
}
