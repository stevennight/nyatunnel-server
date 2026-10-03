// Package config loads the server configuration from NYATUNNEL_* environment variables.
package config

import (
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"strings"
	"time"

	"github.com/stevennight/nyatunnel-common/deeplink"
)

// Config is the runtime configuration of the server.
type Config struct {
	// PublicURL is where browsers reach the admin console and devices connect, e.g. https://tunnel.example.com.
	PublicURL string
	// PublicHost is PublicURL's host as used in deep links and device signatures.
	PublicHost string
	// Listen serves the admin console, the API and device connections (behind the reverse proxy).
	Listen string
	// IngressListen serves HTTP tunnels (behind the reverse proxy). It never serves the console.
	IngressListen string
	// InternalListen serves the reverse proxy's on-demand TLS question. It must not be reachable from outside.
	InternalListen string
	// PortBindAddr is the address TCP/UDP tunnel ports listen on.
	PortBindAddr string
	// TrustedProxies are the peers whose X-Forwarded-For / X-Forwarded-Proto headers are believed.
	TrustedProxies []netip.Prefix
	// DataDir holds the database and other state.
	DataDir string
	// SecretsKeyFile seals TOTP secrets; created on first start when it does not exist.
	SecretsKeyFile string
	// WebDir is the web console build served at /; the embedded placeholder is used when it is missing.
	WebDir string
	// SessionTTL is how long a console login lasts without use.
	SessionTTL time.Duration
}

// Load reads the configuration; getenv is os.Getenv outside tests.
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		PublicURL:      strings.TrimRight(envOr(getenv, "NYATUNNEL_PUBLIC_URL", "http://127.0.0.1:8080"), "/"),
		Listen:         envOr(getenv, "NYATUNNEL_LISTEN", "127.0.0.1:8080"),
		IngressListen:  envOr(getenv, "NYATUNNEL_INGRESS_LISTEN", "127.0.0.1:8081"),
		InternalListen: envOr(getenv, "NYATUNNEL_INTERNAL_LISTEN", "127.0.0.1:8082"),
		PortBindAddr:   envOr(getenv, "NYATUNNEL_PORT_BIND", "0.0.0.0"),
		DataDir:        envOr(getenv, "NYATUNNEL_DATA", "data"),
		WebDir:         envOr(getenv, "NYATUNNEL_WEB_DIR", ".tmp-webdist"),
	}
	cfg.SecretsKeyFile = envOr(getenv, "NYATUNNEL_SECRETS_KEY_FILE", filepath.Join(cfg.DataDir, "secrets.key"))

	host, err := deeplink.ServerHost(cfg.PublicURL)
	if err != nil {
		return Config{}, fmt.Errorf("NYATUNNEL_PUBLIC_URL: %w", err)
	}
	cfg.PublicHost = host
	for key, addr := range map[string]string{
		"NYATUNNEL_LISTEN": cfg.Listen, "NYATUNNEL_INGRESS_LISTEN": cfg.IngressListen, "NYATUNNEL_INTERNAL_LISTEN": cfg.InternalListen,
	} {
		if _, _, err := net.SplitHostPort(addr); err != nil {
			return Config{}, fmt.Errorf("%s: %w", key, err)
		}
	}
	if _, err := netip.ParseAddr(cfg.PortBindAddr); err != nil {
		return Config{}, fmt.Errorf("NYATUNNEL_PORT_BIND: %w", err)
	}
	for _, s := range strings.Split(envOr(getenv, "NYATUNNEL_TRUSTED_PROXY_CIDRS", "127.0.0.1/32,::1/128"), ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		p, err := netip.ParsePrefix(s)
		if err != nil {
			a, aerr := netip.ParseAddr(s)
			if aerr != nil {
				return Config{}, fmt.Errorf("NYATUNNEL_TRUSTED_PROXY_CIDRS: %q: %w", s, err)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		cfg.TrustedProxies = append(cfg.TrustedProxies, p.Masked())
	}
	ttl, err := time.ParseDuration(envOr(getenv, "NYATUNNEL_SESSION_TTL", "720h"))
	if err != nil || ttl < time.Minute {
		return Config{}, fmt.Errorf("NYATUNNEL_SESSION_TTL: must be a duration of at least 1m")
	}
	cfg.SessionTTL = ttl
	return cfg, nil
}

// HTTPS reports whether the public URL is served over TLS (decides the cookie Secure flag).
func (c Config) HTTPS() bool { return strings.HasPrefix(c.PublicURL, "https://") }

func envOr(getenv func(string) string, key, def string) string {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		return v
	}
	return def
}
