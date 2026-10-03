// Package hub keeps one authenticated session per connected device (docs/协议.md §4): it runs the
// handshake, pushes configuration snapshots on the control stream and opens data streams for
// visitors.
package hub

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/stevennight/nyatunnel-common/tunnelproto"

	"nyatunnel-server/internal/server/auth"
	"nyatunnel-server/internal/server/rules"
	"nyatunnel-server/internal/server/store"
)

const (
	handshakeTimeout = 10 * time.Second
	streamTimeout    = 15 * time.Second
	pingInterval     = 30 * time.Second
)

// Options configures a Hub.
type Options struct {
	Store *store.Store
	Log   *slog.Logger
	// PublicHost is the host devices sign for (config.PublicHost).
	PublicHost string
	// TCPHost is the host name shown for TCP/UDP tunnels (the public host without port).
	TCPHost string
	// OnChange is called after a device changed a tunnel through the control stream, so routing can reload.
	OnChange func()
	// Audit records an event; may be nil.
	Audit func(ctx context.Context, e store.AuditEvent)
	Now   func() time.Time
}

// Hub tracks device sessions.
type Hub struct {
	opt      Options
	mu       sync.Mutex
	sessions map[string]*Session
}

// New returns an empty hub.
func New(opt Options) *Hub {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.OnChange == nil {
		opt.OnChange = func() {}
	}
	if opt.Audit == nil {
		opt.Audit = func(context.Context, store.AuditEvent) {}
	}
	return &Hub{opt: opt, sessions: map[string]*Session{}}
}

// Session is a connected device.
type Session struct {
	ID            string
	DeviceID      string
	UserID        string
	DeviceName    string
	RemoteIP      string
	ClientVersion string
	ConnectedAt   time.Time

	ws      *websocket.Conn
	mux     *tunnelproto.Session
	control *tunnelproto.Control
	cancel  context.CancelFunc

	mu     sync.Mutex
	status map[string]tunnelproto.Status
}

// Info is a snapshot of a session for the API.
type Info struct {
	SessionID     string
	RemoteIP      string
	ClientVersion string
	ConnectedAt   time.Time
	Status        map[string]tunnelproto.Status
}

// Online returns the session info of a connected device.
func (h *Hub) Online(deviceID string) (Info, bool) {
	h.mu.Lock()
	s := h.sessions[deviceID]
	h.mu.Unlock()
	if s == nil {
		return Info{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := make(map[string]tunnelproto.Status, len(s.status))
	for k, v := range s.status {
		st[k] = v
	}
	return Info{SessionID: s.ID, RemoteIP: s.RemoteIP, ClientVersion: s.ClientVersion, ConnectedAt: s.ConnectedAt, Status: st}, true
}

// OnlineCount returns how many devices are connected.
func (h *Hub) OnlineCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.sessions)
}

// Kick closes a device's session with code (CloseUnauthorized when it was revoked).
func (h *Hub) Kick(deviceID string, code websocket.StatusCode, reason string) {
	h.mu.Lock()
	s := h.sessions[deviceID]
	delete(h.sessions, deviceID)
	h.mu.Unlock()
	if s != nil {
		s.close(code, reason)
	}
}

// close ends the session without blocking the caller (the close handshake can take seconds).
func (s *Session) close(code websocket.StatusCode, reason string) {
	go func() {
		// Close the WebSocket first so the device receives the close code, then tear down the multiplexer.
		_ = s.ws.Close(code, reason)
		s.cancel()
		if s.mux != nil {
			_ = s.mux.Close()
		}
	}()
}

// Push sends the current configuration to a device, if it is connected.
func (h *Hub) Push(ctx context.Context, deviceID string) {
	h.mu.Lock()
	s := h.sessions[deviceID]
	h.mu.Unlock()
	if s == nil || s.control == nil {
		return
	}
	cfg, err := h.BuildConfig(ctx, deviceID)
	if err != nil {
		h.opt.Log.Warn("hub: build config", "device", deviceID, "err", err)
		return
	}
	if err := s.control.Send(tunnelproto.TypeConfig, "", cfg); err != nil {
		h.opt.Log.Info("hub: push config failed", "device", deviceID, "err", err)
	}
}

