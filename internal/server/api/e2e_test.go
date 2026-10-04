package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stevennight/nyatunnel-common/tunnelproto"

	"nyatunnel-server/internal/server/auth"
	"nyatunnel-server/internal/server/config"
	"nyatunnel-server/internal/server/edge"
	"nyatunnel-server/internal/server/hub"
	"nyatunnel-server/internal/server/realip"
	"nyatunnel-server/internal/server/secrets"
	"nyatunnel-server/internal/server/store"
)

type testEnv struct {
	t       *testing.T
	console *httptest.Server
	ingress *httptest.Server
	cfg     config.Config
	store   *store.Store
	hub     *hub.Hub
	edge    *edge.Edge
	handler *Handler
	// dns answers custom-domain lookups.
	dnsMu sync.Mutex
	dns   map[string][]net.IP
}

const testSetupToken = "setup-token"

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := secrets.New(bytes.Repeat([]byte{1}, 32))
	log := slog.New(slog.DiscardHandler)
	env := &testEnv{t: t, store: st}

	var handler http.Handler
	env.console = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(env.console.Close)
	cfg, err := config.Load(func(k string) string {
		switch k {
		case "NYATUNNEL_PUBLIC_URL":
			return env.console.URL
		case "NYATUNNEL_PORT_BIND":
			return "127.0.0.1"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg.WebDir = t.TempDir()
	cfg.PublicIPs = []net.IP{net.ParseIP("203.0.113.10")}
	env.cfg = cfg
	env.dns = map[string][]net.IP{}
	resolver := &realip.Resolver{Trusted: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}}
	env.hub = hub.New(hub.Options{Store: st, Log: log, PublicHost: cfg.PublicHost, TCPHost: "127.0.0.1",
		OnChange: func() { env.edge.Reload(context.Background()) }, Audit: AuditFunc(st, log, time.Now),
		OnRequest: func(ctx context.Context, deviceID string, req tunnelproto.TunnelRequest) error {
			return env.handler.DeviceRequest(ctx, deviceID, req)
		},
		MinClientVersion: func(ctx context.Context) string { return env.handler.MinClientVersion(ctx) }})
	env.edge = edge.New(edge.Options{Store: st, Dialer: env.hub, Log: log, RealIP: resolver, BindAddr: "127.0.0.1",
		GateKey: bytes.Repeat([]byte{2}, 32), ConsoleURL: env.console.URL, Audit: AuditFunc(st, log, time.Now)})
	t.Cleanup(env.edge.Close)
	t.Cleanup(env.hub.Close)
	h := New(Options{Config: cfg, Store: st, Hub: env.hub, Edge: env.edge, Secrets: box, Log: log, SetupToken: testSetupToken,
		LookupIP: func(_ context.Context, host string) ([]net.IP, error) {
			env.dnsMu.Lock()
			defer env.dnsMu.Unlock()
			if ips, ok := env.dns[host]; ok {
				return ips, nil
			}
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}})
	handler = h
	env.handler = h
	env.ingress = httptest.NewServer(env.edge)
	t.Cleanup(env.ingress.Close)
	return env
}

// client is a console session.
type client struct {
	t    *testing.T
	base string
	http *http.Client
}

func (e *testEnv) newClient() *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: e.t, base: e.console.URL, http: &http.Client{Jar: jar}}
}

func (c *client) do(method, path string, body any, out any) int {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrfHeader, "1")
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, raw)
		}
	}
	return resp.StatusCode
}

func (c *client) must(method, path string, body any, out any) {
	c.t.Helper()
	var raw json.RawMessage
	code := c.do(method, path, body, &raw)
	if code >= 300 {
		c.t.Fatalf("%s %s: %d %s", method, path, code, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatal(err)
		}
	}
}

// fakeDevice speaks the device side of the protocol and forwards streams to local targets.
type fakeDevice struct {
	t      *testing.T
	ws     *websocket.Conn
	ctl    *tunnelproto.Control
	mu     sync.Mutex
	cfg    tunnelproto.Config
	cfgs   chan tunnelproto.Config
	result chan tunnelproto.Result
	closed chan error
	// unconfirmed tunnels are refused like a device whose owner has not confirmed them.
	unconfirmed map[string]bool
}

