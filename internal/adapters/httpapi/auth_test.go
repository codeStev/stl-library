package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"

	"github.com/codeStev/stl-library/internal/adapters/authcrypto"
	"github.com/codeStev/stl-library/internal/adapters/secrets"
	"github.com/codeStev/stl-library/internal/adapters/sqlite"
	"github.com/codeStev/stl-library/internal/app"
)

// authServer serves the API with sign-in on (real store and crypto).
func authServer(t *testing.T, configure ...func(*app.Auth)) *httptest.Server {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	keys, _ := secrets.New([]byte(strings.Repeat("k", 32)))
	auth := &app.Auth{Accounts: store, Hasher: authcrypto.Bcrypt{Cost: bcrypt.MinCost},
		Tokens: authcrypto.JWT{Key: keys.Derive("jwt", 32), Issuer: "stlib"}, TOTP: authcrypto.TOTP{Issuer: "STL Library"},
		Sealer: keys}
	for _, c := range configure {
		c(auth)
	}
	notes := auth.Notify
	if notes == nil {
		notes = &app.Notifications{Store: store, Sealer: keys, Channels: func(app.NotificationSettings) []app.Notifier { return nil }}
	} else {
		notes.Store, notes.Sealer = store, keys
	}
	api := &API{Store: store, User: app.UserData{Store: store}, Auth: auth, Notifications: notes}
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	return srv
}

type browser struct {
	t   *testing.T
	srv *httptest.Server
	c   *http.Client
}

func newBrowser(t *testing.T, srv *httptest.Server) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t, srv, &http.Client{Jar: jar}}
}

