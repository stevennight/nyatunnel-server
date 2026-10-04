package store

import (
	"context"
	"encoding/json"
	"strings"
)

// Quota is what a normal user may do without asking an administrator (docs/设计方案.md §4).
type Quota struct {
	// Enabled allows self-service tunnel creation within the limits below.
	Enabled bool `json:"enabled"`
	// MaxTunnels counts all of the user's tunnels, including those an admin created.
	MaxTunnels int `json:"maxTunnels"`
	// Types allowed for self-service (https, tcp, udp).
	Types []string `json:"types"`
	// MaxBandwidthKbps caps self-service tunnels (0 = no cap).
	MaxBandwidthKbps int `json:"maxBandwidthKbps"`
	// MaxDays is the longest lifetime of a self-service tunnel (0 = unlimited).
	MaxDays int `json:"maxDays"`
	// Interstitial forces the first-visit warning page on self-service HTTPS tunnels.
	Interstitial bool `json:"interstitial"`
	// MonthlyTrafficMB limits all of the user's tunnels together (0 = unlimited); applies even
	// when self-service is off.
	MonthlyTrafficMB int `json:"monthlyTrafficMb"`
}

// AllowsType reports whether self-service may create a tunnel of type t; tcpudp needs both tcp
// and udp.
func (q *Quota) AllowsType(t string) bool {
	if t == TypeTCPUDP {
		return q.AllowsType(TypeTCP) && q.AllowsType(TypeUDP)
	}
	for _, x := range q.Types {
		if x == t {
			return true
		}
	}
	return false
}

// ParseQuota decodes a stored quota; a broken value means no quota.
func ParseQuota(s string) Quota {
	var q Quota
	if s != "" {
		_ = json.Unmarshal([]byte(s), &q)
	}
	return q
}

func (s *Store) SetUserQuota(ctx context.Context, id string, q Quota, now int64) error {
	b, _ := json.Marshal(q)
	return s.exec1(ctx, `UPDATE users SET quota = ?, updated_at = ? WHERE id = ?`, string(b), now, id)
}

// UserQuotas returns the quota of every user, for traffic enforcement.
func (s *Store) UserQuotas(ctx context.Context) (map[string]Quota, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, quota FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Quota{}
	for rows.Next() {
		var id, q string
		if err := rows.Scan(&id, &q); err != nil {
			return nil, err
		}
		out[id] = ParseQuota(q)
	}
	return out, rows.Err()
}

// Domain kinds and custom-domain states.
const (
	DomainRoot   = "root"
	DomainCustom = "custom"

	DomainPending  = "pending"  // a user asked for it; an admin must approve
	DomainDNS      = "dns"      // approved, waiting for DNS to point here
	DomainActive   = "active"   // serving
	DomainDisabled = "disabled" // switched off by an admin
)

