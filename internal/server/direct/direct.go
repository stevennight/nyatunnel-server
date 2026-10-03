// Package direct provides the self-signed certificate of the optional direct device listener
// (docs/协议.md §4.5). Devices learn its fingerprint over an authenticated session and pin it, so
// no public certificate is needed and the reverse proxy is bypassed.
package direct

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/stevennight/nyatunnel-common/tunnelproto"
)

// LoadOrCreate returns the certificate in dir (direct.crt / direct.key), creating a fresh one on
// first use, and its pin.
func LoadOrCreate(dir string) (tls.Certificate, string, error) {
	crtPath, keyPath := filepath.Join(dir, "direct.crt"), filepath.Join(dir, "direct.key")
	if _, err := os.Stat(crtPath); errors.Is(err, os.ErrNotExist) {
		if err := create(crtPath, keyPath); err != nil {
			return tls.Certificate{}, "", err
		}
	}
	cert, err := tls.LoadX509KeyPair(crtPath, keyPath)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	return cert, tunnelproto.CertSHA256(cert.Certificate[0]), nil
}

func create(crtPath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "NyaTunnel direct"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(20, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return err
	}
	return os.WriteFile(crtPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}