func (e *testEnv) connect(deviceID string, priv ed25519.PrivateKey) (*fakeDevice, error) {
	ctx := context.Background()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.console.URL, "http")+tunnelproto.ConnectPath, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tunnelproto.ClientHandshake(ctx, ws, e.cfg.PublicHost, tunnelproto.Hello{DeviceID: deviceID, ClientVersion: "test"}, priv); err != nil {
		return nil, err
	}
	sess, err := tunnelproto.ClientSession(ctx, ws)
	if err != nil {
		return nil, err
	}
	stream, err := sess.Open()
	if err != nil {
		return nil, err
	}
	d := &fakeDevice{t: e.t, ws: ws, ctl: tunnelproto.NewControl(stream), cfgs: make(chan tunnelproto.Config, 8),
		result: make(chan tunnelproto.Result, 8), closed: make(chan error, 1), unconfirmed: map[string]bool{}}
	go func() {
		for {
			m, err := d.ctl.Recv()
			if err != nil {
				d.closed <- err
				return
			}
			switch m.Type {
			case tunnelproto.TypeConfig:
				var c tunnelproto.Config
				m.Decode(&c)
				d.mu.Lock()
				d.cfg = c
				d.mu.Unlock()
				d.cfgs <- c
			case tunnelproto.TypeResult:
				var r tunnelproto.Result
				m.Decode(&r)
				d.result <- r
			}
		}
	}()
	go func() {
		for {
			s, err := sess.Accept()
			if err != nil {
				return
			}
			go d.serve(s)
		}
	}()
	return d, nil
}

func (d *fakeDevice) serve(s net.Conn) {
	defer s.Close()
	h, err := tunnelproto.ReadStreamHeader(s)
	if err != nil {
		return
	}
	d.mu.Lock()
	var target *tunnelproto.Tunnel
	for i := range d.cfg.Tunnels {
		if d.cfg.Tunnels[i].ID == h.TunnelID {
			target = &d.cfg.Tunnels[i]
		}
	}
	unconfirmed := d.unconfirmed[h.TunnelID]
	d.mu.Unlock()
	if target == nil {
		tunnelproto.WriteStreamReply(s, tunnelproto.ReplyUnknownTunnel)
		return
	}
	if unconfirmed {
		tunnelproto.WriteStreamReply(s, tunnelproto.ReplyUnconfirmed)
		return
	}
	local, err := net.Dial(h.Proto, net.JoinHostPort(target.LocalIP, strconv.Itoa(target.LocalPort)))
	if err != nil {
		tunnelproto.WriteStreamReply(s, tunnelproto.ReplyDialFailed)
		return
	}
	defer local.Close()
	tunnelproto.WriteStreamReply(s, tunnelproto.ReplyOK)
	if h.Proto == "udp" {
		go func() {
			buf := make([]byte, tunnelproto.MaxDatagram)
			for {
				n, err := local.Read(buf)
				if err != nil || tunnelproto.WriteDatagram(s, buf[:n]) != nil {
					return
				}
			}
		}()
		buf := make([]byte, tunnelproto.MaxDatagram)
		for {
			d, err := tunnelproto.ReadDatagram(s, buf)
			if err != nil {
				return
			}
			local.Write(d)
		}
	}
	go func() {
		io.Copy(local, s)
		local.(*net.TCPConn).CloseWrite() // like the real agent: pass the visitor's EOF on
	}()
	io.Copy(s, local)
}

func (d *fakeDevice) waitConfig(pred func(tunnelproto.Config) bool) tunnelproto.Config {
	d.t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case c := <-d.cfgs:
			if pred(c) {
				return c
			}
		case <-timeout:
			d.t.Fatal("timed out waiting for a config")
		}
	}
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func localPort(t *testing.T, rawURL string) int {
	_, p, _ := net.SplitHostPort(strings.TrimPrefix(rawURL, "http://"))
	n, _ := strconv.Atoi(p)
	return n
}

