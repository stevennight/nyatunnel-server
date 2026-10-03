// Package api wires the HTTP handlers of the admin console, the enrollment endpoints and the device
// connection.
package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/stevennight/nyatunnel-common/tunnelproto"

	"nyatunnel-server/internal/server/config"
	"nyatunnel-server/internal/server/edge"
	"nyatunnel-server/internal/server/hub"
	"nyatunnel-server/internal/server/notify"
	"nyatunnel-server/internal/server/realip"
	"nyatunnel-server/internal/server/rules"
	"nyatunnel-server/internal/server/secrets"
	"nyatunnel-server/internal/server/store"
	"nyatunnel-server/internal/shared/version"
)

// Options configures the console handler.
type Options struct {
	Config  config.Config
	Store   *store.Store
	Hub     *hub.Hub
	Edge    *edge.Edge
	Secrets *secrets.Box
	Log     *slog.Logger
	// SetupToken must accompany the first-run setup request (printed to the log at startup).
	SetupToken string
	Notify     *notify.Notifier
	// LookupIP resolves custom domains; tests replace it.
	LookupIP func(ctx context.Context, host string) ([]net.IP, error)
	// Background, when set, runs periodic jobs (custom-domain DNS checks) until it ends.
	Background context.Context
	Now        func() time.Time
}

type server struct {
	Options
	realIP        *realip.Resolver
	loginFailures *failureLimiter
	enrollFails   *failureLimiter
}

// Handler is the console handler plus hooks the hub needs.
type Handler struct {
	http.Handler
	s *server
}

// DeviceRequest handles request.create from a device's control stream.
func (h *Handler) DeviceRequest(ctx context.Context, deviceID string, req tunnelproto.TunnelRequest) error {
	return h.s.DeviceRequest(ctx, deviceID, req)
}

// MinClientVersion is the oldest client the server accepts ("" = any).
func (h *Handler) MinClientVersion(ctx context.Context) string { return h.s.minClientVersion(ctx) }

