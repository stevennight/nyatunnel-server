package edge

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"nyatunnel-server/internal/server/store"
)

// meter counts and limits the traffic of one tunnel. It survives routing reloads.
type meter struct {
	in, out, conns atomic.Int64 // since the last flush
	active         atomic.Int64 // open visitor connections

	mu       sync.Mutex
	kbps     int
	limitIn  *rate.Limiter // visitor -> device
	limitOut *rate.Limiter // device -> visitor
}

func (m *meter) setBandwidth(kbps int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if kbps == m.kbps {
		return
	}
	m.kbps = kbps
	if kbps <= 0 {
		m.limitIn, m.limitOut = nil, nil
		return
	}
	bps := float64(kbps) * 1000 / 8
	burst := max(int(bps/4), 32<<10) // a quarter second, at least 32 KiB
	m.limitIn = rate.NewLimiter(rate.Limit(bps), burst)
	m.limitOut = rate.NewLimiter(rate.Limit(bps), burst)
}

func (m *meter) limiters() (in, out *rate.Limiter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.limitIn, m.limitOut
}

func (e *Edge) meter(tunnelID string) *meter {
	e.metersMu.Lock()
	defer e.metersMu.Unlock()
	m := e.meters[tunnelID]
	if m == nil {
		m = &meter{}
		e.meters[tunnelID] = m
	}
	return m
}

// acquire reserves a connection slot; false when the tunnel is at its connection limit.
func (e *Edge) acquire(r *route) (*meter, bool) {
	m := e.meter(r.TunnelID)
	n := m.active.Add(1)
	if r.MaxConns > 0 && n > int64(r.MaxConns) {
		m.active.Add(-1)
		return nil, false
	}
	m.conns.Add(1)
	return m, true
}

// meteredConn wraps the device stream: writes are visitor -> device, reads device -> visitor.
type meteredConn struct {
	net.Conn
	m      *meter
	closed atomic.Bool
}

func (e *Edge) metered(c net.Conn, m *meter) net.Conn { return &meteredConn{Conn: c, m: m} }

func wait(l *rate.Limiter, n int) {
	if l == nil {
		return
	}
	for n > 0 {
		chunk := min(n, l.Burst())
		_ = l.WaitN(context.Background(), chunk)
		n -= chunk
	}
}

func (c *meteredConn) Write(p []byte) (int, error) {
	in, _ := c.m.limiters()
	wait(in, len(p))
	n, err := c.Conn.Write(p)
	c.m.in.Add(int64(n))
	return n, err
}

func (c *meteredConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.m.out.Add(int64(n))
	_, out := c.m.limiters()
	wait(out, n)
	return n, err
}

func (c *meteredConn) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		c.m.active.Add(-1)
	}
	return c.Conn.Close()
}

// CloseWrite half-closes when the stream supports it (yamux streams half-close on Close).
func (c *meteredConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return c.Conn.Close()
}

func hourStart(t time.Time) int64 { return t.UTC().Truncate(time.Hour).UnixMilli() }

// MonthStart is the first millisecond of t's month (UTC), where monthly quotas reset.
func MonthStart(t time.Time) int64 {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC).UnixMilli()
}

// flush writes metered traffic to the database and re-evaluates monthly quotas.
func (e *Edge) flush(ctx context.Context) {
	now := e.opt.Now()
	owners := e.owners()
	var deltas []store.TrafficDelta
	e.metersMu.Lock()
	for id, m := range e.meters {
		d := store.TrafficDelta{TunnelID: id, UserID: owners[id], BucketHour: hourStart(now), BytesIn: m.in.Swap(0), BytesOut: m.out.Swap(0), Conns: m.conns.Swap(0)}
		if d.BytesIn+d.BytesOut+d.Conns > 0 && d.UserID != "" {
			deltas = append(deltas, d)
		}
	}
	e.metersMu.Unlock()
	if err := e.opt.Store.AddTraffic(ctx, deltas); err != nil {
		e.opt.Log.Warn("edge: store traffic", "err", err)
		return
	}
	e.checkQuotas(ctx, now)
	e.checkSurge(ctx, now)
}

// checkSurge reports tunnels whose traffic this hour passed the configured threshold, once per hour.
func (e *Edge) checkSurge(ctx context.Context, now time.Time) {
	limit := e.opt.SurgeMBPerHour(ctx)
	if limit <= 0 {
		return
	}
	hour := hourStart(now)
	usage, err := e.opt.Store.TrafficByTunnel(ctx, hour)
	if err != nil {
		return
	}
	for id, u := range usage {
		if u.Bytes() < int64(limit)<<20 {
			continue
		}
		e.quotaMu.Lock()
		seen := e.surged[id] == hour
		e.surged[id] = hour
		e.quotaMu.Unlock()
		if seen {
			continue
		}
		r := e.routeByID(id)
		name := id
		if r != nil {
			name = r.Name
		}
		detail := fmt.Sprintf("%s 本小时已产生 %.1f MB 流量（阈值 %d MB）", name, float64(u.Bytes())/(1<<20), limit)
		e.opt.Audit(ctx, store.AuditEvent{ActorType: "system", Action: "tunnel.traffic_surge", Target: id, Detail: detail})
		e.opt.Notify("traffic.surge", "流量突增："+name, detail)
	}
}

