package api

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"nyatunnel-server/internal/server/auth"
	"nyatunnel-server/internal/server/edge"
	"nyatunnel-server/internal/server/notify"
	"nyatunnel-server/internal/server/rules"
	"nyatunnel-server/internal/server/store"
	"nyatunnel-server/internal/shared/version"
)

const (
	loginFailureLimit  = 5
	loginFailureWindow = 5 * time.Minute
	totpIssuer         = "NyaTunnel"
	recoveryCodeCount  = 10
)

type userView struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Role        string `json:"role"`
	TOTPEnabled bool   `json:"totpEnabled"`
	Disabled    bool   `json:"disabled"`
	CreatedAt   int64  `json:"createdAt"`
	// RecoveryCodesLeft is only filled for the signed-in user.
	RecoveryCodesLeft *int `json:"recoveryCodesLeft,omitempty"`
}

func viewUser(u *store.User) userView {
	return userView{ID: u.ID, Username: u.Username, Role: u.Role, TOTPEnabled: u.TOTPEnabled(), Disabled: u.DisabledAt != nil, CreatedAt: u.CreatedAt}
}

// handleBootstrap tells the web app which screen to show.
func (s *server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.UserCount(r.Context())
	if err != nil {
		s.fail(w, "bootstrap", err)
		return
	}
	out := map[string]any{
		"version": version.Version, "needsSetup": n == 0, "serverName": s.serverName(r.Context()),
		"publicUrl": s.Config.PublicURL, "forceTotp": s.forceTOTP(r.Context()),
	}
	if p, err := s.authenticate(w, r); err == nil {
		v := viewUser(p.user)
		out["user"] = v
	}
	writeJSON(w, http.StatusOK, out)
}

