package app

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/codeStev/stl-library/internal/core/account"
)

// The login flow follows the campaign organizer's (ADR-0110/0111/0112/0113):
// open self-registration (the first account becomes Admin), responses that
// never reveal whether an email has an account, a correct password that
// only earns a short-lived PASSWORD-factor token, mandatory second factor
// (TOTP or a passkey) with single-use recovery codes that double as
// password recovery, revocable tokens (token_version + per-device
// sessions), and optional Google sign-in that still needs the local
// second factor.

var (
	// ErrUnauthorized is the one answer for every failed credential check.
	ErrUnauthorized = errors.New("invalid credentials")
	// ErrForbidden: authenticated, but not allowed.
	ErrForbidden = errors.New("not allowed")
	// ErrConflict: e.g. a Google sign-in whose email already has a
	// password account (never merged silently).
	ErrConflict = errors.New("conflict")
	// ErrExists is returned by an AccountStore for a duplicate email.
	ErrExists = errors.New("already exists")
)

// Claims of an access token.
type Claims struct {
	Subject string // account id
	Role    account.Role
	Version int
	Factors []string
	ID      string // jti
	Expires int64
}

// Full reports a token that proved both factors.
func (c Claims) Full() bool {
	for _, f := range c.Factors {
		if f == account.FactorMFA {
			return true
		}
	}
	return false
}

// TokenIssuer signs and verifies tokens.
type TokenIssuer interface {
	Issue(c Claims) (string, error)
	Parse(token string) (Claims, error)
}

// PasswordHasher hashes passwords (and recovery codes).
type PasswordHasher interface {
	Hash(secret string) (string, error)
	Compare(hash, secret string) bool
}

// TOTP generates and checks time-based one-time passwords.
type TOTP interface {
	// NewSecret returns a secret, its otpauth:// URL and a QR code of it
	// as a data: URI.
	NewSecret(accountName string) (secret, url, qrDataURI string, err error)
	Validate(secret, code string, now time.Time) bool
}

// Credential is a stored passkey.
type Credential struct {
	ID          []byte
	Data        []byte // the passkey library's own serialization
	Name        string
	CreatedUnix int64
}

// Passkeys runs WebAuthn ceremonies; options/responses are the JSON the
// browser's navigator.credentials API speaks.
type Passkeys interface {
	Enabled() bool
	BeginRegistration(a account.Account, existing []Credential) (options, session []byte, err error)
	FinishRegistration(a account.Account, existing []Credential, session, response []byte) (Credential, error)
	BeginLogin(a account.Account, creds []Credential) (options, session []byte, err error)
	FinishLogin(a account.Account, creds []Credential, session, response []byte) (id, data []byte, err error)
}

// Identity is what an external provider confirmed.
type Identity struct {
	Subject       string
	Email         string
	EmailVerified bool
}

// OIDC is an external sign-in provider (Google).
type OIDC interface {
	Enabled() bool
	AuthURL(state, verifier string) string
	Exchange(ctx context.Context, code, verifier string) (Identity, error)
}

// RecoveryCode is a stored (hashed) recovery code.
type RecoveryCode struct {
	ID   int64
	Hash string
}

// Session is one device holding a full token.
type Session struct {
	ID          string // the token's jti
	AccountID   string
	CreatedUnix int64
	ExpiresUnix int64
	RevokedUnix int64
	UserAgent   string
	IP          string
}

// AccountStore keeps accounts and everything around them.
type AccountStore interface {
	// CreateAccount inserts a; when the table is empty it becomes Admin
	// (decided inside the store, atomically). ErrExists for a duplicate
	// email.
	CreateAccount(ctx context.Context, a account.Account) (account.Account, error)
	// CreateFirstAccount inserts a as the admin only if there are no
	// accounts yet (atomically); ErrExists otherwise.
	CreateFirstAccount(ctx context.Context, a account.Account) (account.Account, error)
	AccountByEmail(ctx context.Context, email string) (account.Account, error)
	AccountByID(ctx context.Context, id string) (account.Account, error)
	AccountByExternal(ctx context.Context, provider, subject string) (account.Account, error)
	SaveAccount(ctx context.Context, a account.Account) error
	ListAccounts(ctx context.Context) ([]account.Account, error)

	ReplaceRecoveryCodes(ctx context.Context, accountID string, hashes []string) error
	RecoveryCodes(ctx context.Context, accountID string) ([]RecoveryCode, error) // unused ones
	UseRecoveryCode(ctx context.Context, id int64) (bool, error)                 // false if already used

	AddSession(ctx context.Context, s Session) error
	Session(ctx context.Context, id string) (Session, error)
	Sessions(ctx context.Context, accountID string, now int64) ([]Session, error) // active ones
	RevokeSession(ctx context.Context, accountID, id string, now int64) error

	AddCredential(ctx context.Context, accountID string, c Credential) error
	Credentials(ctx context.Context, accountID string) ([]Credential, error)
	UpdateCredential(ctx context.Context, accountID string, id, data []byte) error
	DeleteCredential(ctx context.Context, accountID string, id []byte) error

	// Short-lived values: WebAuthn challenges, OIDC state, login exchanges.
	PutEphemeral(ctx context.Context, key string, value []byte, expiresUnix int64) error
	TakeEphemeral(ctx context.Context, key string, now int64) ([]byte, bool, error)
}