func (e *Edge) owners() map[string]string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make(map[string]string, len(e.all))
	for id, r := range e.all {
		out[id] = r.UserID
	}
	return out
}

// checkQuotas pauses (or alerts about) tunnels over their monthly traffic quota.
func (e *Edge) checkQuotas(ctx context.Context, now time.Time) {
	usage, err := e.opt.Store.TrafficByTunnel(ctx, MonthStart(now))
	if err != nil {
		e.opt.Log.Warn("edge: quota usage", "err", err)
		return
	}
	byUser, err := e.opt.Store.TrafficByUser(ctx, MonthStart(now))
	if err != nil {
		e.opt.Log.Warn("edge: user quota usage", "err", err)
		return
	}
	month := MonthStart(now)
	e.quotaMu.Lock()
	overUsers := map[string]bool{}
	for id, mb := range e.userQuotas {
		if mb > 0 && byUser[id].Bytes() >= int64(mb)<<20 {
			overUsers[id] = true
		}
	}
	var userAlerts []string
	for id := range overUsers {
		if e.alerted["user:"+id] != month {
			e.alerted["user:"+id] = month
			userAlerts = append(userAlerts, id)
		}
	}
	e.quotaMu.Unlock()
	for _, id := range userAlerts {
		e.opt.Audit(ctx, store.AuditEvent{ActorType: "system", Action: "user.quota_paused", Target: id, Detail: "account monthly traffic quota used up"})
		e.opt.Notify("quota.exceeded", "账号流量已用完", "用户 "+id+" 本月流量配额已用完，其所有隧道已暂停。")
	}

	e.mu.RLock()
	over := map[string]bool{}
	var alerts []*route
	for id, r := range e.all {
		if overUsers[r.UserID] {
			over[id] = true
			continue
		}
		if r.QuotaMB <= 0 || usage[id].Bytes() < int64(r.QuotaMB)<<20 {
			continue
		}
		if r.QuotaAction == "alert" {
			alerts = append(alerts, r)
		} else {
			over[id] = true
		}
	}
	e.mu.RUnlock()

	e.quotaMu.Lock()
	changed := len(over) != len(e.overQuota)
	for id := range over {
		changed = changed || !e.overQuota[id]
	}
	e.overQuota = over
	var notify []*route
	for _, r := range alerts {
		if e.alerted[r.TunnelID] != month {
			e.alerted[r.TunnelID] = month
			notify = append(notify, r)
		}
	}
	for id := range over {
		if r := e.routeByID(id); r != nil && !overUsers[r.UserID] && e.alerted[id] != month {
			e.alerted[id] = month
			notify = append(notify, r)
		}
	}
	e.quotaMu.Unlock()

	for _, r := range notify {
		if r == nil {
			continue
		}
		action, text := "tunnel.quota_exceeded", "隧道 "+r.Name+" 本月流量已超过配额。"
		if over[r.TunnelID] {
			action, text = "tunnel.quota_paused", "隧道 "+r.Name+" 本月流量配额已用完，已自动暂停。"
		}
		e.opt.Audit(ctx, store.AuditEvent{ActorType: "system", Action: action, Target: r.TunnelID, Detail: r.Name})
		e.opt.Notify("quota.exceeded", "流量配额："+r.Name, text)
	}
	if changed {
		if err := e.Reload(ctx); err != nil {
			e.opt.Log.Warn("edge: reload after quota change", "err", err)
		}
	}
}

// OverQuota reports whether a tunnel is paused for exceeding its monthly quota.
func (e *Edge) OverQuota(tunnelID string) bool {
	e.quotaMu.Lock()
	defer e.quotaMu.Unlock()
	return e.overQuota[tunnelID]
}

// PendingTraffic returns metered bytes not yet flushed, so usage displays are current.
func (e *Edge) PendingTraffic(tunnelID string) int64 {
	e.metersMu.Lock()
	defer e.metersMu.Unlock()
	if m := e.meters[tunnelID]; m != nil {
		return m.in.Load() + m.out.Load()
	}
	return 0
}

// Active returns the number of open visitor connections of a tunnel.
func (e *Edge) Active(tunnelID string) int64 {
	e.metersMu.Lock()
	defer e.metersMu.Unlock()
	if m := e.meters[tunnelID]; m != nil {
		return m.active.Load()
	}
	return 0
}