func TestEndToEnd(t *testing.T) {
	env := newEnv(t)
	admin := env.newClient()

	// First-run setup needs the token from the log.
	if code := admin.do("POST", "/api/v1/setup", map[string]string{"token": "wrong", "username": "nya", "password": "admin-password-1"}, nil); code != http.StatusForbidden {
		t.Fatalf("setup with a wrong token: %d", code)
	}
	admin.must("POST", "/api/v1/setup", map[string]string{"token": testSetupToken, "username": "nya", "password": "admin-password-1"}, nil)
	if code := env.newClient().do("POST", "/api/v1/setup", map[string]string{"token": testSetupToken, "username": "evil", "password": "admin-password-1"}, nil); code != http.StatusConflict {
		t.Fatalf("second setup: %d", code)
	}

	// Resources.
	var dom struct{ Domain domainView }
	admin.must("POST", "/api/v1/domains", map[string]any{"name": "*.T.Example.com", "allowUsers": true}, &dom)
	tcpPort := freePort(t)
	admin.must("POST", "/api/v1/port-pools", map[string]any{"proto": "tcp", "start": tcpPort, "end": tcpPort}, nil)
	var alice struct{ User userView }
	admin.must("POST", "/api/v1/users", map[string]any{"username": "alice", "password": "alice-password"}, &alice)

	// Local services on the "device".
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "hello %s from %s", r.Host, r.Header.Get("X-Forwarded-For"))
	}))
	defer web.Close()
	echo, _ := net.Listen("tcp", "127.0.0.1:0")
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()

	// Tunnels for alice, waiting for her device. Normal users must use "<username>-" subdomains.
	tunnel := func(body map[string]any) (int, tunnelView, string) {
		var out struct {
			Tunnel  tunnelView
			Error   string
			Message string
		}
		code := admin.do("POST", "/api/v1/tunnels", body, &out)
		return code, out.Tunnel, out.Error
	}
	base := map[string]any{"userId": alice.User.ID, "localIp": "127.0.0.1", "enabled": true, "clientCanToggle": true}
	with := func(kv ...any) map[string]any {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	if code, _, e := tunnel(with("name", "web", "type", "https", "domainId", dom.Domain.ID, "subdomain", "web", "localPort", localPort(t, web.URL))); code != 400 || e != "subdomain_prefix" {
		t.Fatalf("missing prefix: %d %s", code, e)
	}
	if code, _, e := tunnel(with("name", "web", "type", "https", "domainId", dom.Domain.ID, "subdomain", "alice-paypal", "localPort", 80)); code != 400 || e != "subdomain_blocked" {
		t.Fatalf("brand word: %d %s", code, e)
	}
	code, webTunnel, e := tunnel(with("name", "web", "type", "https", "domainId", dom.Domain.ID, "subdomain", "alice-web", "localPort", localPort(t, web.URL)))
	if code != 201 || webTunnel.Host == nil || *webTunnel.Host != "alice-web.t.example.com" || webTunnel.State != "unassigned" {
		t.Fatalf("https tunnel: %d %s %+v", code, e, webTunnel)
	}
	code, tcpTunnel, e := tunnel(with("name", "echo", "type", "tcp", "localPort", echo.Addr().(*net.TCPAddr).Port))
	if code != 201 || tcpTunnel.RemotePort == nil || *tcpTunnel.RemotePort != tcpPort {
		t.Fatalf("tcp tunnel: %d %s %+v", code, e, tcpTunnel)
	}
	if code, _, e := tunnel(with("name", "echo2", "type", "tcp", "localPort", 1)); code != 400 || e != "pool_exhausted" {
		t.Fatalf("exhausted pool: %d %s", code, e)
	}

	// Alice signs in and invites her own device, preassigning both tunnels.
	aliceC := env.newClient()
	aliceC.must("POST", "/api/v1/auth/login", map[string]string{"username": "Alice", "password": "alice-password"}, nil)
	if code := aliceC.do("GET", "/api/v1/users", nil, nil); code != http.StatusForbidden {
		t.Fatalf("user reached admin endpoint: %d", code)
	}
	if code := aliceC.do("POST", "/api/v1/tunnels", with("name", "x", "type", "tcp", "localPort", 1), nil); code != http.StatusForbidden {
		t.Fatalf("user created a tunnel: %d", code)
	}
	var enr struct {
		Code string
		URL  string
	}
	aliceC.must("POST", "/api/v1/enrollments", map[string]any{"deviceNameHint": "alice-pc", "tunnelIds": []string{webTunnel.ID, tcpTunnel.ID}}, &enr)
	if !strings.HasPrefix(enr.URL, "nyatunnel://enroll?") {
		t.Fatalf("enroll url %q", enr.URL)
	}
	var preview struct {
		Owner   string
		Tunnels []struct{ Name, PublicURL string }
	}
	anon := env.newClient()
	anon.must("GET", "/api/v1/enroll/preview?code="+strings.ToLower(strings.ReplaceAll(enr.Code, "-", "")), nil, &preview)
	if preview.Owner != "alice" || len(preview.Tunnels) != 2 {
		t.Fatalf("preview %+v", preview)
	}
	pub, priv, _ := ed25519.GenerateKey(nil)
	var claimed struct{ DeviceID string }
	anon.must("POST", "/api/v1/enroll/claim", map[string]any{"code": enr.Code, "publicKey": base64.StdEncoding.EncodeToString(pub), "deviceName": "alice-pc", "platform": "test"}, &claimed)
	if code := anon.do("POST", "/api/v1/enroll/claim", map[string]any{"code": enr.Code, "publicKey": base64.StdEncoding.EncodeToString(pub), "deviceName": "again"}, nil); code != http.StatusNotFound {
		t.Fatalf("code reused: %d", code)
	}

	// A device with the wrong key is refused with 4401.
	_, wrongPriv, _ := ed25519.GenerateKey(nil)
	if _, err := env.connect(claimed.DeviceID, wrongPriv); websocket.CloseStatus(err) != tunnelproto.CloseUnauthorized {
		t.Fatalf("wrong key: %v", err)
	}

	dev, err := env.connect(claimed.DeviceID, priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := dev.waitConfig(func(c tunnelproto.Config) bool { return len(c.Tunnels) == 2 })
	if cfg.Tunnels[0].PublicURL == "" {
		t.Fatalf("config %+v", cfg)
	}

	// A tunnel the device owner has not confirmed yet: visitors get a 503 that says so, and the
	// console shows the state the device reported.
	dev.mu.Lock()
	dev.unconfirmed[webTunnel.ID] = true
	dev.mu.Unlock()
	dev.ctl.Send(tunnelproto.TypeStatus, "", tunnelproto.Status{TunnelID: webTunnel.ID, State: tunnelproto.StateUnconfirmed})
	req, _ := http.NewRequest("GET", env.ingress.URL+"/", nil)
	req.Host = "alice-web.t.example.com"
	req.Header.Set("X-Forwarded-For", "198.51.100.7")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "等待设备确认") {
		t.Fatalf("unconfirmed tunnel: %d %s", resp.StatusCode, body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var got struct{ Tunnels []tunnelView }
		aliceC.must("GET", "/api/v1/tunnels", nil, &got)
		state := ""
		for _, v := range got.Tunnels {
			if v.ID == webTunnel.ID {
				state = v.State
			}
		}
		if state == "unconfirmed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("console state %q, want unconfirmed", state)
		}
		time.Sleep(20 * time.Millisecond)
	}
	dev.mu.Lock()
	delete(dev.unconfirmed, webTunnel.ID)
	dev.mu.Unlock()
	dev.ctl.Send(tunnelproto.TypeStatus, "", tunnelproto.Status{TunnelID: webTunnel.ID, State: tunnelproto.StateRunning})

	// HTTPS tunnel through the ingress, as Caddy would forward it.
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "hello alice-web.t.example.com from 198.51.100.7" {
		t.Fatalf("ingress: %d %s", resp.StatusCode, body)
	}
	req.Host = "nobody.t.example.com"
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown host: %d", resp.StatusCode)
	}

	// TCP tunnel.
	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(tcpPort)))
	if err != nil {
		t.Fatal(err)
	}
	c.Write([]byte("ping"))
	buf := make([]byte, 4)
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("tcp echo: %q %v", buf, err)
	}
	c.Close()

	// The device may pause (allowed) but not move its local target (not allowed).
	dev.ctl.Send(tunnelproto.TypeTunnelUpdate, "1", tunnelproto.TunnelUpdate{TunnelID: webTunnel.ID, LocalPort: ptr(9)})
	if r := <-dev.result; r.OK || r.Error != "field_not_allowed" {
		t.Fatalf("local edit: %+v", r)
	}
	dev.ctl.Send(tunnelproto.TypeTunnelUpdate, "2", tunnelproto.TunnelUpdate{TunnelID: webTunnel.ID, PausedByClient: ptr(true)})
	if r := <-dev.result; !r.OK {
		t.Fatalf("pause: %+v", r)
	}
	dev.waitConfig(func(c tunnelproto.Config) bool {
		for _, t := range c.Tunnels {
			if t.ID == webTunnel.ID {
				return t.PausedByClient
			}
		}
		return false
	})
	req.Host = "alice-web.t.example.com"
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("paused tunnel: %d", resp.StatusCode)
	}
	var list struct{ Tunnels []tunnelView }
	aliceC.must("GET", "/api/v1/tunnels", nil, &list)
	if len(list.Tunnels) != 2 {
		t.Fatalf("alice sees %d tunnels", len(list.Tunnels))
	}

	// Revoking the device closes its session immediately with 4401 and frees the port.
	aliceC.must("POST", "/api/v1/devices/"+claimed.DeviceID+"/revoke", nil, nil)
	select {
	case err := <-dev.closed:
		_ = err
	case <-time.After(5 * time.Second):
		t.Fatal("revoked device still connected")
	}
	if _, err := env.connect(claimed.DeviceID, priv); websocket.CloseStatus(err) != tunnelproto.CloseUnauthorized {
		t.Fatalf("revoked device reconnect: %v", err)
	}
	if c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(tcpPort)), time.Second); err == nil {
		c.Close()
		t.Fatal("port of an unassigned tunnel still open")
	}

	// Audit trail and CSRF.
	var audit struct{ Events []auditView }
	admin.must("GET", "/api/v1/audit", nil, &audit)
	actions := map[string]bool{}
	for _, e := range audit.Events {
		actions[e.Action] = true
	}
	for _, a := range []string{"auth.setup", "device.enrolled", "device.auth_failed", "tunnel.device_update", "tunnel.update_denied", "device.revoke"} {
		if !actions[a] {
			t.Errorf("audit misses %s", a)
		}
	}
	req2, _ := http.NewRequest("POST", env.console.URL+"/api/v1/users", strings.NewReader(`{}`))
	for _, ck := range admin.http.Jar.Cookies(req2.URL) {
		req2.AddCookie(ck)
	}
	if resp, _ := http.DefaultClient.Do(req2); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF header: %d", resp.StatusCode)
	}
}

