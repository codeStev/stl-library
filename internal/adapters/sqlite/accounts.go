package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/codeStev/stl-library/internal/app"
	"github.com/codeStev/stl-library/internal/core/account"
)

var _ app.AccountStore = (*Store)(nil)

const accountColumns = `id, email, password_hash, auth_provider, external_subject, role, enabled, token_version,
	failed_attempts, locked_until, mfa, totp_secret, totp_pending, created_unix`

func scanAccount(row interface{ Scan(...any) error }) (account.Account, error) {
	var a account.Account
	var role, mfa string
	err := row.Scan(&a.ID, &a.Email, &a.PasswordHash, &a.AuthProvider, &a.ExternalSubject, &role, &a.Enabled,
		&a.TokenVersion, &a.FailedAttempts, &a.LockedUntil, &mfa, &a.TOTPSecret, &a.TOTPPending, &a.CreatedUnix)
	a.Role, a.MFA = account.Role(role), account.MFA(mfa)
	return a, notFound(err)
}

func uniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// CreateAccount inserts an account; the first one ever becomes Admin (in
// the same transaction, so two simultaneous first registrations can't both
// be admins).
func (s *Store) CreateAccount(ctx context.Context, a account.Account) (account.Account, error) {
	return s.createAccount(ctx, a, false)
}

// CreateFirstAccount inserts a as the admin only if there is no account
// yet.
func (s *Store) CreateFirstAccount(ctx context.Context, a account.Account) (account.Account, error) {
	return s.createAccount(ctx, a, true)
}

func (s *Store) createAccount(ctx context.Context, a account.Account, onlyFirst bool) (account.Account, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return a, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM account`).Scan(&n); err != nil {
		return a, err
	}
	if n > 0 && onlyFirst {
		return a, app.ErrExists
	}
	if n == 0 {
		a.Role = account.Admin
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO account (`+accountColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.Email, a.PasswordHash, a.AuthProvider, a.ExternalSubject, string(a.Role), a.Enabled, a.TokenVersion,
		a.FailedAttempts, a.LockedUntil, string(a.MFA), a.TOTPSecret, a.TOTPPending, a.CreatedUnix)
	if uniqueViolation(err) {
		return a, app.ErrExists
	}
	if err != nil {
		return a, err
	}
	return a, tx.Commit()
}

func (s *Store) AccountByEmail(ctx context.Context, email string) (account.Account, error) {
	return scanAccount(s.db.QueryRowContext(ctx, `SELECT `+accountColumns+` FROM account WHERE email = ?`, email))
}

func (s *Store) AccountByID(ctx context.Context, id string) (account.Account, error) {
	return scanAccount(s.db.QueryRowContext(ctx, `SELECT `+accountColumns+` FROM account WHERE id = ?`, id))
}

func (s *Store) AccountByExternal(ctx context.Context, provider, subject string) (account.Account, error) {
	return scanAccount(s.db.QueryRowContext(ctx, `SELECT `+accountColumns+` FROM account
		WHERE auth_provider = ? AND external_subject = ?`, provider, subject))
}

// SaveAccount updates everything but the id and creation time.
func (s *Store) SaveAccount(ctx context.Context, a account.Account) error {
	res, err := s.db.ExecContext(ctx, `UPDATE account SET email = ?, password_hash = ?, auth_provider = ?,
		external_subject = ?, role = ?, enabled = ?, token_version = ?, failed_attempts = ?, locked_until = ?,
		mfa = ?, totp_secret = ?, totp_pending = ? WHERE id = ?`,
		a.Email, a.PasswordHash, a.AuthProvider, a.ExternalSubject, string(a.Role), a.Enabled, a.TokenVersion,
		a.FailedAttempts, a.LockedUntil, string(a.MFA), a.TOTPSecret, a.TOTPPending, a.ID)
	if uniqueViolation(err) {
		return app.ErrExists
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return app.ErrNotFound
	}
	return nil
}

