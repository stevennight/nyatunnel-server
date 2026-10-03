package edge

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"nyatunnel-server/internal/server/auth"
)

// Paths under internalPrefix are answered by the edge itself and never reach the device.
const internalPrefix = "/__nyatunnel/"

const (
	gateCookie = "__nyatunnel_gate"
	ackCookie  = "__nyatunnel_ack"
	gateTTL    = 7 * 24 * time.Hour
	ackTTL     = 30 * 24 * time.Hour
)

// gate enforces HTTPS access policies (docs/设计方案.md §6.2).
type gate struct {
	e *Edge

	mu    sync.Mutex
	fails map[string][]time.Time // ip|tunnel -> failed password attempts
	basic map[string]time.Time   // verified basic credentials digest -> valid until
}

func newGate(e *Edge) *gate {
	return &gate{e: e, fails: map[string][]time.Time{}, basic: map[string]time.Time{}}
}

// sign makes "<payload>.<mac>" with the gate key.
func (g *gate) sign(payload string) string {
	m := hmac.New(sha256.New, g.e.opt.GateKey)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// verify returns the payload of a valid signed value.
func (g *gate) verify(v string) (string, bool) {
	p, mac, ok := strings.Cut(v, ".")
	if !ok {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return "", false
	}
	want, err := base64.RawURLEncoding.DecodeString(mac)
	if err != nil {
		return "", false
	}
	m := hmac.New(sha256.New, g.e.opt.GateKey)
	m.Write(payload)
	if !hmac.Equal(m.Sum(nil), want) {
		return "", false
	}
	return string(payload), true
}

// cookieValue binds a cookie to the tunnel, its policy revision (changing the password logs
// everyone out) and an expiry.
func (g *gate) cookieValue(kind string, r *route, ttl time.Duration) string {
	exp := g.e.opt.Now().Add(ttl).Unix()
	return g.sign(fmt.Sprintf("%s|%s|%d|%d", kind, r.TunnelID, r.PolicyRev, exp))
}

func (g *gate) hasCookie(req *http.Request, name, kind string, r *route) bool {
	c, err := req.Cookie(name)
	if err != nil {
		return false
	}
	payload, ok := g.verify(c.Value)
	if !ok {
		return false
	}
	parts := strings.Split(payload, "|")
	if len(parts) != 4 || parts[0] != kind || parts[1] != r.TunnelID || parts[2] != strconv.FormatInt(r.PolicyRev, 10) {
		return false
	}
	exp, err := strconv.ParseInt(parts[3], 10, 64)
	return err == nil && g.e.opt.Now().Unix() < exp
}

func (g *gate) setCookie(w http.ResponseWriter, req *http.Request, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", MaxAge: int(ttl.Seconds()), HttpOnly: true,
		Secure: g.e.opt.RealIP.Proto(req) == "https", SameSite: http.SameSiteLaxMode, // host-only: no Domain attribute
	})
}

// navigation reports whether the request is a browser page load (where a gate page makes sense).
func navigation(req *http.Request) bool {
	return req.Method == http.MethodGet && strings.Contains(req.Header.Get("Accept"), "text/html")
}

// safeReturn keeps redirects on the same host.
func safeReturn(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") || strings.HasPrefix(p, internalPrefix) {
		return "/"
	}
	return p
}

// check applies the access policy. It returns true when the request may be forwarded; otherwise it
// has written the response.
func (g *gate) check(w http.ResponseWriter, req *http.Request, r *route, clientIP string) bool {
	if strings.HasPrefix(req.URL.Path, internalPrefix) {
		g.internal(w, req, r, clientIP)
		return false
	}
	switch r.Policy {
	case "password":
		if !g.hasCookie(req, gateCookie, "gate", r) {
			if navigation(req) {
				g.passwordPage(w, req, r, "", http.StatusUnauthorized)
			} else {
				plain(w, http.StatusUnauthorized, "access password required")
			}
			return false
		}
	case "basic":
		if !g.basicOK(req, r) {
			w.Header().Set("WWW-Authenticate", `Basic realm="`+r.Name+`", charset="UTF-8"`)
			plain(w, http.StatusUnauthorized, "authentication required")
			return false
		}
		req.Header.Del("Authorization") // the gate's credentials are not the application's business
	case "login":
		if !g.hasCookie(req, gateCookie, "gate", r) {
			if navigation(req) && g.e.opt.ConsoleURL != "" {
				ret := (&url.URL{Scheme: g.e.opt.RealIP.Proto(req), Host: req.Host, Path: "/__nyatunnel/auth"}).String()
				q := url.Values{"t": {r.TunnelID}, "r": {safeReturn(req.URL.RequestURI())}, "cb": {ret}}
				http.Redirect(w, req, g.e.opt.ConsoleURL+"/tunnel-login?"+q.Encode(), http.StatusFound)
			} else {
				plain(w, http.StatusUnauthorized, "sign-in required")
			}
			return false
		}
	}
	if r.Interstitial && navigation(req) && !g.hasCookie(req, ackCookie, "ack", r) {
		g.interstitialPage(w, req, r)
		return false
	}
	return true
}

