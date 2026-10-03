package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"nyatunnel-server/internal/server/auth"
	"nyatunnel-server/internal/server/notify"
	"nyatunnel-server/internal/server/rules"
	"nyatunnel-server/internal/server/store"
)

type domainView struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Kind        string  `json:"kind"`
	AllowUsers  bool    `json:"allowUsers"`
	OwnerUserID *string `json:"ownerUserId"`
	OwnerName   string  `json:"ownerName"`
	Status      string  `json:"status"`
	CheckedAt   *int64  `json:"checkedAt"`
	CheckError  string  `json:"checkError"`
	TunnelCount int     `json:"tunnelCount"`
	CreatedAt   int64   `json:"createdAt"`
}

func (s *server) viewDomain(d *store.Domain, names map[string]string, count map[string]int) domainView {
	v := domainView{ID: d.ID, Name: d.Name, Kind: d.Kind, AllowUsers: d.AllowUsers, OwnerUserID: d.OwnerUserID, Status: d.Status,
		CheckedAt: d.CheckedAt, CheckError: d.CheckError, TunnelCount: count[d.ID], CreatedAt: d.CreatedAt}
	if d.OwnerUserID != nil {
		v.OwnerName = names[*d.OwnerUserID]
	}
	return v
}

// handleListDomains: admins see everything; users see root domains open to them and their own
// custom domains.
func (s *server) handleListDomains(w http.ResponseWriter, r *http.Request, p *principal) {
	domains, err := s.Store.Domains(r.Context())
	if err != nil {
		s.fail(w, "list domains", err)
		return
	}
	scope := ""
	if !p.admin() {
		scope = p.user.ID
	}
	tunnels, _ := s.Store.Tunnels(r.Context(), scope)
	count := map[string]int{}
	for _, t := range tunnels {
		count[deref(t.DomainID)]++
	}
	names := s.usernames(r)
	out := []domainView{}
	for _, d := range domains {
		if !p.admin() {
			mine := d.Kind == store.DomainCustom && d.OwnerUserID != nil && *d.OwnerUserID == p.user.ID
			if !(d.Kind == store.DomainRoot && d.AllowUsers) && !mine {
				continue
			}
		}
		out = append(out, s.viewDomain(d, names, count))
	}
	writeJSON(w, http.StatusOK, map[string]any{"domains": out, "publicIps": s.expectedIPStrings(r.Context())})
}

func normalizeDomain(s string) string {
	s = strings.Trim(strings.ToLower(strings.TrimSpace(s)), ".")
	return strings.TrimPrefix(s, "*.")
}

// handleCreateDomain: admins add root domains, or custom domains (optionally owned by a user)
// that go live once DNS points here.
func (s *server) handleCreateDomain(w http.ResponseWriter, r *http.Request, p *principal) {
	var body struct {
		Name        string `json:"name"`
		Kind        string `json:"kind"`
		AllowUsers  bool   `json:"allowUsers"`
		OwnerUserID string `json:"ownerUserId"`
	}
	if !decode(w, r, &body) {
		return
	}
	name := normalizeDomain(body.Name)
	if err := rules.DomainName(name); err != nil {
		s.fail(w, "create domain", err)
		return
	}
	now := s.now().UnixMilli()
	d := &store.Domain{ID: auth.NewID("dom_"), Name: name, AllowUsers: body.AllowUsers, CreatedAt: now, Kind: store.DomainRoot, Status: store.DomainActive}
	var err error
	if body.Kind == store.DomainCustom {
		d.Kind, d.Status = store.DomainCustom, store.DomainDNS
		if body.OwnerUserID != "" {
			if _, err := s.Store.UserByID(r.Context(), body.OwnerUserID); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_user", "")
				return
			}
			d.OwnerUserID = &body.OwnerUserID
		}
		err = s.Store.CreateCustomDomain(r.Context(), d)
	} else {
		err = s.Store.CreateDomain(r.Context(), d)
	}
	if err != nil {
		s.fail(w, "create domain", err)
		return
	}
	s.audit(r, p, "domain.create", d.ID, d.Kind+" "+d.Name)
	if d.Kind == store.DomainCustom {
		s.checkDomain(r.Context(), d)
		d, _ = s.Store.DomainByID(r.Context(), d.ID)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"domain": s.viewDomain(d, s.usernames(r), nil)})
}

