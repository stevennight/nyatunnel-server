package store

import (
	"context"
	"database/sql"
	"errors"
)

// AuditEvent is one row of the audit trail.
type AuditEvent struct {
	ID        int64
	At        int64
	ActorType string // user, device, system, anonymous
	ActorID   string
	ActorName string
	Action    string
	Target    string
	Detail    string
	IP        string
}

func (s *Store) Audit(ctx context.Context, e AuditEvent) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_logs (at, actor_type, actor_id, actor_name, action, target, detail, ip) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.At, e.ActorType, e.ActorID, e.ActorName, e.Action, e.Target, truncate(e.Detail, 2000), e.IP)
	return err
}

// AuditLogs returns events older than beforeID (0 = newest), newest first.
func (s *Store) AuditLogs(ctx context.Context, beforeID int64, limit int) ([]*AuditEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, at, actor_type, actor_id, actor_name, action, target, detail, ip FROM audit_logs
		WHERE ? = 0 OR id < ? ORDER BY id DESC LIMIT ?`, beforeID, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AuditEvent{}
	for rows.Next() {
		var e AuditEvent
		if err := rows.Scan(&e.ID, &e.At, &e.ActorType, &e.ActorID, &e.ActorName, &e.Action, &e.Target, &e.Detail, &e.IP); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// DeleteAuditBefore is retention housekeeping.
func (s *Store) DeleteAuditBefore(ctx context.Context, before int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM audit_logs WHERE at < ?`, before)
	return err
}

// Setting returns a stored setting, or "" when it has never been set.
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
