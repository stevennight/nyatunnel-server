// Package auth holds password hashing, TOTP and random identifier helpers.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters (OWASP: m=19 MiB, t=2, p=1 is the minimum; we use more memory).
const (
	argonTime    = 2
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 1
	argonKeyLen  = 32
	saltBytes    = 16

	maxPasswordBytes = 1024

	// MinPasswordLength is the shortest password accepted.
	MinPasswordLength = 10
)

// ErrWeakPassword is returned for passwords that do not meet the policy.
var ErrWeakPassword = fmt.Errorf("密码至少需要 %d 个字符", MinPasswordLength)

// HashPassword returns "argon2id$v=19$m=…,t=…,p=…$<salt>$<key>".
func HashPassword(password string) (string, error) {
	if len([]rune(password)) < MinPasswordLength {
		return "", ErrWeakPassword
	}
	return HashSecret(password)
}

// HashSecret hashes like HashPassword without the length policy (tunnel access passwords).
func HashSecret(password string) (string, error) {
	if len(password) > maxPasswordBytes {
		return "", errors.New("password is too long")
	}
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks password against an encoded hash in constant time.
func VerifyPassword(encoded, password string) bool {
	if len(password) > maxPasswordBytes {
		return false
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" || parts[1] != fmt.Sprintf("v=%d", argon2.Version) {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[2], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	if m < 8*1024 || m > 1024*1024 || t < 1 || t > 10 || p < 1 || p > 16 {
		return false // refuse parameters that would be weak or a denial of service
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(salt) < 8 {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(want) != argonKeyLen {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, argonKeyLen)
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash lets a login for an unknown user cost as much as a real one.
var dummyHash, _ = HashPassword("not-a-real-password")

// BurnPasswordCheck spends the time of one VerifyPassword, so response times do not reveal whether a
// username exists.
func BurnPasswordCheck(password string) { VerifyPassword(dummyHash, password) }

// NewToken returns prefix + 32 random bytes (base64url). Used for session ids.
func NewToken(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken returns the hex SHA-256 of a high-entropy secret (session ids, enroll codes, recovery
// codes). Only the hash is stored.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

var idEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// NewID returns prefix + 16 random lowercase base32 characters (80 bits), e.g. "tun_k3x…".
func NewID(prefix string) string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return prefix + idEncoding.EncodeToString(b)
}

// NewRecoveryCodes returns n codes like "a3k9-x2mq-7hdp" (60 bits each).
func NewRecoveryCodes(n int) ([]string, error) {
	codes := make([]string, n)
	for i := range codes {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		s := idEncoding.EncodeToString(b)[:12]
		codes[i] = s[:4] + "-" + s[4:8] + "-" + s[8:]
	}
	return codes, nil
}

// NormalizeRecoveryCode makes "A3K9 X2MQ 7HDP" and "a3k9-x2mq-7hdp" the same code.
func NormalizeRecoveryCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(code) {
		if (r >= 'a' && r <= 'z') || (r >= '2' && r <= '7') {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if len(s) != 12 {
		return s
	}
	return s[:4] + "-" + s[4:8] + "-" + s[8:]
}
