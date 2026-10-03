package secrets

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestSealAndOpenRoundTrip(t *testing.T) {
	box, err := New(key(1))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := box.Seal([]byte(`{"token":"x"}`))
	b, _ := box.Seal([]byte(`{"token":"x"}`))
	if bytes.Equal(a, b) {
		t.Fatal("sealing the same value twice must differ (fresh nonce)")
	}
	if bytes.Contains(a, []byte("token")) {
		t.Fatal("ciphertext leaks the plaintext")
	}
	got, err := box.Open(a)
	if err != nil || string(got) != `{"token":"x"}` {
		t.Fatalf("Open = %q, %v", got, err)
	}
}

func TestWrongKeyAndDamagedDataAreErrors(t *testing.T) {
	a, _ := New(key(1))
	b, _ := New(key(2))
	sealed, _ := a.Seal([]byte("secret"))
	if _, err := b.Open(sealed); err == nil {
		t.Fatal("opened with the wrong key")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := a.Open(sealed); err == nil {
		t.Fatal("opened damaged data")
	}
	if _, err := a.Open([]byte("short")); err == nil {
		t.Fatal("opened a too short value")
	}
}

func TestKeyMustBe32Bytes(t *testing.T) {
	if _, err := New(make([]byte, 16)); err == nil {
		t.Fatal("accepted a 16 byte key")
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "key")
	os.WriteFile(good, []byte(base64.StdEncoding.EncodeToString(key(7))+"\n"), 0o600)
	box, err := LoadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	if s, err := box.Seal([]byte("x")); err != nil || len(s) == 0 {
		t.Fatalf("seal: %v", err)
	}

	bad := filepath.Join(dir, "bad")
	os.WriteFile(bad, []byte("not base64 !!"), 0o600)
	if _, err := LoadFile(bad); err == nil {
		t.Fatal("accepted a key file that is not base64")
	}
	if _, err := LoadFile(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("accepted a missing key file")
	}
}

func TestWithoutAKeyNothingIsSealed(t *testing.T) {
	box, err := LoadFile("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := box.Seal([]byte("x")); !errors.Is(err, ErrNoKey) {
		t.Fatalf("Seal without key = %v", err)
	}
	if _, err := box.Open([]byte("whatever-whatever-whatever")); !errors.Is(err, ErrNoKey) {
		t.Fatalf("Open without key = %v", err)
	}
}

func TestLoadOrCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "secrets.key")
	a, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := a.Seal([]byte("totp"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreate(path) // second start reads the same key
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := b.Open(sealed); err != nil || string(plain) != "totp" {
		t.Fatalf("reopen: %q %v", plain, err)
	}
}
