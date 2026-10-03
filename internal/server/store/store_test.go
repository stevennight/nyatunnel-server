package store

import (
	"context"
	"errors"
	"testing"
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
