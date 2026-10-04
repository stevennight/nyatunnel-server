package store

import (
	"context"
	"encoding/json"
	"strings"
)

// Tunnel types.
const (
	TypeHTTPS  = "https"
	TypeTCP    = "tcp"
	TypeUDP    = "udp"
	TypeTCPUDP = "tcpudp" // one remote port, both protocols
)

// UsesTCP reports whether a tunnel type listens for TCP on its remote port.
func UsesTCP(t string) bool { return t == TypeTCP || t == TypeTCPUDP }

// UsesUDP reports whether a tunnel type listens for UDP on its remote port.
func UsesUDP(t string) bool { return t == TypeUDP || t == TypeTCPUDP }

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

	// Access policy for HTTPS tunnels (docs/设计方案.md §6.2).
	AccessPolicy       string // public, password, basic, login
	AccessPasswordHash string
	BasicUsername      string
	PolicyRev          int64 // bumped when credentials change, invalidating gate cookies
	IPAllowlist        string
	Interstitial       bool
	HostRewrite        string

	// Limits; 0 means unlimited.
	BandwidthKbps  int
	MaxConns       int
	MonthlyQuotaMB int
	QuotaAction    string // pause, alert

	// Login gate scope (AccessPolicy "login"): owner, users (owner + LoginUsers) or all accounts.
	LoginAccess string
	LoginUsers  []string // user ids
}

// Login gate scopes.
const (
	LoginOwner = "owner"
	LoginUsers = "users"
	LoginAll   = "all"
)

// LoginAllowed reports whether userID may pass the tunnel's login gate.
func (t *Tunnel) LoginAllowed(userID string) bool {
	switch t.LoginAccess {
	case LoginAll:
		return true
	case LoginUsers:
		for _, id := range t.LoginUsers {
			if id == userID {
				return true
			}
		}
	}
	return userID == t.UserID
}

// Access policies.
const (
	PolicyPublic   = "public"
	PolicyPassword = "password"
	PolicyBasic    = "basic"
	PolicyLogin    = "login"
)

// Live reports whether the tunnel should carry traffic at now (ignoring whether its device is online).
func (t *Tunnel) Live(now int64) bool {
	return t.DeviceID != nil && t.Enabled && !t.PausedByClient && (t.ExpiresAt == nil || now < *t.ExpiresAt)
}

const tunnelCols = `id, user_id, device_id, name, type, domain_id, subdomain, host, remote_port, local_ip, local_port,
	client_can_edit_local, local_loopback_only, client_can_toggle, enabled, paused_by_client, note, expires_at, created_at, updated_at,
	access_policy, access_password_hash, basic_username, policy_rev, ip_allowlist, interstitial, host_rewrite,
	bandwidth_kbps, max_conns, monthly_quota_mb, quota_action, login_access, login_users`

