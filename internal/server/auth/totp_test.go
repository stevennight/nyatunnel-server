package auth

import (
	"strings"
	"testing"
	"time"
)

// The secret and codes from RFC 6238 appendix B (the test vectors use the ASCII key "12345678901234567890").
const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestTOTPCodeMatchesRFC6238(t *testing.T) {
	cases := []struct {
		unix int64
		want string
	}{
		{59, "287082"},          // 94287082, truncated to six digits
		{1111111109, "081804"},  // 07081804
		{1111111111, "050471"},  // 14050471
		{1234567890, "005924"},  // 89005924
		{2000000000, "279037"},  // 69279037
		{20000000000, "353130"}, // 65353130
	}
	for _, c := range cases {
		got, err := TOTPCode(rfcSecret, time.Unix(c.unix, 0))
		if err != nil {
			t.Fatalf("TOTPCode(%d): %v", c.unix, err)
		}
		if got != c.want {
			t.Errorf("TOTPCode(%d) = %s, want %s", c.unix, got, c.want)
		}
	}
}

func TestVerifyTOTPAcceptsOneStepOfDrift(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	previous, _ := TOTPCode(rfcSecret, at.Add(-totpPeriod))
	current, _ := TOTPCode(rfcSecret, at)
	next, _ := TOTPCode(rfcSecret, at.Add(totpPeriod))
	twoStepsOn, _ := TOTPCode(rfcSecret, at.Add(2*totpPeriod))

	for _, code := range []string{previous, current, next} {
		if !VerifyTOTP(rfcSecret, code, at) {
			t.Errorf("code %s should be accepted at %v", code, at)
		}
	}
	if VerifyTOTP(rfcSecret, twoStepsOn, at) {
		t.Error("a code two steps away must not be accepted")
	}
}

func TestVerifyTOTPRejectsMalformedInput(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	current, _ := TOTPCode(rfcSecret, at)
	for _, code := range []string{"", "12345", "1234567", "12345a", "abcdef", "12 456"} {
		if VerifyTOTP(rfcSecret, code, at) {
			t.Errorf("code %q must be rejected", code)
		}
	}
	if VerifyTOTP("not-base32!!", current, at) {
		t.Error("a broken secret must never verify")
	}
	// Surrounding spaces are tolerated: people paste codes out of messages.
	if !VerifyTOTP(rfcSecret, " "+current+" ", at) {
		t.Error("spaces around a valid code should be ignored")
	}
	// Spaces are tolerated the way authenticator apps show secrets, not as part of the code.
	if !VerifyTOTP("GEZD GNBV GY3T QOJQ GEZD GNBV GY3T QOJQ", current, at) {
		t.Error("a secret with spaces should still decode")
	}
}

func TestNewTOTPSecretIsUsable(t *testing.T) {
	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != 32 || strings.ToUpper(secret) != secret || strings.Contains(secret, "=") {
		t.Fatalf("secret = %q, want 32 uppercase base32 characters without padding", secret)
	}
	code, err := TOTPCode(secret, time.Now())
	if err != nil || len(code) != totpDigits {
		t.Fatalf("code = %q, %v", code, err)
	}
	if !VerifyTOTP(secret, code, time.Now()) {
		t.Error("a fresh secret must verify its own code")
	}
}

func TestTOTPAuthURL(t *testing.T) {
	u := TOTPAuthURL("NyaSmsForward", "admin", rfcSecret)
	for _, want := range []string{"otpauth://totp/NyaSmsForward:admin?", "secret=" + rfcSecret, "issuer=NyaSmsForward", "digits=6", "period=30", "algorithm=SHA1"} {
		if !strings.Contains(u, want) {
			t.Errorf("URL %q is missing %q", u, want)
		}
	}
}
