package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/stevennight/nyatunnel-common/tunnelproto"

	"nyatunnel-server/internal/server/auth"
	"nyatunnel-server/internal/server/edge"
	"nyatunnel-server/internal/server/rules"
	"nyatunnel-server/internal/server/store"
)

type tunnelView struct {
	ID                 string  `json:"id"`
	UserID             string  `json:"userId"`
	Username           string  `json:"username"`
	DeviceID           *string `json:"deviceId"`
	DeviceName         string  `json:"deviceName"`
	Name               string  `json:"name"`
	Type               string  `json:"type"`
	DomainID           *string `json:"domainId"`
	Subdomain          *string `json:"subdomain"`
	Host               *string `json:"host"`
	RemotePort         *int    `json:"remotePort"`
	PublicURL          string  `json:"publicUrl"`
	LocalIP            string  `json:"localIp"`
	LocalPort          int     `json:"localPort"`
	ClientCanEditLocal bool    `json:"clientCanEditLocal"`
	LocalLoopbackOnly  bool    `json:"localLoopbackOnly"`
	ClientCanToggle    bool    `json:"clientCanToggle"`
	Enabled            bool    `json:"enabled"`
	PausedByClient     bool    `json:"pausedByClient"`
	Note               string  `json:"note"`
	ExpiresAt          *int64  `json:"expiresAt"`
	CreatedAt          int64   `json:"createdAt"`
	UpdatedAt          int64   `json:"updatedAt"`
	AccessPolicy       string  `json:"accessPolicy"`
	BasicUsername      string  `json:"basicUsername"`
	HasPassword        bool    `json:"hasPassword"`
	IPAllowlist        string  `json:"ipAllowlist"`
	Interstitial       bool    `json:"interstitial"`
	HostRewrite        string  `json:"hostRewrite"`
	BandwidthKbps      int     `json:"bandwidthKbps"`
	MaxConns           int     `json:"maxConns"`
	MonthlyQuotaMB     int     `json:"monthlyQuotaMb"`
	QuotaAction        string  `json:"quotaAction"`
	MonthBytes         int64   `json:"monthBytes"`
	ActiveConns        int64   `json:"activeConns"`
	// State is running, offline, paused, disabled, expired, over_quota, unassigned or error.
	State      string `json:"state"`
	StateError string `json:"stateError,omitempty"`
}

func (s *server) viewTunnel(t *store.Tunnel, users, devices map[string]string, month map[string]store.TrafficTotal) tunnelView {
	v := tunnelView{
		ID: t.ID, UserID: t.UserID, Username: users[t.UserID], DeviceID: t.DeviceID, Name: t.Name, Type: t.Type, DomainID: t.DomainID,
		Subdomain: t.Subdomain, Host: t.Host, RemotePort: t.RemotePort, PublicURL: s.Hub.PublicURL(t), LocalIP: t.LocalIP, LocalPort: t.LocalPort,
		ClientCanEditLocal: t.ClientCanEditLocal, LocalLoopbackOnly: t.LocalLoopbackOnly, ClientCanToggle: t.ClientCanToggle,
		Enabled: t.Enabled, PausedByClient: t.PausedByClient, Note: t.Note, ExpiresAt: t.ExpiresAt, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
		AccessPolicy: t.AccessPolicy, BasicUsername: t.BasicUsername, HasPassword: t.AccessPasswordHash != "", IPAllowlist: t.IPAllowlist,
		Interstitial: t.Interstitial, HostRewrite: t.HostRewrite, BandwidthKbps: t.BandwidthKbps, MaxConns: t.MaxConns,
		MonthlyQuotaMB: t.MonthlyQuotaMB, QuotaAction: t.QuotaAction,
		MonthBytes: month[t.ID].Bytes() + s.Edge.PendingTraffic(t.ID), ActiveConns: s.Edge.Active(t.ID),
	}
	now := s.now().UnixMilli()
	switch {
	case !t.Enabled:
		v.State = "disabled"
	case t.ExpiresAt != nil && now >= *t.ExpiresAt:
		v.State = "expired"
	case s.Edge.OverQuota(t.ID):
		v.State = "over_quota"
	case t.DeviceID == nil:
		v.State = "unassigned"
	case t.PausedByClient:
		v.State = "paused"
	default:
		v.DeviceName = devices[*t.DeviceID]
		info, online := s.Hub.Online(*t.DeviceID)
		v.State = "offline"
		if online {
			v.State = "running"
			if st, ok := info.Status[t.ID]; ok && st.State == tunnelproto.StateError {
				v.State, v.StateError = "error", st.Error
			}
		}
	}
	if t.DeviceID != nil && v.DeviceName == "" {
		v.DeviceName = devices[*t.DeviceID]
	}
	return v
}