// handleRequestCustomDomain lets a user ask for their own domain; an admin approves it.
func (s *server) handleRequestCustomDomain(w http.ResponseWriter, r *http.Request, p *principal) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	name := normalizeDomain(body.Name)
	if err := rules.DomainName(name); err != nil {
		s.fail(w, "request domain", err)
		return
	}
	d := &store.Domain{ID: auth.NewID("dom_"), Name: name, CreatedAt: s.now().UnixMilli(), Kind: store.DomainCustom, OwnerUserID: &p.user.ID, Status: store.DomainPending}
	if p.admin() {
		d.Status = store.DomainDNS
	}
	if err := s.Store.CreateCustomDomain(r.Context(), d); err != nil {
		s.fail(w, "request domain", err)
		return
	}
	s.audit(r, p, "domain.request", d.ID, d.Name)
	if d.Status == store.DomainPending {
		s.Notify.Send(notify.EventDomain, "自定义域名申请", p.user.Username+" 申请使用自定义域名 "+d.Name)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"domain": s.viewDomain(d, s.usernames(r), nil)})
}

// handleUpdateDomain changes allowUsers (root) or the status of a custom domain:
// approve (pending → dns check), disable, enable.
func (s *server) handleUpdateDomain(w http.ResponseWriter, r *http.Request, p *principal) {
	var body struct {
		AllowUsers *bool  `json:"allowUsers"`
		Action     string `json:"action"` // approve, disable, enable
	}
	if !decode(w, r, &body) {
		return
	}
	d, err := s.Store.DomainByID(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, "update domain", err)
		return
	}
	if body.AllowUsers != nil && d.Kind == store.DomainRoot {
		if err := s.Store.SetDomainAllowUsers(r.Context(), d.ID, *body.AllowUsers); err != nil {
			s.fail(w, "update domain", err)
			return
		}
	}
	if body.Action != "" {
		if d.Kind != store.DomainCustom {
			writeError(w, http.StatusBadRequest, "not_custom", "只有自定义域名有状态")
			return
		}
		switch body.Action {
		case "approve", "enable":
			if err := s.Store.SetDomainStatus(r.Context(), d.ID, store.DomainDNS, nil, ""); err != nil {
				s.fail(w, "update domain", err)
				return
			}
			d.Status = store.DomainDNS
			s.checkDomain(r.Context(), d)
		case "disable":
			if err := s.Store.SetDomainStatus(r.Context(), d.ID, store.DomainDisabled, nil, ""); err != nil {
				s.fail(w, "update domain", err)
				return
			}
			s.changed(r.Context(), s.domainDevices(r.Context(), d.ID)...)
		default:
			writeError(w, http.StatusBadRequest, "invalid_action", "")
			return
		}
	}
	s.audit(r, p, "domain.update", d.ID, d.Name+" "+body.Action)
	writeOK(w)
}

// handleCheckDomain re-runs the DNS check now (owner or admin).
func (s *server) handleCheckDomain(w http.ResponseWriter, r *http.Request, p *principal) {
	d, err := s.Store.DomainByID(r.Context(), r.PathValue("id"))
	if err != nil || d.Kind != store.DomainCustom || !(p.admin() || (d.OwnerUserID != nil && *d.OwnerUserID == p.user.ID)) {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	if d.Status == store.DomainDNS || d.Status == store.DomainActive {
		s.checkDomain(r.Context(), d)
	}
	d, _ = s.Store.DomainByID(r.Context(), d.ID)
	writeJSON(w, http.StatusOK, map[string]any{"domain": s.viewDomain(d, s.usernames(r), nil)})
}

func (s *server) handleDeleteDomain(w http.ResponseWriter, r *http.Request, p *principal) {
	d, err := s.Store.DomainByID(r.Context(), r.PathValue("id"))
	if err != nil || !(p.admin() || (d.Kind == store.DomainCustom && d.OwnerUserID != nil && *d.OwnerUserID == p.user.ID)) {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err := s.Store.DeleteDomain(r.Context(), d.ID); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "domain_in_use", "仍有隧道使用该域名")
			return
		}
		s.fail(w, "delete domain", err)
		return
	}
	s.audit(r, p, "domain.delete", d.ID, d.Name)
	writeOK(w)
}