// Token lifetimes.
const (
	PendingTokenTTL = 10 * time.Minute
	FullTokenTTL    = 24 * time.Hour
	recoveryCodes   = 10
)

// Login statuses (what the password step says comes next).
const (
	StatusMFASetup     = "MFA_SETUP_REQUIRED"
	StatusMFAChallenge = "MFA_CHALLENGE_REQUIRED"
)

// LoginResult is the answer to a correct first factor.
type LoginResult struct {
	Status       string
	PendingToken string
	Method       account.MFA // for a challenge
	Passkeys     bool        // passkeys can be used on this server
}

// FullLogin is the answer to a completed second factor.
type FullLogin struct {
	Token         string
	Claims        Claims
	RecoveryCodes []string // shown once, after enrollment
}

// Client describes the device a request comes from.
type Client struct {
	UserAgent string
	IP        string
}

// Principal is an authenticated caller.
type Principal struct {
	AccountID string
	Email     string
	Role      account.Role
	Full      bool
	SessionID string
}

// Auth is the login flow.
type Auth struct {
	Accounts AccountStore
	Hasher   PasswordHasher
	Tokens   TokenIssuer
	TOTP     TOTP
	Sealer   Sealer
	Passkeys Passkeys // may be disabled
	Google   OIDC     // may be disabled
	Now      func() time.Time
	// ClosedRegistration: after the first account, only admins create
	// accounts.
	ClosedRegistration bool
	// Notify is told about new self-registered accounts; may be nil.
	Notify *Notifications

	dummyOnce sync.Once
	dummyHash string
}

func (a *Auth) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// dummy returns a real hash to compare against when there is no account,
// so a missing account costs the same time as a wrong password.
func (a *Auth) dummy() string {
	a.dummyOnce.Do(func() { a.dummyHash, _ = a.Hasher.Hash("not-a-real-password-" + randomToken(8)) })
	return a.dummyHash
}

// ---- registration and password login ----

// ErrRegistrationClosed: only admins create accounts.
var ErrRegistrationClosed = fmt.Errorf("%w: registration is closed - ask an admin for an account", ErrForbidden)

// RegistrationOpen reports whether anyone may create an account (always
// true while there are none).
func (a *Auth) RegistrationOpen(ctx context.Context) bool {
	if !a.ClosedRegistration {
		return true
	}
	all, err := a.Accounts.ListAccounts(ctx)
	return err == nil && len(all) == 0
}

// create inserts a self-registered account (only the first one when
// registration is closed) and tells the admins.
func (a *Auth) create(ctx context.Context, acc account.Account) (account.Account, error) {
	var err error
	if a.ClosedRegistration {
		acc, err = a.Accounts.CreateFirstAccount(ctx, acc)
		if errors.Is(err, ErrExists) {
			return acc, ErrRegistrationClosed
		}
	} else {
		acc, err = a.Accounts.CreateAccount(ctx, acc)
	}
	if err == nil && a.Notify != nil {
		// In the background: the answer must not take longer for a new
		// email than for a taken one.
		go a.Notify.Notify(context.WithoutCancel(ctx), Notification{Event: EventAccountRegistered, Title: "New account",
			Message: fmt.Sprintf("%s created an account (%s).", acc.Email, strings.ToLower(string(acc.Role)))})
	}
	return acc, err
}

// Register creates an account. It answers the same way (nil) whether the
// email was free or taken, and costs the same either way; the caller must
// never sign the user in from it.
func (a *Auth) Register(ctx context.Context, email, password string) error {
	email, err := account.NormalizeEmail(email)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := account.CheckPassword(password); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	hash, err := a.Hasher.Hash(password)
	if err != nil {
		return err
	}
	_, err = a.create(ctx, account.Account{ID: newID(), Email: email, PasswordHash: hash,
		Role: account.User, Enabled: true, CreatedUnix: a.now().Unix()})
	if errors.Is(err, ErrExists) {
		return nil // same answer as a new account
	}
	return err
}

