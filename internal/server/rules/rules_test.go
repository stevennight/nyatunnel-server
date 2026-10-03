package rules

import "testing"

func TestSubdomain(t *testing.T) {
	ok := []struct{ s, prefix string }{{"blog", ""}, {"alice-blog", "alice-"}, {"nas-01", ""}, {"jdoe", ""}, {"qqq", ""}}
	for _, c := range ok {
		if err := Subdomain(c.s, c.prefix); err != nil {
			t.Errorf("Subdomain(%q, %q): %v", c.s, c.prefix, err)
		}
	}
	bad := []struct{ s, prefix, code string }{
		{"Blog", "", "invalid_subdomain"},
		{"-x", "", "invalid_subdomain"},
		{"a.b", "", "invalid_subdomain"},
		{"admin", "", "subdomain_reserved"},
		{"paypal-login", "", "subdomain_blocked"},
		{"my-wechat", "", "subdomain_blocked"},
		{"qq", "", "subdomain_blocked"},
		{"blog", "alice-", "subdomain_prefix"},
		{"alice-", "alice-", "invalid_subdomain"},
	}
	for _, c := range bad {
		err := Subdomain(c.s, c.prefix)
		if e, ok := AsError(err); !ok || e.Code != c.code {
			t.Errorf("Subdomain(%q, %q) = %v, want %s", c.s, c.prefix, err, c.code)
		}
	}
}

func TestLocalTarget(t *testing.T) {
	for _, c := range []struct {
		ip       string
		loopback bool
		ok       bool
	}{
		{"127.0.0.1", true, true}, {"::1", true, true}, {"localhost", true, true}, {"127.8.8.8", true, true},
		{"192.168.1.10", false, true}, {"192.168.1.10", true, false}, {"::ffff:127.0.0.1", true, true},
		{"0.0.0.0", false, false}, {"224.0.0.1", false, false}, {"example.com", false, false}, {"fe80::1%eth0", false, false},
	} {
		if got := LocalTarget(c.ip, 80, c.loopback) == nil; got != c.ok {
			t.Errorf("LocalTarget(%q, loopback=%v) ok=%v, want %v", c.ip, c.loopback, got, c.ok)
		}
	}
	if LocalTarget("127.0.0.1", 0, false) == nil || LocalTarget("127.0.0.1", 70000, false) == nil {
		t.Error("bad ports accepted")
	}
}

func TestNames(t *testing.T) {
	if TunnelName("web-1") != nil || TunnelName("Web") == nil || TunnelName("") == nil {
		t.Error("tunnel names")
	}
	if Username("alice") != nil || Username("a") == nil || Username("Alice") == nil {
		t.Error("usernames")
	}
	if DomainName("dev.example.com") != nil || DomainName("example") == nil || DomainName("a..b") == nil {
		t.Error("domains")
	}
}