// do sends a JSON request (with an optional Bearer token) and decodes a
// JSON answer into out.
func (b *browser) do(method, path, bearer string, body, out any) int {
	b.t.Helper()
	var rd io.Reader
	if body != nil {
		j, _ := json.Marshal(body)
		rd = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, b.srv.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode < 300 {
		if err := json.Unmarshal(raw, out); err != nil {
			b.t.Fatalf("%s %s: %v in %s", method, path, err, raw)
		}
	}
	return resp.StatusCode
}

type creds map[string]string

// enroll registers and signs in an account with TOTP; it returns the TOTP
// secret and the recovery codes.
func enroll(t *testing.T, b *browser, email, password string) (string, []string) {
	t.Helper()
	if code := b.do("POST", "/api/accounts/register", "", creds{"email": email, "password": password}, nil); code != 202 {
		t.Fatalf("register: %d", code)
	}
	var lr loginResultJSON
	if code := b.do("POST", "/api/auth/login", "", creds{"email": email, "password": password}, &lr); code != 200 || lr.Status != app.StatusMFASetup {
		t.Fatalf("login: %d %+v", code, lr)
	}
	// The pending token alone opens nothing.
	if code := b.do("GET", "/api/models", lr.PendingToken, nil, nil); code != 401 {
		t.Errorf("pending token opened the library: %d", code)
	}
	var setup map[string]string
	if code := b.do("POST", "/api/auth/mfa/totp/setup", lr.PendingToken, nil, &setup); code != 200 || !strings.HasPrefix(setup["qr"], "data:image/png;base64,") {
		t.Fatalf("setup: %d %v", code, setup)
	}
	if code := b.do("POST", "/api/auth/mfa/totp/confirm", lr.PendingToken, creds{"code": "000000"}, nil); code != 401 {
		t.Errorf("wrong code accepted: %d", code)
	}
	otp, _ := totp.GenerateCode(setup["secret"], time.Now())
	var me meJSON
	if code := b.do("POST", "/api/auth/mfa/totp/confirm", lr.PendingToken, creds{"code": otp}, &me); code != 200 || len(me.RecoveryCodes) != 10 {
		t.Fatalf("confirm: %d %+v", code, me)
	}
	return setup["secret"], me.RecoveryCodes
}

func TestSignInFlow(t *testing.T) {
	srv := authServer(t)
	anon := newBrowser(t, srv)
	if code := anon.do("GET", "/api/models", "", nil, nil); code != 401 {
		t.Fatalf("library open without sign-in: %d", code)
	}
	if code := anon.do("POST", "/api/accounts/register", "", creds{"email": "a@x.org", "password": "short"}, nil); code != 400 {
		t.Errorf("weak password: %d", code)
	}

	admin := newBrowser(t, srv)
	secret, codes := enroll(t, admin, "Admin@X.org", "correct horse battery")
	var me meJSON
	if code := admin.do("GET", "/api/accounts/me", "", nil, &me); code != 200 || me.Role != "ADMIN" || me.Email != "admin@x.org" || me.MFA != "totp" {
		t.Fatalf("me: %d %+v", code, me)
	}
	if code := admin.do("GET", "/api/models", "", nil, nil); code != 200 {
		t.Errorf("library via cookie: %d", code)
	}

	// Registering a taken email looks exactly like a new one.
	if code := anon.do("POST", "/api/accounts/register", "", creds{"email": "admin@x.org", "password": "another password"}, nil); code != 202 {
		t.Errorf("duplicate register: %d", code)
	}
	// Unknown email and wrong password: the same answer.
	if a, b := anon.do("POST", "/api/auth/login", "", creds{"email": "nobody@x.org", "password": "whatever12345"}, nil),
		anon.do("POST", "/api/auth/login", "", creds{"email": "admin@x.org", "password": "wrong password"}, nil); a != 401 || b != 401 {
		t.Errorf("login failures: %d %d", a, b)
	}

	// The second account is a user: no settings, no roster.
	user := newBrowser(t, srv)
	enroll(t, user, "user@x.org", "user password 1")
	if code := user.do("GET", "/api/settings/notifications", "", nil, nil); code != 403 {
		t.Errorf("user reached settings: %d", code)
	}
	if code := user.do("GET", "/api/accounts", "", nil, nil); code != 403 {
		t.Errorf("user reached roster: %d", code)
	}
	if code := admin.do("GET", "/api/settings/notifications", "", nil, nil); code != 200 {
		t.Errorf("admin settings: %d", code)
	}

	// Second sign-in on another device: challenge with a TOTP code.
	phone := newBrowser(t, srv)
	var lr loginResultJSON
	phone.do("POST", "/api/auth/login", "", creds{"email": "admin@x.org", "password": "correct horse battery"}, &lr)
	if lr.Status != app.StatusMFAChallenge || lr.Method != "totp" {
		t.Fatalf("challenge: %+v", lr)
	}
	otp, _ := totp.GenerateCode(secret, time.Now())
	if code := phone.do("POST", "/api/auth/mfa/verify", lr.PendingToken, creds{"code": otp}, &me); code != 200 {
		t.Fatalf("verify: %d", code)
	}
	var sessions []sessionJSON
	admin.do("GET", "/api/accounts/me/sessions", "", nil, &sessions)
	if len(sessions) != 2 {
		t.Fatalf("sessions: %+v", sessions)
	}
	for _, s := range sessions {
		if !s.Current {
			admin.do("DELETE", "/api/accounts/me/sessions/"+s.ID, "", nil, nil)
		}
	}
	if code := phone.do("GET", "/api/models", "", nil, nil); code != 401 {
		t.Errorf("revoked device still signed in: %d", code)
	}

	// Cross-site writes through the cookie are refused.
	req, _ := http.NewRequest("POST", srv.URL+"/api/auth/logout", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if resp, _ := admin.c.Do(req); resp.StatusCode != 403 {
		t.Errorf("cross-site logout: %d", resp.StatusCode)
	}

	// Forgotten password: a recovery code sets a new one, once.
	if code := anon.do("POST", "/api/auth/recover-password", "", creds{"email": "admin@x.org", "recoveryCode": codes[0], "newPassword": "a brand new password"}, nil); code != 204 {
		t.Fatalf("recover: %d", code)
	}
	if code := anon.do("POST", "/api/auth/recover-password", "", creds{"email": "admin@x.org", "recoveryCode": codes[0], "newPassword": "yet another password"}, nil); code != 401 {
		t.Errorf("recovery code used twice: %d", code)
	}
	// Recovering revoked every token.
	if code := admin.do("GET", "/api/models", "", nil, nil); code != 401 {
		t.Errorf("old session survived password recovery: %d", code)
	}

	// Lost authenticator: a recovery code resets the second factor.
	lost := newBrowser(t, srv)
	lost.do("POST", "/api/auth/login", "", creds{"email": "admin@x.org", "password": "a brand new password"}, &lr)
	var reset loginResultJSON
	if code := lost.do("POST", "/api/auth/mfa/verify-recovery-code", lr.PendingToken, creds{"code": strings.ToUpper(codes[1])}, &reset); code != 200 || reset.Status != app.StatusMFASetup {
		t.Fatalf("verify recovery: %d %+v", code, reset)
	}
	if code := lost.do("POST", "/api/auth/mfa/verify", lr.PendingToken, creds{"code": otp}, nil); code != 401 {
		t.Errorf("old pending token still valid after the reset: %d", code)
	}
}

func TestAdminKeepsOneAdmin(t *testing.T) {
	srv := authServer(t)
	admin := newBrowser(t, srv)
	enroll(t, admin, "admin@x.org", "admin password 1")
	var me meJSON
	admin.do("GET", "/api/accounts/me", "", nil, &me)
	if code := admin.do("PATCH", "/api/accounts/"+me.ID, "", map[string]any{"role": "USER"}, nil); code != 409 {
		t.Errorf("demoted the last admin: %d", code)
	}
	user := newBrowser(t, srv)
	enroll(t, user, "user@x.org", "user password 1")
	var list []accountJSON
	admin.do("GET", "/api/accounts", "", nil, &list)
	if len(list) != 2 {
		t.Fatalf("roster: %+v", list)
	}
	if code := admin.do("PATCH", "/api/accounts/"+list[1].ID, "", map[string]any{"enabled": false}, nil); code != 204 {
		t.Fatalf("disable: %d", code)
	}
	if code := user.do("GET", "/api/models", "", nil, nil); code != 401 {
		t.Errorf("disabled account still signed in: %d", code)
	}
}

func TestLoginIsRateLimited(t *testing.T) {
	srv := authServer(t)
	b := newBrowser(t, srv)
	last := 0
	for i := 0; i < 12; i++ {
		last = b.do("POST", "/api/auth/login", "", creds{"email": "x@x.org", "password": "nope nope nope"}, nil)
	}
	if last != 429 {
		t.Errorf("12th attempt: %d", last)
	}
}

// sent collects notifications.
type sent struct {
	mu    sync.Mutex
	notes []app.Notification
}

func (s *sent) Send(_ context.Context, n app.Notification) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notes = append(s.notes, n)
	return nil
}

func (s *sent) list() []app.Notification {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]app.Notification(nil), s.notes...)
}

