package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stevennight/nyatunnel-common/tunnelproto"
)

// policyEnv is an admin with one connected device and helpers to create tunnels for it.
type policyEnv struct {
	*testEnv
	admin    *client
	adminID  string
	domainID string
	deviceID string
	dev      *fakeDevice
	backend  *httptest.Server
	seenAuth chan string
}

func newPolicyEnv(t *testing.T) *policyEnv {
	env := newEnv(t)
	p := &policyEnv{testEnv: env, admin: env.newClient(), seenAuth: make(chan string, 16)}
	p.admin.must("POST", "/api/v1/setup", map[string]string{"token": testSetupToken, "username": "nya", "password": "admin-password-1"}, nil)
	var me struct{ User userView }
	p.admin.must("GET", "/api/v1/me", nil, &me)
	p.adminID = me.User.ID
	var dom struct{ Domain domainView }
	p.admin.must("POST", "/api/v1/domains", map[string]any{"name": "t.example.com"}, &dom)
	p.domainID = dom.Domain.ID

	p.backend = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case p.seenAuth <- r.Header.Get("Authorization"):
		default:
		}
		if n, err := strconv.Atoi(r.URL.Query().Get("bytes")); err == nil {
			w.Write(bytes.Repeat([]byte{'x'}, n))
			return
		}
		fmt.Fprint(w, "backend ok")
	}))
	t.Cleanup(p.backend.Close)

	var enr struct{ Code string }
	p.admin.must("POST", "/api/v1/enrollments", map[string]any{}, &enr)
	pub, priv, _ := ed25519.GenerateKey(nil)
	var claimed struct{ DeviceID string }
	env.newClient().must("POST", "/api/v1/enroll/claim", map[string]any{"code": enr.Code, "publicKey": base64.StdEncoding.EncodeToString(pub), "deviceName": "pc"}, &claimed)
	p.deviceID = claimed.DeviceID
	dev, err := env.connect(p.deviceID, priv)
	if err != nil {
		t.Fatal(err)
	}
	p.dev = dev
	dev.waitConfig(func(tunnelproto.Config) bool { return true })
	return p
}

// tunnel creates (or with id updates) an HTTPS tunnel to the backend with extra settings.
func (p *policyEnv) tunnel(id, sub string, extra map[string]any) tunnelView {
	body := map[string]any{"userId": p.adminID, "deviceId": p.deviceID, "name": sub, "type": "https", "domainId": p.domainID,
		"subdomain": sub, "localIp": "127.0.0.1", "localPort": localPort(p.t, p.backend.URL), "enabled": true}
	for k, v := range extra {
		body[k] = v
	}
	var out struct{ Tunnel tunnelView }
	if id == "" {
		p.admin.must("POST", "/api/v1/tunnels", body, &out)
	} else {
		p.admin.must("PUT", "/api/v1/tunnels/"+id, body, &out)
	}
	p.dev.waitConfig(func(c tunnelproto.Config) bool {
		for _, t := range c.Tunnels {
			if t.ID == out.Tunnel.ID {
				return true
			}
		}
		return false
	})
	return out.Tunnel
}

// visitor is a browser on the tunnel side: it keeps cookies and does not follow redirects.
type visitor struct {
	t      *testing.T
	ingres string
	host   string
	ip     string
	http   *http.Client
}