// New returns the handler for the console listener.
func New(opts Options) *Handler {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.Notify == nil {
		opts.Notify = &notify.Notifier{Store: opts.Store, Secrets: opts.Secrets, Log: opts.Log}
	}
	s := &server{
		Options:       opts,
		realIP:        &realip.Resolver{Trusted: opts.Config.TrustedProxies},
		loginFailures: newFailureLimiter(loginFailureLimit, loginFailureWindow),
		enrollFails:   newFailureLimiter(10, 10*time.Minute),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)

	// Public.
	mux.HandleFunc("GET /api/v1/bootstrap", s.handleBootstrap)
	mux.HandleFunc("POST /api/v1/setup", s.handleSetup)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/v1/enroll/preview", s.handleEnrollPreview)
	mux.HandleFunc("POST /api/v1/enroll/claim", s.handleEnrollClaim)
	mux.HandleFunc("GET /api/v1/device/connect", func(w http.ResponseWriter, r *http.Request) {
		s.Hub.ServeConnect(w, r, s.realIP.ClientIP(r))
	})

	// Any signed-in user (own resources).
	mux.Handle("GET /api/v1/me", s.user(s.handleMe, allowDuringTOTPSetup))
	mux.Handle("POST /api/v1/me/password", s.user(s.handleChangePassword, allowDuringTOTPSetup))
	mux.Handle("POST /api/v1/me/totp/setup", s.user(s.handleTOTPSetup, allowDuringTOTPSetup))
	mux.Handle("POST /api/v1/me/totp/enable", s.user(s.handleTOTPEnable, allowDuringTOTPSetup))
	mux.Handle("POST /api/v1/me/totp/disable", s.user(s.handleTOTPDisable))
	mux.Handle("GET /api/v1/devices", s.user(s.handleListDevices))
	mux.Handle("PATCH /api/v1/devices/{id}", s.user(s.handleRenameDevice))
	mux.Handle("POST /api/v1/devices/{id}/revoke", s.user(s.handleRevokeDevice))
	mux.Handle("POST /api/v1/enrollments", s.user(s.handleCreateEnrollment))
	mux.Handle("GET /api/v1/tunnels", s.user(s.handleListTunnels))
	mux.Handle("POST /api/v1/tunnels", s.user(s.handleCreateTunnel))
	mux.Handle("PUT /api/v1/tunnels/{id}", s.user(s.handleUpdateTunnel))
	mux.Handle("DELETE /api/v1/tunnels/{id}", s.user(s.handleDeleteTunnel))
	mux.Handle("GET /api/v1/domains", s.user(s.handleListDomains))
	mux.Handle("POST /api/v1/domains/custom", s.user(s.handleRequestCustomDomain))
	mux.Handle("POST /api/v1/domains/{id}/check", s.user(s.handleCheckDomain))
	mux.Handle("DELETE /api/v1/domains/{id}", s.user(s.handleDeleteDomain))
	mux.Handle("GET /api/v1/requests", s.user(s.handleListRequests))
	mux.Handle("POST /api/v1/requests", s.user(s.handleCreateRequest))
	mux.Handle("POST /api/v1/requests/{id}/cancel", s.user(s.handleCancelRequest))
	mux.Handle("GET /api/v1/enrollments", s.user(s.handleListEnrollments))
	mux.Handle("DELETE /api/v1/enrollments/{id}", s.user(s.handleCancelEnrollment))
	mux.Handle("GET /api/v1/me/sessions", s.user(s.handleListSessions))
	mux.Handle("DELETE /api/v1/me/sessions/{id}", s.user(s.handleDeleteSession))
	mux.Handle("POST /api/v1/me/totp/recovery-codes", s.user(s.handleRegenerateRecoveryCodes))
	mux.Handle("GET /api/v1/traffic", s.user(s.handleTraffic))
	mux.HandleFunc("GET /tunnel-login", s.handleTunnelLogin)

	// Administrators.
	mux.Handle("GET /api/v1/users", s.admin(s.handleListUsers))
	mux.Handle("POST /api/v1/users", s.admin(s.handleCreateUser))
	mux.Handle("PATCH /api/v1/users/{id}", s.admin(s.handleUpdateUser))
	mux.Handle("POST /api/v1/users/{id}/totp/reset", s.admin(s.handleResetUserTOTP))
	mux.Handle("PUT /api/v1/users/{id}/quota", s.admin(s.handleSetUserQuota))
	mux.Handle("POST /api/v1/domains", s.admin(s.handleCreateDomain))
	mux.Handle("PATCH /api/v1/domains/{id}", s.admin(s.handleUpdateDomain))
	mux.Handle("POST /api/v1/requests/{id}/approve", s.admin(s.handleApproveRequest))
	mux.Handle("POST /api/v1/requests/{id}/reject", s.admin(s.handleRejectRequest))
	mux.Handle("GET /api/v1/channels", s.admin(s.handleListChannels))
	mux.Handle("POST /api/v1/channels", s.admin(s.handleCreateChannel))
	mux.Handle("PUT /api/v1/channels/{id}", s.admin(s.handleUpdateChannel))
	mux.Handle("DELETE /api/v1/channels/{id}", s.admin(s.handleDeleteChannel))
	mux.Handle("POST /api/v1/channels/{id}/test", s.admin(s.handleTestChannel))
	mux.Handle("GET /api/v1/port-pools", s.admin(s.handleListPortPools))
	mux.Handle("POST /api/v1/port-pools", s.admin(s.handleCreatePortPool))
	mux.Handle("DELETE /api/v1/port-pools/{id}", s.admin(s.handleDeletePortPool))
	mux.Handle("GET /api/v1/audit", s.admin(s.handleAudit))
	mux.Handle("GET /api/v1/settings", s.admin(s.handleGetSettings))
	mux.Handle("PUT /api/v1/settings", s.admin(s.handlePutSettings))
	mux.Handle("GET /api/v1/dashboard", s.admin(s.handleDashboard))

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "")
	})
	mux.Handle("/", webHandler(opts.Config.WebDir))

	var h http.Handler = mux
	h = limitBody(1<<20, h)
	h = securityHeaders(h)
	h = requestLog(opts.Log, h)
	h = recoverer(opts.Log, h)
	if opts.Background != nil {
		go s.background(opts.Background)
	}
	return &Handler{Handler: h, s: s}
}

