package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TOTP follows RFC 6238 with the defaults every authenticator app assumes: HMAC-SHA1, six digits, 30-second steps.
const (
	totpPeriod   = 30 * time.Second
	totpDigits   = 6
	totpKeyBytes = 20 // RFC 4226 recommends a 160-bit key
	totpSkew     = 1  // accept the step before and after, for clock drift
)

var totpBase32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a fresh base32 secret (uppercase, no padding) for a new authenticator enrollment.
func NewTOTPSecret() (string, error) {
	b := make([]byte, totpKeyBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return totpBase32.EncodeToString(b), nil
}

// TOTPCode returns the six-digit code for secret at time t.
func TOTPCode(secret string, t time.Time) (string, error) {
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return "", err
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(t.Unix()/int64(totpPeriod/time.Second)))
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, value%1_000_000), nil
}

// VerifyTOTP reports whether code is valid for secret at t. One step of clock drift either way is accepted; every
// window is checked even after a match so the result does not depend on which one matched.
func VerifyTOTP(secret, code string, t time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	ok := false
	for delta := -totpSkew; delta <= totpSkew; delta++ {
		want, err := TOTPCode(secret, t.Add(time.Duration(delta)*totpPeriod))
		if err != nil {
			return false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			ok = true
		}
	}
	return ok
}

// TOTPAuthURL is the otpauth:// URI an authenticator app scans.
func TOTPAuthURL(issuer, account, secret string) string {
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", strconv.Itoa(totpDigits))
	q.Set("period", strconv.Itoa(int(totpPeriod/time.Second)))
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + q.Encode()
}

// decodeTOTPSecret accepts what authenticator apps display: base32, case-insensitive, spaces allowed.
func decodeTOTPSecret(secret string) ([]byte, error) {
	s := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	key, err := totpBase32.DecodeString(s)
	if err != nil || len(key) < 10 {
		return nil, errors.New("invalid TOTP secret")
	}
	return key, nil
}