// CreateCustomDomain inserts a custom domain.
func (s *Store) CreateCustomDomain(ctx context.Context, d *Domain) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO domains (id, name, allow_users, created_at, kind, owner_user_id, status) VALUES (?, ?, 0, ?, 'custom', ?, ?)`,
		d.ID, strings.ToLower(d.Name), d.CreatedAt, d.OwnerUserID, d.Status)
	return conflictOr(err)
}

// SetDomainStatus records a state change or a DNS check result.
func (s *Store) SetDomainStatus(ctx context.Context, id, status string, checkedAt *int64, checkErr string) error {
	return s.exec1(ctx, `UPDATE domains SET status = ?, checked_at = COALESCE(?, checked_at), check_error = ? WHERE id = ?`, status, checkedAt, checkErr, id)
}

// TunnelRequest asks an administrator for a tunnel.
type TunnelRequest struct {
	ID         string
	UserID     string
	DeviceID   *string
	Payload    RequestPayload
	Reason     string
	Status     string // pending, approved, rejected, cancelled
	ReviewNote string
	ReviewedBy *string
	ReviewedAt *int64
	TunnelID   *string
	CreatedAt  int64
}

// RequestPayload is what was asked for.
type RequestPayload struct {
	Type          string `json:"type"`
	Name          string `json:"name,omitempty"`
	DomainID      string `json:"domainId,omitempty"`
	Subdomain     string `json:"subdomain,omitempty"`
	CustomDomain  string `json:"customDomain,omitempty"`
	RemotePort    int    `json:"remotePort,omitempty"`
	LocalIP       string `json:"localIp"`
	LocalPort     int    `json:"localPort"`
	DurationHours int    `json:"durationHours,omitempty"` // 0 = permanent
}

func (s *Store) CreateTunnelRequest(ctx context.Context, r *TunnelRequest) error {
	b, _ := json.Marshal(r.Payload)
	_, err := s.db.ExecContext(ctx, `INSERT INTO tunnel_requests (id, user_id, device_id, payload, reason, status, created_at) VALUES (?, ?, ?, ?, ?, 'pending', ?)`,
		r.ID, r.UserID, r.DeviceID, string(b), r.Reason, r.CreatedAt)
	return err
}

const requestCols = `id, user_id, device_id, payload, reason, status, review_note, reviewed_by, reviewed_at, tunnel_id, created_at`

func scanRequest(row interface{ Scan(...any) error }) (*TunnelRequest, error) {
	var r TunnelRequest
	var payload string
	if err := row.Scan(&r.ID, &r.UserID, &r.DeviceID, &payload, &r.Reason, &r.Status, &r.ReviewNote, &r.ReviewedBy, &r.ReviewedAt, &r.TunnelID, &r.CreatedAt); err != nil {
		return nil, notFoundOr(err)
	}
	_ = json.Unmarshal([]byte(payload), &r.Payload)
	return &r, nil
}

func (s *Store) TunnelRequestByID(ctx context.Context, id string) (*TunnelRequest, error) {
	return scanRequest(s.db.QueryRowContext(ctx, `SELECT `+requestCols+` FROM tunnel_requests WHERE id = ?`, id))
}

// TunnelRequests lists requests, pending first then newest; userID "" means all users.
func (s *Store) TunnelRequests(ctx context.Context, userID string, limit int) ([]*TunnelRequest, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+requestCols+` FROM tunnel_requests WHERE ? = '' OR user_id = ?
		ORDER BY status != 'pending', created_at DESC LIMIT ?`, userID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*TunnelRequest{}
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PendingRequestCount is shown as a badge.
func (s *Store) PendingRequestCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tunnel_requests WHERE status = 'pending'`).Scan(&n)
	return n, err
}

// PendingRequestCountOf counts one user's pending requests.
func (s *Store) PendingRequestCountOf(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tunnel_requests WHERE status = 'pending' AND user_id = ?`, userID).Scan(&n)
	return n, err
}

// ResolveTunnelRequest moves a pending request to approved / rejected / cancelled. ErrNotFound when
// it is no longer pending.
func (s *Store) ResolveTunnelRequest(ctx context.Context, id, status, note string, reviewer *string, tunnelID *string, now int64) error {
	return s.exec1(ctx, `UPDATE tunnel_requests SET status = ?, review_note = ?, reviewed_by = ?, reviewed_at = ?, tunnel_id = ? WHERE id = ? AND status = 'pending'`,
		status, note, reviewer, now, tunnelID, id)
}

// Channel is a notification target.
type Channel struct {
	ID        string
	Kind      string // webhook, telegram
	Name      string
	Config    []byte // sealed JSON
	Events    []string
	Enabled   bool
	LastError string
	LastSent  *int64
	CreatedAt int64
}

func (s *Store) Channels(ctx context.Context) ([]*Channel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, name, config, events, enabled, last_error, last_sent, created_at FROM channels ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Channel{}
	for rows.Next() {
		var c Channel
		var events string
		if err := rows.Scan(&c.ID, &c.Kind, &c.Name, &c.Config, &events, &c.Enabled, &c.LastError, &c.LastSent, &c.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(events), &c.Events)
		out = append(out, &c)
	}
	return out, rows.Err()
}

// SaveChannel inserts or replaces a channel.
func (s *Store) SaveChannel(ctx context.Context, c *Channel) error {
	events, _ := json.Marshal(nonNil(c.Events))
	_, err := s.db.ExecContext(ctx, `INSERT INTO channels (id, kind, name, config, events, enabled, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET name = excluded.name, config = excluded.config, events = excluded.events, enabled = excluded.enabled`,
		c.ID, c.Kind, c.Name, c.Config, string(events), c.Enabled, c.CreatedAt)
	return err
}

func (s *Store) DeleteChannel(ctx context.Context, id string) error {
	return s.exec1(ctx, `DELETE FROM channels WHERE id = ?`, id)
}

// ChannelResult records the outcome of the latest delivery.
func (s *Store) ChannelResult(ctx context.Context, id, errMsg string, at int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE channels SET last_error = ?, last_sent = ? WHERE id = ?`, truncate(errMsg, 500), at, id)
	return err
}

// TunnelCount returns how many tunnels a user has.
func (s *Store) TunnelCount(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tunnels WHERE user_id = ?`, userID).Scan(&n)
	return n, err
}
