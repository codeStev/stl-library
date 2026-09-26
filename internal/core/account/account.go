// Package account is the domain of user accounts: identity, roles, the
// second factor, and the rules around them (after the campaign
// organizer's accounts context, ADR-0110/0111). Pure.
package account

import (
	"errors"
	"net/mail"
	"strings"
	"unicode/utf8"
)

// Role of an account. The first account ever registered is an Admin.
type Role string

const (
	Admin Role = "ADMIN"
	User  Role = "USER"
)

// MFA method an account proved at enrollment.
type MFA string

const (
	MFANone     MFA = ""
	MFATOTP     MFA = "totp"
	MFAWebAuthn MFA = "webauthn"
)

// Factors a token can carry: a correct password (or Google sign-in) alone
// is only PASSWORD; after the second factor it is PASSWORD+MFA.
const (
	FactorPassword = "PASSWORD"
	FactorMFA      = "MFA"
)

// Account is one user.
type Account struct {
	ID              string
	Email           string
	PasswordHash    string // empty for an external (Google) account
	AuthProvider    string // "google" for an external account
	ExternalSubject string
	Role            Role
	Enabled         bool
	TokenVersion    int // bumped to revoke every token at once
	FailedAttempts  int
	LockedUntil     int64 // unix seconds
	MFA             MFA
	TOTPSecret      string // sealed
	TOTPPending     string // sealed, during enrollment
	CreatedUnix     int64
}

// External reports an account that signs in with an external provider.
func (a Account) External() bool { return a.AuthProvider != "" }

// Password rules (OWASP ASVS): at least 12 characters, at most 128 (bcrypt
// only looks at the first 72 bytes).
const (
	MinPasswordLength = 12
	maxPasswordBytes  = 72
)

var (
	ErrWeakPassword = errors.New("the password must be at least 12 characters long")
	ErrLongPassword = errors.New("the password is too long (at most 72 bytes)")
	ErrBadEmail     = errors.New("that is not a valid email address")
)

// CheckPassword enforces the password rules.
func CheckPassword(pw string) error {
	if utf8.RuneCountInString(pw) < MinPasswordLength {
		return ErrWeakPassword
	}
	if len(pw) > maxPasswordBytes {
		return ErrLongPassword
	}
	return nil
}

// NormalizeEmail returns the canonical form (trimmed, lower case) of a
// plain address.
func NormalizeEmail(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || len(s) > 254 {
		return "", ErrBadEmail
	}
	return s, nil
}

// Lockout after repeated failed logins: a short, self-expiring lock.
const (
	MaxFailedAttempts = 5
	LockoutSeconds    = 15 * 60
)

// Locked reports a lockout still in effect.
func (a Account) Locked(now int64) bool { return a.LockedUntil > now }

// FailedLogin counts a failed attempt and locks after too many.
func (a *Account) FailedLogin(now int64) {
	a.FailedAttempts++
	if a.FailedAttempts >= MaxFailedAttempts {
		a.LockedUntil = now + LockoutSeconds
		a.FailedAttempts = 0
	}
}

// SucceededLogin clears the failure count.
func (a *Account) SucceededLogin() { a.FailedAttempts, a.LockedUntil = 0, 0 }

// Revoke invalidates every token issued so far.
func (a *Account) Revoke() { a.TokenVersion++ }

// ResetMFA clears the second factor; the next login has to enroll again.
func (a *Account) ResetMFA() {
	a.MFA, a.TOTPSecret, a.TOTPPending = MFANone, "", ""
	a.Revoke()
}
