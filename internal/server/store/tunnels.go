package store

import (
	"context"
	"strings"
)

// Tunnel types.
const (
	TypeHTTPS = "https"
	TypeTCP   = "tcp"
	TypeUDP   = "udp"
)

// Tunnel is one public entry point (see docs/设计方案.md §6).
type Tunnel struct {
	ID                 string
	UserID             string
	DeviceID           *string
	Name               string
	Type               string // https, tcp, udp
	DomainID           *string
	Subdomain          *string
	Host               *string // https: subdomain.domain
	RemotePort         *int    // tcp/udp
	LocalIP            string
	LocalPort          int
	ClientCanEditLocal bool
	LocalLoopbackOnly  bool
	ClientCanToggle    bool
	Enabled            bool
	PausedByClient     bool
	Note               string
	ExpiresAt          *int64
	CreatedAt          int64
	UpdatedAt          int64
}

// Live reports whether the tunnel should carry traffic at now (ignoring whether its device is online).
func (t *Tunnel) Live(now int64) bool {
	return t.DeviceID != nil && t.Enabled && !t.PausedByClient && (t.ExpiresAt == nil || now < *t.ExpiresAt)
}

const tunnelCols = `id, user_id, device_id, name, type, domain_id, subdomain, host, remote_port, local_ip, local_port,
	client_can_edit_local, local_loopback_only, client_can_toggle, enabled, paused_by_client, note, expires_at, created_at, updated_at`

func scanTunnel(row interface{ Scan(...any) error }) (*Tunnel, error) {
	var t Tunnel
	if err := row.Scan(&t.ID, &t.UserID, &t.DeviceID, &t.Name, &t.Type, &t.DomainID, &t.Subdomain, &t.Host, &t.RemotePort,
		&t.LocalIP, &t.LocalPort, &t.ClientCanEditLocal, &t.LocalLoopbackOnly, &t.ClientCanToggle, &t.Enabled, &t.PausedByClient,
		&t.Note, &t.ExpiresAt, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, notFoundOr(err)
	}
	return &t, nil
}

