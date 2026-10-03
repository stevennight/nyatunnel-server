package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/stevennight/nyatunnel-common/tunnelproto"

	"nyatunnel-server/internal/server/auth"
	"nyatunnel-server/internal/server/notify"
	"nyatunnel-server/internal/server/rules"
	"nyatunnel-server/internal/server/store"
)

// maxPendingRequests keeps one user (or a misbehaving device) from flooding the review queue.
const maxPendingRequests = 5

type requestView struct {
	ID         string               `json:"id"`
	UserID     string               `json:"userId"`
	Username   string               `json:"username"`
	DeviceID   *string              `json:"deviceId"`
	DeviceName string               `json:"deviceName"`
	Payload    store.RequestPayload `json:"payload"`
	Reason     string               `json:"reason"`
	Status     string               `json:"status"`
	ReviewNote string               `json:"reviewNote"`
	ReviewedAt *int64               `json:"reviewedAt"`
	TunnelID   *string              `json:"tunnelId"`
	CreatedAt  int64                `json:"createdAt"`
}

func (s *server) handleListRequests(w http.ResponseWriter, r *http.Request, p *principal) {
	scope := p.user.ID
	if p.admin() {
		scope = r.URL.Query().Get("userId")
	}
	reqs, err := s.Store.TunnelRequests(r.Context(), scope, 200)
	if err != nil {
		s.fail(w, "list requests", err)
		return
	}
	names, devices := s.usernames(r), s.deviceNames(r)
	out := make([]requestView, 0, len(reqs))
	for _, q := range reqs {
		v := requestView{ID: q.ID, UserID: q.UserID, Username: names[q.UserID], DeviceID: q.DeviceID, Payload: q.Payload, Reason: q.Reason,
			Status: q.Status, ReviewNote: q.ReviewNote, ReviewedAt: q.ReviewedAt, TunnelID: q.TunnelID, CreatedAt: q.CreatedAt}
		if q.DeviceID != nil {
			v.DeviceName = devices[*q.DeviceID]
		}
		out = append(out, v)
	}
	var pending int
	if p.admin() {
		pending, _ = s.Store.PendingRequestCount(r.Context())
	} else {
		pending, _ = s.Store.PendingRequestCountOf(r.Context(), p.user.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": out, "pending": pending})
}

// validRequest checks a request payload loosely: the administrator decides the details.
func validRequest(pl *store.RequestPayload, reason string) error {
	switch pl.Type {
	case store.TypeHTTPS, store.TypeTCP, store.TypeUDP:
	default:
		return &rules.Error{Code: "invalid_type", Message: "类型必须是 https、tcp 或 udp"}
	}
	if err := rules.LocalTarget(pl.LocalIP, pl.LocalPort, false); err != nil {
		return err
	}
	if pl.DurationHours < 0 || pl.DurationHours > 24*366 {
		return &rules.Error{Code: "invalid_duration", Message: "时长无效"}
	}
	if utf8.RuneCountInString(reason) > 500 || len(pl.Name) > 40 || len(pl.Subdomain) > 63 || len(pl.CustomDomain) > 200 {
		return &rules.Error{Code: "too_long", Message: "内容过长"}
	}
	return nil
}

// createRequest stores a request and tells the administrators.
func (s *server) createRequest(ctx context.Context, u *store.User, deviceID *string, pl store.RequestPayload, reason string) (*store.TunnelRequest, error) {
	pl.Subdomain = strings.ToLower(strings.TrimSpace(pl.Subdomain))
	pl.CustomDomain = normalizeDomain(pl.CustomDomain)
	reason = strings.TrimSpace(reason)
	if err := validRequest(&pl, reason); err != nil {
		return nil, err
	}
	if n, err := s.Store.PendingRequestCountOf(ctx, u.ID); err != nil {
		return nil, err
	} else if n >= maxPendingRequests {
		return nil, &rules.Error{Code: "too_many_requests", Message: "待审批的申请过多，请等待管理员处理"}
	}
	q := &store.TunnelRequest{ID: auth.NewID("req_"), UserID: u.ID, DeviceID: deviceID, Payload: pl, Reason: reason, CreatedAt: s.now().UnixMilli()}
	if err := s.Store.CreateTunnelRequest(ctx, q); err != nil {
		return nil, err
	}
	what := pl.Type
	if pl.Subdomain != "" {
		what += " " + pl.Subdomain
	}
	if pl.CustomDomain != "" {
		what += " " + pl.CustomDomain
	}
	s.Notify.Send(notify.EventRequestCreated, "新的隧道申请", fmt.Sprintf("%s 申请 %s → %s:%d。理由：%s", u.Username, what, pl.LocalIP, pl.LocalPort, reason))
	return q, nil
}

func (s *server) handleCreateRequest(w http.ResponseWriter, r *http.Request, p *principal) {
	var body struct {
		store.RequestPayload
		DeviceID *string `json:"deviceId"`
		Reason   string  `json:"reason"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.DeviceID != nil && *body.DeviceID != "" {
		d, err := s.Store.DeviceByID(r.Context(), *body.DeviceID)
		if err != nil || d.UserID != p.user.ID || d.RevokedAt != nil {
			writeError(w, http.StatusBadRequest, "invalid_device", "")
			return
		}
	} else {
		body.DeviceID = nil
	}
	q, err := s.createRequest(r.Context(), p.user, body.DeviceID, body.RequestPayload, body.Reason)
	if err != nil {
		s.fail(w, "create request", err)
		return
	}
	s.audit(r, p, "request.create", q.ID, q.Payload.Type)
	writeJSON(w, http.StatusCreated, map[string]any{"id": q.ID})
}

// DeviceRequest is the hub's callback for request.create on the control stream.
func (s *server) DeviceRequest(ctx context.Context, deviceID string, req tunnelproto.TunnelRequest) error {
	d, err := s.Store.DeviceByID(ctx, deviceID)
	if err != nil {
		return err
	}
	u, err := s.Store.UserByID(ctx, d.UserID)
	if err != nil {
		return err
	}
	hours := 0
	switch req.Duration {
	case "24h":
		hours = 24
	case "7d":
		hours = 24 * 7
	case "30d":
		hours = 24 * 30
	}
	pl := store.RequestPayload{Type: req.Type, Subdomain: req.Subdomain, LocalIP: req.LocalIP, LocalPort: req.LocalPort, DurationHours: hours}
	q, err := s.createRequest(ctx, u, &d.ID, pl, req.Reason)
	if err != nil {
		return err
	}
	_ = s.Store.Audit(context.WithoutCancel(ctx), store.AuditEvent{At: s.now().UnixMilli(), ActorType: "device", ActorID: d.ID, ActorName: d.Name,
		Action: "request.create", Target: q.ID, Detail: req.Type})
	return nil
}

// handleApproveRequest creates the tunnel from the administrator's (possibly edited) form.
func (s *server) handleApproveRequest(w http.ResponseWriter, r *http.Request, p *principal) {
	var body struct {
		Tunnel tunnelInput `json:"tunnel"`
		Note   string      `json:"note"`
	}
	if !decode(w, r, &body) {
		return
	}
	q, err := s.Store.TunnelRequestByID(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, "approve request", err)
		return
	}
	if q.Status != "pending" {
		writeError(w, http.StatusConflict, "not_pending", "该申请已处理")
		return
	}
	body.Tunnel.UserID = q.UserID
	now := s.now().UnixMilli()
	t := &store.Tunnel{ID: auth.NewID("tun_"), CreatedAt: now}
	if !s.applyTunnelInput(w, r, t, &body.Tunnel, nil) {
		return
	}
	t.UpdatedAt = now
	if err := s.Store.SaveTunnel(r.Context(), t); err != nil {
		s.fail(w, "approve request", err)
		return
	}
	if err := s.Store.ResolveTunnelRequest(r.Context(), q.ID, "approved", strings.TrimSpace(body.Note), &p.user.ID, &t.ID, now); err != nil {
		_ = s.Store.DeleteTunnel(r.Context(), t.ID) // lost a race with another reviewer
		s.fail(w, "approve request", err)
		return
	}
	s.changed(r.Context(), deref(t.DeviceID))
	s.audit(r, p, "request.approve", q.ID, t.Name+" "+s.Hub.PublicURL(t))
	writeJSON(w, http.StatusOK, map[string]any{"tunnelId": t.ID})
}

func (s *server) handleRejectRequest(w http.ResponseWriter, r *http.Request, p *principal) {
	var body struct {
		Note string `json:"note"`
	}
	if !decode(w, r, &body) {
		return
	}
	id := r.PathValue("id")
	q, err := s.Store.TunnelRequestByID(r.Context(), id)
	if err != nil {
		s.fail(w, "reject request", err)
		return
	}
	if q.Status != "pending" {
		writeError(w, http.StatusConflict, "not_pending", "该申请已处理")
		return
	}
	if err := s.Store.ResolveTunnelRequest(r.Context(), id, "rejected", strings.TrimSpace(body.Note), &p.user.ID, nil, s.now().UnixMilli()); err != nil {
		s.fail(w, "reject request", err)
		return
	}
	s.audit(r, p, "request.reject", id, body.Note)
	writeOK(w)
}

func (s *server) handleCancelRequest(w http.ResponseWriter, r *http.Request, p *principal) {
	q, err := s.Store.TunnelRequestByID(r.Context(), r.PathValue("id"))
	if err != nil || q.UserID != p.user.ID {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	if q.Status != "pending" {
		writeError(w, http.StatusConflict, "not_pending", "该申请已处理")
		return
	}
	if err := s.Store.ResolveTunnelRequest(r.Context(), q.ID, "cancelled", "", nil, nil, s.now().UnixMilli()); err != nil {
		s.fail(w, "cancel request", err)
		return
	}
	s.audit(r, p, "request.cancel", q.ID, "")
	writeOK(w)
}