func TestClosedRegistration(t *testing.T) {
	box := &sent{}
	srv := authServer(t, func(a *app.Auth) {
		a.ClosedRegistration = true
		a.Notify = &app.Notifications{Channels: func(app.NotificationSettings) []app.Notifier { return []app.Notifier{box} }}
	})
	anon := newBrowser(t, srv)
	var opts map[string]bool
	anon.do("GET", "/api/auth/options", "", nil, &opts)
	if !opts["registration"] {
		t.Error("registration closed before the first account")
	}
	admin := newBrowser(t, srv)
	enroll(t, admin, "admin@x.org", "admin password 1")
	anon.do("GET", "/api/auth/options", "", nil, &opts)
	if opts["registration"] {
		t.Error("registration still open")
	}
	if code := anon.do("POST", "/api/accounts/register", "", creds{"email": "new@x.org", "password": "new password 12"}, nil); code != 403 {
		t.Errorf("registered while closed: %d", code)
	}

	// Admins still add accounts; the new user enrolls at the first sign-in.
	var created accountJSON
	if code := admin.do("POST", "/api/accounts", "", map[string]any{"email": "Friend@x.org", "password": "friend password", "role": "USER"}, &created); code != 201 || created.Email != "friend@x.org" {
		t.Fatalf("admin create: %d %+v", code, created)
	}
	if code := admin.do("POST", "/api/accounts", "", map[string]any{"email": "friend@x.org", "password": "friend password", "role": "USER"}, nil); code != 409 {
		t.Errorf("duplicate: %d", code)
	}
	if code := admin.do("POST", "/api/accounts", "", map[string]any{"email": "g@x.org", "google": true, "role": "USER"}, nil); code != 400 {
		t.Errorf("Google account without Google sign-in: %d", code)
	}
	var lr loginResultJSON
	if code := anon.do("POST", "/api/auth/login", "", creds{"email": "friend@x.org", "password": "friend password"}, &lr); code != 200 || lr.Status != app.StatusMFASetup {
		t.Errorf("created account can't sign in: %d %+v", code, lr)
	}
	friend := newBrowser(t, srv)
	var fl loginResultJSON
	friend.do("POST", "/api/auth/login", "", creds{"email": "friend@x.org", "password": "friend password"}, &fl)
	if code := friend.do("POST", "/api/accounts", fl.PendingToken, map[string]any{"email": "x@x.org", "password": "xxxxxxxxxxxxx", "role": "ADMIN"}, nil); code != 401 {
		t.Errorf("non-admin created an account: %d", code)
	}

	// The event was never switched on (TestRegistrationNotifies covers it).
	if n := box.list(); len(n) != 0 {
		t.Errorf("notified while the event was off: %+v", n)
	}
}

func TestRegistrationNotifies(t *testing.T) {
	box := &sent{}
	srv := authServer(t, func(a *app.Auth) {
		a.Notify = &app.Notifications{Channels: func(app.NotificationSettings) []app.Notifier { return []app.Notifier{box} }}
	})
	admin := newBrowser(t, srv)
	enroll(t, admin, "admin@x.org", "admin password 1")
	if code := admin.do("PUT", "/api/settings/notifications", "", map[string]any{"events": map[string]bool{"account.registered": true}}, nil); code != 204 {
		t.Fatalf("settings: %d", code)
	}
	anon := newBrowser(t, srv)
	anon.do("POST", "/api/accounts/register", "", creds{"email": "new@x.org", "password": "new password 12"}, nil)
	anon.do("POST", "/api/accounts/register", "", creds{"email": "new@x.org", "password": "new password 12"}, nil) // taken: no news
	deadline := time.Now().Add(2 * time.Second)
	for len(box.list()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	n := box.list()
	if len(n) != 1 || n[0].Event != app.EventAccountRegistered || !strings.Contains(n[0].Message, "new@x.org") {
		t.Errorf("notifications: %+v", n)
	}
}