func TestLoginWithTOTPAndForcedPolicy(t *testing.T) {
	env := newEnv(t)
	admin := env.newClient()
	admin.must("POST", "/api/v1/setup", map[string]string{"token": testSetupToken, "username": "nya", "password": "admin-password-1"}, nil)
	var setup struct{ Secret string }
	admin.must("POST", "/api/v1/me/totp/setup", nil, &setup)
	code, _ := auth.TOTPCode(setup.Secret, time.Now())
	var enabled struct{ RecoveryCodes []string }
	admin.must("POST", "/api/v1/me/totp/enable", map[string]string{"secret": setup.Secret, "code": code}, &enabled)
	if len(enabled.RecoveryCodes) != recoveryCodeCount {
		t.Fatalf("recovery codes %v", enabled.RecoveryCodes)
	}
	admin.must("PUT", "/api/v1/settings", map[string]any{"serverName": "Nya", "forceTotp": true}, nil)
	admin.must("POST", "/api/v1/users", map[string]any{"username": "bob", "password": "bob-password"}, nil)

	c := env.newClient()
	var errOut struct{ Error string }
	if c.do("POST", "/api/v1/auth/login", map[string]string{"username": "nya", "password": "admin-password-1"}, &errOut); errOut.Error != "totp_required" {
		t.Fatalf("expected totp_required, got %s", errOut.Error)
	}
	if c.do("POST", "/api/v1/auth/login", map[string]string{"username": "nya", "password": "admin-password-1", "totp": "000000"}, &errOut); errOut.Error != "invalid_totp" {
		t.Fatalf("expected invalid_totp, got %s", errOut.Error)
	}
	c.must("POST", "/api/v1/auth/login", map[string]string{"username": "nya", "password": "admin-password-1", "recoveryCode": strings.ToUpper(enabled.RecoveryCodes[0])}, nil)
	if c2 := env.newClient(); c2.do("POST", "/api/v1/auth/login", map[string]string{"username": "nya", "password": "admin-password-1", "recoveryCode": enabled.RecoveryCodes[0]}, nil) != http.StatusUnauthorized {
		t.Fatal("recovery code accepted twice")
	}

	// Bob has no TOTP while it is mandatory: he can sign in but only reach the setup endpoints.
	bob := env.newClient()
	bob.must("POST", "/api/v1/auth/login", map[string]string{"username": "bob", "password": "bob-password"}, nil)
	var me struct{ MustSetupTotp bool }
	bob.must("GET", "/api/v1/me", nil, &me)
	if !me.MustSetupTotp {
		t.Fatal("mustSetupTotp not reported")
	}
	if bob.do("GET", "/api/v1/tunnels", nil, &errOut); errOut.Error != "totp_setup_required" {
		t.Fatalf("expected totp_setup_required, got %q", errOut.Error)
	}

	// Brute force is throttled per account.
	for i := 0; i < loginFailureLimit; i++ {
		env.newClient().do("POST", "/api/v1/auth/login", map[string]string{"username": "bob", "password": "wrong"}, nil)
	}
	if code := env.newClient().do("POST", "/api/v1/auth/login", map[string]string{"username": "bob", "password": "bob-password"}, nil); code != http.StatusTooManyRequests {
		t.Fatalf("throttle: %d", code)
	}
}

func ptr[T any](v T) *T { return &v }