func (s *Store) queryTunnels(ctx context.Context, where string, args ...any) ([]*Tunnel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+tunnelCols+` FROM tunnels `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Tunnel{}
	for rows.Next() {
		t, err := scanTunnel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) TunnelByID(ctx context.Context, id string) (*Tunnel, error) {
	return scanTunnel(s.db.QueryRowContext(ctx, `SELECT `+tunnelCols+` FROM tunnels WHERE id = ?`, id))
}

// Tunnels lists tunnels, all of them when userID is "".
func (s *Store) Tunnels(ctx context.Context, userID string) ([]*Tunnel, error) {
	return s.queryTunnels(ctx, `WHERE ? = '' OR user_id = ? ORDER BY name`, userID, userID)
}

// DeviceTunnels lists the tunnels assigned to a device.
func (s *Store) DeviceTunnels(ctx context.Context, deviceID string) ([]*Tunnel, error) {
	return s.queryTunnels(ctx, `WHERE device_id = ? ORDER BY name`, deviceID)
}

// AllTunnels returns every tunnel (used to rebuild the routing table).
func (s *Store) AllTunnels(ctx context.Context) ([]*Tunnel, error) {
	return s.queryTunnels(ctx, `ORDER BY id`)
}

// SaveTunnel inserts or fully updates a tunnel; ErrConflict for a taken name, host or port.
func (s *Store) SaveTunnel(ctx context.Context, t *Tunnel) error {
	if t.Host != nil {
		h := strings.ToLower(*t.Host)
		t.Host = &h
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO tunnels (`+tunnelCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET user_id = excluded.user_id, device_id = excluded.device_id, name = excluded.name, type = excluded.type,
		domain_id = excluded.domain_id, subdomain = excluded.subdomain, host = excluded.host, remote_port = excluded.remote_port,
		local_ip = excluded.local_ip, local_port = excluded.local_port, client_can_edit_local = excluded.client_can_edit_local,
		local_loopback_only = excluded.local_loopback_only, client_can_toggle = excluded.client_can_toggle, enabled = excluded.enabled,
		paused_by_client = excluded.paused_by_client, note = excluded.note, expires_at = excluded.expires_at, updated_at = excluded.updated_at`,
		t.ID, t.UserID, t.DeviceID, t.Name, t.Type, t.DomainID, t.Subdomain, t.Host, t.RemotePort, t.LocalIP, t.LocalPort,
		t.ClientCanEditLocal, t.LocalLoopbackOnly, t.ClientCanToggle, t.Enabled, t.PausedByClient, t.Note, t.ExpiresAt, t.CreatedAt, t.UpdatedAt)
	return conflictOr(err)
}

func (s *Store) DeleteTunnel(ctx context.Context, id string) error {
	return s.exec1(ctx, `DELETE FROM tunnels WHERE id = ?`, id)
}

// DisableUserTunnels turns off every tunnel of a user (when the user is disabled).
func (s *Store) DisableUserTunnels(ctx context.Context, userID string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tunnels SET enabled = 0, updated_at = ? WHERE user_id = ? AND enabled = 1`, now, userID)
	return err
}

// Domain is a tunnel root domain: tunnels get <subdomain>.<name>.
type Domain struct {
	ID         string
	Name       string
	AllowUsers bool
	CreatedAt  int64
}

func (s *Store) Domains(ctx context.Context) ([]*Domain, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, allow_users, created_at FROM domains ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Domain{}
	for rows.Next() {
		var d Domain
		if err := rows.Scan(&d.ID, &d.Name, &d.AllowUsers, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &d)
	}
	return out, rows.Err()
}

func (s *Store) DomainByID(ctx context.Context, id string) (*Domain, error) {
	var d Domain
	err := s.db.QueryRowContext(ctx, `SELECT id, name, allow_users, created_at FROM domains WHERE id = ?`, id).Scan(&d.ID, &d.Name, &d.AllowUsers, &d.CreatedAt)
	if err != nil {
		return nil, notFoundOr(err)
	}
	return &d, nil
}

func (s *Store) CreateDomain(ctx context.Context, d *Domain) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO domains (id, name, allow_users, created_at) VALUES (?, ?, ?, ?)`, d.ID, strings.ToLower(d.Name), d.AllowUsers, d.CreatedAt)
	return conflictOr(err)
}

func (s *Store) SetDomainAllowUsers(ctx context.Context, id string, allow bool) error {
	return s.exec1(ctx, `UPDATE domains SET allow_users = ? WHERE id = ?`, allow, id)
}

// DeleteDomain removes a domain; ErrConflict while tunnels still use it.
func (s *Store) DeleteDomain(ctx context.Context, id string) error {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tunnels WHERE domain_id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrConflict
	}
	return s.exec1(ctx, `DELETE FROM domains WHERE id = ?`, id)
}

// PortPool is a range of ports TCP or UDP tunnels may use.
type PortPool struct {
	ID         string
	Proto      string
	RangeStart int
	RangeEnd   int
	CreatedAt  int64
}

func (s *Store) PortPools(ctx context.Context) ([]*PortPool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, proto, range_start, range_end, created_at FROM port_pools ORDER BY proto, range_start`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*PortPool{}
	for rows.Next() {
		var p PortPool
		if err := rows.Scan(&p.ID, &p.Proto, &p.RangeStart, &p.RangeEnd, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

func (s *Store) CreatePortPool(ctx context.Context, p *PortPool) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO port_pools (id, proto, range_start, range_end, created_at) VALUES (?, ?, ?, ?, ?)`,
		p.ID, p.Proto, p.RangeStart, p.RangeEnd, p.CreatedAt)
	return err
}

func (s *Store) DeletePortPool(ctx context.Context, id string) error {
	return s.exec1(ctx, `DELETE FROM port_pools WHERE id = ?`, id)
}

// UsedPorts returns the remote ports taken by tunnels of proto.
func (s *Store) UsedPorts(ctx context.Context, proto string) (map[int]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT remote_port, id FROM tunnels WHERE type = ? AND remote_port IS NOT NULL`, proto)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]string{}
	for rows.Next() {
		var port int
		var id string
		if err := rows.Scan(&port, &id); err != nil {
			return nil, err
		}
		out[port] = id
	}
	return out, rows.Err()
}