// background re-checks custom domains: waiting ones every 5 minutes, active ones every 6 hours.
func (s *server) background(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for n := 0; ; n++ {
		s.CheckDomains(ctx, n%72 == 0)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *server) now() time.Time { return s.Now() }

func (s *server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version.Version})
}

// changed propagates a configuration change: bump the revision of affected devices, rebuild
// routing, and push the new snapshots.
func (s *server) changed(ctx context.Context, deviceIDs ...string) {
	ids := map[string]bool{}
	for _, id := range deviceIDs {
		if id != "" {
			ids[id] = true
		}
	}
	for id := range ids {
		if err := s.Store.BumpDeviceRev(ctx, id); err != nil {
			s.Log.Warn("bump device rev", "device", id, "err", err)
		}
	}
	if err := s.Edge.Reload(ctx); err != nil {
		s.Log.Error("edge reload", "err", err)
	}
	for id := range ids {
		s.Hub.Push(context.WithoutCancel(ctx), id)
	}
}

func (s *server) audit(r *http.Request, p *principal, action, target, detail string) {
	e := store.AuditEvent{At: s.now().UnixMilli(), ActorType: "anonymous", Action: action, Target: target, Detail: detail, IP: s.realIP.ClientIP(r)}
	if p != nil {
		e.ActorType, e.ActorID, e.ActorName = "user", p.user.ID, p.user.Username
	}
	if err := s.Store.Audit(r.Context(), e); err != nil {
		s.Log.Warn("audit write failed", "action", action, "err", err)
	}
}

// AuditFunc lets other packages (the hub) write audit events with the server clock.
func AuditFunc(st *store.Store, log *slog.Logger, now func() time.Time) func(context.Context, store.AuditEvent) {
	return func(ctx context.Context, e store.AuditEvent) {
		e.At = now().UnixMilli()
		if err := st.Audit(context.WithoutCancel(ctx), e); err != nil {
			log.Warn("audit write failed", "action", e.Action, "err", err)
		}
	}
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}

func writeOK(w http.ResponseWriter) { writeJSON(w, http.StatusOK, map[string]bool{"ok": true}) }

// decode reads a JSON body. Unknown fields are ignored on purpose: newer consoles may send more.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "")
		} else {
			writeError(w, http.StatusBadRequest, "bad_request", "请求格式不正确")
		}
		return false
	}
	return true
}

// fail maps store and validation errors to responses; anything unexpected is logged as a 500.
func (s *server) fail(w http.ResponseWriter, what string, err error) {
	if e, ok := rules.AsError(err); ok {
		writeError(w, http.StatusBadRequest, e.Code, e.Message)
		return
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "名称、域名或端口已被占用")
	default:
		s.Log.Error(what, "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "")
	}
}

// --- middleware ---

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Hijack lets the device WebSocket upgrade through the recorder.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(r.ResponseWriter).Hijack()
	if err == nil && r.status == 0 {
		r.status = http.StatusSwitchingProtocols
	}
	return conn, rw, err
}

func recoverer(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				log.Error("panic while handling request", "method", r.Method, "path", r.URL.Path, "panic", v)
				writeError(w, http.StatusInternalServerError, "internal_error", "")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func limitBody(max int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = http.MaxBytesReader(w, r.Body, max)
		}
		next.ServeHTTP(w, r)
	})
}

func requestLog(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if r.URL.Path == "/healthz" || r.URL.Path == "/api/v1/device/connect" {
			return // health checks and long-lived device sessions would drown everything else
		}
		log.Debug("request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration", time.Since(start).Round(time.Microsecond))
	})
}