func (s *server) domainDevices(ctx context.Context, domainID string) []string {
	tunnels, _ := s.Store.Tunnels(ctx, "")
	var ids []string
	for _, t := range tunnels {
		if deref(t.DomainID) == domainID && t.DeviceID != nil {
			ids = append(ids, *t.DeviceID)
		}
	}
	return ids
}

// expectedIPs are the addresses a custom domain must resolve to: NYATUNNEL_PUBLIC_IPS, or else the
// addresses of the public host.
func (s *server) expectedIPs(ctx context.Context) ([]net.IP, error) {
	if len(s.Config.PublicIPs) > 0 {
		return s.Config.PublicIPs, nil
	}
	host := s.Config.PublicHost
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	return s.lookupIP(ctx, host)
}

func (s *server) expectedIPStrings(ctx context.Context) []string {
	ips, _ := s.expectedIPs(ctx)
	out := []string{}
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	return out
}

func (s *server) lookupIP(ctx context.Context, host string) ([]net.IP, error) {
	if s.LookupIP != nil {
		return s.LookupIP(ctx, host)
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}
	return ips, err
}

// checkDomain resolves a custom domain and activates it when it points at this server. An active
// domain is only taken offline when the lookup succeeds and points elsewhere, not on DNS hiccups.
func (s *server) checkDomain(ctx context.Context, d *store.Domain) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	now := s.now().UnixMilli()
	want, err := s.expectedIPs(ctx)
	if err != nil || len(want) == 0 {
		_ = s.Store.SetDomainStatus(ctx, d.ID, d.Status, &now, "无法解析本服务器的地址，请设置 NYATUNNEL_PUBLIC_IPS")
		return
	}
	got, err := s.lookupIP(ctx, d.Name)
	match := false
	for _, a := range got {
		for _, b := range want {
			match = match || a.Equal(b)
		}
	}
	status, msg := d.Status, ""
	switch {
	case match:
		status = store.DomainActive
	case err != nil:
		msg = "DNS 查询失败：" + err.Error()
	default:
		var gs []string
		for _, a := range got {
			gs = append(gs, a.String())
		}
		msg = fmt.Sprintf("%s 解析到 %s，应指向 %s", d.Name, strings.Join(gs, ", "), strings.Join(s.expectedIPStrings(ctx), ", "))
		status = store.DomainDNS
	}
	if err := s.Store.SetDomainStatus(ctx, d.ID, status, &now, msg); err != nil {
		s.Log.Warn("domain check: store", "domain", d.Name, "err", err)
		return
	}
	if status != d.Status {
		s.audit0(ctx, "domain.status", d.ID, d.Name+": "+d.Status+" -> "+status)
		if status == store.DomainActive {
			s.Notify.Send(notify.EventDomain, "自定义域名已生效", d.Name+" 已指向本服务器，访客首次访问时将自动签发证书。")
		}
		s.changed(ctx, s.domainDevices(ctx, d.ID)...)
	}
}

// CheckDomains is the background loop: pending-DNS domains every run, active ones every sixth run.
func (s *server) CheckDomains(ctx context.Context, includeActive bool) {
	domains, err := s.Store.Domains(ctx)
	if err != nil {
		return
	}
	for _, d := range domains {
		if d.Kind == store.DomainCustom && (d.Status == store.DomainDNS || (includeActive && d.Status == store.DomainActive)) {
			s.checkDomain(ctx, d)
		}
	}
}

// audit0 writes a system audit event outside of a request.
func (s *server) audit0(ctx context.Context, action, target, detail string) {
	_ = s.Store.Audit(context.WithoutCancel(ctx), store.AuditEvent{At: s.now().UnixMilli(), ActorType: "system", Action: action, Target: target, Detail: detail})
}