func (g *gate) basicOK(req *http.Request, r *route) bool {
	user, pass, ok := req.BasicAuth()
	if !ok || r.PasswordHash == "" || subtle.ConstantTimeCompare([]byte(user), []byte(r.BasicUser)) != 1 {
		return false
	}
	// Password hashing is deliberately slow; remember good credentials so every request is not.
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s|%s", r.TunnelID, r.PolicyRev, user, pass)))
	key := string(sum[:])
	now := g.e.opt.Now()
	g.mu.Lock()
	until, cached := g.basic[key]
	g.mu.Unlock()
	if cached && now.Before(until) {
		return true
	}
	if !auth.VerifyPassword(r.PasswordHash, pass) {
		return false
	}
	g.mu.Lock()
	if len(g.basic) > 10_000 {
		g.basic = map[string]time.Time{}
	}
	g.basic[key] = now.Add(10 * time.Minute)
	g.mu.Unlock()
	return true
}

const maxGateFailures = 10

func (g *gate) blocked(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	cut := g.e.opt.Now().Add(-10 * time.Minute)
	kept := g.fails[key][:0]
	for _, t := range g.fails[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	g.fails[key] = kept
	return len(kept) >= maxGateFailures
}

func (g *gate) fail(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.fails) > 10_000 {
		g.fails = map[string][]time.Time{}
	}
	g.fails[key] = append(g.fails[key], g.e.opt.Now())
}

// internal serves /__nyatunnel/*: the password form, the interstitial acknowledgement and the
// login-gate callback.
func (g *gate) internal(w http.ResponseWriter, req *http.Request, r *route, clientIP string) {
	switch req.URL.Path {
	case internalPrefix + "gate":
		if req.Method != http.MethodPost || r.Policy != "password" {
			http.NotFound(w, req)
			return
		}
		key := clientIP + "|" + r.TunnelID
		if g.blocked(key) {
			g.passwordPage(w, req, r, "尝试次数过多，请 10 分钟后再试。", http.StatusTooManyRequests)
			return
		}
		req.Body = http.MaxBytesReader(w, req.Body, 4096)
		if err := req.ParseForm(); err != nil || !auth.VerifyPassword(r.PasswordHash, req.PostForm.Get("password")) {
			g.fail(key)
			g.passwordPage(w, req, r, "密码不正确。", http.StatusUnauthorized)
			return
		}
		g.setCookie(w, req, gateCookie, g.cookieValue("gate", r, gateTTL), gateTTL)
		http.Redirect(w, req, safeReturn(req.PostForm.Get("r")), http.StatusSeeOther)
	case internalPrefix + "continue":
		if req.Method != http.MethodPost {
			http.NotFound(w, req)
			return
		}
		req.Body = http.MaxBytesReader(w, req.Body, 4096)
		_ = req.ParseForm()
		g.setCookie(w, req, ackCookie, g.cookieValue("ack", r, ackTTL), ackTTL)
		http.Redirect(w, req, safeReturn(req.PostForm.Get("r")), http.StatusSeeOther)
	case internalPrefix + "auth":
		if r.Policy != "login" || !g.loginTokenOK(req.URL.Query().Get("token"), r.TunnelID) {
			plain(w, http.StatusForbidden, "sign-in failed")
			return
		}
		g.setCookie(w, req, gateCookie, g.cookieValue("gate", r, gateTTL), gateTTL)
		http.Redirect(w, req, safeReturn(req.URL.Query().Get("r")), http.StatusSeeOther)
	default:
		http.NotFound(w, req)
	}
}

// loginTokenTTL is how long the console's hand-off token is valid; it only travels in one redirect.
const loginTokenTTL = time.Minute