func scanTunnel(row interface{ Scan(...any) error }) (*Tunnel, error) {
	var t Tunnel
	var loginUsers string
	if err := row.Scan(&t.ID, &t.UserID, &t.DeviceID, &t.Name, &t.Type, &t.DomainID, &t.Subdomain, &t.Host, &t.RemotePort,
		&t.LocalIP, &t.LocalPort, &t.ClientCanEditLocal, &t.LocalLoopbackOnly, &t.ClientCanToggle, &t.Enabled, &t.PausedByClient,
		&t.Note, &t.ExpiresAt, &t.CreatedAt, &t.UpdatedAt,
		&t.AccessPolicy, &t.AccessPasswordHash, &t.BasicUsername, &t.PolicyRev, &t.IPAllowlist, &t.Interstitial, &t.HostRewrite,
		&t.BandwidthKbps, &t.MaxConns, &t.MonthlyQuotaMB, &t.QuotaAction, &t.LoginAccess, &loginUsers); err != nil {
		return nil, notFoundOr(err)
	}
	_ = json.Unmarshal([]byte(loginUsers), &t.LoginUsers)
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
	if t.AccessPolicy == "" {
		t.AccessPolicy = PolicyPublic
	}
	if t.QuotaAction == "" {
		t.QuotaAction = "pause"
	}
	if t.PolicyRev == 0 {
		t.PolicyRev = 1
	}
	if t.LoginAccess == "" {
		t.LoginAccess = LoginOwner
	}
	loginUsers, _ := json.Marshal(nonNil(t.LoginUsers))
	_, err := s.db.ExecContext(ctx, `INSERT INTO tunnels (`+tunnelCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET user_id = excluded.user_id, device_id = excluded.device_id, name = excluded.name, type = excluded.type,
		domain_id = excluded.domain_id, subdomain = excluded.subdomain, host = excluded.host, remote_port = excluded.remote_port,
		local_ip = excluded.local_ip, local_port = excluded.local_port, client_can_edit_local = excluded.client_can_edit_local,
		local_loopback_only = excluded.local_loopback_only, client_can_toggle = excluded.client_can_toggle, enabled = excluded.enabled,
		paused_by_client = excluded.paused_by_client, note = excluded.note, expires_at = excluded.expires_at, updated_at = excluded.updated_at,
		access_policy = excluded.access_policy, access_password_hash = excluded.access_password_hash, basic_username = excluded.basic_username,
		policy_rev = excluded.policy_rev, ip_allowlist = excluded.ip_allowlist, interstitial = excluded.interstitial, host_rewrite = excluded.host_rewrite,
		bandwidth_kbps = excluded.bandwidth_kbps, max_conns = excluded.max_conns, monthly_quota_mb = excluded.monthly_quota_mb,
		quota_action = excluded.quota_action, login_access = excluded.login_access, login_users = excluded.login_users`,
		t.ID, t.UserID, t.DeviceID, t.Name, t.Type, t.DomainID, t.Subdomain, t.Host, t.RemotePort, t.LocalIP, t.LocalPort,
		t.ClientCanEditLocal, t.LocalLoopbackOnly, t.ClientCanToggle, t.Enabled, t.PausedByClient, t.Note, t.ExpiresAt, t.CreatedAt, t.UpdatedAt,
		t.AccessPolicy, t.AccessPasswordHash, t.BasicUsername, t.PolicyRev, t.IPAllowlist, t.Interstitial, t.HostRewrite,
		t.BandwidthKbps, t.MaxConns, t.MonthlyQuotaMB, t.QuotaAction, t.LoginAccess, string(loginUsers))
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

// Domain is a root domain (tunnels get <subdomain>.<name>) or a custom domain (one tunnel gets
// the whole name).
type Domain struct {
	ID          string
	Name        string
	AllowUsers  bool // root domains: whether normal users may use it
	CreatedAt   int64
	Kind        string // root, custom
	OwnerUserID *string
	Status      string // custom domains: pending, dns, active, disabled
	CheckedAt   *int64
	CheckError  string
}

const domainCols = `id, name, allow_users, created_at, kind, owner_user_id, status, checked_at, check_error`

func scanDomain(row interface{ Scan(...any) error }) (*Domain, error) {
	var d Domain
	if err := row.Scan(&d.ID, &d.Name, &d.AllowUsers, &d.CreatedAt, &d.Kind, &d.OwnerUserID, &d.Status, &d.CheckedAt, &d.CheckError); err != nil {
		return nil, notFoundOr(err)
	}
	return &d, nil
}

func (s *Store) Domains(ctx context.Context) ([]*Domain, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+domainCols+` FROM domains ORDER BY kind DESC, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Domain{}
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) DomainByID(ctx context.Context, id string) (*Domain, error) {
	return scanDomain(s.db.QueryRowContext(ctx, `SELECT `+domainCols+` FROM domains WHERE id = ?`, id))
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

// UsedPorts returns the remote ports taken for proto ("tcp" or "udp"), including tcpudp tunnels.
func (s *Store) UsedPorts(ctx context.Context, proto string) (map[int]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT remote_port, id FROM tunnels WHERE (type = ? OR type = 'tcpudp') AND remote_port IS NOT NULL`, proto)
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
