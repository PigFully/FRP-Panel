// Package certutil generates the agent's self-signed TLS certificate and
// computes the SHA-256 fingerprint the panel pins.
package certutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"
)

// GenerateSelfSigned creates an ECDSA P-256 self-signed certificate valid for
// 10 years with the given hosts as SANs (IPs or DNS names). Returns PEM blocks.
func GenerateSelfSigned(hosts []string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "frpanel-agent"},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// Fingerprint returns "sha256:<hex>" over a DER-encoded certificate.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// FingerprintPEM parses the first CERTIFICATE block of a PEM and returns its
// fingerprint.
func FingerprintPEM(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("no CERTIFICATE block in PEM")
	}
	return Fingerprint(block.Bytes), nil
}

// EqualFingerprint compares two fingerprints case-insensitively, tolerating an
// optional "sha256:" prefix on either side.
func EqualFingerprint(a, b string) bool {
	norm := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		s = strings.TrimPrefix(s, "sha256:")
		return strings.ReplaceAll(s, ":", "")
	}
	return norm(a) != "" && norm(a) == norm(b)
}

// FormatDER is a helper for error messages.
func FormatDER(der []byte) string { return fmt.Sprintf("%d bytes", len(der)) }
