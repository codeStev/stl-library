package account

import (
	"strings"
	"testing"
)

func TestPasswordRules(t *testing.T) {
	if CheckPassword("short") != ErrWeakPassword || CheckPassword("elevenchars") != ErrWeakPassword {
		t.Error("short passwords accepted")
	}
	if CheckPassword("twelve chars") != nil || CheckPassword("äöüäöüäöüäöü") != nil {
		t.Error("12 characters refused")
	}
	if CheckPassword(strings.Repeat("x", 73)) != ErrLongPassword {
		t.Error("too long accepted")
	}
}

func TestNormalizeEmail(t *testing.T) {
	if e, err := NormalizeEmail("  Me@Example.ORG "); err != nil || e != "me@example.org" {
		t.Errorf("%q %v", e, err)
	}
	for _, bad := range []string{"", "no-at", "Name <a@b.c>", "a@b.c, d@e.f"} {
		if _, err := NormalizeEmail(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestLockoutAfterRepeatedFailures(t *testing.T) {
	var a Account
	for i := 0; i < MaxFailedAttempts-1; i++ {
		a.FailedLogin(1000)
	}
	if a.Locked(1000) {
		t.Fatal("locked too early")
	}
	a.FailedLogin(1000)
	if !a.Locked(1000) || a.Locked(1000+LockoutSeconds) {
		t.Errorf("lock: until %d", a.LockedUntil)
	}
	a.SucceededLogin()
	if a.Locked(1000) || a.FailedAttempts != 0 {
		t.Error("success did not clear")
	}
}

func TestResetMFARevokesTokens(t *testing.T) {
	a := Account{MFA: MFATOTP, TOTPSecret: "s", TokenVersion: 3}
	a.ResetMFA()
	if a.MFA != MFANone || a.TOTPSecret != "" || a.TokenVersion != 4 {
		t.Errorf("%+v", a)
	}
}
