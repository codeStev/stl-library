package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/codeStev/stl-library/internal/app"
	"github.com/codeStev/stl-library/internal/core/account"
)

// Access levels of a route.
type level int

const (
	public  level = iota // anyone
	pending              // a PASSWORD-factor (or full) token: the second-factor steps
	full                 // signed in with both factors
	admin                // full, and an Admin
)

// CookieName holds the full token. The browser sends it with every request
// (also <img> and download links); it is HttpOnly and SameSite=Strict.
// Pending tokens never go into a cookie: the SPA keeps them in memory and
// sends them as a Bearer token.
const CookieName = "stlib_token"

type principalKey struct{}

// principal returns the caller (the zero Principal as an open-access admin
// when the API runs without Auth, as in tests).
func principal(r *http.Request) app.Principal {
	p, _ := r.Context().Value(principalKey{}).(app.Principal)
	return p
}

// guard enforces an access level.
func (a *API) guard(lvl level, next http.HandlerFunc) http.HandlerFunc {
	if lvl == public {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if a.Auth == nil {
			p := app.Principal{Role: account.Admin, Full: true}
			next(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
			return
		}
		token, fromCookie := bearer(r)
		if token == "" {
			http.Error(w, "sign in first", http.StatusUnauthorized)
			return
		}
		// A cookie comes along with cross-site requests in some older
		// browsers; writes through it must come from our own pages.
		if fromCookie && r.Method != http.MethodGet && r.Method != http.MethodHead && crossSite(r) {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		p, err := a.Auth.Authenticate(r.Context(), token)
		if err != nil {
			if fromCookie {
				clearCookie(w, r, a.SecureCookies)
			}
			http.Error(w, "sign in first", http.StatusUnauthorized)
			return
		}
		switch {
		case lvl >= full && !p.Full:
			http.Error(w, "finish signing in first", http.StatusUnauthorized)
			return
		case lvl == admin && p.Role != account.Admin:
			http.Error(w, "only admins can do that", http.StatusForbidden)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	}
}

func bearer(r *http.Request) (token string, fromCookie bool) {
	if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:]), false
	}
	if c, err := r.Cookie(CookieName); err == nil {
		return c.Value, true
	}
	return "", false
}

// crossSite reports a request that a browser says comes from another site.
func crossSite(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "cross-site", "same-site":
		return true
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		return err != nil || u.Host != r.Host
	}
	return false
}

func secure(r *http.Request, always bool) bool {
	return always || r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func setCookie(w http.ResponseWriter, r *http.Request, always bool, token string, expires int64) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: token, Path: "/", Expires: time.Unix(expires, 0),
		HttpOnly: true, Secure: secure(r, always), SameSite: http.SameSiteStrictMode})
}

func clearCookie(w http.ResponseWriter, r *http.Request, always bool) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: secure(r, always), SameSite: http.SameSiteStrictMode})
}

// client describes the requesting device.
func (a *API) client(r *http.Request) app.Client {
	return app.Client{UserAgent: r.UserAgent(), IP: a.clientIP(r)}
}

// clientIP is the peer address, or - behind a trusted reverse proxy - the
// address the proxy appended to X-Forwarded-For.
func (a *API) clientIP(r *http.Request) string {
	if a.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---- rate limiting ----

// limiter is a token bucket per client IP and route group.
type limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	burst   float64
	every   time.Duration // one token per
	swept   time.Time
}

type bucket struct {
	tokens float64
	at     time.Time
}

func newLimiter(burst int, every time.Duration) *limiter {
	return &limiter{buckets: map[string]*bucket{}, burst: float64(burst), every: every}
}