// handleTunnelLogin is where a login-gated tunnel sends visitors: signed-in accounts are handed back
// to the tunnel with a one-minute token; others go through the console login first.
func (s *server) handleTunnelLogin(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tunnelID, cb, ret := q.Get("t"), q.Get("cb"), q.Get("r")
	if !s.Edge.LoginCallbackOK(tunnelID, cb) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>链接无效</title></head>` +
			`<body style="font-family:system-ui,sans-serif;display:grid;place-items:center;min-height:90vh"><main style="text-align:center"><h1>链接无效</h1><p>这个登录链接无效或已过期，请回到原网页重新访问。</p></main></body></html>`))
		return
	}
	p, err := s.authenticate(w, r)
	if err != nil {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
		return
	}
	s.audit(r, p, "tunnel.gate_login", tunnelID, "")
	target := cb + "?" + url.Values{"token": {s.Edge.IssueLoginToken(tunnelID, p.user.ID)}, "r": {ret}}.Encode()
	http.Redirect(w, r, target, http.StatusFound)
}

// handleSetup creates the first administrator. It needs the setup token from the server log, so
// whoever reaches a fresh console first cannot claim it.
func (s *server) handleSetup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	ip := s.realIP.ClientIP(r)
	if blocked, wait := s.loginFailures.blocked("setup|"+ip, s.now()); blocked {
		retryAfter(w, wait)
		return
	}
	if s.SetupToken == "" || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(body.Token)), []byte(s.SetupToken)) != 1 {
		s.loginFailures.fail("setup|"+ip, s.now())
		writeError(w, http.StatusForbidden, "invalid_setup_token", "初始化令牌不正确，请查看服务端日志")
		return
	}
	username := strings.ToLower(strings.TrimSpace(body.Username))
	if err := rules.Username(username); err != nil {
		s.fail(w, "setup", err)
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, "weak_password", err.Error())
		return
	}
	now := s.now().UnixMilli()
	u := &store.User{ID: auth.NewID("usr_"), Username: username, PasswordHash: hash, Role: store.RoleAdmin, CreatedAt: now, UpdatedAt: now}
	if err := s.Store.CreateFirstAdmin(r.Context(), u); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "already_set_up", "")
			return
		}
		s.fail(w, "setup", err)
		return
	}
	if err := s.startSession(w, r, u); err != nil {
		s.fail(w, "setup: session", err)
		return
	}
	s.audit(r, &principal{user: u}, "auth.setup", u.ID, "")
	writeJSON(w, http.StatusOK, map[string]any{"user": viewUser(u)})
}

func retryAfter(w http.ResponseWriter, wait time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
	writeError(w, http.StatusTooManyRequests, "too_many_attempts", "尝试次数过多，请稍后再试")
}

// handleLogin checks the password and, when enabled, the TOTP code or a recovery code.
func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username     string `json:"username"`
		Password     string `json:"password"`
		TOTP         string `json:"totp"`
		RecoveryCode string `json:"recoveryCode"`
	}
	if !decode(w, r, &body) {
		return
	}
	ip, now := s.realIP.ClientIP(r), s.now()
	username := strings.ToLower(strings.TrimSpace(body.Username))
	for _, key := range []string{"ip|" + ip, "user|" + username} {
		if blocked, wait := s.loginFailures.blocked(key, now); blocked {
			retryAfter(w, wait)
			return
		}
	}
	failed := func(reason string, u *store.User) {
		s.loginFailures.fail("ip|"+ip, now)
		s.loginFailures.fail("user|"+username, now)
		if blocked, _ := s.loginFailures.blocked("user|"+username, now); blocked {
			s.Notify.Send(notify.EventBruteForce, "登录失败过多", "账号 "+username+" 在短时间内多次登录失败（最近一次来自 "+ip+"），已临时锁定 5 分钟。")
		}
		var p *principal
		if u != nil {
			p = &principal{user: u}
		}
		s.audit(r, p, "auth.login_failed", username, reason)
	}

	u, err := s.Store.UserByName(r.Context(), username)
	if errors.Is(err, store.ErrNotFound) {
		auth.BurnPasswordCheck(body.Password)
		failed("unknown user", nil)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "用户名或密码错误")
		return
	}
	if err != nil {
		s.fail(w, "login", err)
		return
	}
	if !auth.VerifyPassword(u.PasswordHash, body.Password) {
		failed("password", u)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "用户名或密码错误")
		return
	}
	if u.DisabledAt != nil {
		failed("disabled", u)
		writeError(w, http.StatusForbidden, "user_disabled", "账号已被禁用")
		return
	}
	if u.TOTPEnabled() {
		switch {
		case strings.TrimSpace(body.RecoveryCode) != "":
			ok, err := s.Store.UseRecoveryCode(r.Context(), u.ID, auth.HashToken(auth.NormalizeRecoveryCode(body.RecoveryCode)), now.UnixMilli())
			if err != nil {
				s.fail(w, "login: recovery code", err)
				return
			}
			if !ok {
				failed("recovery code", u)
				writeError(w, http.StatusUnauthorized, "invalid_recovery_code", "恢复码无效")
				return
			}
			s.audit(r, &principal{user: u}, "auth.recovery_code_used", u.ID, "")
		case strings.TrimSpace(body.TOTP) == "":
			writeError(w, http.StatusUnauthorized, "totp_required", "请输入两步验证码")
			return
		default:
			secret, err := s.Secrets.Open(u.TOTPSecret)
			if err != nil {
				s.Log.Error("login: cannot open TOTP secret (secrets key replaced?)", "user", u.ID, "err", err)
				writeError(w, http.StatusInternalServerError, "totp_unavailable", "")
				return
			}
			if !auth.VerifyTOTP(string(secret), body.TOTP, now) {
				failed("totp", u)
				writeError(w, http.StatusUnauthorized, "invalid_totp", "验证码不正确")
				return
			}
		}
	}
	s.loginFailures.reset("ip|" + ip)
	s.loginFailures.reset("user|" + username)
	if err := s.startSession(w, r, u); err != nil {
		s.fail(w, "login: session", err)
		return
	}
	s.audit(r, &principal{user: u}, "auth.login", u.ID, "")
	writeJSON(w, http.StatusOK, map[string]any{"user": viewUser(u)})
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(csrfHeader) == "" {
		writeError(w, http.StatusForbidden, "csrf", "")
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		_ = s.Store.DeleteSession(r.Context(), auth.HashToken(c.Value))
	}
	s.clearSessionCookie(w)
	writeOK(w)
}

func (s *server) handleMe(w http.ResponseWriter, r *http.Request, p *principal) {
	v := viewUser(p.user)
	left := len(p.user.RecoveryCodes)
	v.RecoveryCodesLeft = &left
	q := p.user.Quota
	if q.Types == nil {
		q.Types = []string{}
	}
	tunnels, _ := s.Store.TunnelCount(r.Context(), p.user.ID)
	month, _ := s.Store.TrafficByUser(r.Context(), edge.MonthStart(s.now()))
	writeJSON(w, http.StatusOK, map[string]any{
		"user": v, "mustSetupTotp": !p.user.TOTPEnabled() && s.forceTOTP(r.Context()),
		"quota": q, "tunnelCount": tunnels, "monthBytes": month[p.user.ID].Bytes(),
	})
}

// handleChangePassword needs the current password; other sessions of the user are ended.
func (s *server) handleChangePassword(w http.ResponseWriter, r *http.Request, p *principal) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !s.checkPassword(w, r, p, body.Current) {
		return
	}
	hash, err := auth.HashPassword(body.New)
	if err != nil {
		writeError(w, http.StatusBadRequest, "weak_password", err.Error())
		return
	}
	if err := s.Store.SetUserPassword(r.Context(), p.user.ID, hash, s.now().UnixMilli()); err != nil {
		s.fail(w, "change password", err)
		return
	}
	_ = s.Store.DeleteUserSessions(r.Context(), p.user.ID, p.sessionHash)
	s.audit(r, p, "auth.password_changed", p.user.ID, "")
	writeOK(w)
}

// checkPassword re-verifies the signed-in user's password with the login rate limit.
func (s *server) checkPassword(w http.ResponseWriter, r *http.Request, p *principal, password string) bool {
	key := "user|" + p.user.Username
	if blocked, wait := s.loginFailures.blocked(key, s.now()); blocked {
		retryAfter(w, wait)
		return false
	}
	if !auth.VerifyPassword(p.user.PasswordHash, password) {
		s.loginFailures.fail(key, s.now())
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "当前密码不正确")
		return false
	}
	return true
}

// handleTOTPSetup returns a fresh secret; nothing is stored until enable confirms a code.
func (s *server) handleTOTPSetup(w http.ResponseWriter, r *http.Request, p *principal) {
	secret, err := auth.NewTOTPSecret()
	if err != nil {
		s.fail(w, "totp setup", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "otpauthUrl": auth.TOTPAuthURL(totpIssuer, p.user.Username, secret)})
}

// handleTOTPEnable stores the secret once the authenticator produces a matching code and returns
// the recovery codes (shown once).
func (s *server) handleTOTPEnable(w http.ResponseWriter, r *http.Request, p *principal) {
	var body struct {
		Secret string `json:"secret"`
		Code   string `json:"code"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !auth.VerifyTOTP(body.Secret, body.Code, s.now()) {
		writeError(w, http.StatusBadRequest, "invalid_totp", "验证码不正确")
		return
	}
	sealed, err := s.Secrets.Seal([]byte(strings.ToUpper(strings.ReplaceAll(body.Secret, " ", ""))))
	if err != nil {
		s.fail(w, "totp seal", err)
		return
	}
	codes, err := auth.NewRecoveryCodes(recoveryCodeCount)
	if err != nil {
		s.fail(w, "recovery codes", err)
		return
	}
	hashes := make([]string, len(codes))
	for i, c := range codes {
		hashes[i] = auth.HashToken(c)
	}
	if err := s.Store.SetUserTOTP(r.Context(), p.user.ID, sealed, hashes, s.now().UnixMilli()); err != nil {
		s.fail(w, "totp store", err)
		return
	}
	s.audit(r, p, "auth.totp_enabled", p.user.ID, "")
	writeJSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes})
}

// handleTOTPDisable needs the password and is refused while TOTP is mandatory.
func (s *server) handleTOTPDisable(w http.ResponseWriter, r *http.Request, p *principal) {
	var body struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if s.forceTOTP(r.Context()) {
		writeError(w, http.StatusForbidden, "totp_required_by_policy", "管理员要求所有账号开启两步验证")
		return
	}
	if !s.checkPassword(w, r, p, body.Password) {
		return
	}
	if err := s.Store.SetUserTOTP(r.Context(), p.user.ID, nil, nil, s.now().UnixMilli()); err != nil {
		s.fail(w, "totp disable", err)
		return
	}
	s.audit(r, p, "auth.totp_disabled", p.user.ID, "")
	writeOK(w)
}
