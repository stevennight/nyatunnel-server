package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/stevennight/nyatunnel-common/tunnelproto"

	"nyatunnel-server/internal/server/auth"
	"nyatunnel-server/internal/server/edge"
	"nyatunnel-server/internal/server/rules"
	"nyatunnel-server/internal/server/store"
)

type adminUserView struct {
	userView
	DeviceCount int         `json:"deviceCount"`
	TunnelCount int         `json:"tunnelCount"`
	Quota       store.Quota `json:"quota"`
	MonthBytes  int64       `json:"monthBytes"`
}

func (s *server) handleListUsers(w http.ResponseWriter, r *http.Request, p *principal) {
	users, err := s.Store.Users(r.Context())
	if err != nil {
		s.fail(w, "list users", err)
		return
	}
	devices, err := s.Store.Devices(r.Context(), "")
	if err != nil {
		s.fail(w, "list users: devices", err)
		return
	}
	tunnels, err := s.Store.Tunnels(r.Context(), "")
	if err != nil {
		s.fail(w, "list users: tunnels", err)
		return
	}
	dc, tc := map[string]int{}, map[string]int{}
	for _, d := range devices {
		if d.RevokedAt == nil {
			dc[d.UserID]++
		}
	}
	for _, t := range tunnels {
		tc[t.UserID]++
	}
	month, _ := s.Store.TrafficByUser(r.Context(), edge.MonthStart(s.now()))
	out := make([]adminUserView, 0, len(users))
	for _, u := range users {
		q := u.Quota
		if q.Types == nil {
			q.Types = []string{}
		}
		out = append(out, adminUserView{userView: viewUser(u), DeviceCount: dc[u.ID], TunnelCount: tc[u.ID], Quota: q, MonthBytes: month[u.ID].Bytes()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (s *server) handleCreateUser(w http.ResponseWriter, r *http.Request, p *principal) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if !decode(w, r, &body) {
		return
	}
	username := strings.ToLower(strings.TrimSpace(body.Username))
	if err := rules.Username(username); err != nil {
		s.fail(w, "create user", err)
		return
	}
	if body.Role != store.RoleAdmin {
		body.Role = store.RoleUser
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, "weak_password", err.Error())
		return
	}
	now := s.now().UnixMilli()
	u := &store.User{ID: auth.NewID("usr_"), Username: username, PasswordHash: hash, Role: body.Role, CreatedAt: now, UpdatedAt: now}
	if err := s.Store.CreateUser(r.Context(), u); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "username_taken", "用户名已存在")
			return
		}
		s.fail(w, "create user", err)
		return
	}
	s.audit(r, p, "user.create", u.ID, u.Username+" ("+u.Role+")")
	writeJSON(w, http.StatusCreated, map[string]any{"user": viewUser(u)})
}

// handleUpdateUser changes the role, resets the password, or disables / re-enables an account.
// Disabling revokes nothing permanently: devices are disconnected and tunnels switched off.
func (s *server) handleUpdateUser(w http.ResponseWriter, r *http.Request, p *principal) {
	var body struct {
		Role     *string `json:"role"`
		Password *string `json:"password"`
		Disabled *bool   `json:"disabled"`
	}
	if !decode(w, r, &body) {
		return
	}
	id := r.PathValue("id")
	u, err := s.Store.UserByID(r.Context(), id)
	if err != nil {
		s.fail(w, "update user", err)
		return
	}
	now := s.now().UnixMilli()
	self := u.ID == p.user.ID
	if body.Role != nil {
		role := *body.Role
		if role != store.RoleAdmin && role != store.RoleUser {
			writeError(w, http.StatusBadRequest, "invalid_role", "")
			return
		}
		if self && role != store.RoleAdmin {
			writeError(w, http.StatusBadRequest, "cannot_demote_self", "不能取消自己的管理员身份")
			return
		}
		if err := s.Store.SetUserRole(r.Context(), id, role, now); err != nil {
			s.fail(w, "update user: role", err)
			return
		}
		s.audit(r, p, "user.role", id, u.Username+" -> "+role)
	}
	if body.Password != nil {
		hash, err := auth.HashPassword(*body.Password)
		if err != nil {
			writeError(w, http.StatusBadRequest, "weak_password", err.Error())
			return
		}
		if err := s.Store.SetUserPassword(r.Context(), id, hash, now); err != nil {
			s.fail(w, "update user: password", err)
			return
		}
		_ = s.Store.DeleteUserSessions(r.Context(), id, "")
		s.audit(r, p, "user.password_reset", id, u.Username)
	}
	if body.Disabled != nil {
		if self {
			writeError(w, http.StatusBadRequest, "cannot_disable_self", "不能禁用自己")
			return
		}
		var at *int64
		if *body.Disabled {
			at = &now
		}
		if err := s.Store.SetUserDisabled(r.Context(), id, at, now); err != nil {
			s.fail(w, "update user: disable", err)
			return
		}
		if *body.Disabled {
			if err := s.Store.DisableUserTunnels(r.Context(), id, now); err != nil {
				s.fail(w, "update user: tunnels", err)
				return
			}
			ids, _ := s.Store.ActiveDeviceIDs(r.Context(), id)
			for _, d := range ids {
				s.Hub.Kick(d, tunnelproto.CloseUnauthorized, "owner disabled")
			}
			s.changed(r.Context(), ids...)
			s.audit(r, p, "user.disable", id, u.Username)
		} else {
			s.audit(r, p, "user.enable", id, u.Username)
		}
	}
	u, _ = s.Store.UserByID(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]any{"user": viewUser(u)})
}

// handleResetUserTOTP removes a user's second factor (lost phone). They must set it up again when
// TOTP is mandatory.
func (s *server) handleResetUserTOTP(w http.ResponseWriter, r *http.Request, p *principal) {
	id := r.PathValue("id")
	u, err := s.Store.UserByID(r.Context(), id)
	if err != nil {
		s.fail(w, "reset totp", err)
		return
	}
	if err := s.Store.SetUserTOTP(r.Context(), id, nil, nil, s.now().UnixMilli()); err != nil {
		s.fail(w, "reset totp", err)
		return
	}
	_ = s.Store.DeleteUserSessions(r.Context(), id, "")
	s.audit(r, p, "user.totp_reset", id, u.Username)
	writeOK(w)
}