// Login checks email and password. It never returns a usable token: the
// result says whether the second factor must be set up or proven.
func (a *Auth) Login(ctx context.Context, email, password string) (LoginResult, error) {
	email, _ = account.NormalizeEmail(email)
	now := a.now().Unix()
	acc, err := a.Accounts.AccountByEmail(ctx, email)
	if err != nil || acc.External() {
		a.Hasher.Compare(a.dummy(), password)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return LoginResult{}, err
		}
		return LoginResult{}, ErrUnauthorized
	}
	ok := a.Hasher.Compare(acc.PasswordHash, password)
	switch {
	case !acc.Enabled || acc.Locked(now):
		return LoginResult{}, ErrUnauthorized
	case !ok:
		acc.FailedLogin(now)
		a.Accounts.SaveAccount(ctx, acc)
		return LoginResult{}, ErrUnauthorized
	}
	if acc.FailedAttempts > 0 {
		acc.SucceededLogin()
		if err := a.Accounts.SaveAccount(ctx, acc); err != nil {
			return LoginResult{}, err
		}
	}
	return a.pending(acc)
}

// pending issues the PASSWORD-factor token and says what comes next.
func (a *Auth) pending(acc account.Account) (LoginResult, error) {
	tok, _, err := a.issue(acc, false)
	if err != nil {
		return LoginResult{}, err
	}
	r := LoginResult{Status: StatusMFASetup, PendingToken: tok, Passkeys: a.passkeys()}
	if acc.MFA != account.MFANone {
		r.Status, r.Method = StatusMFAChallenge, acc.MFA
	}
	return r, nil
}

func (a *Auth) passkeys() bool { return a.Passkeys != nil && a.Passkeys.Enabled() }

// issue signs a token; a full one is also recorded as a device session by
// the caller (see complete).
func (a *Auth) issue(acc account.Account, full bool) (string, Claims, error) {
	c := Claims{Subject: acc.ID, Role: acc.Role, Version: acc.TokenVersion, ID: randomToken(16),
		Factors: []string{account.FactorPassword}}
	ttl := PendingTokenTTL
	if full {
		c.Factors = append(c.Factors, account.FactorMFA)
		ttl = FullTokenTTL
	}
	c.Expires = a.now().Add(ttl).Unix()
	tok, err := a.Tokens.Issue(c)
	return tok, c, err
}

// complete issues the full token and records its device session.
func (a *Auth) complete(ctx context.Context, acc account.Account, client Client, codes []string) (FullLogin, error) {
	tok, c, err := a.issue(acc, true)
	if err != nil {
		return FullLogin{}, err
	}
	if err := a.Accounts.AddSession(ctx, Session{ID: c.ID, AccountID: acc.ID, CreatedUnix: a.now().Unix(),
		ExpiresUnix: c.Expires, UserAgent: truncate(client.UserAgent, 300), IP: client.IP}); err != nil {
		return FullLogin{}, err
	}
	return FullLogin{Token: tok, Claims: c, RecoveryCodes: codes}, nil
}

// ---- authenticating requests ----

// Authenticate checks a token: signature and expiry, the account still
// enabled with the same token version, and - for a full token - its
// device session not revoked.
func (a *Auth) Authenticate(ctx context.Context, token string) (Principal, error) {
	c, err := a.Tokens.Parse(token)
	if err != nil || c.Expires <= a.now().Unix() {
		return Principal{}, ErrUnauthorized
	}
	acc, err := a.Accounts.AccountByID(ctx, c.Subject)
	if err != nil || !acc.Enabled || acc.TokenVersion != c.Version {
		return Principal{}, ErrUnauthorized
	}
	p := Principal{AccountID: acc.ID, Email: acc.Email, Role: acc.Role}
	if c.Full() {
		s, err := a.Accounts.Session(ctx, c.ID)
		if err != nil || s.AccountID != acc.ID || s.RevokedUnix != 0 || s.ExpiresUnix <= a.now().Unix() {
			return Principal{}, ErrUnauthorized
		}
		p.Full, p.SessionID = true, s.ID
	}
	return p, nil
}

// ---- TOTP ----

// TOTPSetup is what an authenticator app needs.
type TOTPSetup struct {
	Secret string
	URL    string
	QR     string // data: URI
}

