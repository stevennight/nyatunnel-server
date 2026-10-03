package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("store: not found")

// ErrConflict is returned when a unique constraint (name, host, port…) is violated.
var ErrConflict = errors.New("store: conflict")

func conflictOr(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrConflict
	}
	return err
}

func notFoundOr(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// User roles.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// User is an account of the admin console.
type User struct {
	ID            string
	Username      string
	PasswordHash  string
	Role          string
	TOTPSecret    []byte // sealed; nil when TOTP is off
	RecoveryCodes []string
	DisabledAt    *int64
	CreatedAt     int64
	UpdatedAt     int64
}

// IsAdmin reports whether the user is an administrator.
func (u *User) IsAdmin() bool { return u.Role == RoleAdmin }

// TOTPEnabled reports whether the user has a second factor.
func (u *User) TOTPEnabled() bool { return len(u.TOTPSecret) > 0 }

const userCols = `id, username, password_hash, role, totp_secret, recovery_codes, disabled_at, created_at, updated_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var codes sql.NullString
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.TOTPSecret, &codes, &u.DisabledAt, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return nil, notFoundOr(err)
	}
	if codes.Valid && codes.String != "" {
		_ = json.Unmarshal([]byte(codes.String), &u.RecoveryCodes)
	}
	return &u, nil
}

// UserCount returns how many accounts exist (0 means first-run setup is pending).
func (s *Store) UserCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUser inserts a user; ErrConflict when the username is taken.
func (s *Store) CreateUser(ctx context.Context, u *User) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		u.ID, u.Username, u.PasswordHash, u.Role, u.CreatedAt, u.UpdatedAt)
	return conflictOr(err)
}

// CreateFirstAdmin inserts the first account only while no account exists, so two concurrent setup
// requests cannot both succeed.
func (s *Store) CreateFirstAdmin(ctx context.Context, u *User) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		SELECT ?, ?, ?, 'admin', ?, ? WHERE NOT EXISTS (SELECT 1 FROM users)`,
		u.ID, u.Username, u.PasswordHash, u.CreatedAt, u.UpdatedAt)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrConflict
	}
	return nil
}

func (s *Store) UserByID(ctx context.Context, id string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

func (s *Store) UserByName(ctx context.Context, username string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username = ?`, username))
}

// Users lists every account, admins first.
func (s *Store) Users(ctx context.Context) ([]*User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY role = 'user', username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) exec1(ctx context.Context, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return conflictOr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetUserPassword(ctx context.Context, id, hash string, now int64) error {
	return s.exec1(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, hash, now, id)
}

func (s *Store) SetUserRole(ctx context.Context, id, role string, now int64) error {
	return s.exec1(ctx, `UPDATE users SET role = ?, updated_at = ? WHERE id = ?`, role, now, id)
}

// SetUserDisabled disables (at != nil) or re-enables a user. Disabling also ends their sessions.
func (s *Store) SetUserDisabled(ctx context.Context, id string, at *int64, now int64) error {
	if err := s.exec1(ctx, `UPDATE users SET disabled_at = ?, updated_at = ? WHERE id = ?`, at, now, id); err != nil {
		return err
	}
	if at != nil {
		_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id)
		return err
	}
	return nil
}

// SetUserTOTP stores (or with nil secret clears) the sealed TOTP secret and the recovery code hashes.
func (s *Store) SetUserTOTP(ctx context.Context, id string, sealed []byte, recoveryHashes []string, now int64) error {
	var codes any
	if recoveryHashes != nil {
		b, _ := json.Marshal(recoveryHashes)
		codes = string(b)
	}
	return s.exec1(ctx, `UPDATE users SET totp_secret = ?, recovery_codes = ?, updated_at = ? WHERE id = ?`, sealed, codes, now, id)
}

// UseRecoveryCode removes hash from the user's recovery codes; false when it was not there.
func (s *Store) UseRecoveryCode(ctx context.Context, id, hash string, now int64) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	u, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
	if err != nil {
		return false, err
	}
	kept := make([]string, 0, len(u.RecoveryCodes))
	found := false
	for _, c := range u.RecoveryCodes {
		if c == hash && !found {
			found = true
			continue
		}
		kept = append(kept, c)
	}
	if !found {
		return false, nil
	}
	b, _ := json.Marshal(kept)
	if _, err := tx.ExecContext(ctx, `UPDATE users SET recovery_codes = ?, updated_at = ? WHERE id = ?`, string(b), now, id); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
