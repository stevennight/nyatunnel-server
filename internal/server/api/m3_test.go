package api

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stevennight/nyatunnel-common/tunnelproto"

	"nyatunnel-server/internal/server/hub"
	"nyatunnel-server/internal/server/store"
)

// m3Env: an admin, a normal user "alice" with a connected device, a root domain.
type m3Env struct {
	*policyEnv
	alice    *client
	aliceID  string
	aliceDev string
	aliceFD  *fakeDevice
	aliceKey ed25519.PrivateKey
}

func newM3Env(t *testing.T) *m3Env {
	p := newPolicyEnv(t)
	e := &m3Env{policyEnv: p, alice: p.newClient()}
	var u struct{ User userView }
	p.admin.must("POST", "/api/v1/users", map[string]any{"username": "alice", "password": "alice-password"}, &u)
	e.aliceID = u.User.ID
	p.admin.must("PATCH", "/api/v1/domains/"+p.domainID, map[string]any{"allowUsers": true}, nil)
	e.alice.must("POST", "/api/v1/auth/login", map[string]string{"username": "alice", "password": "alice-password"}, nil)
	var enr struct{ Code string }
	e.alice.must("POST", "/api/v1/enrollments", map[string]any{}, &enr)
	pub, priv, _ := ed25519.GenerateKey(nil)
	var claimed struct{ DeviceID string }
	p.newClient().must("POST", "/api/v1/enroll/claim", map[string]any{"code": enr.Code, "publicKey": base64.StdEncoding.EncodeToString(pub), "deviceName": "alice-pc"}, &claimed)
	e.aliceDev = claimed.DeviceID
	fd, err := p.connect(e.aliceDev, priv)
	if err != nil {
		t.Fatal(err)
	}
	e.aliceFD, e.aliceKey = fd, priv
	fd.waitConfig(func(tunnelproto.Config) bool { return true })
	return e
}

func (e *m3Env) aliceTunnel(extra map[string]any) (int, string, tunnelView) {
	body := map[string]any{"deviceId": e.aliceDev, "name": "web", "type": "https", "domainId": e.domainID, "subdomain": "alice-web",
		"localIp": "127.0.0.1", "localPort": localPort(e.t, e.backend.URL), "enabled": true}
	for k, v := range extra {
		body[k] = v
	}
	var out struct {
		Tunnel tunnelView
		Error  string
	}
	code := e.alice.do("POST", "/api/v1/tunnels", body, &out)
	return code, out.Error, out.Tunnel
}

func TestSelfServiceQuota(t *testing.T) {
	e := newM3Env(t)
	if code, errc, _ := e.aliceTunnel(nil); code != 403 || errc != "self_service_disabled" {
		t.Fatalf("without quota: %d %s", code, errc)
	}
	e.admin.must("PUT", "/api/v1/users/"+e.aliceID+"/quota", map[string]any{
		"enabled": true, "maxTunnels": 1, "types": []string{"https"}, "maxBandwidthKbps": 500, "maxDays": 7, "interstitial": true,
	}, nil)
	code, errc, tun := e.aliceTunnel(map[string]any{"bandwidthKbps": 0, "userId": e.adminID})
	if code != 201 {
		t.Fatalf("self-service create: %d %s", code, errc)
	}
	if tun.UserID != e.aliceID || tun.BandwidthKbps != 500 || !tun.Interstitial || tun.ExpiresAt == nil ||
		*tun.ExpiresAt > time.Now().Add(7*24*time.Hour+time.Minute).UnixMilli() {
		t.Fatalf("quota caps not applied: %+v", tun)
	}
	if code, errc, _ := e.aliceTunnel(map[string]any{"name": "web2", "subdomain": "alice-two"}); code != 403 || errc != "tunnel_limit" {
		t.Fatalf("second tunnel: %d %s", code, errc)
	}
	e.alice.must("DELETE", "/api/v1/tunnels/"+tun.ID, nil, nil)
	if code, errc, _ := e.aliceTunnel(map[string]any{"type": "tcp", "localPort": 22}); code != 403 || errc != "type_not_allowed" {
		t.Fatalf("tcp: %d %s", code, errc)
	}
	// Alice cannot touch the admin's tunnels.
	admins := e.tunnel("", "adminsite", nil)
	if code := e.alice.do("DELETE", "/api/v1/tunnels/"+admins.ID, nil, nil); code != 404 {
		t.Fatalf("foreign delete: %d", code)
	}
}