// StartTOTP creates a pending secret for an account without a second
// factor (enrollment) or, with a full token, for re-enrollment.
func (a *Auth) StartTOTP(ctx context.Context, p Principal) (TOTPSetup, error) {
	acc, err := a.Accounts.AccountByID(ctx, p.AccountID)
	if err != nil {
		return TOTPSetup{}, err
	}
	if acc.MFA != account.MFANone && !p.Full {
		return TOTPSetup{}, ErrForbidden
	}
	secret, url, qr, err := a.TOTP.NewSecret(acc.Email)
	if err != nil {
		return TOTPSetup{}, err
	}
	if acc.TOTPPending, err = a.Sealer.Seal(secret); err != nil {
		return TOTPSetup{}, err
	}
	if err := a.Accounts.SaveAccount(ctx, acc); err != nil {
		return TOTPSetup{}, err
	}
	return TOTPSetup{Secret: secret, URL: url, QR: qr}, nil
}

// ConfirmTOTP activates the pending secret with a code from the app,
// issues fresh recovery codes and signs the user in fully.
func (a *Auth) ConfirmTOTP(ctx context.Context, p Principal, code string, client Client) (FullLogin, error) {
	acc, err := a.Accounts.AccountByID(ctx, p.AccountID)
	if err != nil {
		return FullLogin{}, err
	}
	if acc.MFA != account.MFANone && !p.Full {
		return FullLogin{}, ErrForbidden
	}
	secret, err := a.Sealer.Open(acc.TOTPPending)
	if err != nil || secret == "" {
		return FullLogin{}, fmt.Errorf("%w: start the setup first", ErrInvalid)
	}
	if !a.TOTP.Validate(secret, code, a.now()) {
		return FullLogin{}, ErrUnauthorized
	}
	acc.TOTPSecret, acc.TOTPPending, acc.MFA = acc.TOTPPending, "", account.MFATOTP
	if p.Full { // re-enrollment: other devices must sign in again
		acc.Revoke()
	}
	return a.enrolled(ctx, acc, client)
}

// enrolled saves an account that just activated a second factor, issues
// its recovery codes and the full token.
func (a *Auth) enrolled(ctx context.Context, acc account.Account, client Client) (FullLogin, error) {
	if err := a.Accounts.SaveAccount(ctx, acc); err != nil {
		return FullLogin{}, err
	}
	codes, err := a.newRecoveryCodes(ctx, acc.ID)
	if err != nil {
		return FullLogin{}, err
	}
	return a.complete(ctx, acc, client, codes)
}

// VerifyTOTP is the challenge after the password.
func (a *Auth) VerifyTOTP(ctx context.Context, p Principal, code string, client Client) (FullLogin, error) {
	acc, err := a.Accounts.AccountByID(ctx, p.AccountID)
	if err != nil {
		return FullLogin{}, err
	}
	if acc.MFA != account.MFATOTP {
		return FullLogin{}, ErrForbidden
	}
	now := a.now()
	if acc.Locked(now.Unix()) {
		return FullLogin{}, ErrUnauthorized
	}
	// Wrong codes count towards the same lockout as wrong passwords, so a
	// stolen password can't be used to guess codes.
	secret, err := a.Sealer.Open(acc.TOTPSecret)
	if err != nil || !a.TOTP.Validate(secret, code, now) {
		acc.FailedLogin(now.Unix())
		a.Accounts.SaveAccount(ctx, acc)
		return FullLogin{}, ErrUnauthorized
	}
	if acc.FailedAttempts > 0 {
		acc.SucceededLogin()
		if err := a.Accounts.SaveAccount(ctx, acc); err != nil {
			return FullLogin{}, err
		}
	}
	return a.complete(ctx, acc, client, nil)
}

// ---- passkeys ----

func (a *Auth) passkeyAccount(ctx context.Context, p Principal) (account.Account, []Credential, error) {
	if !a.passkeys() {
		return account.Account{}, nil, fmt.Errorf("%w: passkeys are not set up on this server", ErrInvalid)
	}
	acc, err := a.Accounts.AccountByID(ctx, p.AccountID)
	if err != nil {
		return acc, nil, err
	}
	creds, err := a.Accounts.Credentials(ctx, acc.ID)
	return acc, creds, err
}

const ceremonyTTL = 5 * time.Minute

// BeginPasskeyRegistration starts adding a passkey: as the second factor at
// enrollment, or (full token) as another passkey.
func (a *Auth) BeginPasskeyRegistration(ctx context.Context, p Principal) ([]byte, error) {
	acc, creds, err := a.passkeyAccount(ctx, p)
	if err != nil {
		return nil, err
	}
	if acc.MFA != account.MFANone && !p.Full {
		return nil, ErrForbidden
	}
	opts, session, err := a.Passkeys.BeginRegistration(acc, creds)
	if err != nil {
		return nil, err
	}
	return opts, a.Accounts.PutEphemeral(ctx, "webauthn-reg:"+acc.ID, session, a.now().Add(ceremonyTTL).Unix())
}

