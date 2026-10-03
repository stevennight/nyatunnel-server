package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type auditView struct {
	ID        int64  `json:"id"`
	At        int64  `json:"at"`
	ActorType string `json:"actorType"`
	ActorID   string `json:"actorId"`
	ActorName string `json:"actorName"`
	Action    string `json:"action"`
	Target    string `json:"target"`
	Detail    string `json:"detail"`
	IP        string `json:"ip"`
}

func (s *server) handleAudit(w http.ResponseWriter, r *http.Request, p *principal) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := s.Store.AuditLogs(r.Context(), before, limit)
	if err != nil {
		s.fail(w, "audit", err)
		return
	}
	out := make([]auditView, 0, len(events))
	for _, e := range events {
		out = append(out, auditView{ID: e.ID, At: e.At, ActorType: e.ActorType, ActorID: e.ActorID, ActorName: e.ActorName,
			Action: e.Action, Target: e.Target, Detail: e.Detail, IP: e.IP})
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}

type settingsView struct {
	ServerName string `json:"serverName"`
	ForceTOTP  bool   `json:"forceTotp"`
}

func (s *server) handleGetSettings(w http.ResponseWriter, r *http.Request, p *principal) {
	writeJSON(w, http.StatusOK, settingsView{ServerName: s.serverName(r.Context()), ForceTOTP: s.forceTOTP(r.Context())})
}

func (s *server) handlePutSettings(w http.ResponseWriter, r *http.Request, p *principal) {
	var body settingsView
	if !decode(w, r, &body) {
		return
	}
	name := strings.TrimSpace(body.ServerName)
	if n := utf8.RuneCountInString(name); n == 0 || n > 64 {
		writeError(w, http.StatusBadRequest, "invalid_name", "服务器名称为 1–64 个字符")
		return
	}
	if body.ForceTOTP && !p.user.TOTPEnabled() {
		writeError(w, http.StatusBadRequest, "enable_own_totp_first", "请先为自己开启两步验证")
		return
	}
	if err := s.Store.SetSetting(r.Context(), settingServerName, name); err != nil {
		s.fail(w, "settings", err)
		return
	}
	if err := s.Store.SetSetting(r.Context(), settingForceTOTP, strconv.FormatBool(body.ForceTOTP)); err != nil {
		s.fail(w, "settings", err)
		return
	}
	s.audit(r, p, "settings.update", "", "serverName="+name+" forceTotp="+strconv.FormatBool(body.ForceTOTP))
	s.handleGetSettings(w, r, p)
}

// handleTraffic returns an hourly series for one tunnel, one user or everything. Normal users only
// see their own traffic.
func (s *server) handleTraffic(w http.ResponseWriter, r *http.Request, p *principal) {
	q := r.URL.Query()
	tunnelID, userID := q.Get("tunnelId"), q.Get("userId")
	hours, _ := strconv.Atoi(q.Get("hours"))
	if hours <= 0 || hours > 24*92 {
		hours = 24
	}
	if !p.admin() {
		if tunnelID != "" {
			t, err := s.Store.TunnelByID(r.Context(), tunnelID)
			if err != nil || t.UserID != p.user.ID {
				writeError(w, http.StatusNotFound, "not_found", "")
				return
			}
		}
		userID = p.user.ID
	}
	since := s.now().Add(-time.Duration(hours) * time.Hour).Truncate(time.Hour).UnixMilli()
	series, err := s.Store.TrafficSeries(r.Context(), tunnelID, userID, since)
	if err != nil {
		s.fail(w, "traffic", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"series": series})
}

// handleDashboard summarises the system for the admin overview.
func (s *server) handleDashboard(w http.ResponseWriter, r *http.Request, p *principal) {
	ctx := r.Context()
	users, err := s.Store.Users(ctx)
	if err != nil {
		s.fail(w, "dashboard", err)
		return
	}
	devices, _ := s.Store.Devices(ctx, "")
	tunnels, _ := s.Store.Tunnels(ctx, "")
	events, _ := s.Store.AuditLogs(ctx, 0, 200)

	active := 0
	for _, d := range devices {
		if d.RevokedAt == nil {
			active++
		}
	}
	byType := map[string]int{}
	running := 0
	names, devNames, month := s.usernames(r), s.deviceNames(r), s.monthTraffic(r)
	for _, t := range tunnels {
		byType[t.Type]++
		if s.viewTunnel(t, names, devNames, month).State == "running" {
			running++
		}
	}
	// Recent refusals are the first sign of someone probing or misusing the system.
	dayAgo := s.now().Add(-24 * time.Hour).UnixMilli()
	denied := []auditView{}
	deniedCount := 0
	for _, e := range events {
		if e.At < dayAgo {
			break
		}
		if strings.HasSuffix(e.Action, "_failed") || strings.HasSuffix(e.Action, "_denied") || strings.HasSuffix(e.Action, "invalid_code") {
			deniedCount++
			if len(denied) < 10 {
				denied = append(denied, auditView{ID: e.ID, At: e.At, ActorType: e.ActorType, ActorID: e.ActorID, ActorName: e.ActorName,
					Action: e.Action, Target: e.Target, Detail: e.Detail, IP: e.IP})
			}
		}
	}
	tcp, udp := s.Edge.ListenerPorts()
	series, _ := s.Store.TrafficSeries(ctx, "", "", s.now().Add(-24*time.Hour).Truncate(time.Hour).UnixMilli())
	var traffic24h int64
	for _, p := range series {
		traffic24h += p.BytesIn + p.BytesOut
	}
	var monthTotal int64
	for _, t := range month {
		monthTotal += t.Bytes()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"traffic24h":     traffic24h,
		"trafficMonth":   monthTotal,
		"trafficSeries":  series,
		"users":          len(users),
		"devices":        active,
		"devicesOnline":  s.Hub.OnlineCount(),
		"tunnels":        len(tunnels),
		"tunnelsRunning": running,
		"tunnelsByType":  byType,
		"openPorts":      len(tcp) + len(udp),
		"denied24h":      deniedCount,
		"recentDenied":   denied,
	})
}
