package lumoCmd

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"

	"pgregory.net/rapid"
)

// parseCertFromFile reads and parses the first PEM certificate from path.
func parseCertFromFile(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path) //nolint:gosec // test helper
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	return x509.ParseCertificate(block.Bytes)
}

// TestTLSCert_SANAndAlgorithm_Property verifies that generated certs are
// self-signed, use ECDSA P-256, and contain both localhost and 127.0.0.1
// in their Subject Alternative Names.
//
// Feature: lumo-serve, Property 8: TLS cert SAN and algorithm invariants
//
// **Validates: Requirements 3.1, 3.4**
func TestTLSCert_SANAndAlgorithm_Property(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir := t.TempDir()
		certPath, _, err := LoadOrGenerateTLS(dir)
		if err != nil {
			rt.Fatalf("LoadOrGenerateTLS: %v", err)
		}

		cert, err := parseCertFromFile(certPath)
		if err != nil {
			rt.Fatalf("parseCert: %v", err)
		}

		// Self-signed: issuer == subject.
		if cert.Issuer.CommonName != cert.Subject.CommonName {
			rt.Fatalf("not self-signed: issuer=%q subject=%q", cert.Issuer.CommonName, cert.Subject.CommonName)
		}

		// ECDSA P-256.
		pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
		if !ok {
			rt.Fatal("public key is not ECDSA")
		}
		if pub.Curve != elliptic.P256() {
			rt.Fatalf("curve = %v, want P-256", pub.Curve.Params().Name)
		}

		// SANs contain localhost.
		hasLocalhost := false
		for _, name := range cert.DNSNames {
			if name == "localhost" {
				hasLocalhost = true
			}
		}
		if !hasLocalhost {
			rt.Fatalf("DNSNames %v does not contain localhost", cert.DNSNames)
		}

		// SANs contain 127.0.0.1.
		hasLoopback := false
		for _, ip := range cert.IPAddresses {
			if ip.String() == "127.0.0.1" {
				hasLoopback = true
			}
		}
		if !hasLoopback {
			rt.Fatalf("IPAddresses %v does not contain 127.0.0.1", cert.IPAddresses)
		}
	})
}

// TestTLSCert_PEMRoundTrip_Property verifies that PEM-encoding then
// PEM-decoding the generated cert and key produces byte-identical DER content.
//
// Feature: lumo-serve, Property 9: TLS cert PEM round-trip
//
// **Validates: Requirements 3.2**
func TestTLSCert_PEMRoundTrip_Property(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir := t.TempDir()
		certPath, keyPath, err := LoadOrGenerateTLS(dir)
		if err != nil {
			rt.Fatalf("LoadOrGenerateTLS: %v", err)
		}

		// Cert round-trip.
		certPEM, err := os.ReadFile(certPath) //nolint:gosec // test code
		if err != nil {
			rt.Fatalf("ReadFile cert: %v", err)
		}
		certBlock, rest := pem.Decode(certPEM)
		if certBlock == nil {
			rt.Fatal("failed to decode cert PEM")
			return
		}
		if len(rest) != 0 {
			rt.Fatalf("unexpected trailing data in cert PEM: %d bytes", len(rest))
		}
		// Re-encode and decode to verify round-trip.
		reEncoded := pem.EncodeToMemory(certBlock)
		reBlock, _ := pem.Decode(reEncoded)
		if reBlock == nil {
			rt.Fatal("failed to re-decode cert PEM")
			return
		}
		if string(reBlock.Bytes) != string(certBlock.Bytes) {
			rt.Fatal("cert DER mismatch after PEM round-trip")
		}

		// Key round-trip.
		keyPEM, err := os.ReadFile(keyPath) //nolint:gosec // test code
		if err != nil {
			rt.Fatalf("ReadFile key: %v", err)
		}
		keyBlock, rest := pem.Decode(keyPEM)
		if keyBlock == nil {
			rt.Fatal("failed to decode key PEM")
			return
		}
		if len(rest) != 0 {
			rt.Fatalf("unexpected trailing data in key PEM: %d bytes", len(rest))
		}
		reEncodedKey := pem.EncodeToMemory(keyBlock)
		reKeyBlock, _ := pem.Decode(reEncodedKey)
		if reKeyBlock == nil {
			rt.Fatal("failed to re-decode key PEM")
			return
		}
		if string(reKeyBlock.Bytes) != string(keyBlock.Bytes) {
			rt.Fatal("key DER mismatch after PEM round-trip")
		}
	})
}