// FinishPasskeyRegistration stores the new passkey. At enrollment it
// becomes the second factor: recovery codes and a full login follow.
func (a *Auth) FinishPasskeyRegistration(ctx context.Context, p Principal, response []byte, name string, client Client) (*FullLogin, error) {
	acc, creds, err := a.passkeyAccount(ctx, p)
	if err != nil {
		return nil, err
	}
	if acc.MFA != account.MFANone && !p.Full {
		return nil, ErrForbidden
	}
	session, ok, err := a.Accounts.TakeEphemeral(ctx, "webauthn-reg:"+acc.ID, a.now().Unix())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: the passkey request expired, try again", ErrInvalid)
	}
	cred, err := a.Passkeys.FinishRegistration(acc, creds, session, response)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnauthorized, err)
	}
	cred.Name, cred.CreatedUnix = truncate(strings.TrimSpace(name), 60), a.now().Unix()
	if cred.Name == "" {
		cred.Name = "Passkey"
	}
	if err := a.Accounts.AddCredential(ctx, acc.ID, cred); err != nil {
		return nil, err
	}
	if acc.MFA != account.MFANone {
		return nil, nil // another passkey for an enrolled account
	}
	acc.MFA = account.MFAWebAuthn
	fl, err := a.enrolled(ctx, acc, client)
	return &fl, err
}

// BeginPasskeyLogin starts the passkey challenge after the password.
func (a *Auth) BeginPasskeyLogin(ctx context.Context, p Principal) ([]byte, error) {
	acc, creds, err := a.passkeyAccount(ctx, p)
	if err != nil {
		return nil, err
	}
	if acc.MFA != account.MFAWebAuthn || len(creds) == 0 {
		return nil, ErrForbidden
	}
	opts, session, err := a.Passkeys.BeginLogin(acc, creds)
	if err != nil {
		return nil, err
	}
	return opts, a.Accounts.PutEphemeral(ctx, "webauthn-login:"+acc.ID, session, a.now().Add(ceremonyTTL).Unix())
}

// FinishPasskeyLogin completes the challenge.
func (a *Auth) FinishPasskeyLogin(ctx context.Context, p Principal, response []byte, client Client) (FullLogin, error) {
	acc, creds, err := a.passkeyAccount(ctx, p)
	if err != nil {
		return FullLogin{}, err
	}
	session, ok, err := a.Accounts.TakeEphemeral(ctx, "webauthn-login:"+acc.ID, a.now().Unix())
	if err != nil {
		return FullLogin{}, err
	}
	if !ok || acc.MFA != account.MFAWebAuthn {
		return FullLogin{}, ErrUnauthorized
	}
	id, data, err := a.Passkeys.FinishLogin(acc, creds, session, response)
	if err != nil {
		return FullLogin{}, ErrUnauthorized
	}
	if err := a.Accounts.UpdateCredential(ctx, acc.ID, id, data); err != nil {
		return FullLogin{}, err
	}
	return a.complete(ctx, acc, client, nil)
}

// Passkeys lists an account's passkeys.
func (a *Auth) ListPasskeys(ctx context.Context, p Principal) ([]Credential, error) {
	return a.Accounts.Credentials(ctx, p.AccountID)
}

// DeletePasskey removes a passkey - never the last one of an account that
// uses passkeys as its second factor.
func (a *Auth) DeletePasskey(ctx context.Context, p Principal, id []byte) error {
	acc, err := a.Accounts.AccountByID(ctx, p.AccountID)
	if err != nil {
		return err
	}
	creds, err := a.Accounts.Credentials(ctx, acc.ID)
	if err != nil {
		return err
	}
	if acc.MFA == account.MFAWebAuthn && len(creds) <= 1 {
		return fmt.Errorf("%w: this is your only passkey - add another one first", ErrForbidden)
	}
	return a.Accounts.DeleteCredential(ctx, acc.ID, id)
}

// ---- recovery codes ----

func (a *Auth) newRecoveryCodes(ctx context.Context, accountID string) ([]string, error) {
	codes := make([]string, recoveryCodes)
	hashes := make([]string, recoveryCodes)
	for i := range codes {
		codes[i] = recoveryCode()
		h, err := a.Hasher.Hash(normalizeCode(codes[i]))
		if err != nil {
			return nil, err
		}
		hashes[i] = h
	}
	return codes, a.Accounts.ReplaceRecoveryCodes(ctx, accountID, hashes)
}

