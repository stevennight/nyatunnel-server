// Package edge is where visitors arrive: the HTTP tunnel ingress (behind the reverse proxy), the
// TCP/UDP port listeners and the reverse proxy's on-demand TLS question. Every visitor connection
// becomes a data stream on the owning device's session (docs/设计方案.md §6.1).
package edge

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"nyatunnel-server/internal/server/realip"
	"nyatunnel-server/internal/server/store"
)

// Dialer opens a data stream to a device (hub.Hub).
type Dialer interface {
	Dial(ctx context.Context, deviceID, tunnelID, proto, remoteAddr string) (net.Conn, error)
}

// Options configures an Edge.
type Options struct {
	Store  *store.Store
	Dialer Dialer
	Log    *slog.Logger
	RealIP *realip.Resolver
	// BindAddr is the address TCP/UDP tunnel ports listen on.
	BindAddr string
	Now      func() time.Time
	// Listen opens TCP listeners; tests replace it.
	Listen func(network, addr string) (net.Listener, error)
	// ListenPacket opens UDP sockets; tests replace it.
	ListenPacket func(network, addr string) (net.PacketConn, error)
}

// route is the routing view of a tunnel.
type route struct {
	TunnelID  string
	DeviceID  string
	Type      string
	Host      string
	Port      int
	Enabled   bool
	Paused    bool
	ExpiresAt *int64
}

func (r *route) live(now time.Time) bool {
	return r.DeviceID != "" && r.Enabled && !r.Paused && (r.ExpiresAt == nil || now.UnixMilli() < *r.ExpiresAt)
}

// Edge routes visitors to tunnels.
type Edge struct {
	opt Options

	mu     sync.RWMutex
	byHost map[string]*route
	tcp    map[int]*portListener
	udp    map[int]*udpListener

	proxy *httpProxy
}

// New returns an Edge with an empty routing table; call Reload.
func New(opt Options) *Edge {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Listen == nil {
		opt.Listen = net.Listen
	}
	if opt.ListenPacket == nil {
		opt.ListenPacket = net.ListenPacket
	}
	e := &Edge{opt: opt, byHost: map[string]*route{}, tcp: map[int]*portListener{}, udp: map[int]*udpListener{}}
	e.proxy = newHTTPProxy(e)
	return e
}

// Reload rebuilds the routing table from the database and opens/closes port listeners to match.
func (e *Edge) Reload(ctx context.Context) error {
	tunnels, err := e.opt.Store.AllTunnels(ctx)
	if err != nil {
		return err
	}
	now := e.opt.Now()
	byHost := map[string]*route{}
	wantTCP := map[int]*route{}
	wantUDP := map[int]*route{}
	for _, t := range tunnels {
		r := &route{TunnelID: t.ID, Type: t.Type, Enabled: t.Enabled, Paused: t.PausedByClient, ExpiresAt: t.ExpiresAt}
		if t.DeviceID != nil {
			r.DeviceID = *t.DeviceID
		}
		switch {
		case t.Host != nil:
			r.Host = strings.ToLower(*t.Host)
			byHost[r.Host] = r
		case t.RemotePort != nil && r.live(now):
			// Ports of tunnels that cannot carry traffic stay closed, so scanners see nothing.
			r.Port = *t.RemotePort
			if t.Type == store.TypeUDP {
				wantUDP[r.Port] = r
			} else {
				wantTCP[r.Port] = r
			}
		}
	}

	e.mu.Lock()
	e.byHost = byHost
	var closeTCP []*portListener
	for port, l := range e.tcp {
		if r, ok := wantTCP[port]; ok {
			l.route.Store(r)
			continue
		}
		closeTCP = append(closeTCP, l)
		delete(e.tcp, port)
	}
	var closeUDP []*udpListener
	for port, l := range e.udp {
		if r, ok := wantUDP[port]; ok {
			l.route.Store(r)
			continue
		}
		closeUDP = append(closeUDP, l)
		delete(e.udp, port)
	}
	for port, r := range wantTCP {
		if _, ok := e.tcp[port]; ok {
			continue
		}
		l, err := e.listenTCP(port, r)
		if err != nil {
			e.opt.Log.Error("edge: cannot listen", "proto", "tcp", "port", port, "tunnel", r.TunnelID, "err", err)
			continue
		}
		e.tcp[port] = l
	}
	for port, r := range wantUDP {
		if _, ok := e.udp[port]; ok {
			continue
		}
		l, err := e.listenUDP(port, r)
		if err != nil {
			e.opt.Log.Error("edge: cannot listen", "proto", "udp", "port", port, "tunnel", r.TunnelID, "err", err)
			continue
		}
		e.udp[port] = l
	}
	e.mu.Unlock()

	for _, l := range closeTCP {
		l.close()
	}
	for _, l := range closeUDP {
		l.close()
	}
	// Pooled HTTP connections may point at an old local target or a replaced session.
	e.proxy.transport.CloseIdleConnections()
	return nil
}

// Run reloads periodically so expiring tunnels close their ports without an explicit change.
func (e *Edge) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := e.Reload(ctx); err != nil {
				e.opt.Log.Warn("edge: periodic reload", "err", err)
			}
		}
	}
}

func (e *Edge) routeByHost(host string) *route {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.byHost[strings.ToLower(host)]
}

// AllowCertificate answers the reverse proxy's on-demand TLS question: is this a tunnel host?
func (e *Edge) AllowCertificate(host string) bool {
	r := e.routeByHost(strings.TrimSuffix(host, "."))
	return r != nil && r.Enabled
}

// ListenerPorts returns the ports currently open, for the dashboard.
func (e *Edge) ListenerPorts() (tcp, udp []int) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for p := range e.tcp {
		tcp = append(tcp, p)
	}
	for p := range e.udp {
		udp = append(udp, p)
	}
	return tcp, udp
}

// Close stops every listener.
func (e *Edge) Close() {
	e.mu.Lock()
	tcp, udp := e.tcp, e.udp
	e.tcp, e.udp = map[int]*portListener{}, map[int]*udpListener{}
	e.mu.Unlock()
	for _, l := range tcp {
		l.close()
	}
	for _, l := range udp {
		l.close()
	}
}