func (s *server) monthTraffic(r *http.Request) map[string]store.TrafficTotal {
	m, err := s.Store.TrafficByTunnel(r.Context(), edge.MonthStart(s.now()))
	if err != nil {
		s.Log.Warn("month traffic", "err", err)
	}
	return m
}

func (s *server) deviceNames(r *http.Request) map[string]string {
	ds, _ := s.Store.Devices(r.Context(), "")
	out := map[string]string{}
	for _, d := range ds {
		out[d.ID] = d.Name
	}
	return out
}

func (s *server) handleListTunnels(w http.ResponseWriter, r *http.Request, p *principal) {
	scope := p.user.ID
	if p.admin() {
		scope = r.URL.Query().Get("userId")
	}
	tunnels, err := s.Store.Tunnels(r.Context(), scope)
	if err != nil {
		s.fail(w, "list tunnels", err)
		return
	}
	users, devices, month := s.usernames(r), s.deviceNames(r), s.monthTraffic(r)
	out := make([]tunnelView, 0, len(tunnels))
	for _, t := range tunnels {
		out = append(out, s.viewTunnel(t, users, devices, month))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tunnels": out})
}

// tunnelInput is the full editable state of a tunnel (POST and PUT).
type tunnelInput struct {
	UserID             string  `json:"userId"`
	DeviceID           *string `json:"deviceId"`
	Name               string  `json:"name"`
	Type               string  `json:"type"`
	DomainID           *string `json:"domainId"`
	Subdomain          *string `json:"subdomain"`
	RemotePort         *int    `json:"remotePort"` // tcp/udp: nil or 0 picks a free port from the pool
	LocalIP            string  `json:"localIp"`
	LocalPort          int     `json:"localPort"`
	ClientCanEditLocal bool    `json:"clientCanEditLocal"`
	LocalLoopbackOnly  bool    `json:"localLoopbackOnly"`
	ClientCanToggle    bool    `json:"clientCanToggle"`
	Enabled            bool    `json:"enabled"`
	Note               string  `json:"note"`
	ExpiresAt          *int64  `json:"expiresAt"`

	AccessPolicy   string  `json:"accessPolicy"`
	AccessPassword *string `json:"accessPassword"` // nil or "" keeps the current password
	BasicUsername  string  `json:"basicUsername"`
	IPAllowlist    string  `json:"ipAllowlist"`
	Interstitial   bool    `json:"interstitial"`
	HostRewrite    string  `json:"hostRewrite"`
	BandwidthKbps  int     `json:"bandwidthKbps"`
	MaxConns       int     `json:"maxConns"`
	MonthlyQuotaMB int     `json:"monthlyQuotaMb"`
	QuotaAction    string  `json:"quotaAction"`
}

func (s *server) handleCreateTunnel(w http.ResponseWriter, r *http.Request, p *principal) {
	var in tunnelInput
	if !decode(w, r, &in) {
		return
	}
	now := s.now().UnixMilli()
	t := &store.Tunnel{ID: auth.NewID("tun_"), CreatedAt: now}
	if !s.applyTunnelInput(w, r, t, &in, nil) {
		return
	}
	t.UpdatedAt = now
	if err := s.Store.SaveTunnel(r.Context(), t); err != nil {
		s.fail(w, "create tunnel", err)
		return
	}
	s.changed(r.Context(), deref(t.DeviceID))
	s.audit(r, p, "tunnel.create", t.ID, t.Name+" "+s.Hub.PublicURL(t))
	writeJSON(w, http.StatusCreated, map[string]any{"tunnel": s.viewTunnel(t, s.usernames(r), s.deviceNames(r), s.monthTraffic(r))})
}

func (s *server) handleUpdateTunnel(w http.ResponseWriter, r *http.Request, p *principal) {
	var in tunnelInput
	if !decode(w, r, &in) {
		return
	}
	old, err := s.Store.TunnelByID(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, "update tunnel", err)
		return
	}
	t := *old
	if !s.applyTunnelInput(w, r, &t, &in, old) {
		return
	}
	t.UpdatedAt = s.now().UnixMilli()
	if err := s.Store.SaveTunnel(r.Context(), &t); err != nil {
		s.fail(w, "update tunnel", err)
		return
	}
	s.changed(r.Context(), deref(old.DeviceID), deref(t.DeviceID))
	s.audit(r, p, "tunnel.update", t.ID, t.Name+" "+s.Hub.PublicURL(&t))
	writeJSON(w, http.StatusOK, map[string]any{"tunnel": s.viewTunnel(&t, s.usernames(r), s.deviceNames(r), s.monthTraffic(r))})
}