func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.swept) > 10*time.Minute { // forget idle clients
		for k, b := range l.buckets {
			if now.Sub(b.at) > time.Duration(l.burst)*l.every {
				delete(l.buckets, k)
			}
		}
		l.swept = now
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, at: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.burst, b.tokens+float64(now.Sub(b.at))/float64(l.every))
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// limited rate-limits a credential-checking route: 10 attempts, then one
// every 30 seconds, per client and group.
func (a *API) limited(group string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.limits.allow(group+"|"+a.clientIP(r), time.Now()) {
			w.Header().Set("Retry-After", "30")
			http.Error(w, "too many attempts - wait a minute and try again", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

// ---- routes ----

func (a *API) authRoutes(h func(pattern string, lvl level, f http.HandlerFunc)) {
	h("GET /api/auth/options", public, a.authOptions)
	h("POST /api/accounts/register", public, a.limited("register", a.register))
	h("POST /api/auth/login", public, a.limited("login", a.login))
	h("POST /api/auth/recover-password", public, a.limited("recover", a.recoverPassword))
	h("GET /oauth2/authorization/google", public, a.limited("google", a.beginGoogle))
	h("GET /login/oauth2/code/google", public, a.finishGoogle)
	h("POST /api/auth/oidc/exchange", public, a.limited("google", a.redeemGoogle))

	h("POST /api/auth/mfa/totp/setup", pending, a.startTOTP)
	h("POST /api/auth/mfa/totp/confirm", pending, a.limited("mfa", a.confirmTOTP))
	h("POST /api/auth/mfa/verify", pending, a.limited("mfa", a.verifyTOTP))
	h("POST /api/auth/mfa/verify-recovery-code", pending, a.limited("mfa", a.verifyRecoveryCode))
	h("POST /api/auth/mfa/webauthn/register/begin", pending, a.beginPasskeyRegistration)
	h("POST /api/auth/mfa/webauthn/register/finish", pending, a.limited("mfa", a.finishPasskeyRegistration))
	h("POST /api/auth/mfa/webauthn/login/begin", pending, a.beginPasskeyLogin)
	h("POST /api/auth/mfa/webauthn/login/finish", pending, a.limited("mfa", a.finishPasskeyLogin))

	h("GET /api/accounts/me", full, a.me)
	h("POST /api/accounts/me/password", full, a.limited("password", a.changePassword))
	h("POST /api/auth/logout", full, a.logout)
	h("POST /api/auth/logout-all", full, a.logoutAll)
	h("GET /api/accounts/me/sessions", full, a.sessions)
	h("DELETE /api/accounts/me/sessions/{sid}", full, a.revokeSession)
	h("POST /api/accounts/me/recovery-codes", full, a.regenerateRecoveryCodes)
	h("GET /api/accounts/me/passkeys", full, a.passkeys)
	h("DELETE /api/accounts/me/passkeys/{pid}", full, a.deletePasskey)

	h("GET /api/accounts", admin, a.accounts)
	h("PATCH /api/accounts/{aid}", admin, a.changeAccount)
}

// authFail maps auth errors; everything else goes to fail.
func authFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, app.ErrUnauthorized):
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
	case errors.Is(err, app.ErrForbidden):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, app.ErrConflict):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		fail(w, err)
	}
}

type loginResultJSON struct {
	Status       string `json:"status"`
	PendingToken string `json:"pendingToken"`
	Method       string `json:"method,omitempty"`
	Passkeys     bool   `json:"passkeys"`
}

func loginResult(r app.LoginResult) loginResultJSON {
	return loginResultJSON{r.Status, r.PendingToken, string(r.Method), r.Passkeys}
}

type meJSON struct {
	ID            string   `json:"id"`
	Email         string   `json:"email"`
	Role          string   `json:"role"`
	MFA           string   `json:"mfa"`
	External      bool     `json:"external"`
	Passkeys      bool     `json:"passkeys"` // passkeys can be used on this server
	RecoveryCodes []string `json:"recoveryCodes,omitempty"`
}

// signedIn finishes a login: the full token goes into the cookie, the body
// describes the account (and carries new recovery codes, once).
func (a *API) signedIn(w http.ResponseWriter, r *http.Request, fl app.FullLogin) {
	setCookie(w, r, a.SecureCookies, fl.Token, fl.Claims.Expires)
	acc, err := a.Auth.Me(r.Context(), app.Principal{AccountID: fl.Claims.Subject})
	if err != nil {
		fail(w, err)
		return
	}
	m := a.meOf(acc)
	m.RecoveryCodes = fl.RecoveryCodes
	writeJSON(w, m)
}

func (a *API) meOf(acc account.Account) meJSON {
	return meJSON{ID: acc.ID, Email: acc.Email, Role: string(acc.Role), MFA: string(acc.MFA), External: acc.External(),
		Passkeys: a.Auth.Passkeys != nil && a.Auth.Passkeys.Enabled()}
}

func (a *API) authOptions(w http.ResponseWriter, r *http.Request) {
	if a.Auth == nil {
		writeJSON(w, map[string]bool{"open": true})
		return
	}
	writeJSON(w, map[string]bool{"google": a.Auth.GoogleEnabled(),
		"passkeys": a.Auth.Passkeys != nil && a.Auth.Passkeys.Enabled()})
}

type credentialsJSON struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (a *API) register(w http.ResponseWriter, r *http.Request) {
	var b credentialsJSON
	if !readJSON(w, r, &b) {
		return
	}
	if err := a.Auth.Register(r.Context(), b.Email, b.Password); err != nil {
		authFail(w, err)
		return
	}
	// The same answer whether or not the email already had an account.
	w.WriteHeader(http.StatusAccepted)
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var b credentialsJSON
	if !readJSON(w, r, &b) {
		return
	}
	res, err := a.Auth.Login(r.Context(), b.Email, b.Password)
	if err != nil {
		authFail(w, err)
		return
	}
	writeJSON(w, loginResult(res))
}

