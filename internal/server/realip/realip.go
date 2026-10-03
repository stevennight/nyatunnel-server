// Package realip finds the visitor's address behind trusted reverse proxies.
package realip

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Resolver knows which peers are trusted proxies.
type Resolver struct {
	Trusted []netip.Prefix
}

func (r *Resolver) trusted(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range r.Trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func peer(req *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	a, err := netip.ParseAddr(host)
	return a.Unmap(), err == nil
}

// FromPeer reports whether the direct peer is a trusted proxy.
func (r *Resolver) FromPeer(req *http.Request) bool {
	a, ok := peer(req)
	return ok && r.trusted(a)
}

// ClientIP returns the caller's address. X-Forwarded-For is only believed when the direct peer is a
// trusted proxy, and is read right to left skipping trusted hops, so a client cannot spoof an address.
func (r *Resolver) ClientIP(req *http.Request) string {
	remote, ok := peer(req)
	if !ok {
		return req.RemoteAddr
	}
	if !r.trusted(remote) {
		return remote.String()
	}
	hops := strings.Split(strings.Join(req.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			continue
		}
		if !r.trusted(a) {
			return a.Unmap().String()
		}
	}
	return remote.String()
}

// Proto returns "https" when a trusted proxy says the visitor used TLS (or the request itself is TLS).
func (r *Resolver) Proto(req *http.Request) string {
	if req.TLS != nil {
		return "https"
	}
	if r.FromPeer(req) && strings.EqualFold(req.Header.Get("X-Forwarded-Proto"), "https") {
		return "https"
	}
	return "http"
}