func (s *server) handleDeleteTunnel(w http.ResponseWriter, r *http.Request, p *principal) {
	t, err := s.Store.TunnelByID(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, "delete tunnel", err)
		return
	}
	if err := s.Store.DeleteTunnel(r.Context(), t.ID); err != nil {
		s.fail(w, "delete tunnel", err)
		return
	}
	s.changed(r.Context(), deref(t.DeviceID))
	s.audit(r, p, "tunnel.delete", t.ID, t.Name)
	writeOK(w)
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// applyTunnelInput validates in and copies it onto t. old is nil for a new tunnel.
func (s *server) applyTunnelInput(w http.ResponseWriter, r *http.Request, t *store.Tunnel, in *tunnelInput, old *store.Tunnel) bool {
	ctx := r.Context()
	bad := func(code, msg string) bool {
		writeError(w, http.StatusBadRequest, code, msg)
		return false
	}
	in.Name = strings.TrimSpace(in.Name)
	if err := rules.TunnelName(in.Name); err != nil {
		s.fail(w, "tunnel", err)
		return false
	}
	owner, err := s.Store.UserByID(ctx, in.UserID)
	if err != nil {
		return bad("invalid_user", "请选择隧道所属用户")
	}
	if in.DeviceID != nil && *in.DeviceID == "" {
		in.DeviceID = nil
	}
	if in.DeviceID != nil {
		d, err := s.Store.DeviceByID(ctx, *in.DeviceID)
		if err != nil || d.UserID != owner.ID || d.RevokedAt != nil {
			return bad("invalid_device", "设备必须属于该用户且未被吊销")
		}
	}
	if err := rules.LocalTarget(strings.TrimSpace(in.LocalIP), in.LocalPort, in.LocalLoopbackOnly); err != nil {
		s.fail(w, "tunnel", err)
		return false
	}
	if in.ExpiresAt != nil && *in.ExpiresAt <= 0 {
		in.ExpiresAt = nil
	}
	if old != nil && old.Type != in.Type {
		return bad("type_immutable", "隧道类型创建后不能修改")
	}

	t.UserID, t.DeviceID, t.Name, t.Type = owner.ID, in.DeviceID, in.Name, in.Type
	t.LocalIP, t.LocalPort = strings.TrimSpace(in.LocalIP), in.LocalPort
	t.ClientCanEditLocal, t.LocalLoopbackOnly, t.ClientCanToggle = in.ClientCanEditLocal, in.LocalLoopbackOnly, in.ClientCanToggle
	t.Enabled, t.Note, t.ExpiresAt = in.Enabled, strings.TrimSpace(in.Note), in.ExpiresAt
	if old == nil || (old.DeviceID != nil && in.DeviceID != nil && *old.DeviceID != *in.DeviceID) {
		t.PausedByClient = false // a new device starts with the tunnel running
	}

	switch in.Type {
	case store.TypeHTTPS:
		if in.DomainID == nil || in.Subdomain == nil {
			return bad("domain_required", "请选择域名并填写子域名")
		}
		d, err := s.Store.DomainByID(ctx, *in.DomainID)
		if err != nil {
			return bad("invalid_domain", "域名不存在")
		}
		sub := strings.ToLower(strings.TrimSpace(*in.Subdomain))
		prefix := ""
		if !owner.IsAdmin() {
			if !d.AllowUsers {
				return bad("domain_admin_only", "该域名仅限管理员使用")
			}
			prefix = owner.Username + "-"
		}
		unchanged := old != nil && old.Subdomain != nil && *old.Subdomain == sub && deref(old.DomainID) == d.ID
		if !unchanged {
			if err := rules.Subdomain(sub, prefix); err != nil {
				s.fail(w, "tunnel", err)
				return false
			}
		}
		host := sub + "." + d.Name
		t.DomainID, t.Subdomain, t.Host, t.RemotePort = &d.ID, &sub, &host, nil
	case store.TypeTCP, store.TypeUDP:
		port, err := s.pickPort(ctx, in.Type, in.RemotePort, t.ID)
		if err != nil {
			s.fail(w, "tunnel", err)
			return false
		}
		t.DomainID, t.Subdomain, t.Host, t.RemotePort = nil, nil, nil, &port
	default:
		return bad("invalid_type", "类型必须是 https、tcp 或 udp")
	}
	return s.applyPolicy(w, t, in, old)
}