func TestTunnelRequests(t *testing.T) {
	e := newM3Env(t)
	hook := make(chan map[string]any, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		json.Unmarshal(b, &m)
		hook <- m
	}))
	defer srv.Close()
	var ch struct{ Channel channelView }
	e.admin.must("POST", "/api/v1/channels", map[string]any{"kind": "webhook", "name": "ops", "enabled": true,
		"events": []string{"request.created"}, "config": map[string]string{"url": srv.URL}}, &ch)
	if ch.Channel.Target != srv.URL {
		t.Fatalf("channel target %q", ch.Channel.Target)
	}
	e.admin.must("POST", "/api/v1/channels/"+ch.Channel.ID+"/test", nil, nil)
	if m := <-hook; m["event"] != "test" {
		t.Fatalf("test message %v", m)
	}

	// From the console…
	var created struct{ ID string }
	e.alice.must("POST", "/api/v1/requests", map[string]any{"type": "https", "subdomain": "alice-demo", "localIp": "127.0.0.1",
		"localPort": localPort(t, e.backend.URL), "durationHours": 24, "reason": "demo for a client", "deviceId": e.aliceDev}, &created)
	select {
	case m := <-hook:
		if m["event"] != "request.created" || !strings.Contains(m["text"].(string), "demo for a client") {
			t.Fatalf("notification %v", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no notification for the request")
	}
	// …and from the device's control stream.
	e.aliceFD.ctl.Send(tunnelproto.TypeRequestCreate, "r1", tunnelproto.TunnelRequest{Type: "tcp", LocalIP: "127.0.0.1", LocalPort: 22, Reason: "ssh"})
	if r := <-e.aliceFD.result; !r.OK {
		t.Fatalf("device request: %+v", r)
	}
	var list struct {
		Requests []requestView
		Pending  int
	}
	e.admin.must("GET", "/api/v1/requests", nil, &list)
	if list.Pending != 2 {
		t.Fatalf("pending %d", list.Pending)
	}

	var approved struct{ TunnelID string }
	e.admin.must("POST", "/api/v1/requests/"+created.ID+"/approve", map[string]any{"note": "ok", "tunnel": map[string]any{
		"deviceId": e.aliceDev, "name": "demo", "type": "https", "domainId": e.domainID, "subdomain": "alice-demo",
		"localIp": "127.0.0.1", "localPort": localPort(t, e.backend.URL), "enabled": true, "accessPolicy": "password", "accessPassword": "letmein",
	}}, &approved)
	if code := e.admin.do("POST", "/api/v1/requests/"+created.ID+"/approve", map[string]any{}, nil); code != http.StatusConflict {
		t.Fatalf("double approval: %d", code)
	}
	e.aliceFD.waitConfig(func(c tunnelproto.Config) bool { return len(c.Tunnels) == 1 && c.Tunnels[0].ID == approved.TunnelID })
	var mine struct{ Requests []requestView }
	e.alice.must("GET", "/api/v1/requests", nil, &mine)
	for _, r := range mine.Requests {
		if r.ID == created.ID && (r.Status != "approved" || r.TunnelID == nil) {
			t.Fatalf("request after approval %+v", r)
		}
	}
	other := mine.Requests[0].ID
	if other == created.ID {
		other = mine.Requests[1].ID
	}
	e.alice.must("POST", "/api/v1/requests/"+other+"/cancel", nil, nil)
	if code := e.admin.do("POST", "/api/v1/requests/"+other+"/reject", map[string]any{"note": "no"}, nil); code != 404 {
		t.Fatalf("reject a cancelled request: %d", code)
	}
}

func TestCustomDomain(t *testing.T) {
	e := newM3Env(t)
	var req struct{ Domain domainView }
	e.alice.must("POST", "/api/v1/domains/custom", map[string]string{"name": "Blog.Alice.Test"}, &req)
	if req.Domain.Status != "pending" || req.Domain.Name != "blog.alice.test" {
		t.Fatalf("requested %+v", req.Domain)
	}
	// Not approved yet: cannot be used.
	e.admin.must("PUT", "/api/v1/users/"+e.aliceID+"/quota", map[string]any{"enabled": true, "maxTunnels": 5, "types": []string{"https"}}, nil)
	if code, errc, _ := e.aliceTunnel(map[string]any{"domainId": req.Domain.ID}); code != 400 || errc != "domain_not_approved" {
		t.Fatalf("pending domain: %d %s", code, errc)
	}
	// Approved, but DNS points elsewhere.
	e.dnsMu.Lock()
	e.dns["blog.alice.test"] = []net.IP{net.ParseIP("198.51.100.99")}
	e.dnsMu.Unlock()
	e.admin.must("PATCH", "/api/v1/domains/"+req.Domain.ID, map[string]any{"action": "approve"}, nil)
	var check struct{ Domain domainView }
	e.alice.must("POST", "/api/v1/domains/"+req.Domain.ID+"/check", nil, &check)
	if check.Domain.Status != "dns" || !strings.Contains(check.Domain.CheckError, "203.0.113.10") {
		t.Fatalf("wrong DNS: %+v", check.Domain)
	}
	code, errc, tun := e.aliceTunnel(map[string]any{"domainId": req.Domain.ID})
	if code != 201 || *tun.Host != "blog.alice.test" {
		t.Fatalf("bind custom domain: %d %s %+v", code, errc, tun)
	}
	e.aliceFD.waitConfig(func(c tunnelproto.Config) bool { return len(c.Tunnels) == 1 })
	v := e.visitor("blog.alice.test")
	if resp, _ := v.do("GET", "/", nil, false, nil); resp.StatusCode != 404 {
		t.Fatalf("routes before DNS is right: %d", resp.StatusCode)
	}
	if e.edge.AllowCertificate("blog.alice.test") {
		t.Fatal("certificate allowed before DNS is right")
	}
	// DNS fixed: the domain goes live.
	e.dnsMu.Lock()
	e.dns["blog.alice.test"] = []net.IP{net.ParseIP("203.0.113.10")}
	e.dnsMu.Unlock()
	e.alice.must("POST", "/api/v1/domains/"+req.Domain.ID+"/check", nil, &check)
	if check.Domain.Status != "active" {
		t.Fatalf("after DNS fix: %+v", check.Domain)
	}
	if resp, body := v.do("GET", "/", nil, false, nil); resp.StatusCode != 200 || body != "backend ok" {
		t.Fatalf("custom domain: %d %s", resp.StatusCode, body)
	}
	if !e.edge.AllowCertificate("blog.alice.test") {
		t.Fatal("certificate refused for an active custom domain")
	}
	e.admin.must("PATCH", "/api/v1/domains/"+req.Domain.ID, map[string]any{"action": "disable"}, nil)
	if e.edge.AllowCertificate("blog.alice.test") {
		t.Fatal("certificate allowed for a disabled domain")
	}
	// Another user cannot bind alice's domain.
	if code, errc, _ := e.tunnelErr(map[string]any{"domainId": req.Domain.ID}); code != 400 || errc != "domain_not_owned" {
		t.Fatalf("foreign bind: %d %s", code, errc)
	}
}

func (e *m3Env) tunnelErr(extra map[string]any) (int, string, tunnelView) {
	body := map[string]any{"userId": e.adminID, "name": "x", "type": "https", "domainId": e.domainID, "subdomain": "x",
		"localIp": "127.0.0.1", "localPort": 80, "enabled": true}
	for k, v := range extra {
		body[k] = v
	}
	var out struct {
		Tunnel tunnelView
		Error  string
	}
	code := e.admin.do("POST", "/api/v1/tunnels", body, &out)
	return code, out.Error, out.Tunnel
}

func TestUserTrafficQuotaAndMinVersion(t *testing.T) {
	e := newM3Env(t)
	e.admin.must("PUT", "/api/v1/users/"+e.aliceID+"/quota", map[string]any{"enabled": true, "maxTunnels": 5, "types": []string{"https"}, "monthlyTrafficMb": 1}, nil)
	if code, errc, _ := e.aliceTunnel(nil); code != 201 {
		t.Fatalf("create: %d %s", code, errc)
	}
	e.aliceFD.waitConfig(func(c tunnelproto.Config) bool { return len(c.Tunnels) == 1 })
	v := e.visitor("alice-web.t.example.com")
	v.do("GET", "/?bytes=1200000", nil, false, nil)
	e.edge.Flush(context.Background())
	if resp, _ := v.do("GET", "/", nil, false, nil); resp.StatusCode != 503 {
		t.Fatalf("account over quota: %d", resp.StatusCode)
	}
	var users struct{ Users []adminUserView }
	e.admin.must("GET", "/api/v1/users", nil, &users)
	for _, u := range users.Users {
		if u.ID == e.aliceID && u.MonthBytes < 1<<20 {
			t.Fatalf("user month bytes %d", u.MonthBytes)
		}
	}

	e.admin.must("PUT", "/api/v1/settings", map[string]any{"serverName": "Nya", "minClientVersion": "v9.0.0", "surgeMbPerHour": 0}, nil)
	// The test device reports version "test" (counts as 0.0.0): a new session is refused with 4426.
	if _, err := e.connect(e.aliceDev, e.aliceKey); websocket.CloseStatus(err) != tunnelproto.CloseUpgradeRequired {
		t.Fatalf("old client: %v", err)
	}
	if !hub.VersionLess("0.9.9", "0.10.0") || hub.VersionLess("v1.2.3-dev", "1.2.3") || !hub.VersionLess("garbage", "0.0.1") {
		t.Fatal("VersionLess")
	}
}

func TestAccountExtras(t *testing.T) {
	e := newM3Env(t)
	second := e.newClient()
	second.must("POST", "/api/v1/auth/login", map[string]string{"username": "alice", "password": "alice-password"}, nil)
	var sess struct{ Sessions []sessionView }
	e.alice.must("GET", "/api/v1/me/sessions", nil, &sess)
	if len(sess.Sessions) != 2 {
		t.Fatalf("sessions %+v", sess)
	}
	var other string
	for _, s := range sess.Sessions {
		if !s.Current {
			other = s.ID
		}
	}
	e.alice.must("DELETE", "/api/v1/me/sessions/"+other, nil, nil)
	if code := second.do("GET", "/api/v1/me", nil, nil); code != 401 {
		t.Fatalf("revoked session still works: %d", code)
	}

	var enr struct{ Code string }
	e.alice.must("POST", "/api/v1/enrollments", map[string]any{"deviceNameHint": "laptop"}, &enr)
	var pending struct{ Enrollments []enrollmentView }
	e.alice.must("GET", "/api/v1/enrollments", nil, &pending)
	if len(pending.Enrollments) != 1 || pending.Enrollments[0].DeviceNameHint != "laptop" {
		t.Fatalf("pending %+v", pending)
	}
	e.alice.must("DELETE", "/api/v1/enrollments/"+pending.Enrollments[0].ID, nil, nil)
	if code := e.newClient().do("GET", "/api/v1/enroll/preview?code="+enr.Code, nil, nil); code != 404 {
		t.Fatalf("cancelled code still valid: %d", code)
	}

	port := freePort(t)
	var pool struct{ PortPool portPoolView }
	e.admin.must("POST", "/api/v1/port-pools", map[string]any{"proto": "tcp", "start": port, "end": port}, &pool)
	e.admin.must("POST", "/api/v1/tunnels", map[string]any{"userId": e.adminID, "name": "p", "type": "tcp", "localIp": "127.0.0.1", "localPort": 1, "enabled": true}, nil)
	if code := e.admin.do("DELETE", "/api/v1/port-pools/"+pool.PortPool.ID, nil, nil); code != http.StatusConflict {
		t.Fatalf("delete used pool: %d", code)
	}
	var disabled struct{ Error string }
	e.admin.must("PATCH", "/api/v1/users/"+e.aliceID, map[string]any{"disabled": true}, nil)
	e.admin.do("POST", "/api/v1/tunnels", map[string]any{"userId": e.aliceID, "name": "z", "type": "https", "domainId": e.domainID,
		"subdomain": "alice-z", "localIp": "127.0.0.1", "localPort": 1, "enabled": true}, &disabled)
	if disabled.Error != "invalid_user" {
		t.Fatalf("tunnel for a disabled user: %q", disabled.Error)
	}
	_ = store.RoleAdmin
}
