package store

import (
	"context"
	"encoding/json"
)

// Device is an enrolled client installation.
type Device struct {
	ID            string
	UserID        string
	Name          string
	PublicKey     []byte
	Platform      string
	ClientVersion string
	GUI           bool
	ConfigRev     int64
	LastIP        string
	LastSeenAt    *int64
	RevokedAt     *int64
	CreatedAt     int64
}

const deviceCols = `id, user_id, name, public_key, platform, client_version, gui, config_rev, last_ip, last_seen_at, revoked_at, created_at`

func scanDevice(row interface{ Scan(...any) error }) (*Device, error) {
	var d Device
	if err := row.Scan(&d.ID, &d.UserID, &d.Name, &d.PublicKey, &d.Platform, &d.ClientVersion, &d.GUI, &d.ConfigRev,
		&d.LastIP, &d.LastSeenAt, &d.RevokedAt, &d.CreatedAt); err != nil {
		return nil, notFoundOr(err)
	}
	return &d, nil
}

func (s *Store) DeviceByID(ctx context.Context, id string) (*Device, error) {
	return scanDevice(s.db.QueryRowContext(ctx, `SELECT `+deviceCols+` FROM devices WHERE id = ?`, id))
}

// Devices lists devices, all of them when userID is "", newest first.
func (s *Store) Devices(ctx context.Context, userID string) ([]*Device, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+deviceCols+` FROM devices WHERE ? = '' OR user_id = ? ORDER BY revoked_at IS NOT NULL, created_at DESC`, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ActiveDeviceIDs returns the ids of a user's devices that are not revoked.
func (s *Store) ActiveDeviceIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM devices WHERE user_id = ? AND revoked_at IS NULL`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Store) RenameDevice(ctx context.Context, id, name string) error {
	return s.exec1(ctx, `UPDATE devices SET name = ? WHERE id = ?`, name, id)
}

// RevokeDevice marks a device revoked. Its tunnels stay but become unassigned.
func (s *Store) RevokeDevice(ctx context.Context, id string, now int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE devices SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, now, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tunnels SET device_id = NULL, updated_at = ? WHERE device_id = ?`, now, id); err != nil {
		return err
	}
	return tx.Commit()
}

// DeviceSeen records a successful connection.
func (s *Store) DeviceSeen(ctx context.Context, id, ip, clientVersion string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE devices SET last_ip = ?, last_seen_at = ?, client_version = CASE WHEN ? = '' THEN client_version ELSE ? END WHERE id = ?`,
		ip, now, clientVersion, clientVersion, id)
	return err
}

// BumpDeviceRev increments the config revision of devices whose configuration changed.
func (s *Store) BumpDeviceRev(ctx context.Context, ids ...string) error {
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE devices SET config_rev = config_rev + 1 WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return nil
}

// Enrollment is a one-time code that lets a device register for a user.
type Enrollment struct {
	ID             string
	UserID         string
	CreatedBy      string
	CodeHash       string
	DeviceNameHint string
	TunnelIDs      []string
	ExpiresAt      int64
	UsedAt         *int64
	DeviceID       *string
	CreatedAt      int64
}

func (s *Store) CreateEnrollment(ctx context.Context, e *Enrollment) error {
	ids, _ := json.Marshal(nonNil(e.TunnelIDs))
	_, err := s.db.ExecContext(ctx, `INSERT INTO enrollments (id, user_id, created_by, code_hash, device_name_hint, tunnel_ids, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, e.ID, e.UserID, e.CreatedBy, e.CodeHash, e.DeviceNameHint, string(ids), e.ExpiresAt, e.CreatedAt)
	return conflictOr(err)
}

// UsableEnrollment returns an unused, unexpired enrollment by code hash.
func (s *Store) UsableEnrollment(ctx context.Context, codeHash string, now int64) (*Enrollment, error) {
	var e Enrollment
	var ids string
	err := s.db.QueryRowContext(ctx, `SELECT id, user_id, created_by, code_hash, device_name_hint, tunnel_ids, expires_at, created_at
		FROM enrollments WHERE code_hash = ? AND used_at IS NULL AND expires_at > ?`, codeHash, now).
		Scan(&e.ID, &e.UserID, &e.CreatedBy, &e.CodeHash, &e.DeviceNameHint, &ids, &e.ExpiresAt, &e.CreatedAt)
	if err != nil {
		return nil, notFoundOr(err)
	}
	_ = json.Unmarshal([]byte(ids), &e.TunnelIDs)
	return &e, nil
}

// ClaimEnrollment consumes the enrollment and creates the device in one transaction, then hands
// the preset tunnels (still unassigned and owned by the same user) to the new device.
// ErrNotFound when the code was used or expired in the meantime; ErrConflict for a reused key.
func (s *Store) ClaimEnrollment(ctx context.Context, enrollmentID string, d *Device, now int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var ids string
	err = tx.QueryRowContext(ctx, `UPDATE enrollments SET used_at = ?, device_id = ? WHERE id = ? AND used_at IS NULL AND expires_at > ? RETURNING tunnel_ids`,
		now, d.ID, enrollmentID, now).Scan(&ids)
	if err != nil {
		return notFoundOr(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO devices (id, user_id, name, public_key, platform, client_version, gui, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.UserID, d.Name, d.PublicKey, d.Platform, d.ClientVersion, d.GUI, d.CreatedAt); err != nil {
		return conflictOr(err)
	}
	var tunnelIDs []string
	_ = json.Unmarshal([]byte(ids), &tunnelIDs)
	for _, id := range tunnelIDs {
		if _, err := tx.ExecContext(ctx, `UPDATE tunnels SET device_id = ?, updated_at = ? WHERE id = ? AND user_id = ? AND device_id IS NULL`,
			d.ID, now, id, d.UserID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteStaleEnrollments is housekeeping.
func (s *Store) DeleteStaleEnrollments(ctx context.Context, before int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM enrollments WHERE expires_at < ?`, before)
	return err
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