// IssueLoginToken is called by the console after it signed the visitor in for a login-gated tunnel.
func (e *Edge) IssueLoginToken(tunnelID, userID string) string {
	return e.gate.sign(fmt.Sprintf("login|%s|%s|%d", tunnelID, userID, e.opt.Now().Add(loginTokenTTL).Unix()))
}

// LoginCallbackOK reports whether cb is the login-gate callback of the tunnel's own host, so the
// console never hands a token to another site.
func (e *Edge) LoginCallbackOK(tunnelID, cb string) bool {
	r := e.routeByID(tunnelID)
	u, err := url.Parse(cb)
	return r != nil && r.Policy == "login" && err == nil && strings.EqualFold(u.Hostname(), r.Host) &&
		u.Path == internalPrefix+"auth" && (u.Scheme == "https" || u.Scheme == "http")
}

func (g *gate) loginTokenOK(token, tunnelID string) bool {
	payload, ok := g.verify(token)
	if !ok {
		return false
	}
	parts := strings.Split(payload, "|")
	if len(parts) != 4 || parts[0] != "login" || parts[1] != tunnelID {
		return false
	}
	exp, err := strconv.ParseInt(parts[3], 10, 64)
	return err == nil && g.e.opt.Now().Unix() < exp
}

func plain(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	fmt.Fprintln(w, msg)
}

const pageStyle = `body{margin:0;min-height:100vh;display:grid;place-items:center;font-family:system-ui,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;background:#f6f7f9;color:#181c23}
main{max-width:30rem;padding:2rem 1.25rem;text-align:center}small,.hint{color:#667085;font-size:.85rem}
input{font:inherit;padding:.55rem .75rem;border:1px solid #d0d5dd;border-radius:8px;width:14rem;max-width:100%}
button{font:inherit;padding:.55rem 1.1rem;border:0;border-radius:8px;background:#6d5dfc;color:#fff;cursor:pointer}
.err{color:#d92d20}.warn{background:#fef4e6;border:1px solid #f79009;border-radius:10px;padding:.75rem 1rem;text-align:left;margin:1rem 0}
@media (prefers-color-scheme:dark){body{background:#0f1319;color:#e8ecf2}small,.hint{color:#98a2b3}input{background:#171a22;color:#e8ecf2;border-color:#2a2f3a}.warn{background:#33240c}}`

func page(w http.ResponseWriter, status int, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>%s</title><style>%s</style></head><body><main>%s</main></body></html>`,
		html.EscapeString(title), pageStyle, body)
}

func (g *gate) passwordPage(w http.ResponseWriter, req *http.Request, r *route, errMsg string, status int) {
	ret := req.URL.RequestURI()
	if req.Method == http.MethodPost {
		ret = req.PostForm.Get("r")
	}
	msg := ""
	if errMsg != "" {
		msg = `<p class="err">` + html.EscapeString(errMsg) + `</p>`
	}
	page(w, status, "需要访问密码", fmt.Sprintf(`<div style="font-size:2.2rem">🔒</div><h2>此页面受访问密码保护</h2><p class="hint">%s · 由 NyaTunnel 提供的内网穿透服务</p>%s
<form method="post" action="%sgate"><input type="hidden" name="r" value="%s"><input type="password" name="password" placeholder="访问密码" autofocus required> <button>进入</button></form>
<p class="hint" style="margin-top:2rem">如果你是被陌生链接引导到这里，并被要求输入账号密码或支付信息，请勿继续。</p>`,
		html.EscapeString(r.Host), msg, internalPrefix, html.EscapeString(safeReturn(ret))))
}

func (g *gate) interstitialPage(w http.ResponseWriter, req *http.Request, r *route) {
	page(w, http.StatusOK, "即将访问内网穿透地址", fmt.Sprintf(`<div style="font-size:2.2rem">⚠️</div><h2>你即将访问一个内网穿透地址</h2>
<p><b>%s</b></p><div class="warn">这个网站运行在某人自己的电脑或服务器上，并通过 NyaTunnel 暴露到公网。它不是任何银行、支付平台或大型网站的官方页面。<br><br>如果它要求你输入其他网站的账号密码、验证码或支付信息，请立即关闭。</div>
<form method="post" action="%scontinue"><input type="hidden" name="r" value="%s"><button>我了解，继续访问</button></form>`,
		html.EscapeString(r.Host), internalPrefix, html.EscapeString(safeReturn(req.URL.RequestURI()))))
}
