package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"testing/fstest"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func ptr[T any](v T) *T { return &v }

func TestFirstAdminOnlyOnce(t *testing.T) {
	s, ctx := openTest(t), context.Background()
	if err := s.CreateFirstAdmin(ctx, &User{ID: "usr_a", Username: "nya", PasswordHash: "h", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateFirstAdmin(ctx, &User{ID: "usr_b", Username: "evil", PasswordHash: "h", CreatedAt: 1, UpdatedAt: 1}); !errors.Is(err, ErrConflict) {
		t.Fatalf("second setup: %v", err)
	}
	if err := s.CreateUser(ctx, &User{ID: "usr_c", Username: "NYA", PasswordHash: "h", Role: RoleUser}); !errors.Is(err, ErrConflict) {
		t.Fatalf("usernames must be case-insensitive: %v", err)
	}
}

func TestEnrollmentClaimAssignsPresetTunnels(t *testing.T) {
	s, ctx := openTest(t), context.Background()
	s.CreateUser(ctx, &User{ID: "usr_a", Username: "alice", PasswordHash: "h", Role: RoleUser})
	s.CreateUser(ctx, &User{ID: "usr_b", Username: "bob", PasswordHash: "h", Role: RoleUser})
	s.CreateDomain(ctx, &Domain{ID: "dom_1", Name: "Dev.Example.com", AllowUsers: true})
	mine := &Tunnel{ID: "tun_1", UserID: "usr_a", Name: "web", Type: "https", DomainID: ptr("dom_1"), Subdomain: ptr("web"), Host: ptr("WEB.dev.example.com"), LocalIP: "127.0.0.1", LocalPort: 80, Enabled: true}
	theirs := &Tunnel{ID: "tun_2", UserID: "usr_b", Name: "ssh", Type: "tcp", RemotePort: ptr(20022), LocalIP: "127.0.0.1", LocalPort: 22, Enabled: true}
	for _, tu := range []*Tunnel{mine, theirs} {
		if err := s.SaveTunnel(ctx, tu); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CreateEnrollment(ctx, &Enrollment{ID: "enr_1", UserID: "usr_a", CreatedBy: "usr_a", CodeHash: "h1", TunnelIDs: []string{"tun_1", "tun_2"}, ExpiresAt: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UsableEnrollment(ctx, "h1", 100); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired enrollment usable: %v", err)
	}
	e, err := s.UsableEnrollment(ctx, "h1", 50)
	if err != nil {
		t.Fatal(err)
	}
	d := &Device{ID: "dev_1", UserID: e.UserID, Name: "pc", PublicKey: []byte("k")}
	if err := s.ClaimEnrollment(ctx, e.ID, d, 60); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimEnrollment(ctx, e.ID, &Device{ID: "dev_2", UserID: "usr_a", PublicKey: []byte("k2")}, 61); !errors.Is(err, ErrNotFound) {
		t.Fatalf("code reused: %v", err)
	}
	got, _ := s.DeviceTunnels(ctx, "dev_1")
	if len(got) != 1 || got[0].ID != "tun_1" || *got[0].Host != "web.dev.example.com" {
		t.Fatalf("assigned %+v; another user's tunnel must not be handed over", got)
	}
	if err := s.RevokeDevice(ctx, "dev_1", 70); err != nil {
		t.Fatal(err)
	}
	if tu, _ := s.TunnelByID(ctx, "tun_1"); tu.DeviceID != nil {
		t.Fatal("revoked device keeps its tunnels")
	}
}

func TestTunnelUniqueness(t *testing.T) {
	s, ctx := openTest(t), context.Background()
	s.CreateUser(ctx, &User{ID: "usr_a", Username: "alice", PasswordHash: "h", Role: RoleUser})
	a := &Tunnel{ID: "tun_1", UserID: "usr_a", Name: "a", Type: "tcp", RemotePort: ptr(20000), LocalIP: "127.0.0.1", LocalPort: 1}
	if err := s.SaveTunnel(ctx, a); err != nil {
		t.Fatal(err)
	}
	clash := &Tunnel{ID: "tun_2", UserID: "usr_a", Name: "b", Type: "tcp", RemotePort: ptr(20000), LocalIP: "127.0.0.1", LocalPort: 1}
	if err := s.SaveTunnel(ctx, clash); !errors.Is(err, ErrConflict) {
		t.Fatalf("port clash: %v", err)
	}
	udp := &Tunnel{ID: "tun_3", UserID: "usr_a", Name: "c", Type: "udp", RemotePort: ptr(20000), LocalIP: "127.0.0.1", LocalPort: 1}
	if err := s.SaveTunnel(ctx, udp); err != nil {
		t.Fatalf("tcp and udp may share a number: %v", err)
	}
	a.LocalPort = 2
	if err := s.SaveTunnel(ctx, a); err != nil {
		t.Fatalf("update: %v", err)
	}
}

func TestTCPUDPPortsArePerProtocol(t *testing.T) {
	s, ctx := openTest(t), context.Background()
	s.CreateUser(ctx, &User{ID: "usr_a", Username: "alice", PasswordHash: "h", Role: RoleUser})
	save := func(id, typ string, port int) error {
		return s.SaveTunnel(ctx, &Tunnel{ID: id, UserID: "usr_a", Name: id, Type: typ, RemotePort: ptr(port), LocalIP: "127.0.0.1", LocalPort: 1})
	}
	if err := save("tun_1", TypeTCP, 30000); err != nil {
		t.Fatal(err)
	}
	if err := save("tun_2", TypeTCPUDP, 30000); !errors.Is(err, ErrConflict) {
		t.Fatalf("tcpudp over a tcp port: %v", err)
	}
	if err := save("tun_3", TypeTCPUDP, 30001); err != nil {
		t.Fatal(err)
	}
	if err := save("tun_4", TypeUDP, 30001); !errors.Is(err, ErrConflict) {
		t.Fatalf("udp over a tcpudp port: %v", err)
	}
	if err := save("tun_5", TypeUDP, 30000); err != nil {
		t.Fatalf("udp next to a tcp tunnel: %v", err)
	}
	tcp, _ := s.UsedPorts(ctx, TypeTCP)
	udp, _ := s.UsedPorts(ctx, TypeUDP)
	if tcp[30001] != "tun_3" || udp[30001] != "tun_3" || udp[30000] != "tun_5" {
		t.Fatalf("used ports tcp=%v udp=%v", tcp, udp)
	}
}

// The tcpudp migration rebuilds the tunnels table; existing rows must survive it.
func TestMigrationKeepsTunnels(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", dsn(":memory:"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	s := &Store{db: db}
	old := fstest.MapFS{}
	for _, name := range []string{"0001_init.sql", "0002_policies_traffic.sql", "0003_requests_quota_domains_channels.sql"} {
		b, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		old["migrations/"+name] = &fstest.MapFile{Data: b}
	}
	if err := s.migrate(ctx, old); err != nil {
		t.Fatal(err)
	}
	s.CreateUser(ctx, &User{ID: "usr_a", Username: "alice", PasswordHash: "h", Role: RoleUser})
	s.CreateDomain(ctx, &Domain{ID: "dom_1", Name: "t.example.com"})
	if _, err := db.ExecContext(ctx, `INSERT INTO tunnels (id, user_id, name, type, domain_id, subdomain, host, local_ip, local_port, access_policy,
		bandwidth_kbps, created_at, updated_at) VALUES ('tun_1', 'usr_a', 'web', 'https', 'dom_1', 'web', 'web.t.example.com', '127.0.0.1', 80, 'password', 800, 1, 2)`); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx, migrationFiles); err != nil {
		t.Fatal(err)
	}
	got, err := s.TunnelByID(ctx, "tun_1")
	if err != nil || *got.Host != "web.t.example.com" || got.AccessPolicy != "password" || got.BandwidthKbps != 800 || got.UpdatedAt != 2 {
		t.Fatalf("after migration: %+v %v", got, err)
	}
}

func TestRecoveryCodesAreSingleUse(t *testing.T) {
	s, ctx := openTest(t), context.Background()
	s.CreateUser(ctx, &User{ID: "usr_a", Username: "alice", PasswordHash: "h", Role: RoleUser})
	if err := s.SetUserTOTP(ctx, "usr_a", []byte("sealed"), []string{"x", "y"}, 1); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.UseRecoveryCode(ctx, "usr_a", "x", 2); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, _ := s.UseRecoveryCode(ctx, "usr_a", "x", 3); ok {
		t.Fatal("recovery code used twice")
	}
	u, _ := s.UserByID(ctx, "usr_a")
	if !u.TOTPEnabled() || len(u.RecoveryCodes) != 1 {
		t.Fatalf("user %+v", u)
	}
}