func (a *API) recoverPassword(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Email        string `json:"email"`
		RecoveryCode string `json:"recoveryCode"`
		NewPassword  string `json:"newPassword"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	if err := a.Auth.RecoverPassword(r.Context(), b.Email, b.RecoveryCode, b.NewPassword); err != nil {
		authFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) beginGoogle(w http.ResponseWriter, r *http.Request) {
	if a.Auth == nil {
		http.NotFound(w, r)
		return
	}
	u, err := a.Auth.BeginGoogle(r.Context())
	if err != nil {
		authFail(w, err)
		return
	}
	http.Redirect(w, r, u, http.StatusFound)
}

// finishGoogle sends the browser back to the app with a single-use code
// (or an error message) - never a token in the URL.
func (a *API) finishGoogle(w http.ResponseWriter, r *http.Request) {
	if a.Auth == nil {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		http.Redirect(w, r, "/?authError="+url.QueryEscape("Google sign-in was cancelled."), http.StatusFound)
		return
	}
	code, err := a.Auth.FinishGoogle(r.Context(), q.Get("state"), q.Get("code"))
	if err != nil {
		msg := "Google sign-in failed."
		if errors.Is(err, app.ErrConflict) {
			msg = strings.TrimPrefix(err.Error(), app.ErrConflict.Error()+": ")
		}
		http.Redirect(w, r, "/?authError="+url.QueryEscape(msg), http.StatusFound)
		return
	}
	http.Redirect(w, r, "/?code="+url.QueryEscape(code), http.StatusFound)
}

func (a *API) redeemGoogle(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	res, err := a.Auth.RedeemGoogle(r.Context(), b.Code)
	if err != nil {
		authFail(w, err)
		return
	}
	writeJSON(w, loginResult(res))
}

// ---- second factor ----

type codeJSON struct {
	Code string `json:"code"`
}

func (a *API) startTOTP(w http.ResponseWriter, r *http.Request) {
	s, err := a.Auth.StartTOTP(r.Context(), principal(r))
	if err != nil {
		authFail(w, err)
		return
	}
	writeJSON(w, map[string]string{"secret": s.Secret, "otpauthUrl": s.URL, "qr": s.QR})
}

func (a *API) confirmTOTP(w http.ResponseWriter, r *http.Request) {
	var b codeJSON
	if !readJSON(w, r, &b) {
		return
	}
	fl, err := a.Auth.ConfirmTOTP(r.Context(), principal(r), b.Code, a.client(r))
	if err != nil {
		authFail(w, err)
		return
	}
	a.signedIn(w, r, fl)
}

func (a *API) verifyTOTP(w http.ResponseWriter, r *http.Request) {
	var b codeJSON
	if !readJSON(w, r, &b) {
		return
	}
	fl, err := a.Auth.VerifyTOTP(r.Context(), principal(r), b.Code, a.client(r))
	if err != nil {
		authFail(w, err)
		return
	}
	a.signedIn(w, r, fl)
}

func (a *API) verifyRecoveryCode(w http.ResponseWriter, r *http.Request) {
	var b codeJSON
	if !readJSON(w, r, &b) {
		return
	}
	res, err := a.Auth.VerifyRecoveryCode(r.Context(), principal(r), b.Code)
	if err != nil {
		authFail(w, err)
		return
	}
	writeJSON(w, loginResult(res))
}

// rawJSON writes JSON the passkey library produced.
func rawJSON(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func (a *API) beginPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	opts, err := a.Auth.BeginPasskeyRegistration(r.Context(), principal(r))
	if err != nil {
		authFail(w, err)
		return
	}
	rawJSON(w, opts)
}

func (a *API) finishPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Name     string          `json:"name"`
		Response json.RawMessage `json:"response"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	fl, err := a.Auth.FinishPasskeyRegistration(r.Context(), principal(r), b.Response, b.Name, a.client(r))
	if err != nil {
		authFail(w, err)
		return
	}
	if fl == nil { // another passkey for a signed-in account
		w.WriteHeader(http.StatusNoContent)
		return
	}
	a.signedIn(w, r, *fl)
}

func (a *API) beginPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	opts, err := a.Auth.BeginPasskeyLogin(r.Context(), principal(r))
	if err != nil {
		authFail(w, err)
		return
	}
	rawJSON(w, opts)
}

