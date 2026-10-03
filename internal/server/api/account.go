package api

import (
	"net/http"

	"nyatunnel-server/internal/server/auth"
	"nyatunnel-server/internal/server/store"
)

type sessionView struct {
	ID         string `json:"id"`
	Current    bool   `json:"current"`
	CreatedAt  int64  `json:"createdAt"`
	LastUsedAt int64  `json:"lastUsedAt"`
	IP         string `json:"ip"`
	UserAgent  string `json:"userAgent"`
}

func (s *server) handleListSessions(w http.ResponseWriter, r *http.Request, p *principal) {
	sessions, err := s.Store.UserSessions(r.Context(), p.user.ID, s.now().UnixMilli())
	if err != nil {
		s.fail(w, "list sessions", err)
		return
	}
	out := make([]sessionView, 0, len(sessions))
	for _, x := range sessions {
		out = append(out, sessionView{ID: x.IDHash, Current: x.IDHash == p.sessionHash, CreatedAt: x.CreatedAt, LastUsedAt: x.LastUsedAt, IP: x.IP, UserAgent: x.UserAgent})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

func (s *server) handleDeleteSession(w http.ResponseWriter, r *http.Request, p *principal) {
	id := r.PathValue("id")
	if id == p.sessionHash {
		writeError(w, http.StatusBadRequest, "current_session", "请使用“退出登录”结束当前会话")
		return
	}
	if err := s.Store.DeleteUserSession(r.Context(), p.user.ID, id); err != nil {
		s.fail(w, "delete session", err)
		return
	}
	s.audit(r, p, "auth.session_revoked", "", "")
	writeOK(w)
}

// handleRegenerateRecoveryCodes replaces all recovery codes (password required).
func (s *server) handleRegenerateRecoveryCodes(w http.ResponseWriter, r *http.Request, p *principal) {
	var body struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !p.user.TOTPEnabled() {
		writeError(w, http.StatusBadRequest, "totp_disabled", "请先开启两步验证")
		return
	}
	if !s.checkPassword(w, r, p, body.Password) {
		return
	}
	codes, err := auth.NewRecoveryCodes(recoveryCodeCount)
	if err != nil {
		s.fail(w, "recovery codes", err)
		return
	}
	hashes := make([]string, len(codes))
	for i, c := range codes {
		hashes[i] = auth.HashToken(c)
	}
	if err := s.Store.SetRecoveryCodes(r.Context(), p.user.ID, hashes, s.now().UnixMilli()); err != nil {
		s.fail(w, "recovery codes", err)
		return
	}
	s.audit(r, p, "auth.recovery_codes_regenerated", p.user.ID, "")
	writeJSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes})
}

type enrollmentView struct {
	ID             string   `json:"id"`
	UserID         string   `json:"userId"`
	Username       string   `json:"username"`
	DeviceNameHint string   `json:"deviceNameHint"`
	TunnelIDs      []string `json:"tunnelIds"`
	ExpiresAt      int64    `json:"expiresAt"`
	CreatedAt      int64    `json:"createdAt"`
}

// handleListEnrollments shows unused codes (the codes themselves are not stored, only their hashes).
func (s *server) handleListEnrollments(w http.ResponseWriter, r *http.Request, p *principal) {
	scope := p.user.ID
	if p.admin() {
		scope = ""
	}
	list, err := s.Store.PendingEnrollments(r.Context(), scope, s.now().UnixMilli())
	if err != nil {
		s.fail(w, "list enrollments", err)
		return
	}
	names := s.usernames(r)
	out := make([]enrollmentView, 0, len(list))
	for _, e := range list {
		out = append(out, enrollmentView{ID: e.ID, UserID: e.UserID, Username: names[e.UserID], DeviceNameHint: e.DeviceNameHint, TunnelIDs: e.TunnelIDs, ExpiresAt: e.ExpiresAt, CreatedAt: e.CreatedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"enrollments": out})
}

func (s *server) handleCancelEnrollment(w http.ResponseWriter, r *http.Request, p *principal) {
	e, err := s.Store.EnrollmentByID(r.Context(), r.PathValue("id"))
	if err != nil || !p.owns(e.UserID) {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err := s.Store.CancelEnrollment(r.Context(), e.ID, s.now().UnixMilli()); err != nil {
		s.fail(w, "cancel enrollment", err)
		return
	}
	s.audit(r, p, "enroll.cancel", e.ID, "")
	writeOK(w)
}

// handleSetUserQuota sets a user's self-service quota.
func (s *server) handleSetUserQuota(w http.ResponseWriter, r *http.Request, p *principal) {
	var q store.Quota
	if !decode(w, r, &q) {
		return
	}
	types := []string{}
	for _, t := range q.Types {
		if t == store.TypeHTTPS || t == store.TypeTCP || t == store.TypeUDP {
			types = append(types, t)
		}
	}
	q.Types = types
	if q.MaxTunnels < 0 || q.MaxBandwidthKbps < 0 || q.MaxDays < 0 || q.MonthlyTrafficMB < 0 {
		writeError(w, http.StatusBadRequest, "invalid_limit", "额度不能为负数")
		return
	}
	id := r.PathValue("id")
	if err := s.Store.SetUserQuota(r.Context(), id, q, s.now().UnixMilli()); err != nil {
		s.fail(w, "set quota", err)
		return
	}
	s.audit(r, p, "user.quota", id, "")
	s.changed(r.Context()) // the account traffic quota is enforced at the edge
	writeOK(w)
}
