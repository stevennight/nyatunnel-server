package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:8080" || cfg.IngressListen != "127.0.0.1:8081" || cfg.InternalListen != "127.0.0.1:8082" {
		t.Fatalf("unexpected listeners: %+v", cfg)
	}
	if cfg.PublicHost != "127.0.0.1:8080" || cfg.HTTPS() || len(cfg.TrustedProxies) != 2 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadPublicURL(t *testing.T) {
	env := map[string]string{"NYATUNNEL_PUBLIC_URL": "https://Tunnel.Example.com/", "NYATUNNEL_TRUSTED_PROXY_CIDRS": "10.0.0.0/8, 192.168.1.1"}
	cfg, err := Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicHost != "tunnel.example.com" || !cfg.HTTPS() || cfg.PublicURL != "https://Tunnel.Example.com" {
		t.Fatalf("public url: %+v", cfg)
	}
	if len(cfg.TrustedProxies) != 2 || cfg.TrustedProxies[1].Bits() != 32 {
		t.Fatalf("proxies: %v", cfg.TrustedProxies)
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	for k, v := range map[string]string{
		"NYATUNNEL_LISTEN":              "8080",
		"NYATUNNEL_PUBLIC_URL":          "tunnel.example.com",
		"NYATUNNEL_TRUSTED_PROXY_CIDRS": "not-an-ip",
		"NYATUNNEL_SESSION_TTL":         "5s",
		"NYATUNNEL_PORT_BIND":           "any",
	} {
		env := map[string]string{k: v}
		if _, err := Load(func(k string) string { return env[k] }); err == nil {
			t.Errorf("%s=%q accepted", k, v)
		}
	}
}