func (a *API) finishPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Response json.RawMessage `json:"response"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	fl, err := a.Auth.FinishPasskeyLogin(r.Context(), principal(r), b.Response, a.client(r))
	if err != nil {
		authFail(w, err)
		return
	}
	a.signedIn(w, r, fl)
}

// ---- self-service ----

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	if a.Auth == nil {
		writeJSON(w, meJSON{Role: string(account.Admin)})
		return
	}
	acc, err := a.Auth.Me(r.Context(), principal(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, a.meOf(acc))
}

func (a *API) changePassword(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Current string `json:"currentPassword"`
		Next    string `json:"newPassword"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	fl, err := a.Auth.ChangePassword(r.Context(), principal(r), b.Current, b.Next, a.client(r))
	if err != nil {
		authFail(w, err)
		return
	}
	a.signedIn(w, r, fl)
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	if err := a.Auth.Logout(r.Context(), principal(r)); err != nil && !errors.Is(err, app.ErrNotFound) {
		fail(w, err)
		return
	}
	clearCookie(w, r, a.SecureCookies)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) logoutAll(w http.ResponseWriter, r *http.Request) {
	if err := a.Auth.LogoutAll(r.Context(), principal(r)); err != nil {
		fail(w, err)
		return
	}
	clearCookie(w, r, a.SecureCookies)
	w.WriteHeader(http.StatusNoContent)
}

type sessionJSON struct {
	ID        string `json:"id"`
	Created   int64  `json:"created"`
	Expires   int64  `json:"expires"`
	UserAgent string `json:"userAgent"`
	IP        string `json:"ip"`
	Current   bool   `json:"current"`
}

func (a *API) sessions(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	list, err := a.Auth.Sessions(r.Context(), p)
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]sessionJSON, 0, len(list))
	for _, s := range list {
		out = append(out, sessionJSON{s.ID, s.CreatedUnix, s.ExpiresUnix, s.UserAgent, s.IP, s.ID == p.SessionID})
	}
	writeJSON(w, out)
}

func (a *API) revokeSession(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	id := r.PathValue("sid")
	if err := a.Auth.RevokeSession(r.Context(), p, id); err != nil {
		fail(w, err)
		return
	}
	if id == p.SessionID {
		clearCookie(w, r, a.SecureCookies)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) regenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	codes, err := a.Auth.RegenerateRecoveryCodes(r.Context(), principal(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string][]string{"recoveryCodes": codes})
}

type passkeyJSON struct {
	ID      string `json:"id"` // base64url
	Name    string `json:"name"`
	Created int64  `json:"created"`
}

func (a *API) passkeys(w http.ResponseWriter, r *http.Request) {
	list, err := a.Auth.ListPasskeys(r.Context(), principal(r))
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]passkeyJSON, 0, len(list))
	for _, c := range list {
		out = append(out, passkeyJSON{base64.RawURLEncoding.EncodeToString(c.ID), c.Name, c.CreatedUnix})
	}
	writeJSON(w, out)
}

func (a *API) deletePasskey(w http.ResponseWriter, r *http.Request) {
	id, err := base64.RawURLEncoding.DecodeString(r.PathValue("pid"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := a.Auth.DeletePasskey(r.Context(), principal(r), id); err != nil {
		authFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- administration ----

type accountJSON struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	Enabled  bool   `json:"enabled"`
	MFA      string `json:"mfa"`
	Provider string `json:"provider,omitempty"`
	Locked   bool   `json:"locked"`
	Created  int64  `json:"created"`
}

func (a *API) accounts(w http.ResponseWriter, r *http.Request) {
	list, err := a.Auth.ListAccounts(r.Context(), principal(r))
	if err != nil {
		authFail(w, err)
		return
	}
	now := time.Now().Unix()
	out := make([]accountJSON, 0, len(list))
	for _, x := range list {
		out = append(out, accountJSON{x.ID, x.Email, string(x.Role), x.Enabled, string(x.MFA), x.AuthProvider, x.Locked(now), x.CreatedUnix})
	}
	writeJSON(w, out)
}

func (a *API) changeAccount(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Role        *string `json:"role"`
		Enabled     *bool   `json:"enabled"`
		NewPassword *string `json:"newPassword"`
		ResetMFA    bool    `json:"resetMfa"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	ch := app.AccountChange{Enabled: b.Enabled, NewPassword: b.NewPassword, ResetMFA: b.ResetMFA}
	if b.Role != nil {
		role := account.Role(*b.Role)
		ch.Role = &role
	}
	if err := a.Auth.ChangeAccount(r.Context(), principal(r), r.PathValue("aid"), ch); err != nil {
		authFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
