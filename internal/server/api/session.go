package api

import (
	"context"
	"errors"
	"net/http"

	"nyatunnel-server/internal/server/auth"
	"nyatunnel-server/internal/server/store"
)

const (
	sessionCookie = "nyatunnel_session"
	// csrfHeader must accompany every unsafe console request. Browsers cannot add custom headers to
	// cross-site form posts, and SameSite=Strict already keeps the cookie off them; this is belt and braces.
	csrfHeader = "X-NyaTunnel-CSRF"

	settingForceTOTP  = "force_totp"
	settingServerName = "server_name"
)

// principal is the signed-in console user.
type principal struct {
	user        *store.User
	sessionHash string
}

func (p *principal) admin() bool { return p.user.IsAdmin() }

// owns reports whether p may manage something belonging to userID.
func (p *principal) owns(userID string) bool { return p.admin() || p.user.ID == userID }

type handler func(w http.ResponseWriter, r *http.Request, p *principal)

type routeOpt int

// allowDuringTOTPSetup marks the few endpoints a user who must still set up TOTP may call.
const allowDuringTOTPSetup routeOpt = 1

func (s *server) user(h handler, opts ...routeOpt) http.Handler {
	return s.guard(h, false, opts...)
}

func (s *server) admin(h handler, opts ...routeOpt) http.Handler {
	return s.guard(h, true, opts...)
}

func (s *server) guard(h handler, adminOnly bool, opts ...routeOpt) http.Handler {
	allowSetup := len(opts) > 0 && opts[0] == allowDuringTOTPSetup
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unsafeMethod(r.Method) && r.Header.Get(csrfHeader) == "" {
			writeError(w, http.StatusForbidden, "csrf", "missing "+csrfHeader+" header")
			return
		}
		p, err := s.authenticate(w, r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "")
			return
		}
		if adminOnly && !p.admin() {
			writeError(w, http.StatusForbidden, "forbidden", "")
			return
		}
		if !allowSetup && !p.user.TOTPEnabled() && s.forceTOTP(r.Context()) {
			writeError(w, http.StatusForbidden, "totp_setup_required", "管理员要求所有账号开启两步验证")
			return
		}
		h(w, r, p)
	})
}

func unsafeMethod(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

var errNoSession = errors.New("no session")

// authenticate resolves the session cookie, sliding its expiry once half of the lifetime is gone.
func (s *server) authenticate(w http.ResponseWriter, r *http.Request) (*principal, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, errNoSession
	}
	now := s.now()
	hash := auth.HashToken(c.Value)
	sess, u, err := s.Store.SessionUser(r.Context(), hash, now.UnixMilli())
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.Log.Error("authenticate", "err", err)
		}
		return nil, errNoSession
	}
	ttl := s.Config.SessionTTL
	if sess.ExpiresAt-now.UnixMilli() < ttl.Milliseconds()/2 {
		if err := s.Store.TouchSession(r.Context(), hash, now.UnixMilli(), now.Add(ttl).UnixMilli()); err == nil {
			s.setSessionCookie(w, c.Value)
		}
	}
	return &principal{user: u, sessionHash: hash}, nil
}

func (s *server) startSession(w http.ResponseWriter, r *http.Request, u *store.User) error {
	id, err := auth.NewToken("")
	if err != nil {
		return err
	}
	now := s.now()
	err = s.Store.CreateSession(r.Context(), store.Session{
		IDHash: auth.HashToken(id), UserID: u.ID, CreatedAt: now.UnixMilli(), LastUsedAt: now.UnixMilli(),
		ExpiresAt: now.Add(s.Config.SessionTTL).UnixMilli(), IP: s.realIP.ClientIP(r), UserAgent: r.UserAgent(),
	})
	if err != nil {
		return err
	}
	s.setSessionCookie(w, id)
	return nil
}

func (s *server) setSessionCookie(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: value, Path: "/", MaxAge: int(s.Config.SessionTTL.Seconds()),
		HttpOnly: true, Secure: s.Config.HTTPS(), SameSite: http.SameSiteStrictMode,
	})
}

func (s *server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.Config.HTTPS(), SameSite: http.SameSiteStrictMode,
	})
}

func (s *server) forceTOTP(ctx context.Context) bool {
	v, _ := s.Store.Setting(ctx, settingForceTOTP)
	return v == "true"
}

func (s *server) serverName(ctx context.Context) string {
	v, _ := s.Store.Setting(ctx, settingServerName)
	if v == "" {
		return "NyaTunnel"
	}
	return v
}
