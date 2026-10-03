// Package secrets seals values that must not sit in the database in the clear (TOTP secrets).
// The key lives in a file outside the data directory (NYATUNNEL_SECRETS_KEY_FILE), so a stolen database alone reveals nothing.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Box seals and opens values with AES-256-GCM. The zero value (no key) refuses to seal anything that is not empty.
type Box struct{ aead cipher.AEAD }

var ErrNoKey = errors.New("secrets: no key configured (set NYATUNNEL_SECRETS_KEY_FILE)")

// New builds a box from a 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("secrets: key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// LoadFile reads a base64-encoded 32-byte key (as written by `openssl rand -base64 32`). An empty path gives a box
// without a key.
func LoadFile(path string) (*Box, error) {
	if path == "" {
		return &Box{}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("secrets key file: %w", err)
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("secrets key file is not base64: %w", err)
	}
	return New(key)
}

// Seal returns nonce || ciphertext. Sealing needs a key.
func (b *Box) Seal(plain []byte) ([]byte, error) {
	if b == nil || b.aead == nil {
		return nil, ErrNoKey
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plain, nil), nil
}

// Ready reports whether a key is loaded. Without one the box can open sealed values that are empty, but it refuses
// to seal anything: features that need encryption must be disabled instead of storing plaintext.
func (b *Box) Ready() bool { return b != nil && b.aead != nil }

// Open reverses Seal. A wrong key or damaged data is an error, never garbage.
func (b *Box) Open(sealed []byte) ([]byte, error) {
	if b == nil || b.aead == nil {
		return nil, ErrNoKey
	}
	n := b.aead.NonceSize()
	if len(sealed) < n+b.aead.Overhead() {
		return nil, errors.New("secrets: sealed value too short")
	}
	return b.aead.Open(nil, sealed[:n], sealed[n:], nil)
}

// LoadOrCreate is LoadFile, but writes a fresh random key (mode 0600) when path does not exist yet.
// Keep a backup of the file: without it, sealed values (TOTP secrets) cannot be opened.
func LoadOrCreate(path string) (*Box, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("secrets key file: %w", err)
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, fmt.Errorf("secrets key file: %w", err)
		}
		_, werr := f.WriteString(base64.StdEncoding.EncodeToString(key) + "\n")
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return nil, fmt.Errorf("secrets key file: %w", werr)
		}
	}
	return LoadFile(path)
}