// useRecoveryCode consumes a matching unused code.
func (a *Auth) useRecoveryCode(ctx context.Context, accountID, code string) (bool, error) {
	codes, err := a.Accounts.RecoveryCodes(ctx, accountID)
	if err != nil {
		return false, err
	}
	code = normalizeCode(code)
	for _, c := range codes {
		if a.Hasher.Compare(c.Hash, code) {
			return a.Accounts.UseRecoveryCode(ctx, c.ID)
		}
	}
	return false, nil
}

// RegenerateRecoveryCodes replaces all codes (full token only).
func (a *Auth) RegenerateRecoveryCodes(ctx context.Context, p Principal) ([]string, error) {
	return a.newRecoveryCodes(ctx, p.AccountID)
}

// VerifyRecoveryCode is for a lost second factor: it consumes a code and
// resets the second factor, so the user enrolls again (a new pending
// token that requires setup).
func (a *Auth) VerifyRecoveryCode(ctx context.Context, p Principal, code string) (LoginResult, error) {
	acc, err := a.Accounts.AccountByID(ctx, p.AccountID)
	if err != nil {
		return LoginResult{}, err
	}
	now := a.now().Unix()
	if acc.Locked(now) {
		return LoginResult{}, ErrUnauthorized
	}
	ok, err := a.useRecoveryCode(ctx, acc.ID, code)
	if err != nil {
		return LoginResult{}, err
	}
	if !ok {
		acc.FailedLogin(now)
		a.Accounts.SaveAccount(ctx, acc)
		return LoginResult{}, ErrUnauthorized
	}
	acc.SucceededLogin()
	acc.ResetMFA()
	if err := a.Accounts.SaveAccount(ctx, acc); err != nil {
		return LoginResult{}, err
	}
	for _, c := range mustCreds(a.Accounts.Credentials(ctx, acc.ID)) {
		a.Accounts.DeleteCredential(ctx, acc.ID, c.ID)
	}
	return a.pending(acc)
}

func mustCreds(c []Credential, err error) []Credential {
	if err != nil {
		return nil
	}
	return c
}

// RecoverPassword is for a forgotten password: a recovery code sets a new
// one (the second factor stays). Same answer and similar cost whether or
// not the email has an account.
func (a *Auth) RecoverPassword(ctx context.Context, email, code, newPassword string) error {
	if err := account.CheckPassword(newPassword); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	email, _ = account.NormalizeEmail(email)
	acc, err := a.Accounts.AccountByEmail(ctx, email)
	if err != nil || acc.External() || !acc.Enabled {
		for i := 0; i < 3; i++ {
			a.Hasher.Compare(a.dummy(), code)
		}
		return ErrUnauthorized
	}
	ok, err := a.useRecoveryCode(ctx, acc.ID, code)
	if err != nil {
		return err
	}
	if !ok {
		return ErrUnauthorized
	}
	if acc.PasswordHash, err = a.Hasher.Hash(newPassword); err != nil {
		return err
	}
	acc.SucceededLogin()
	acc.Revoke()
	return a.Accounts.SaveAccount(ctx, acc)
}

// ---- self-service ----

// Me returns the caller's account.
func (a *Auth) Me(ctx context.Context, p Principal) (account.Account, error) {
	return a.Accounts.AccountByID(ctx, p.AccountID)
}

