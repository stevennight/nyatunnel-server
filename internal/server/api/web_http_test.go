package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebTunnelHTTPSOptions(t *testing.T) {
	p := newPolicyEnv(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "proto="+r.Header.Get("X-Forwarded-Proto"))
	}))
	defer backend.Close()

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	callEdge := func(host, xfp string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("GET", p.ingress.URL+"/ping?a=1", nil)
		req.Host = host
		if xfp != "" {
			req.Header.Set("X-Forwarded-Proto", xfp)
		}
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// 1. Default (enableHttps=true, forceHttps=true): HTTP redirects to HTTPS.
	var t1 struct{ Tunnel tunnelView }
	p.admin.must("POST", "/api/v1/tunnels", map[string]any{
		"userId": p.adminID, "deviceId": p.deviceID, "name": "strict", "type": "https",
		"domainId": p.domainID, "subdomain": "strict",
		"localIp": "127.0.0.1", "localPort": localPort(t, backend.URL), "enabled": true,
	}, &t1)
	if !t1.Tunnel.EnableHTTPS || !t1.Tunnel.ForceHTTPS || t1.Tunnel.PublicURL != "https://strict.t.example.com" {
		t.Fatalf("unexpected defaults: %+v", t1.Tunnel)
	}
	if !p.edge.AllowCertificate("strict.t.example.com") {
		t.Fatal("expected AllowCertificate=true when enableHttps=true")
	}
	resp := callEdge("strict.t.example.com", "http")
	resp.Body.Close()
	if resp.StatusCode != http.StatusPermanentRedirect || resp.Header.Get("Location") != "https://strict.t.example.com/ping?a=1" {
		t.Fatalf("expected 308 redirect to https, got %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp = callEdge("strict.t.example.com", "https")
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(b) != "proto=https" {
		t.Fatalf("expected 200 proto=https, got %d %q", resp.StatusCode, string(b))
	}

	// 2. HTTP + HTTPS (enableHttps=true, forceHttps=false): both HTTP and HTTPS work directly.
	var t2 struct{ Tunnel tunnelView }
	p.admin.must("POST", "/api/v1/tunnels", map[string]any{
		"userId": p.adminID, "deviceId": p.deviceID, "name": "both", "type": "https",
		"domainId": p.domainID, "subdomain": "both",
		"localIp": "127.0.0.1", "localPort": localPort(t, backend.URL), "enabled": true,
		"enableHttps": true, "forceHttps": false,
	}, &t2)
	if !t2.Tunnel.EnableHTTPS || t2.Tunnel.ForceHTTPS || t2.Tunnel.PublicURL != "https://both.t.example.com" {
		t.Fatalf("unexpected dual-mode tunnel: %+v", t2.Tunnel)
	}
	resp = callEdge("both.t.example.com", "http")
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(b) != "proto=http" {
		t.Fatalf("expected 200 proto=http, got %d %q", resp.StatusCode, string(b))
	}
	resp = callEdge("both.t.example.com", "https")
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(b) != "proto=https" {
		t.Fatalf("expected 200 proto=https, got %d %q", resp.StatusCode, string(b))
	}

	// 3. HTTP-only (enableHttps=false): publicUrl is http://, no cert allowed, HTTPS redirects to HTTP.
	var t3 struct{ Tunnel tunnelView }
	p.admin.must("POST", "/api/v1/tunnels", map[string]any{
		"userId": p.adminID, "deviceId": p.deviceID, "name": "plain", "type": "https",
		"domainId": p.domainID, "subdomain": "plain",
		"localIp": "127.0.0.1", "localPort": localPort(t, backend.URL), "enabled": true,
		"enableHttps": false, "forceHttps": true, // forceHttps must be normalized to false
	}, &t3)
	if t3.Tunnel.EnableHTTPS || t3.Tunnel.ForceHTTPS || t3.Tunnel.PublicURL != "http://plain.t.example.com" {
		t.Fatalf("unexpected http-only tunnel: %+v", t3.Tunnel)
	}
	if p.edge.AllowCertificate("plain.t.example.com") {
		t.Fatal("expected AllowCertificate=false when enableHttps=false")
	}
	resp = callEdge("plain.t.example.com", "http")
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(b) != "proto=http" {
		t.Fatalf("expected 200 proto=http, got %d %q", resp.StatusCode, string(b))
	}
	resp = callEdge("plain.t.example.com", "https")
	resp.Body.Close()
	if resp.StatusCode != http.StatusPermanentRedirect || resp.Header.Get("Location") != "http://plain.t.example.com/ping?a=1" {
		t.Fatalf("expected 308 redirect to http, got %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}
