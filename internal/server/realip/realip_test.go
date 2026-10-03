package realip

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientIP(t *testing.T) {
	r := &Resolver{Trusted: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("10.0.0.0/8")}}
	cases := []struct {
		remote, xff, want string
	}{
		{"203.0.113.9:1234", "1.2.3.4", "203.0.113.9"},              // untrusted peer: header ignored
		{"127.0.0.1:5555", "1.2.3.4", "1.2.3.4"},                    // trusted proxy
		{"127.0.0.1:5555", "6.6.6.6, 1.2.3.4, 10.1.1.1", "1.2.3.4"}, // rightmost untrusted hop wins
		{"127.0.0.1:5555", "", "127.0.0.1"},                         // no header
		{"[::ffff:127.0.0.1]:5555", "2001:db8::1", "2001:db8::1"},   // mapped peer address
		{"127.0.0.1:5555", "garbage, 1.2.3.4", "1.2.3.4"},           // junk is skipped
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = c.remote
		if c.xff != "" {
			req.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := r.ClientIP(req); got != c.want {
			t.Errorf("ClientIP(%s, %q) = %s, want %s", c.remote, c.xff, got, c.want)
		}
	}
}