// ChangePassword changes the caller's password (current one required)
// and signs out every other device.
func (a *Auth) ChangePassword(ctx context.Context, p Principal, current, next string, client Client) (FullLogin, error) {
	acc, err := a.Accounts.AccountByID(ctx, p.AccountID)
	if err != nil {
		return FullLogin{}, err
	}
	if acc.External() {
		return FullLogin{}, fmt.Errorf("%w: this account signs in with Google", ErrForbidden)
	}
	if !a.Hasher.Compare(acc.PasswordHash, current) {
		return FullLogin{}, ErrUnauthorized
	}
	if err := account.CheckPassword(next); err != nil {
		return FullLogin{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if acc.PasswordHash, err = a.Hasher.Hash(next); err != nil {
		return FullLogin{}, err
	}
	acc.Revoke()
	if err := a.Accounts.SaveAccount(ctx, acc); err != nil {
		return FullLogin{}, err
	}
	return a.complete(ctx, acc, client, nil) // this device stays signed in
}

// LogoutAll signs out every device, including this one.
func (a *Auth) LogoutAll(ctx context.Context, p Principal) error {
	acc, err := a.Accounts.AccountByID(ctx, p.AccountID)
	if err != nil {
		return err
	}
	acc.Revoke()
	return a.Accounts.SaveAccount(ctx, acc)
}

// Logout signs out this device.
func (a *Auth) Logout(ctx context.Context, p Principal) error {
	if p.SessionID == "" {
		return nil
	}
	return a.Accounts.RevokeSession(ctx, p.AccountID, p.SessionID, a.now().Unix())
}

// Sessions lists the caller's signed-in devices.
func (a *Auth) Sessions(ctx context.Context, p Principal) ([]Session, error) {
	return a.Accounts.Sessions(ctx, p.AccountID, a.now().Unix())
}

// RevokeSession signs out one of the caller's devices.
func (a *Auth) RevokeSession(ctx context.Context, p Principal, id string) error {
	return a.Accounts.RevokeSession(ctx, p.AccountID, id, a.now().Unix())
}

// ---- administration ----

func (a *Auth) admin(p Principal) error {
	if p.Role != account.Admin || !p.Full {
		return ErrForbidden
	}
	return nil
}

// ListAccounts is the roster (admins only).
func (a *Auth) ListAccounts(ctx context.Context, p Principal) ([]account.Account, error) {
	if err := a.admin(p); err != nil {
		return nil, err
	}
	return a.Accounts.ListAccounts(ctx)
}

// NewAccount is an account an admin creates: with a password, or for a
// Google address (bound at its first Google sign-in).
type NewAccount struct {
	Email    string
	Password string
	Google   bool
	Role     account.Role
}

// CreateAccount lets an admin add an account (also when registration is
// closed). The new user sets up the second factor at the first sign-in.
func (a *Auth) CreateAccount(ctx context.Context, p Principal, n NewAccount) (account.Account, error) {
	if err := a.admin(p); err != nil {
		return account.Account{}, err
	}
	email, err := account.NormalizeEmail(n.Email)
	if err != nil {
		return account.Account{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if n.Role != account.Admin && n.Role != account.User {
		return account.Account{}, fmt.Errorf("%w: unknown role", ErrInvalid)
	}
	acc := account.Account{ID: newID(), Email: email, Role: n.Role, Enabled: true, CreatedUnix: a.now().Unix()}
	if n.Google {
		if !a.GoogleEnabled() {
			return acc, fmt.Errorf("%w: Google sign-in is not set up on this server", ErrInvalid)
		}
		acc.AuthProvider = "google"
	} else {
		if err := account.CheckPassword(n.Password); err != nil {
			return acc, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		if acc.PasswordHash, err = a.Hasher.Hash(n.Password); err != nil {
			return acc, err
		}
	}
	created, err := a.Accounts.CreateAccount(ctx, acc)
	if errors.Is(err, ErrExists) {
		return acc, fmt.Errorf("%w: %s already has an account", ErrConflict, email)
	}
	return created, err
}

// AccountChange is an administrative change; nil fields stay.
type AccountChange struct {
	Role        *account.Role
	Enabled     *bool
	NewPassword *string
	ResetMFA    bool
}

// ChangeAccount applies an admin's change. The last enabled admin can't be
// demoted or disabled. Security-relevant changes revoke the account's
// tokens.
func (a *Auth) ChangeAccount(ctx context.Context, p Principal, id string, ch AccountChange) error {
	if err := a.admin(p); err != nil {
		return err
	}
	acc, err := a.Accounts.AccountByID(ctx, id)
	if err != nil {
		return err
	}
	losesAdmin := acc.Role == account.Admin && acc.Enabled &&
		((ch.Role != nil && *ch.Role != account.Admin) || (ch.Enabled != nil && !*ch.Enabled))
	if losesAdmin {
		all, err := a.Accounts.ListAccounts(ctx)
		if err != nil {
			return err
		}
		admins := 0
		for _, x := range all {
			if x.Role == account.Admin && x.Enabled {
				admins++
			}
		}
		if admins <= 1 {
			return fmt.Errorf("%w: there must be at least one enabled admin", ErrConflict)
		}
	}
	if ch.Role != nil {
		if *ch.Role != account.Admin && *ch.Role != account.User {
			return ErrInvalid
		}
		acc.Role = *ch.Role
		acc.Revoke()
	}
	if ch.Enabled != nil {
		acc.Enabled = *ch.Enabled
		acc.Revoke()
	}
	if ch.NewPassword != nil {
		if acc.External() {
			return fmt.Errorf("%w: this account signs in with Google", ErrInvalid)
		}
		if err := account.CheckPassword(*ch.NewPassword); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		if acc.PasswordHash, err = a.Hasher.Hash(*ch.NewPassword); err != nil {
			return err
		}
		acc.SucceededLogin()
		acc.Revoke()
	}
	if ch.ResetMFA {
		// Everything that could still prove the old second factor goes.
		acc.ResetMFA()
		if err := a.Accounts.ReplaceRecoveryCodes(ctx, acc.ID, nil); err != nil {
			return err
		}
		for _, c := range mustCreds(a.Accounts.Credentials(ctx, acc.ID)) {
			a.Accounts.DeleteCredential(ctx, acc.ID, c.ID)
		}
	}
	return a.Accounts.SaveAccount(ctx, acc)
}

// ---- Google sign-in ----

type oidcStart struct {
	Verifier string `json:"v"`
}

// GoogleEnabled reports whether Google sign-in is configured.
func (a *Auth) GoogleEnabled() bool { return a.Google != nil && a.Google.Enabled() }

// BeginGoogle returns the URL to send the browser to.
func (a *Auth) BeginGoogle(ctx context.Context) (string, error) {
	if !a.GoogleEnabled() {
		return "", ErrNotFound
	}
	state, verifier := randomToken(24), randomToken(32)
	b, _ := json.Marshal(oidcStart{Verifier: verifier})
	if err := a.Accounts.PutEphemeral(ctx, "oidc-state:"+state, b, a.now().Add(10*time.Minute).Unix()); err != nil {
		return "", err
	}
	return a.Google.AuthURL(state, verifier), nil
}

// FinishGoogle handles the callback: it resolves (or creates) the account
// and stages the login result behind a single-use code for the browser to
// redeem (never a token in the URL). Google only proves the first factor.
func (a *Auth) FinishGoogle(ctx context.Context, state, code string) (string, error) {
	if !a.GoogleEnabled() {
		return "", ErrNotFound
	}
	raw, ok, err := a.Accounts.TakeEphemeral(ctx, "oidc-state:"+state, a.now().Unix())
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%w: the sign-in request expired, try again", ErrUnauthorized)
	}
	var st oidcStart
	json.Unmarshal(raw, &st)
	id, err := a.Google.Exchange(ctx, code, st.Verifier)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnauthorized, err)
	}
	if !id.EmailVerified || id.Subject == "" {
		return "", fmt.Errorf("%w: Google did not confirm the email address", ErrUnauthorized)
	}
	acc, err := a.Accounts.AccountByExternal(ctx, "google", id.Subject)
	if errors.Is(err, ErrNotFound) {
		email, eerr := account.NormalizeEmail(id.Email)
		if eerr != nil {
			return "", fmt.Errorf("%w: %v", ErrUnauthorized, eerr)
		}
		existing, xerr := a.Accounts.AccountByEmail(ctx, email)
		switch {
		case xerr == nil && existing.AuthProvider == "google" && existing.ExternalSubject == "":
			// An admin added this Google address: the first sign-in binds it.
			existing.ExternalSubject = id.Subject
			acc, err = existing, a.Accounts.SaveAccount(ctx, existing)
		case xerr == nil:
			return "", fmt.Errorf("%w: %s already has an account that signs in with a password", ErrConflict, email)
		default:
			acc, err = a.create(ctx, account.Account{ID: newID(), Email: email, AuthProvider: "google",
				ExternalSubject: id.Subject, Role: account.User, Enabled: true, CreatedUnix: a.now().Unix()})
			if errors.Is(err, ErrExists) {
				return "", fmt.Errorf("%w: %s already has an account", ErrConflict, email)
			}
		}
	}
	if err != nil {
		return "", err
	}
	if !acc.Enabled {
		return "", ErrUnauthorized
	}
	result, err := a.pending(acc)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(result)
	exchange := randomToken(24)
	return exchange, a.Accounts.PutEphemeral(ctx, "oidc-exchange:"+exchange, b, a.now().Add(2*time.Minute).Unix())
}

// RedeemGoogle turns the single-use code into the login result.
func (a *Auth) RedeemGoogle(ctx context.Context, exchange string) (LoginResult, error) {
	raw, ok, err := a.Accounts.TakeEphemeral(ctx, "oidc-exchange:"+exchange, a.now().Unix())
	if err != nil {
		return LoginResult{}, err
	}
	if !ok {
		return LoginResult{}, ErrUnauthorized
	}
	var r LoginResult
	return r, json.Unmarshal(raw, &r)
}

// ---- helpers ----

func randomToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

var codeAlphabet = base32.NewEncoding("abcdefghijkmnpqrstuvwxyz23456789").WithPadding(base32.NoPadding)

// recoveryCode: "xxxxx-xxxxx", 50 bits, no easily confused characters.
func recoveryCode() string {
	b := make([]byte, 7)
	rand.Read(b)
	s := codeAlphabet.EncodeToString(b)[:10]
	return s[:5] + "-" + s[5:]
}

func normalizeCode(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return -1
		}
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		return r
	}, strings.TrimSpace(s))
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
