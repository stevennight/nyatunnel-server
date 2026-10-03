// Package rules validates names, subdomains, ports and local targets. The admin API and the device
// control stream share it, so a device cannot set what the console would refuse.
package rules

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

// Error is a validation failure with a stable code for the API.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func fail(code, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// AsError extracts a validation error.
func AsError(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

var (
	nameRE      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	labelRE     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	usernameRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{1,31}$`)
	domainLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

// TunnelName: lower case letters, digits and dashes, 1–40 characters.
func TunnelName(s string) error {
	if !nameRE.MatchString(s) {
		return fail("invalid_name", "名称只能包含小写字母、数字和连字符，最长 40 个字符")
	}
	return nil
}

// Username: 2–32 characters of lower case letters, digits, '_', '.', '-'.
func Username(s string) error {
	if !usernameRE.MatchString(s) {
		return fail("invalid_username", "用户名为 2–32 位小写字母、数字或 _ . -")
	}
	return nil
}

// reservedSubdomains are never handed out: they look like infrastructure or invite phishing.
var reservedSubdomains = map[string]bool{
	"www": true, "admin": true, "administrator": true, "api": true, "app": true, "auth": true, "login": true, "signin": true,
	"account": true, "accounts": true, "sso": true, "oauth": true, "mail": true, "smtp": true, "imap": true, "pop": true,
	"webmail": true, "ftp": true, "ns": true, "ns1": true, "ns2": true, "dns": true, "mx": true, "tunnel": true, "cdn": true,
	"static": true, "assets": true, "status": true, "support": true, "help": true, "billing": true, "pay": true, "payment": true,
	"secure": true, "security": true, "verify": true, "update": true, "dashboard": true, "console": true, "portal": true,
	"localhost": true, "root": true, "test": true,
}

// brandWords may not appear anywhere in a subdomain.
var brandWords = []string{
	"paypal", "apple", "icloud", "google", "gmail", "microsoft", "office365", "outlook", "amazon", "facebook", "instagram",
	"whatsapp", "telegram", "wechat", "weixin", "alipay", "taobao", "tmall", "jd", "qq", "tencent", "baidu", "bank",
	"steam", "netflix", "binance", "coinbase", "metamask", "wallet", "github", "cloudflare",
}

// Subdomain validates one DNS label for an HTTPS tunnel. requiredPrefix ("" for admins) is the
// "<username>-" prefix normal users must use.
func Subdomain(s, requiredPrefix string) error {
	if !labelRE.MatchString(s) {
		return fail("invalid_subdomain", "子域名只能包含小写字母、数字和连字符，且不能以连字符开头或结尾")
	}
	if reservedSubdomains[s] {
		return fail("subdomain_reserved", "子域名 %q 是保留字", s)
	}
	for _, w := range brandWords {
		if w == "jd" || w == "qq" {
			if s == w || strings.HasPrefix(s, w+"-") || strings.HasSuffix(s, "-"+w) || strings.Contains(s, "-"+w+"-") {
				return fail("subdomain_blocked", "子域名不能包含品牌词 %q", w)
			}
			continue
		}
		if strings.Contains(s, w) {
			return fail("subdomain_blocked", "子域名不能包含品牌词 %q", w)
		}
	}
	if requiredPrefix != "" && (!strings.HasPrefix(s, requiredPrefix) || len(s) == len(requiredPrefix)) {
		return fail("subdomain_prefix", "子域名必须以 %q 开头", requiredPrefix)
	}
	return nil
}

// DomainName validates a root domain such as "dev.example.com".
func DomainName(s string) error {
	labels := strings.Split(s, ".")
	if len(s) > 200 || len(labels) < 2 {
		return fail("invalid_domain", "域名格式不正确")
	}
	for _, l := range labels {
		if !domainLabel.MatchString(l) {
			return fail("invalid_domain", "域名格式不正确")
		}
	}
	return nil
}

// Port validates a TCP/UDP port number.
func Port(p int) error {
	if p < 1 || p > 65535 {
		return fail("invalid_port", "端口必须在 1–65535 之间")
	}
	return nil
}

// LocalTarget validates the address a device dials. With loopbackOnly only 127.0.0.0/8 and ::1 are
// allowed (keeps a tunnel from exposing other machines of the device's network). "localhost" is
// accepted and means loopback.
func LocalTarget(ip string, port int, loopbackOnly bool) error {
	if err := Port(port); err != nil {
		return err
	}
	if ip == "localhost" {
		return nil
	}
	a, err := netip.ParseAddr(ip)
	if err != nil || a.Zone() != "" {
		return fail("invalid_local_ip", "本地地址必须是 IP 地址或 localhost")
	}
	a = a.Unmap()
	if a.IsUnspecified() || a.IsMulticast() {
		return fail("invalid_local_ip", "本地地址不能是 0.0.0.0 或组播地址")
	}
	if loopbackOnly && !a.IsLoopback() {
		return fail("local_not_loopback", "该隧道只允许本机回环地址（127.0.0.1 / ::1）")
	}
	return nil
}

// LocalTargetAllowed is the device-side twin of LocalTarget's loopback rule.
func LocalTargetAllowed(ip string, loopbackOnly bool) bool {
	return LocalTarget(ip, 1, loopbackOnly) == nil
}