func (s *Store) ListAccounts(ctx context.Context) ([]account.Account, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+accountColumns+` FROM account ORDER BY created_unix, email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []account.Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---- recovery codes ----

func (s *Store) ReplaceRecoveryCodes(ctx context.Context, accountID string, hashes []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_code WHERE account_id = ?`, accountID); err != nil {
		return err
	}
	for _, h := range hashes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO recovery_code (account_id, hash) VALUES (?,?)`, accountID, h); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) RecoveryCodes(ctx context.Context, accountID string) ([]app.RecoveryCode, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, hash FROM recovery_code WHERE account_id = ? AND used_unix = 0 ORDER BY id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.RecoveryCode
	for rows.Next() {
		var c app.RecoveryCode
		if err := rows.Scan(&c.ID, &c.Hash); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UseRecoveryCode marks a code used; false if it already was (a
// concurrent use of the same code loses).
func (s *Store) UseRecoveryCode(ctx context.Context, id int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE recovery_code SET used_unix = unixepoch() WHERE id = ? AND used_unix = 0`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ---- device sessions ----

func (s *Store) AddSession(ctx context.Context, x app.Session) error {
	// Expired sessions are dropped along the way.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM account_session WHERE expires_unix < ?`, x.CreatedUnix); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO account_session (id, account_id, created_unix, expires_unix, user_agent, ip)
		VALUES (?,?,?,?,?,?)`, x.ID, x.AccountID, x.CreatedUnix, x.ExpiresUnix, x.UserAgent, x.IP)
	return err
}

const sessionColumns = `id, account_id, created_unix, expires_unix, revoked_unix, user_agent, ip`

func scanSession(row interface{ Scan(...any) error }) (app.Session, error) {
	var x app.Session
	err := row.Scan(&x.ID, &x.AccountID, &x.CreatedUnix, &x.ExpiresUnix, &x.RevokedUnix, &x.UserAgent, &x.IP)
	return x, notFound(err)
}

func (s *Store) Session(ctx context.Context, id string) (app.Session, error) {
	return scanSession(s.db.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM account_session WHERE id = ?`, id))
}

// Sessions lists the active (unrevoked, unexpired) sessions, newest first.
func (s *Store) Sessions(ctx context.Context, accountID string, now int64) ([]app.Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sessionColumns+` FROM account_session
		WHERE account_id = ? AND revoked_unix = 0 AND expires_unix > ? ORDER BY created_unix DESC`, accountID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Session
	for rows.Next() {
		x, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) RevokeSession(ctx context.Context, accountID, id string, now int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE account_session SET revoked_unix = ? WHERE id = ? AND account_id = ? AND revoked_unix = 0`,
		now, id, accountID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return app.ErrNotFound
	}
	return nil
}

// ---- passkeys ----

func (s *Store) AddCredential(ctx context.Context, accountID string, c app.Credential) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO webauthn_credential (id, account_id, data, name, created_unix) VALUES (?,?,?,?,?)`,
		c.ID, accountID, c.Data, c.Name, c.CreatedUnix)
	if uniqueViolation(err) {
		return app.ErrExists
	}
	return err
}

func (s *Store) Credentials(ctx context.Context, accountID string) ([]app.Credential, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, data, name, created_unix FROM webauthn_credential
		WHERE account_id = ? ORDER BY created_unix`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Credential
	for rows.Next() {
		var c app.Credential
		if err := rows.Scan(&c.ID, &c.Data, &c.Name, &c.CreatedUnix); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) UpdateCredential(ctx context.Context, accountID string, id, data []byte) error {
	res, err := s.db.ExecContext(ctx, `UPDATE webauthn_credential SET data = ? WHERE id = ? AND account_id = ?`, data, id, accountID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return app.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteCredential(ctx context.Context, accountID string, id []byte) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM webauthn_credential WHERE id = ? AND account_id = ?`, id, accountID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return app.ErrNotFound
	}
	return nil
}

// ---- short-lived values ----

func (s *Store) PutEphemeral(ctx context.Context, key string, value []byte, expiresUnix int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO ephemeral (key, value, expires_unix) VALUES (?,?,?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, expires_unix = excluded.expires_unix`, key, value, expiresUnix)
	return err
}

// TakeEphemeral returns and deletes a value (single use); expired values
// are cleaned up along the way.
func (s *Store) TakeEphemeral(ctx context.Context, key string, now int64) ([]byte, bool, error) {
	var v []byte
	err := s.db.QueryRowContext(ctx, `DELETE FROM ephemeral WHERE key = ? AND expires_unix > ? RETURNING value`, key, now).Scan(&v)
	if _, cerr := s.db.ExecContext(ctx, `DELETE FROM ephemeral WHERE expires_unix <= ?`, now); cerr != nil && err == nil {
		err = cerr
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return v, err == nil, err
}
