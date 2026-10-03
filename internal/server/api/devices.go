package api

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/stevennight/nyatunnel-common/deeplink"
	"github.com/stevennight/nyatunnel-common/tunnelproto"

	"nyatunnel-server/internal/server/auth"
	"nyatunnel-server/internal/server/notify"
	"nyatunnel-server/internal/server/store"
)

type deviceView struct {
	ID            string `json:"id"`
	UserID        string `json:"userId"`
	Username      string `json:"username"`
	Name          string `json:"name"`
	Platform      string `json:"platform"`
	ClientVersion string `json:"clientVersion"`
	GUI           bool   `json:"gui"`
	LastIP        string `json:"lastIp"`
	LastSeenAt    *int64 `json:"lastSeenAt"`
	Revoked       bool   `json:"revoked"`
	RevokedAt     *int64 `json:"revokedAt"`
	CreatedAt     int64  `json:"createdAt"`
	Online        bool   `json:"online"`
	ConnectedAt   *int64 `json:"connectedAt"`
	TunnelCount   int    `json:"tunnelCount"`
}

func (s *server) usernames(r *http.Request) map[string]string {
	users, _ := s.Store.Users(r.Context())
	out := map[string]string{}
	for _, u := range users {
		out[u.ID] = u.Username
	}
	return out
}

func (s *server) handleListDevices(w http.ResponseWriter, r *http.Request, p *principal) {
	scope := p.user.ID
	if p.admin() {
		scope = r.URL.Query().Get("userId")
	}
	devices, err := s.Store.Devices(r.Context(), scope)
	if err != nil {
		s.fail(w, "list devices", err)
		return
	}
	tunnels, err := s.Store.Tunnels(r.Context(), scope)
	if err != nil {
		s.fail(w, "list devices: tunnels", err)
		return
	}
	count := map[string]int{}
	for _, t := range tunnels {
		if t.DeviceID != nil {
			count[*t.DeviceID]++
		}
	}
	names := s.usernames(r)
	out := make([]deviceView, 0, len(devices))
	for _, d := range devices {
		v := deviceView{
			ID: d.ID, UserID: d.UserID, Username: names[d.UserID], Name: d.Name, Platform: d.Platform, ClientVersion: d.ClientVersion,
			GUI: d.GUI, LastIP: d.LastIP, LastSeenAt: d.LastSeenAt, Revoked: d.RevokedAt != nil, RevokedAt: d.RevokedAt,
			CreatedAt: d.CreatedAt, TunnelCount: count[d.ID],
		}
		if info, ok := s.Hub.Online(d.ID); ok {
			at := info.ConnectedAt.UnixMilli()
			v.Online, v.ConnectedAt, v.LastIP, v.ClientVersion = true, &at, info.RemoteIP, info.ClientVersion
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

func (s *server) ownedDevice(w http.ResponseWriter, r *http.Request, p *principal) *store.Device {
	d, err := s.Store.DeviceByID(r.Context(), r.PathValue("id"))
	if err != nil || !p.owns(d.UserID) {
		writeError(w, http.StatusNotFound, "not_found", "")
		return nil
	}
	return d
}

func (s *server) handleRenameDevice(w http.ResponseWriter, r *http.Request, p *principal) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	d := s.ownedDevice(w, r, p)
	if d == nil {
		return
	}
	name, ok := deviceName(body.Name)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_name", "设备名为 1–64 个字符")
		return
	}
	if err := s.Store.RenameDevice(r.Context(), d.ID, name); err != nil {
		s.fail(w, "rename device", err)
		return
	}
	s.audit(r, p, "device.rename", d.ID, d.Name+" -> "+name)
	writeOK(w)
}

func (s *server) handleRevokeDevice(w http.ResponseWriter, r *http.Request, p *principal) {
	d := s.ownedDevice(w, r, p)
	if d == nil {
		return
	}
	if err := s.Store.RevokeDevice(r.Context(), d.ID, s.now().UnixMilli()); err != nil {
		s.fail(w, "revoke device", err)
		return
	}
	s.Hub.Kick(d.ID, tunnelproto.CloseUnauthorized, "device revoked")
	s.changed(r.Context())
	s.audit(r, p, "device.revoke", d.ID, d.Name)
	writeOK(w)
}

func deviceName(s string) (string, bool) {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	return s, n >= 1 && n <= 64 && !strings.ContainsAny(s, "\r\n\t")
}

// handleCreateEnrollment makes a one-time code. Admins may enroll for any user; users for themselves.
// Preset tunnels must belong to that user and not be assigned to a device yet.
func (s *server) handleCreateEnrollment(w http.ResponseWriter, r *http.Request, p *principal) {
	var body struct {
		UserID         string   `json:"userId"`
		DeviceNameHint string   `json:"deviceNameHint"`
		TunnelIDs      []string `json:"tunnelIds"`
		TTLMinutes     int      `json:"ttlMinutes"`
	}
	if !decode(w, r, &body) {
		return
	}
	userID := p.user.ID
	if body.UserID != "" && body.UserID != userID {
		if !p.admin() {
			writeError(w, http.StatusForbidden, "forbidden", "")
			return
		}
		u, err := s.Store.UserByID(r.Context(), body.UserID)
		if err != nil || u.DisabledAt != nil {
			writeError(w, http.StatusBadRequest, "invalid_user", "")
			return
		}
		userID = u.ID
	}
	ttl := time.Duration(body.TTLMinutes) * time.Minute
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	if ttl > 24*time.Hour {
		ttl = 24 * time.Hour
	}
	for _, id := range body.TunnelIDs {
		t, err := s.Store.TunnelByID(r.Context(), id)
		if err != nil || t.UserID != userID {
			writeError(w, http.StatusBadRequest, "invalid_tunnel", "预分配的隧道必须属于该用户")
			return
		}
		if t.DeviceID != nil {
			writeError(w, http.StatusBadRequest, "tunnel_assigned", "隧道 "+t.Name+" 已经绑定了设备")
			return
		}
	}
	hint, _ := deviceName(body.DeviceNameHint)
	code, err := deeplink.NewCode()
	if err != nil {
		s.fail(w, "enroll code", err)
		return
	}
	now := s.now()
	e := &store.Enrollment{
		ID: auth.NewID("enr_"), UserID: userID, CreatedBy: p.user.ID, CodeHash: auth.HashToken(code), DeviceNameHint: hint,
		TunnelIDs: body.TunnelIDs, ExpiresAt: now.Add(ttl).UnixMilli(), CreatedAt: now.UnixMilli(),
	}
	if err := s.Store.CreateEnrollment(r.Context(), e); err != nil {
		s.fail(w, "create enrollment", err)
		return
	}
	s.audit(r, p, "enroll.create", e.ID, "for "+userID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"code":       code,
		"url":        deeplink.EnrollURL(s.Config.PublicHost, code),
		"cliCommand": "nyatunnel enroll " + s.Config.PublicURL + " " + code,
		"expiresAt":  e.ExpiresAt,
	})
}