var hostRewriteRE = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}(:[0-9]{1,5})?$`)

// applyPolicy validates the access policy and limits.
func (s *server) applyPolicy(w http.ResponseWriter, t *store.Tunnel, in *tunnelInput, old *store.Tunnel) bool {
	bad := func(code, msg string) bool {
		writeError(w, http.StatusBadRequest, code, msg)
		return false
	}
	policy := in.AccessPolicy
	if policy == "" {
		policy = store.PolicyPublic
	}
	switch policy {
	case store.PolicyPublic, store.PolicyPassword, store.PolicyBasic, store.PolicyLogin:
	default:
		return bad("invalid_policy", "访问策略无效")
	}
	if t.Type != store.TypeHTTPS && policy != store.PolicyPublic {
		return bad("policy_https_only", "访问密码 / Basic / 登录门禁只适用于 HTTPS 隧道；TCP / UDP 请使用 IP 白名单")
	}
	if t.Type != store.TypeHTTPS && (in.Interstitial || in.HostRewrite != "") {
		return bad("policy_https_only", "首次访问提示页与 Host 改写只适用于 HTTPS 隧道")
	}
	if _, err := edge.ParseAllowlist(in.IPAllowlist); err != nil {
		return bad("invalid_allowlist", "IP 白名单格式不正确，应为 IP 或 CIDR，用逗号分隔")
	}
	if in.HostRewrite != "" && !hostRewriteRE.MatchString(in.HostRewrite) {
		return bad("invalid_host_rewrite", "Host 改写应为主机名，可带端口")
	}
	if in.BandwidthKbps < 0 || in.MaxConns < 0 || in.MonthlyQuotaMB < 0 {
		return bad("invalid_limit", "限制不能为负数")
	}
	if in.QuotaAction == "" {
		in.QuotaAction = "pause"
	}
	if in.QuotaAction != "pause" && in.QuotaAction != "alert" {
		return bad("invalid_quota_action", "超额动作必须是 pause 或 alert")
	}

	rev := t.PolicyRev
	if old != nil && (old.AccessPolicy != policy || old.BasicUsername != in.BasicUsername) {
		rev++
	}
	hash := t.AccessPasswordHash
	if policy == store.PolicyPassword || policy == store.PolicyBasic {
		if in.AccessPassword != nil && *in.AccessPassword != "" {
			if len([]rune(*in.AccessPassword)) < 6 {
				return bad("weak_access_password", "访问密码至少 6 个字符")
			}
			h, err := auth.HashSecret(*in.AccessPassword)
			if err != nil {
				s.fail(w, "access password", err)
				return false
			}
			hash = h
			rev++
		}
		if hash == "" {
			return bad("access_password_required", "请设置访问密码")
		}
	} else {
		hash = ""
	}
	if policy == store.PolicyBasic && strings.TrimSpace(in.BasicUsername) == "" {
		return bad("basic_username_required", "请设置 Basic 认证用户名")
	}
	t.AccessPolicy, t.AccessPasswordHash, t.BasicUsername, t.PolicyRev = policy, hash, strings.TrimSpace(in.BasicUsername), rev
	t.IPAllowlist, t.Interstitial, t.HostRewrite = strings.TrimSpace(in.IPAllowlist), in.Interstitial, strings.TrimSpace(in.HostRewrite)
	t.BandwidthKbps, t.MaxConns, t.MonthlyQuotaMB, t.QuotaAction = in.BandwidthKbps, in.MaxConns, in.MonthlyQuotaMB, in.QuotaAction
	return true
}

// pickPort validates a requested port against the pools, or picks the lowest free one.
func (s *server) pickPort(ctx context.Context, proto string, requested *int, selfID string) (int, error) {
	pools, err := s.Store.PortPools(ctx)
	if err != nil {
		return 0, err
	}
	used, err := s.Store.UsedPorts(ctx, proto)
	if err != nil {
		return 0, err
	}
	inPool := func(port int) bool {
		for _, p := range pools {
			if p.Proto == proto && port >= p.RangeStart && port <= p.RangeEnd {
				return true
			}
		}
		return false
	}
	if requested != nil && *requested != 0 {
		port := *requested
		if !inPool(port) {
			return 0, &rules.Error{Code: "port_not_in_pool", Message: "端口不在任何 " + strings.ToUpper(proto) + " 端口池内"}
		}
		if id, taken := used[port]; taken && id != selfID {
			return 0, &rules.Error{Code: "port_taken", Message: "端口已被其他隧道占用"}
		}
		return port, nil
	}
	for _, p := range pools {
		if p.Proto != proto {
			continue
		}
		for port := p.RangeStart; port <= p.RangeEnd; port++ {
			if id, taken := used[port]; !taken || id == selfID {
				return port, nil
			}
		}
	}
	return 0, &rules.Error{Code: "pool_exhausted", Message: "没有可用的 " + strings.ToUpper(proto) + " 端口，请先在端口池中添加范围"}
}

type domainView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	AllowUsers  bool   `json:"allowUsers"`
	TunnelCount int    `json:"tunnelCount"`
	CreatedAt   int64  `json:"createdAt"`
}

func (s *server) handleListDomains(w http.ResponseWriter, r *http.Request, p *principal) {
	domains, err := s.Store.Domains(r.Context())
	if err != nil {
		s.fail(w, "list domains", err)
		return
	}
	count := map[string]int{}
	if p.admin() {
		tunnels, _ := s.Store.Tunnels(r.Context(), "")
		for _, t := range tunnels {
			count[deref(t.DomainID)]++
		}
	}
	out := []domainView{}
	for _, d := range domains {
		if !p.admin() && !d.AllowUsers {
			continue
		}
		out = append(out, domainView{ID: d.ID, Name: d.Name, AllowUsers: d.AllowUsers, TunnelCount: count[d.ID], CreatedAt: d.CreatedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"domains": out})
}

func (s *server) handleCreateDomain(w http.ResponseWriter, r *http.Request, p *principal) {
	var body struct {
		Name       string `json:"name"`
		AllowUsers bool   `json:"allowUsers"`
	}
	if !decode(w, r, &body) {
		return
	}
	name := strings.Trim(strings.ToLower(strings.TrimSpace(body.Name)), ".")
	name = strings.TrimPrefix(name, "*.")
	if err := rules.DomainName(name); err != nil {
		s.fail(w, "create domain", err)
		return
	}
	d := &store.Domain{ID: auth.NewID("dom_"), Name: name, AllowUsers: body.AllowUsers, CreatedAt: s.now().UnixMilli()}
	if err := s.Store.CreateDomain(r.Context(), d); err != nil {
		s.fail(w, "create domain", err)
		return
	}
	s.audit(r, p, "domain.create", d.ID, d.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"domain": domainView{ID: d.ID, Name: d.Name, AllowUsers: d.AllowUsers, CreatedAt: d.CreatedAt}})
}

func (s *server) handleUpdateDomain(w http.ResponseWriter, r *http.Request, p *principal) {
	var body struct {
		AllowUsers bool `json:"allowUsers"`
	}
	if !decode(w, r, &body) {
		return
	}
	id := r.PathValue("id")
	if err := s.Store.SetDomainAllowUsers(r.Context(), id, body.AllowUsers); err != nil {
		s.fail(w, "update domain", err)
		return
	}
	s.audit(r, p, "domain.update", id, "")
	writeOK(w)
}

func (s *server) handleDeleteDomain(w http.ResponseWriter, r *http.Request, p *principal) {
	id := r.PathValue("id")
	if err := s.Store.DeleteDomain(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "domain_in_use", "仍有隧道使用该域名")
			return
		}
		s.fail(w, "delete domain", err)
		return
	}
	s.audit(r, p, "domain.delete", id, "")
	writeOK(w)
}

type portPoolView struct {
	ID    string `json:"id"`
	Proto string `json:"proto"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Used  int    `json:"used"`
}

