package compute

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Fingerprint is the SHA-256 of a certificate, as lowercase hex.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

func normalizePin(pin string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(pin), ":", ""))
}

// pinnedTLS trusts exactly the certificate whose fingerprint is pin, which
// suits a self-signed certificate made by `compute serve --tls`.
func pinnedTLS(pin string) *tls.Config {
	want := normalizePin(pin)
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // the pin below replaces CA verification
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 || Fingerprint(raw[0]) != want {
				return errors.New("compute: certificate does not match the pinned fingerprint")
			}
			return nil
		},
	}
}

// LoadOrCreateCert returns a self-signed certificate kept in home, creating it
// on first use, and its fingerprint.
func LoadOrCreateCert(home string) (tls.Certificate, string, error) {
	crt, key := filepath.Join(home, "compute-tls.crt"), filepath.Join(home, "compute-tls.key")
	if cert, err := tls.LoadX509KeyPair(crt, key); err == nil {
		return cert, Fingerprint(cert.Certificate[0]), nil
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "compa-compute"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := writePEM(crt, "CERTIFICATE", der, 0o644); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := writePEM(key, "EC PRIVATE KEY", keyDER, 0o600); err != nil {
		return tls.Certificate{}, "", err
	}
	cert, err := tls.LoadX509KeyPair(crt, key)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("compute: load new certificate: %w", err)
	}
	return cert, Fingerprint(der), nil
}

func writePEM(path, typ string, der []byte, mode os.FileMode) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), mode)
}
