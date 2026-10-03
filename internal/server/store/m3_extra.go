package store

import (
	"context"
	"encoding/json"
)

// UserSessions lists a user's live console sessions, newest first.
func (s *Store) UserSessions(ctx context.Context, userID string, now int64) ([]*Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id_hash, user_id, created_at, last_used_at, expires_at, ip, user_agent FROM sessions
		WHERE user_id = ? AND expires_at > ? ORDER BY last_used_at DESC`, userID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Session{}
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.IDHash, &s.UserID, &s.CreatedAt, &s.LastUsedAt, &s.ExpiresAt, &s.IP, &s.UserAgent); err != nil {
			return nil, err
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

// DeleteUserSession ends one session of a user.
func (s *Store) DeleteUserSession(ctx context.Context, userID, idHash string) error {
	return s.exec1(ctx, `DELETE FROM sessions WHERE user_id = ? AND id_hash = ?`, userID, idHash)
}

// SetRecoveryCodes replaces the recovery code hashes of a user who has TOTP.
func (s *Store) SetRecoveryCodes(ctx context.Context, userID string, hashes []string, now int64) error {
	b, _ := json.Marshal(hashes)
	return s.exec1(ctx, `UPDATE users SET recovery_codes = ?, updated_at = ? WHERE id = ? AND totp_secret IS NOT NULL`, string(b), now, userID)
}

// PendingEnrollments lists unused, unexpired enrollment codes (userID "" = all).
func (s *Store) PendingEnrollments(ctx context.Context, userID string, now int64) ([]*Enrollment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, created_by, code_hash, device_name_hint, tunnel_ids, expires_at, created_at
		FROM enrollments WHERE used_at IS NULL AND expires_at > ? AND (? = '' OR user_id = ?) ORDER BY created_at DESC`, now, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Enrollment{}
	for rows.Next() {
		var e Enrollment
		var ids string
		if err := rows.Scan(&e.ID, &e.UserID, &e.CreatedBy, &e.CodeHash, &e.DeviceNameHint, &ids, &e.ExpiresAt, &e.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(ids), &e.TunnelIDs)
		out = append(out, &e)
	}
	return out, rows.Err()
}

// EnrollmentByID returns an enrollment regardless of state.
func (s *Store) EnrollmentByID(ctx context.Context, id string) (*Enrollment, error) {
	var e Enrollment
	var ids string
	err := s.db.QueryRowContext(ctx, `SELECT id, user_id, created_by, code_hash, device_name_hint, tunnel_ids, expires_at, created_at FROM enrollments WHERE id = ?`, id).
		Scan(&e.ID, &e.UserID, &e.CreatedBy, &e.CodeHash, &e.DeviceNameHint, &ids, &e.ExpiresAt, &e.CreatedAt)
	if err != nil {
		return nil, notFoundOr(err)
	}
	_ = json.Unmarshal([]byte(ids), &e.TunnelIDs)
	return &e, nil
}

// CancelEnrollment expires an unused code.
func (s *Store) CancelEnrollment(ctx context.Context, id string, now int64) error {
	return s.exec1(ctx, `UPDATE enrollments SET expires_at = ? WHERE id = ? AND used_at IS NULL`, now, id)
}

// PortsInRange counts tunnels using ports of proto inside [start, end].
func (s *Store) PortsInRange(ctx context.Context, proto string, start, end int) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tunnels WHERE type = ? AND remote_port BETWEEN ? AND ?`, proto, start, end).Scan(&n)
	return n, err
}

// PortPoolByID returns one pool.
func (s *Store) PortPoolByID(ctx context.Context, id string) (*PortPool, error) {
	var p PortPool
	err := s.db.QueryRowContext(ctx, `SELECT id, proto, range_start, range_end, created_at FROM port_pools WHERE id = ?`, id).
		Scan(&p.ID, &p.Proto, &p.RangeStart, &p.RangeEnd, &p.CreatedAt)
	if err != nil {
		return nil, notFoundOr(err)
	}
	return &p, nil
}
