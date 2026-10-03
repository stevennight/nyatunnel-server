// Package edge is where visitors arrive: the HTTP tunnel ingress (behind the reverse proxy), the
// TCP/UDP port listeners and the reverse proxy's on-demand TLS question. Every visitor connection
// becomes a data stream on the owning device's session (docs/设计方案.md §6.1), passing through the
// tunnel's access policy, connection limit, bandwidth limit and traffic meter on the way.
package edge

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
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
	// GateKey signs the access cookies of password, basic and login gates.
	GateKey []byte
	// ConsoleURL is where the login gate sends visitors to sign in.
	ConsoleURL string
	// Audit records events (quota alerts); may be nil.
	Audit func(ctx context.Context, e store.AuditEvent)
	// Notify sends an operational notification (quota, traffic surge); may be nil.
	Notify func(kind, title, text string)
	// SurgeMBPerHour returns the hourly traffic per tunnel above which a surge is reported (0 = off).
	SurgeMBPerHour func(ctx context.Context) int
	Now            func() time.Time
	// Listen opens TCP listeners; tests replace it.
	Listen func(network, addr string) (net.Listener, error)
	// ListenPacket opens UDP sockets; tests replace it.
	ListenPacket func(network, addr string) (net.PacketConn, error)
}

// route is the routing view of a tunnel.
type route struct {
	TunnelID  string
	UserID    string
	DeviceID  string
	Name      string
	Type      string
	Host      string
	Port      int
	Enabled   bool
	Paused    bool
	OverQuota bool
	ExpiresAt *int64

	Policy       string
	PasswordHash string
	BasicUser    string
	PolicyRev    int64
	Allow        []netip.Prefix
	Interstitial bool
	HostRewrite  string

	BandwidthKbps int
	MaxConns      int
	QuotaMB       int
	QuotaAction   string
}

func (r *route) live(now time.Time) bool {
	return r.DeviceID != "" && r.Enabled && !r.Paused && !r.OverQuota && (r.ExpiresAt == nil || now.UnixMilli() < *r.ExpiresAt)
}

// allowed applies the IP allowlist (empty = everyone).
func (r *route) allowed(ip string) bool {
	if len(r.Allow) == 0 {
		return true
	}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, p := range r.Allow {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// ParseAllowlist parses "10.0.0.0/8, 203.0.113.4" into prefixes.
func ParseAllowlist(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == ';' }) {
		if p, err := netip.ParsePrefix(f); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(f)
		if err != nil {
			return nil, err
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// Edge routes visitors to tunnels.
type Edge struct {
	opt Options

	mu     sync.RWMutex
	all    map[string]*route // by tunnel id
	byHost map[string]*route
	tcp    map[int]*portListener
	udp    map[int]*udpListener

	metersMu sync.Mutex
	meters   map[string]*meter

	quotaMu    sync.Mutex
	overQuota  map[string]bool
	alerted    map[string]int64 // tunnel or "user:<id>" -> month already reported
	surged     map[string]int64 // tunnel -> hour already reported
	userQuotas map[string]int   // user -> monthly MB (0 = unlimited)

	gate  *gate
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
	if opt.Audit == nil {
		opt.Audit = func(context.Context, store.AuditEvent) {}
	}
	if opt.Notify == nil {
		opt.Notify = func(string, string, string) {}
	}
	if opt.SurgeMBPerHour == nil {
		opt.SurgeMBPerHour = func(context.Context) int { return 0 }
	}
	e := &Edge{
		opt: opt, all: map[string]*route{}, byHost: map[string]*route{}, tcp: map[int]*portListener{}, udp: map[int]*udpListener{},
		meters: map[string]*meter{}, overQuota: map[string]bool{}, alerted: map[string]int64{}, surged: map[string]int64{}, userQuotas: map[string]int{},
	}
	e.gate = newGate(e)
	e.proxy = newHTTPProxy(e)
	return e
}

func (e *Edge) routeFor(t *store.Tunnel) *route {
	r := &route{
		TunnelID: t.ID, UserID: t.UserID, Name: t.Name, Type: t.Type, Enabled: t.Enabled, Paused: t.PausedByClient, ExpiresAt: t.ExpiresAt,
		Policy: t.AccessPolicy, PasswordHash: t.AccessPasswordHash, BasicUser: t.BasicUsername, PolicyRev: t.PolicyRev,
		Interstitial: t.Interstitial, HostRewrite: t.HostRewrite,
		BandwidthKbps: t.BandwidthKbps, MaxConns: t.MaxConns, QuotaMB: t.MonthlyQuotaMB, QuotaAction: t.QuotaAction,
	}
	if t.DeviceID != nil {
		r.DeviceID = *t.DeviceID
	}
	if allow, err := ParseAllowlist(t.IPAllowlist); err == nil {
		r.Allow = allow
	} else {
		// A broken list must not open the tunnel to everyone.
		r.Allow = []netip.Prefix{netip.MustParsePrefix("255.255.255.255/32")}
	}
	r.OverQuota = e.OverQuota(t.ID)
	return r
}

// Reload rebuilds the routing table from the database and opens/closes port listeners to match.
func (e *Edge) Reload(ctx context.Context) error {
	tunnels, err := e.opt.Store.AllTunnels(ctx)
	if err != nil {
		return err
	}
	domains, err := e.opt.Store.Domains(ctx)
	if err != nil {
		return err
	}
	quotas, err := e.opt.Store.UserQuotas(ctx)
	if err != nil {
		return err
	}
	e.quotaMu.Lock()
	e.userQuotas = map[string]int{}
	for id, q := range quotas {
		e.userQuotas[id] = q.MonthlyTrafficMB
	}
	e.quotaMu.Unlock()
	// Custom domains only route once they are active; root domains always do.
	routable := map[string]bool{}
	for _, d := range domains {
		routable[d.ID] = d.Kind != store.DomainCustom || d.Status == store.DomainActive
	}
	now := e.opt.Now()
	all := map[string]*route{}
	byHost := map[string]*route{}
	wantTCP := map[int]*route{}
	wantUDP := map[int]*route{}
	for _, t := range tunnels {
		r := e.routeFor(t)
		all[r.TunnelID] = r
		e.meter(r.TunnelID).setBandwidth(r.BandwidthKbps)
		switch {
		case t.Host != nil:
			r.Host = strings.ToLower(*t.Host)
			if t.DomainID == nil || routable[*t.DomainID] {
				byHost[r.Host] = r
			}
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
	e.all, e.byHost = all, byHost
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

// Run flushes traffic every minute and reloads periodically, so expiring tunnels close their ports
// without an explicit change.
func (e *Edge) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	tick := 0
	for {
		select {
		case <-ctx.Done():
			e.flush(context.Background())
			return
		case <-t.C:
		}
		tick++
		if tick%2 == 0 {
			e.flush(ctx)
		}
		if err := e.Reload(ctx); err != nil {
			e.opt.Log.Warn("edge: periodic reload", "err", err)
		}
	}
}

// Flush writes pending traffic now (tests and shutdown).
func (e *Edge) Flush(ctx context.Context) { e.flush(ctx) }

func (e *Edge) routeByHost(host string) *route {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.byHost[strings.ToLower(host)]
}

func (e *Edge) routeByID(id string) *route {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.all[id]
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