// PushAll re-sends the configuration of every connected device.
func (h *Hub) PushAll(ctx context.Context) {
	h.mu.Lock()
	ids := make([]string, 0, len(h.sessions))
	for id := range h.sessions {
		ids = append(ids, id)
	}
	h.mu.Unlock()
	for _, id := range ids {
		h.Push(ctx, id)
	}
}

// BuildConfig renders a device's configuration snapshot.
func (h *Hub) BuildConfig(ctx context.Context, deviceID string) (*tunnelproto.Config, error) {
	d, err := h.opt.Store.DeviceByID(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	tunnels, err := h.opt.Store.DeviceTunnels(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	cfg := &tunnelproto.Config{Rev: d.ConfigRev, Tunnels: make([]tunnelproto.Tunnel, 0, len(tunnels))}
	for _, t := range tunnels {
		cfg.Tunnels = append(cfg.Tunnels, h.ProtoTunnel(t))
	}
	return cfg, nil
}

// PublicURL is how visitors reach a tunnel.
func (h *Hub) PublicURL(t *store.Tunnel) string {
	switch {
	case t.Host != nil:
		return "https://" + *t.Host
	case t.RemotePort != nil:
		return t.Type + "://" + net.JoinHostPort(h.opt.TCPHost, strconv.Itoa(*t.RemotePort))
	}
	return ""
}

// ProtoTunnel converts a stored tunnel to its wire form.
func (h *Hub) ProtoTunnel(t *store.Tunnel) tunnelproto.Tunnel {
	pt := tunnelproto.Tunnel{
		ID: t.ID, Name: t.Name, Type: t.Type, PublicURL: h.PublicURL(t), LocalIP: t.LocalIP, LocalPort: t.LocalPort,
		Enabled: t.Enabled, PausedByClient: t.PausedByClient,
		Permissions: tunnelproto.Permissions{EditLocal: t.ClientCanEditLocal, LoopbackOnly: t.LocalLoopbackOnly, Toggle: t.ClientCanToggle},
		Display:     tunnelproto.Display{AccessPolicy: "public"},
	}
	if t.ExpiresAt != nil {
		at := time.UnixMilli(*t.ExpiresAt).UTC()
		pt.ExpiresAt = &at
	}
	return pt
}

// ErrOffline means the tunnel's device has no session.
var ErrOffline = errors.New("device offline")

// Dial opens a data stream to a device and waits for its verdict. The returned conn carries the payload.
func (h *Hub) Dial(ctx context.Context, deviceID, tunnelID, proto, remoteAddr string) (net.Conn, error) {
	h.mu.Lock()
	s := h.sessions[deviceID]
	h.mu.Unlock()
	if s == nil || s.mux == nil {
		return nil, ErrOffline
	}
	stream, err := s.mux.OpenStream()
	if err != nil {
		return nil, fmt.Errorf("open stream: %w", err)
	}
	deadline := time.Now().Add(streamTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = stream.SetDeadline(deadline)
	if err := tunnelproto.WriteStreamHeader(stream, tunnelproto.StreamHeader{TunnelID: tunnelID, Proto: proto, RemoteAddr: remoteAddr}); err != nil {
		stream.Close()
		return nil, err
	}
	if err := tunnelproto.ReadStreamReply(stream); err != nil {
		stream.Close()
		return nil, err
	}
	_ = stream.SetDeadline(time.Time{})
	return stream, nil
}

// ServeConnect handles GET /api/v1/device/connect. clientIP is the device's address as seen through
// trusted proxies.
func (h *Hub) ServeConnect(w http.ResponseWriter, r *http.Request, clientIP string) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
	if err != nil {
		return // Accept already wrote the error response
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var device *store.Device
	var owner *store.User
	hctx, hcancel := context.WithTimeout(ctx, handshakeTimeout)
	hello, err := tunnelproto.ServerHandshake(hctx, ws, h.opt.PublicHost, func(ctx context.Context, hello *tunnelproto.Hello) (ed25519.PublicKey, error) {
		d, err := h.opt.Store.DeviceByID(ctx, hello.DeviceID)
		if errors.Is(err, store.ErrNotFound) {
			return nil, tunnelproto.ErrUnauthorized
		}
		if err != nil {
			return nil, err
		}
		if d.RevokedAt != nil {
			return nil, tunnelproto.ErrUnauthorized
		}
		u, err := h.opt.Store.UserByID(ctx, d.UserID)
		if err != nil {
			return nil, err
		}
		if u.DisabledAt != nil {
			return nil, tunnelproto.ErrUnauthorized
		}
		device, owner = d, u
		return ed25519.PublicKey(d.PublicKey), nil
	})
	hcancel()
	if err != nil {
		if errors.Is(err, tunnelproto.ErrUnauthorized) {
			id := ""
			if hello != nil {
				id = hello.DeviceID
			}
			h.opt.Audit(ctx, store.AuditEvent{ActorType: "anonymous", ActorID: id, Action: "device.auth_failed", Target: id, IP: clientIP})
			ws.Close(tunnelproto.CloseUnauthorized, "unauthorized")
			return
		}
		h.opt.Log.Info("hub: handshake failed", "ip", clientIP, "err", err)
		ws.Close(websocket.StatusPolicyViolation, "handshake failed")
		return
	}

	s := &Session{
		ID: auth.NewID("ses_"), DeviceID: device.ID, UserID: owner.ID, DeviceName: device.Name, RemoteIP: clientIP,
		ClientVersion: hello.ClientVersion, ConnectedAt: h.opt.Now(), ws: ws, cancel: cancel, status: map[string]tunnelproto.Status{},
	}
	if err := tunnelproto.SendWelcome(ctx, ws, tunnelproto.Welcome{SessionID: s.ID}); err != nil {
		ws.CloseNow()
		return
	}
	mux, err := tunnelproto.ServerSession(ctx, ws)
	if err != nil {
		ws.CloseNow()
		return
	}
	actx, acancel := context.WithTimeout(ctx, handshakeTimeout)
	stream, err := mux.AcceptStreamWithContext(actx)
	acancel()
	if err != nil {
		mux.Close()
		ws.CloseNow()
		return
	}
	// The session is published only once complete, so readers never see half-initialised fields.
	s.mux, s.control = mux, tunnelproto.NewControl(stream)
	if tunnelproto.HasFeature(hello.Features, tunnelproto.FeatureProbe) {
		// A status query: answer with the configuration, leave the real session alone.
		defer mux.Close()
		cfg, err := h.BuildConfig(ctx, device.ID)
		if err == nil {
			_ = s.control.Send(tunnelproto.TypeConfig, "", cfg)
			for {
				if _, err := s.control.Recv(); err != nil {
					break
				}
			}
		}
		return
	}
	h.mu.Lock()
	old := h.sessions[device.ID]
	h.sessions[device.ID] = s
	h.mu.Unlock()
	if old != nil {
		old.close(tunnelproto.CloseReplaced, "replaced by a newer session")
	}
	defer h.unregister(s)

	_ = h.opt.Store.DeviceSeen(ctx, device.ID, clientIP, hello.ClientVersion, h.opt.Now().UnixMilli())
	h.opt.Log.Info("device connected", "device", device.ID, "name", device.Name, "ip", clientIP, "version", hello.ClientVersion)
	h.Push(ctx, device.ID)

	go h.keepalive(ctx, s)
	h.readLoop(ctx, s)
	h.opt.Log.Info("device disconnected", "device", device.ID)
	_ = h.opt.Store.DeviceSeen(context.Background(), device.ID, clientIP, "", h.opt.Now().UnixMilli())
}

func (h *Hub) unregister(s *Session) {
	h.mu.Lock()
	if h.sessions[s.DeviceID] == s {
		delete(h.sessions, s.DeviceID)
	}
	h.mu.Unlock()
	s.cancel()
	if s.mux != nil {
		s.mux.Close()
	}
	s.ws.CloseNow()
}

func (h *Hub) keepalive(ctx context.Context, s *Session) {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.control.Send(tunnelproto.TypePing, "", nil); err != nil {
				s.cancel()
				return
			}
		}
	}
}

func (h *Hub) readLoop(ctx context.Context, s *Session) {
	for {
		m, err := s.control.Recv()
		if err != nil {
			return
		}
		switch m.Type {
		case tunnelproto.TypePing:
			_ = s.control.Send(tunnelproto.TypePong, m.ID, nil)
		case tunnelproto.TypePong:
		case tunnelproto.TypeStatus:
			var st tunnelproto.Status
			if m.Decode(&st) == nil && st.TunnelID != "" {
				s.mu.Lock()
				s.status[st.TunnelID] = st
				s.mu.Unlock()
			}
		case tunnelproto.TypeStats:
			// Traffic is metered on the server side; device stats are informational only.
		case tunnelproto.TypeTunnelUpdate:
			var u tunnelproto.TunnelUpdate
			result := tunnelproto.Result{OK: true}
			if err := m.Decode(&u); err != nil {
				result = tunnelproto.Result{Error: "bad_request"}
			} else if err := h.applyUpdate(ctx, s, &u); err != nil {
				result = tunnelproto.Result{Error: errorCode(err)}
			}
			_ = s.control.Send(tunnelproto.TypeResult, m.ID, result)
		default:
			if m.ID != "" {
				_ = s.control.Send(tunnelproto.TypeResult, m.ID, tunnelproto.Result{Error: "unsupported"})
			}
		}
	}
}

func errorCode(err error) string {
	if e, ok := rules.AsError(err); ok {
		return e.Code
	}
	if errors.Is(err, errNotAllowed) {
		return "field_not_allowed"
	}
	if errors.Is(err, store.ErrNotFound) {
		return "not_found"
	}
	return "internal_error"
}

var errNotAllowed = errors.New("field not allowed")

// applyUpdate lets a device change what its tunnel's permissions allow.
func (h *Hub) applyUpdate(ctx context.Context, s *Session, u *tunnelproto.TunnelUpdate) error {
	t, err := h.opt.Store.TunnelByID(ctx, u.TunnelID)
	if err != nil {
		return err
	}
	if t.DeviceID == nil || *t.DeviceID != s.DeviceID {
		return store.ErrNotFound
	}
	var changes []string
	if u.LocalIP != nil || u.LocalPort != nil {
		if !t.ClientCanEditLocal {
			h.auditDevice(ctx, s, "tunnel.update_denied", t.ID, "local target")
			return errNotAllowed
		}
		ip, port := t.LocalIP, t.LocalPort
		if u.LocalIP != nil {
			ip = *u.LocalIP
		}
		if u.LocalPort != nil {
			port = *u.LocalPort
		}
		if err := rules.LocalTarget(ip, port, t.LocalLoopbackOnly); err != nil {
			return err
		}
		changes = append(changes, fmt.Sprintf("local %s:%d -> %s:%d", t.LocalIP, t.LocalPort, ip, port))
		t.LocalIP, t.LocalPort = ip, port
	}
	if u.PausedByClient != nil {
		if !t.ClientCanToggle {
			h.auditDevice(ctx, s, "tunnel.update_denied", t.ID, "pause")
			return errNotAllowed
		}
		changes = append(changes, fmt.Sprintf("paused=%v", *u.PausedByClient))
		t.PausedByClient = *u.PausedByClient
	}
	if len(changes) == 0 {
		return nil
	}
	t.UpdatedAt = h.opt.Now().UnixMilli()
	if err := h.opt.Store.SaveTunnel(ctx, t); err != nil {
		return err
	}
	if err := h.opt.Store.BumpDeviceRev(ctx, s.DeviceID); err != nil {
		return err
	}
	h.auditDevice(ctx, s, "tunnel.device_update", t.ID, fmt.Sprint(changes))
	h.opt.OnChange()
	go h.Push(context.Background(), s.DeviceID)
	return nil
}

func (h *Hub) auditDevice(ctx context.Context, s *Session, action, target, detail string) {
	h.opt.Audit(ctx, store.AuditEvent{ActorType: "device", ActorID: s.DeviceID, ActorName: s.DeviceName, Action: action, Target: target, Detail: detail, IP: s.RemoteIP})
}

// Close ends every session (server shutdown).
func (h *Hub) Close() {
	h.mu.Lock()
	all := h.sessions
	h.sessions = map[string]*Session{}
	h.mu.Unlock()
	for _, s := range all {
		s.close(websocket.StatusGoingAway, "server shutting down")
	}
}