// enrollment resolves a code from a public request, counting failures per address.
func (s *server) enrollment(w http.ResponseWriter, r *http.Request, raw string) *store.Enrollment {
	ip, now := s.realIP.ClientIP(r), s.now()
	if blocked, wait := s.enrollFails.blocked(ip, now); blocked {
		retryAfter(w, wait)
		return nil
	}
	code, err := deeplink.NormalizeCode(raw)
	var e *store.Enrollment
	if err == nil {
		e, err = s.Store.UsableEnrollment(r.Context(), auth.HashToken(code), now.UnixMilli())
	}
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) && !errors.Is(err, deeplink.ErrInvalid) {
			s.fail(w, "enrollment lookup", err)
			return nil
		}
		s.enrollFails.fail(ip, now)
		s.audit(r, nil, "enroll.invalid_code", "", "")
		writeError(w, http.StatusNotFound, "enrollment_invalid", "注册码无效或已过期")
		return nil
	}
	return e
}

// handleEnrollPreview shows what the device is about to join; it does not consume the code.
func (s *server) handleEnrollPreview(w http.ResponseWriter, r *http.Request) {
	e := s.enrollment(w, r, r.URL.Query().Get("code"))
	if e == nil {
		return
	}
	owner, err := s.Store.UserByID(r.Context(), e.UserID)
	if err != nil {
		s.fail(w, "enroll preview", err)
		return
	}
	type preset struct {
		Name      string `json:"name"`
		PublicURL string `json:"publicUrl"`
	}
	tunnels := []preset{}
	for _, id := range e.TunnelIDs {
		if t, err := s.Store.TunnelByID(r.Context(), id); err == nil && t.DeviceID == nil {
			tunnels = append(tunnels, preset{Name: t.Name, PublicURL: s.Hub.PublicURL(t)})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"serverName": s.serverName(r.Context()), "owner": owner.Username, "deviceNameHint": e.DeviceNameHint,
		"tunnels": tunnels, "expiresAt": e.ExpiresAt,
	})
}

func (s *server) handleEnrollClaim(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code          string `json:"code"`
		PublicKey     string `json:"publicKey"`
		DeviceName    string `json:"deviceName"`
		Platform      string `json:"platform"`
		ClientVersion string `json:"clientVersion"`
		GUI           bool   `json:"gui"`
	}
	if !decode(w, r, &body) {
		return
	}
	key, err := base64.StdEncoding.DecodeString(body.PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		writeError(w, http.StatusBadRequest, "invalid_public_key", "")
		return
	}
	name, ok := deviceName(body.DeviceName)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_name", "设备名为 1–64 个字符")
		return
	}
	e := s.enrollment(w, r, body.Code)
	if e == nil {
		return
	}
	now := s.now().UnixMilli()
	d := &store.Device{
		ID: auth.NewID("dev_"), UserID: e.UserID, Name: name, PublicKey: key, Platform: truncateString(body.Platform, 64),
		ClientVersion: truncateString(body.ClientVersion, 32), GUI: body.GUI, CreatedAt: now,
	}
	if err := s.Store.ClaimEnrollment(r.Context(), e.ID, d, now); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "enrollment_invalid", "注册码无效或已过期")
		case errors.Is(err, store.ErrConflict):
			writeError(w, http.StatusConflict, "key_in_use", "该设备密钥已注册")
		default:
			s.fail(w, "enroll claim", err)
		}
		return
	}
	_ = s.Store.Audit(r.Context(), store.AuditEvent{At: now, ActorType: "device", ActorID: d.ID, ActorName: d.Name, Action: "device.enrolled",
		Target: d.ID, Detail: "enrollment " + e.ID + ", " + d.Platform, IP: s.realIP.ClientIP(r)})
	s.changed(r.Context(), d.ID)
	if owner, err := s.Store.UserByID(r.Context(), d.UserID); err == nil {
		s.Notify.Send(notify.EventDeviceEnrolled, "新设备已注册", owner.Username+" 的设备 "+d.Name+"（"+d.Platform+"）已注册，来自 "+s.realIP.ClientIP(r))
	}
	writeJSON(w, http.StatusCreated, map[string]any{"deviceId": d.ID})
}

func truncateString(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}
