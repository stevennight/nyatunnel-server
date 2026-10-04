package edge

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/stevennight/nyatunnel-common/tunnelproto"

	"nyatunnel-server/internal/server/hub"
)

var errTooManyConns = errors.New("edge: connection limit reached")

type ctxKey int

const (
	routeKey ctxKey = iota
	clientKey
)

// httpProxy forwards HTTPS tunnel requests to devices. Its transport dials "<tunnel id>:80", which
// the dialer turns into a data stream; keep-alive connections are pooled per tunnel.
type httpProxy struct {
	e         *Edge
	transport *http.Transport
	rp        *httputil.ReverseProxy
}

func newHTTPProxy(e *Edge) *httpProxy {
	p := &httpProxy{e: e}
	p.transport = &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			r, _ := ctx.Value(routeKey).(*route)
			client, _ := ctx.Value(clientKey).(string)
			if r == nil {
				return nil, errors.New("edge: no route in context")
			}
			m, ok := e.acquire(r)
			if !ok {
				return nil, errTooManyConns
			}
			c, err := e.opt.Dialer.Dial(ctx, r.DeviceID, r.TunnelID, "tcp", client)
			if err != nil {
				m.active.Add(-1)
				return nil, err
			}
			return e.metered(c, m), nil
		},
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       60 * time.Second,
		ResponseHeaderTimeout: 0, // long polling and slow backends are the device's business
		ExpectContinueTimeout: time.Second,
	}
	p.rp = &httputil.ReverseProxy{
		Transport:     p.transport,
		FlushInterval: -1, // stream responses (SSE, downloads) as they come
		Rewrite: func(pr *httputil.ProxyRequest) {
			r := pr.In.Context().Value(routeKey).(*route)
			pr.SetURL(&url.URL{Scheme: "http", Host: r.TunnelID + ":80"})
			pr.Out.Host = pr.In.Host // the local service sees the public host name
			if r.HostRewrite != "" {
				pr.Out.Host = r.HostRewrite
			}
			client, _ := pr.In.Context().Value(clientKey).(string)
			pr.Out.Header.Set("X-Forwarded-For", client)
			pr.Out.Header.Set("X-Real-IP", client)
			pr.Out.Header.Set("X-Forwarded-Host", pr.In.Host)
			pr.Out.Header.Set("X-Forwarded-Proto", e.opt.RealIP.Proto(pr.In))
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			switch {
			case errors.Is(err, errTooManyConns):
				errorPage(w, http.StatusServiceUnavailable, "连接数已满", "这个隧道的并发连接已达上限，请稍后再试。")
			case errors.Is(err, hub.ErrOffline):
				errorPage(w, http.StatusBadGateway, "设备离线", "提供这个地址的设备目前不在线，请稍后再试。")
			case errors.Is(err, tunnelproto.ReplyDialFailed):
				errorPage(w, http.StatusBadGateway, "本地服务不可达", "设备在线，但它本地的服务没有响应。")
			case errors.Is(err, tunnelproto.ReplyUnconfirmed):
				errorPage(w, http.StatusServiceUnavailable, "等待设备确认", "这个隧道还没有在提供它的设备上确认，确认后即可访问。")
			case errors.Is(err, tunnelproto.ReplyInactive), errors.Is(err, tunnelproto.ReplyUnknownTunnel):
				errorPage(w, http.StatusServiceUnavailable, "隧道已暂停", "这个隧道目前没有启用。")
			case errors.Is(err, context.Canceled):
				// The visitor went away; nothing to answer.
			default:
				e.opt.Log.Debug("edge: proxy error", "host", req.Host, "err", err)
				errorPage(w, http.StatusBadGateway, "网关错误", "转发请求时出错，请稍后再试。")
			}
		},
	}
	return p
}

// ServeHTTP is the ingress listener's handler.
func (e *Edge) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	host := req.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	r := e.routeByHost(host)
	if r == nil {
		errorPage(w, http.StatusNotFound, "隧道不存在", "这个地址没有对应的隧道。")
		return
	}
	if !r.live(e.opt.Now()) {
		switch {
		case r.DeviceID == "":
			errorPage(w, http.StatusServiceUnavailable, "隧道未就绪", "这个隧道还没有分配设备。")
		case r.OverQuota:
			errorPage(w, http.StatusServiceUnavailable, "流量已用完", "这个隧道本月的流量配额已用完。")
		default:
			errorPage(w, http.StatusServiceUnavailable, "隧道已暂停", "这个隧道目前没有启用或已过期。")
		}
		return
	}
	client := e.opt.RealIP.ClientIP(req)
	if !r.allowed(client) {
		errorPage(w, http.StatusForbidden, "禁止访问", "你的网络地址不在这个隧道的访问白名单内。")
		return
	}
	if !e.gate.check(w, req, r, client) {
		return
	}
	ctx := context.WithValue(req.Context(), routeKey, r)
	ctx = context.WithValue(ctx, clientKey, client)
	e.proxy.rp.ServeHTTP(w, req.WithContext(ctx))
}

// errorPage renders a small self-contained page (it must not load anything from the tunnel's origin).
func errorPage(w http.ResponseWriter, status int, title, text string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-NyaTunnel-Error", fmt.Sprint(status))
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>%s</title>
<style>body{margin:0;min-height:100vh;display:grid;place-items:center;font-family:system-ui,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;background:#f6f7f9;color:#181c23}
main{max-width:32rem;padding:2rem;text-align:center}small{color:#667085}@media (prefers-color-scheme:dark){body{background:#0f1319;color:#e8ecf2}small{color:#98a2b3}}</style></head>
<body><main><h1>%s</h1><p>%s</p><small>NyaTunnel · %d</small></main></body></html>`,
		html.EscapeString(title), html.EscapeString(title), html.EscapeString(text), status)
}

// stripPort is used by the internal listener.
func stripPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return strings.TrimSuffix(host, ".")
}

// AskHandler answers Caddy's on_demand_tls ask: 200 only for tunnel hosts.
func (e *Edge) AskHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/caddy/ask" {
			http.NotFound(w, req)
			return
		}
		if e.AllowCertificate(stripPort(req.URL.Query().Get("domain"))) {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "not a tunnel host", http.StatusNotFound)
	})
}