func (s *server) handleListPortPools(w http.ResponseWriter, r *http.Request, p *principal) {
	pools, err := s.Store.PortPools(r.Context())
	if err != nil {
		s.fail(w, "list port pools", err)
		return
	}
	used := map[string]map[int]string{}
	for _, proto := range []string{store.TypeTCP, store.TypeUDP} {
		used[proto], _ = s.Store.UsedPorts(r.Context(), proto)
	}
	out := []portPoolView{}
	for _, pp := range pools {
		v := portPoolView{ID: pp.ID, Proto: pp.Proto, Start: pp.RangeStart, End: pp.RangeEnd}
		for port := range used[pp.Proto] {
			if port >= pp.RangeStart && port <= pp.RangeEnd {
				v.Used++
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"portPools": out})
}

func (s *server) handleCreatePortPool(w http.ResponseWriter, r *http.Request, p *principal) {
	var body struct {
		Proto string `json:"proto"`
		Start int    `json:"start"`
		End   int    `json:"end"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Proto != store.TypeTCP && body.Proto != store.TypeUDP {
		writeError(w, http.StatusBadRequest, "invalid_proto", "协议必须是 tcp 或 udp")
		return
	}
	if body.Start < 1024 || body.End > 65535 || body.End < body.Start || body.End-body.Start > 10000 {
		writeError(w, http.StatusBadRequest, "invalid_range", "端口范围须在 1024–65535 内，且一次最多 10000 个")
		return
	}
	pp := &store.PortPool{ID: auth.NewID("pp_"), Proto: body.Proto, RangeStart: body.Start, RangeEnd: body.End, CreatedAt: s.now().UnixMilli()}
	if err := s.Store.CreatePortPool(r.Context(), pp); err != nil {
		s.fail(w, "create port pool", err)
		return
	}
	s.audit(r, p, "port_pool.create", pp.ID, body.Proto)
	writeJSON(w, http.StatusCreated, map[string]any{"portPool": portPoolView{ID: pp.ID, Proto: pp.Proto, Start: pp.RangeStart, End: pp.RangeEnd}})
}

func (s *server) handleDeletePortPool(w http.ResponseWriter, r *http.Request, p *principal) {
	id := r.PathValue("id")
	if err := s.Store.DeletePortPool(r.Context(), id); err != nil {
		s.fail(w, "delete port pool", err)
		return
	}
	s.audit(r, p, "port_pool.delete", id, "")
	writeOK(w)
}