func (p *policyEnv) visitor(host string) *visitor {
	jar, _ := cookiejar.New(nil)
	return &visitor{t: p.t, ingres: p.ingress.URL, host: host, ip: "198.51.100.1", http: &http.Client{
		Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (v *visitor) do(method, path string, form url.Values, html bool, mod func(*http.Request)) (*http.Response, string) {
	v.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, v.ingres+path, body)
	req.Host = v.host
	// Cookies are kept per host name; the ingress URL host differs from the tunnel host, so add them by hand.
	u, _ := url.Parse("http://" + v.host + "/")
	for _, c := range v.http.Jar.Cookies(u) {
		req.AddCookie(c)
	}
	req.Header.Set("X-Forwarded-For", v.ip)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if html {
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
	}
	if mod != nil {
		mod(req)
	}
	resp, err := v.http.Do(req)
	if err != nil {
		v.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	v.http.Jar.SetCookies(u, resp.Cookies())
	return resp, string(b)
}

func TestPasswordGate(t *testing.T) {
	p := newPolicyEnv(t)
	tun := p.tunnel("", "secret", map[string]any{"accessPolicy": "password", "accessPassword": "open-sesame"})
	if !tun.HasPassword {
		t.Fatal("hasPassword not reported")
	}
	v := p.visitor("secret.t.example.com")
	if resp, body := v.do("GET", "/page", nil, true, nil); resp.StatusCode != 401 || !strings.Contains(body, "访问密码") {
		t.Fatalf("gate page: %d", resp.StatusCode)
	}
	if resp, _ := v.do("GET", "/api", nil, false, nil); resp.StatusCode != 401 {
		t.Fatalf("api without cookie: %d", resp.StatusCode)
	}
	if resp, _ := v.do("POST", "/__nyatunnel/gate", url.Values{"password": {"wrong"}, "r": {"/page"}}, true, nil); resp.StatusCode != 401 {
		t.Fatalf("wrong password: %d", resp.StatusCode)
	}
	resp, _ := v.do("POST", "/__nyatunnel/gate", url.Values{"password": {"open-sesame"}, "r": {"//evil.com"}}, true, nil)
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/" {
		t.Fatalf("login: %d %q (open redirect must be refused)", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, body := v.do("GET", "/page", nil, true, nil); resp.StatusCode != 200 || body != "backend ok" {
		t.Fatalf("after login: %d %s", resp.StatusCode, body)
	}
	// Changing the password logs everybody out.
	p.tunnel(tun.ID, "secret", map[string]any{"accessPolicy": "password", "accessPassword": "new-secret"})
	if resp, _ := v.do("GET", "/page", nil, true, nil); resp.StatusCode != 401 {
		t.Fatalf("old cookie after password change: %d", resp.StatusCode)
	}
	// Another tunnel's cookie is useless here.
	p.tunnel("", "other", map[string]any{"accessPolicy": "password", "accessPassword": "other-pass"})
	o := p.visitor("other.t.example.com")
	o.do("POST", "/__nyatunnel/gate", url.Values{"password": {"other-pass"}}, true, nil)
	u, _ := url.Parse("http://other.t.example.com/")
	stolen := o.http.Jar.Cookies(u)
	if resp, _ := v.do("GET", "/page", nil, true, func(r *http.Request) {
		for _, c := range stolen {
			r.AddCookie(c)
		}
	}); resp.StatusCode != 401 {
		t.Fatalf("cookie of another tunnel accepted: %d", resp.StatusCode)
	}
}

func TestBasicAllowlistAndInterstitial(t *testing.T) {
	p := newPolicyEnv(t)
	p.tunnel("", "rest", map[string]any{"accessPolicy": "basic", "basicUsername": "bob", "accessPassword": "hunter22"})
	v := p.visitor("rest.t.example.com")
	resp, _ := v.do("GET", "/", nil, false, nil)
	if resp.StatusCode != 401 || !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Basic") {
		t.Fatalf("basic challenge: %d %v", resp.StatusCode, resp.Header)
	}
	if resp, _ := v.do("GET", "/", nil, false, func(r *http.Request) { r.SetBasicAuth("bob", "nope") }); resp.StatusCode != 401 {
		t.Fatalf("bad basic: %d", resp.StatusCode)
	}
	if resp, body := v.do("GET", "/", nil, false, func(r *http.Request) { r.SetBasicAuth("bob", "hunter22") }); resp.StatusCode != 200 || body != "backend ok" {
		t.Fatalf("basic ok: %d %s", resp.StatusCode, body)
	}
	if got := <-p.seenAuth; got != "" {
		t.Fatalf("gate credentials leaked to the backend: %q", got)
	}

	p.tunnel("", "office", map[string]any{"ipAllowlist": "203.0.113.0/24, 2001:db8::1"})
	o := p.visitor("office.t.example.com")
	if resp, _ := o.do("GET", "/", nil, false, nil); resp.StatusCode != 403 {
		t.Fatalf("outside allowlist: %d", resp.StatusCode)
	}
	o.ip = "203.0.113.77"
	if resp, _ := o.do("GET", "/", nil, false, nil); resp.StatusCode != 200 {
		t.Fatalf("inside allowlist: %d", resp.StatusCode)
	}
	var bad struct{ Error string }
	p.admin.do("POST", "/api/v1/tunnels", map[string]any{"userId": p.adminID, "name": "x", "type": "https", "domainId": p.domainID,
		"subdomain": "x", "localIp": "127.0.0.1", "localPort": 80, "enabled": true, "ipAllowlist": "not-an-ip"}, &bad)
	if bad.Error != "invalid_allowlist" {
		t.Fatalf("bad allowlist accepted: %s", bad.Error)
	}

	p.tunnel("", "share", map[string]any{"interstitial": true})
	s := p.visitor("share.t.example.com")
	if resp, body := s.do("GET", "/doc", nil, true, nil); resp.StatusCode != 200 || !strings.Contains(body, "内网穿透地址") {
		t.Fatalf("interstitial: %d %s", resp.StatusCode, body)
	}
	if _, body := s.do("GET", "/doc", nil, false, nil); body != "backend ok" {
		t.Fatalf("non-navigation requests pass the interstitial: %s", body)
	}
	if resp, _ := s.do("POST", "/__nyatunnel/continue", url.Values{"r": {"/doc"}}, true, nil); resp.StatusCode != 303 || resp.Header.Get("Location") != "/doc" {
		t.Fatalf("continue: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if _, body := s.do("GET", "/doc", nil, true, nil); body != "backend ok" {
		t.Fatalf("after acknowledging: %s", body)
	}
}

func TestLoginGate(t *testing.T) {
	p := newPolicyEnv(t)
	p.tunnel("", "family", map[string]any{"accessPolicy": "login"})
	v := p.visitor("family.t.example.com")
	resp, _ := v.do("GET", "/photos", nil, true, nil)
	if resp.StatusCode != 302 || !strings.HasPrefix(resp.Header.Get("Location"), p.console.URL+"/tunnel-login?") {
		t.Fatalf("login gate redirect: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	// Not signed in: the console sends the visitor to its login page first.
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r1, _ := anon.Get(resp.Header.Get("Location"))
	if r1.StatusCode != 302 || !strings.HasPrefix(r1.Header.Get("Location"), "/login?next=") {
		t.Fatalf("anonymous: %d %s", r1.StatusCode, r1.Header.Get("Location"))
	}
	// Signed in: handed back to the tunnel with a token.
	admin := p.admin.http
	admin.CheckRedirect = anon.CheckRedirect
	r2, err := admin.Get(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	cb, _ := url.Parse(r2.Header.Get("Location"))
	if r2.StatusCode != 302 || cb.Host != "family.t.example.com" || cb.Path != "/__nyatunnel/auth" {
		t.Fatalf("console hand-off: %d %s", r2.StatusCode, cb)
	}
	if resp, _ := v.do("GET", "/__nyatunnel/auth?token=forged&r=/photos", nil, true, nil); resp.StatusCode != 403 {
		t.Fatalf("forged token: %d", resp.StatusCode)
	}
	if resp, _ := v.do("GET", cb.RequestURI(), nil, true, nil); resp.StatusCode != 303 || resp.Header.Get("Location") != "/photos" {
		t.Fatalf("callback: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if _, body := v.do("GET", "/photos", nil, true, nil); body != "backend ok" {
		t.Fatalf("after login: %s", body)
	}
	// The console refuses to hand tokens to other hosts.
	bad := p.console.URL + "/tunnel-login?" + url.Values{"t": {cb.Query().Get("t")}, "cb": {"https://evil.example/__nyatunnel/auth"}}.Encode()
	if r3, _ := admin.Get(bad); r3.StatusCode != 400 {
		t.Fatalf("foreign callback: %d", r3.StatusCode)
	}
}

func TestLimitsAndQuota(t *testing.T) {
	p := newPolicyEnv(t)
	p.tunnel("", "limited", map[string]any{"bandwidthKbps": 1600, "monthlyQuotaMb": 1})
	v := p.visitor("limited.t.example.com")
	start := time.Now()
	resp, body := v.do("GET", "/?bytes=450000", nil, false, nil)
	if resp.StatusCode != 200 || len(body) != 450000 {
		t.Fatalf("download: %d %d", resp.StatusCode, len(body))
	}
	if took := time.Since(start); took < 1200*time.Millisecond {
		t.Fatalf("1600 kbps limit not applied: 450 kB took %v", took)
	}
	v.do("GET", "/?bytes=700000", nil, false, nil) // now over 1 MB this month
	p.edge.Flush(context.Background())
	if resp, body := v.do("GET", "/", nil, false, nil); resp.StatusCode != 503 || !strings.Contains(body, "流量已用完") {
		t.Fatalf("over quota: %d", resp.StatusCode)
	}
	var list struct{ Tunnels []tunnelView }
	p.admin.must("GET", "/api/v1/tunnels", nil, &list)
	if list.Tunnels[0].State != "over_quota" || list.Tunnels[0].MonthBytes < 1<<20 {
		t.Fatalf("view: %+v", list.Tunnels[0])
	}
	var series struct{ Series []struct{ BytesOut int64 } }
	p.admin.must("GET", "/api/v1/traffic?tunnelId="+list.Tunnels[0].ID, nil, &series)
	if len(series.Series) != 1 || series.Series[0].BytesOut < 1<<20 {
		t.Fatalf("series %+v", series)
	}
	var audit struct{ Events []auditView }
	p.admin.must("GET", "/api/v1/audit", nil, &audit)
	if audit.Events[0].Action != "tunnel.quota_paused" {
		t.Fatalf("latest audit %+v", audit.Events[0])
	}
}

func TestMaxConns(t *testing.T) {
	p := newPolicyEnv(t)
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
	port := freePort(t)
	p.admin.must("POST", "/api/v1/port-pools", map[string]any{"proto": "tcp", "start": port, "end": port}, nil)
	var out struct{ Tunnel tunnelView }
	p.admin.must("POST", "/api/v1/tunnels", map[string]any{"userId": p.adminID, "deviceId": p.deviceID, "name": "one", "type": "tcp",
		"localIp": "127.0.0.1", "localPort": echo.Addr().(*net.TCPAddr).Port, "enabled": true, "maxConns": 1}, &out)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	roundTrip := func(c net.Conn) error {
		c.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := c.Write([]byte("x")); err != nil {
			return err
		}
		_, err := io.ReadFull(c, make([]byte, 1))
		return err
	}
	first, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := roundTrip(first); err != nil {
		t.Fatalf("first connection: %v", err)
	}
	second, _ := net.Dial("tcp", addr)
	if err := roundTrip(second); err == nil {
		t.Fatal("second connection served beyond maxConns=1")
	}
	second.Close()
	first.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		third, _ := net.Dial("tcp", addr)
		err := roundTrip(third)
		third.Close()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("slot not released: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestAbuseReport(t *testing.T) {
	p := newPolicyEnv(t)
	p.tunnel("", "shady", map[string]any{"interstitial": true})
	v := p.visitor("shady.t.example.com")
	if _, body := v.do("GET", "/", nil, true, nil); !strings.Contains(body, "/__nyatunnel/report") {
		t.Fatal("warning page has no report link")
	}
	if resp, body := v.do("GET", "/__nyatunnel/report", nil, true, nil); resp.StatusCode != 200 || !strings.Contains(body, "举报此页面") {
		t.Fatalf("report form: %d", resp.StatusCode)
	}
	resp, _ := v.do("POST", "/__nyatunnel/report", url.Values{"kind": {"phishing"}, "detail": {"looks like a bank"}}, true, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("report: %d", resp.StatusCode)
	}
	var audit struct{ Events []auditView }
	p.admin.must("GET", "/api/v1/audit", nil, &audit)
	if audit.Events[0].Action != "tunnel.reported" || !strings.Contains(audit.Events[0].Detail, "looks like a bank") || audit.Events[0].IP != "198.51.100.1" {
		t.Fatalf("audit %+v", audit.Events[0])
	}
	for i := 0; i < 10; i++ {
		resp, _ = v.do("POST", "/__nyatunnel/report", url.Values{"kind": {"other"}}, true, nil)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("report flood: %d", resp.StatusCode)
	}
}
