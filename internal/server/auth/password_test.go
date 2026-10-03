package auth

import (
	"strings"
	"testing"
)

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "argon2id$v=19$") {
		t.Fatalf("hash format: %s", h)
	}
	if !VerifyPassword(h, "correct horse battery") || VerifyPassword(h, "correct horse batterY") {
		t.Fatal("verify mismatch")
	}
	if _, err := HashPassword("short"); err != ErrWeakPassword {
		t.Fatalf("weak password: %v", err)
	}
}

func TestVerifyRejectsHostileParameters(t *testing.T) {
	for _, h := range []string{
		"",
		"pbkdf2-sha256$1$x$y",
		"argon2id$v=19$m=4294967295,t=1,p=1$c2FsdHNhbHQ$" + strings.Repeat("A", 43),
		"argon2id$v=19$m=65536,t=1000,p=1$c2FsdHNhbHQ$" + strings.Repeat("A", 43),
	} {
		if VerifyPassword(h, "whatever-password") {
			t.Fatalf("accepted %q", h)
		}
	}
}

func TestIDsAndRecoveryCodes(t *testing.T) {
	id := NewID("tun_")
	if len(id) != 20 || !strings.HasPrefix(id, "tun_") {
		t.Fatalf("id %q", id)
	}
	codes, err := NewRecoveryCodes(10)
	if err != nil || len(codes) != 10 {
		t.Fatal(codes, err)
	}
	for _, c := range codes {
		if NormalizeRecoveryCode(strings.ToUpper(strings.ReplaceAll(c, "-", " "))) != c {
			t.Fatalf("normalize %q", c)
		}
	}
}
