package api

import (
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stevennight/nyatunnel-common/tunnelproto"
)

func TestTCPAndUDPOnOnePort(t *testing.T) {
	p := newPolicyEnv(t)
	// One local service answering on the same port number for TCP and UDP.
	tl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tl.Close()
	localPort := tl.Addr().(*net.TCPAddr).Port
	ul, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort)))
	if err != nil {
		t.Skip("UDP port busy:", err)
	}
	defer ul.Close()
	go func() {
		for {
			c, err := tl.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := ul.ReadFrom(buf)
			if err != nil {
				return
			}
			ul.WriteTo(append([]byte("udp:"), buf[:n]...), addr)
		}
	}()

	port := freePort(t)
	p.admin.must("POST", "/api/v1/port-pools", map[string]any{"proto": "tcp", "start": port, "end": port}, nil)
	var errOut struct{ Error string }
	body := map[string]any{"userId": p.adminID, "deviceId": p.deviceID, "name": "game", "type": "tcpudp",
		"localIp": "127.0.0.1", "localPort": localPort, "enabled": true}
	if code := p.admin.do("POST", "/api/v1/tunnels", body, &errOut); code != 400 || errOut.Error != "pool_exhausted" {
		t.Fatalf("tcpudp without a UDP pool: %d %s", code, errOut.Error)
	}
	p.admin.must("POST", "/api/v1/port-pools", map[string]any{"proto": "udp", "start": port, "end": port}, nil)
	var out struct{ Tunnel tunnelView }
	p.admin.must("POST", "/api/v1/tunnels", body, &out)
	if out.Tunnel.RemotePort == nil || *out.Tunnel.RemotePort != port || out.Tunnel.PublicURL != "tcp+udp://127.0.0.1:"+strconv.Itoa(port) {
		t.Fatalf("tunnel %+v", out.Tunnel)
	}
	p.dev.waitConfig(func(c tunnelproto.Config) bool {
		for _, tu := range c.Tunnels {
			if tu.ID == out.Tunnel.ID {
				return tu.Type == tunnelproto.TunnelTCPUDP
			}
		}
		return false
	})
	// The port is taken for both protocols now.
	for _, typ := range []string{"tcp", "udp"} {
		if code := p.admin.do("POST", "/api/v1/tunnels", map[string]any{"userId": p.adminID, "name": "x" + typ, "type": typ,
			"localIp": "127.0.0.1", "localPort": 1, "remotePort": port, "enabled": true}, &errOut); code != 400 || errOut.Error != "port_taken" {
			t.Fatalf("%s on a tcpudp port: %d %s", typ, code, errOut.Error)
		}
	}

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	c.Write([]byte("tcp?"))
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "tcp?" {
		t.Fatalf("tcp: %q %v", buf, err)
	}
	u, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	u.SetDeadline(time.Now().Add(5 * time.Second))
	u.Write([]byte("ping"))
	ubuf := make([]byte, 64)
	n, err := u.Read(ubuf)
	if err != nil || string(ubuf[:n]) != "udp:ping" {
		t.Fatalf("udp: %q %v", ubuf[:n], err)
	}
}
