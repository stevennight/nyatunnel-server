package store

import "context"

// Session is a console login. Only the hash of the cookie value is stored.
type Session struct {
	IDHash     string
	UserID     string
	CreatedAt  int64
	LastUsedAt int64
	ExpiresAt  int64
	IP         string
	UserAgent  string
}

func (s *Store) CreateSession(ctx context.Context, sess Session) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (id_hash, user_id, created_at, last_used_at, expires_at, ip, user_agent) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sess.IDHash, sess.UserID, sess.CreatedAt, sess.LastUsedAt, sess.ExpiresAt, sess.IP, truncate(sess.UserAgent, 300))
	return err
}

// SessionUser returns a live session and its user. Expired sessions and disabled users are not found.
func (s *Store) SessionUser(ctx context.Context, idHash string, now int64) (*Session, *User, error) {
	var sess Session
	row := s.db.QueryRowContext(ctx, `SELECT id_hash, user_id, created_at, last_used_at, expires_at, ip, user_agent
		FROM sessions WHERE id_hash = ? AND expires_at > ?`, idHash, now)
	if err := row.Scan(&sess.IDHash, &sess.UserID, &sess.CreatedAt, &sess.LastUsedAt, &sess.ExpiresAt, &sess.IP, &sess.UserAgent); err != nil {
		return nil, nil, notFoundOr(err)
	}
	u, err := s.UserByID(ctx, sess.UserID)
	if err != nil {
		return nil, nil, err
	}
	if u.DisabledAt != nil {
		return nil, nil, ErrNotFound
	}
	return &sess, u, nil
}

// TouchSession records use and moves the expiry (sliding sessions).
func (s *Store) TouchSession(ctx context.Context, idHash string, now, expiresAt int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_used_at = ?, expires_at = ? WHERE id_hash = ?`, now, expiresAt, idHash)
	return err
}

func (s *Store) DeleteSession(ctx context.Context, idHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, idHash)
	return err
}

// DeleteUserSessions ends every session of a user except keepHash (pass "" to end all).
func (s *Store) DeleteUserSessions(ctx context.Context, userID, keepHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND id_hash != ?`, userID, keepHash)
	return err
}

// DeleteExpiredSessions is housekeeping.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now)
	return err
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
