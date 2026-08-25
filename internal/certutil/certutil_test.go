package certutil

import (
	"crypto/tls"
	"testing"
)

func TestGenerateAndFingerprint(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSigned([]string{"203.0.113.10", "example.com"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		t.Fatalf("keypair does not load: %v", err)
	}
	fp, err := FingerprintPEM(certPEM)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if len(fp) != len("sha256:")+64 {
		t.Errorf("fingerprint length unexpected: %q", fp)
	}
	if !EqualFingerprint(fp, fp) {
		t.Error("fingerprint should equal itself")
	}
	if !EqualFingerprint("SHA256:"+fp[7:], fp[7:]) {
		t.Error("prefix/case-insensitive compare failed")
	}
	if EqualFingerprint("sha256:aa", "sha256:bb") {
		t.Error("different fingerprints must not be equal")
	}
	if EqualFingerprint("", "") {
		t.Error("empty fingerprints must not compare equal")
	}
}
