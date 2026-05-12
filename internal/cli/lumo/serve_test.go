package lumoCmd

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestPrintBanner_HTTPS verifies the startup banner contains the expected
// URL, API key, and cert path for HTTPS mode.
func TestPrintBanner_HTTPS(t *testing.T) {
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	f := serveFlags{addr: "127.0.0.1:8443"}
	printBanner(f, "abc123", "/path/to/cert.pem")

	_ = w.Close()
	os.Stderr = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "https://127.0.0.1:8443/v1") {
		t.Errorf("banner missing base URL, got: %s", output)
	}
	if !strings.Contains(output, "abc123") {
		t.Errorf("banner missing API key, got: %s", output)
	}
	if !strings.Contains(output, "/path/to/cert.pem") {
		t.Errorf("banner missing cert path, got: %s", output)
	}
}

// TestPrintBanner_NoTLS verifies the startup banner uses http:// when
// TLS is disabled.
func TestPrintBanner_NoTLS(t *testing.T) {
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	f := serveFlags{addr: "127.0.0.1:8080", noTLS: true}
	printBanner(f, "key456", "")

	_ = w.Close()
	os.Stderr = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "http://127.0.0.1:8080/v1") {
		t.Errorf("banner missing http URL, got: %s", output)
	}
	if strings.Contains(output, "TLS Cert") {
		t.Errorf("banner should not show TLS cert when disabled, got: %s", output)
	}
}

// TestFlagDefaults verifies the default flag values.
func TestFlagDefaults(t *testing.T) {
	addr, _ := serveCmd.Flags().GetString("addr")
	if addr != "127.0.0.1:8443" {
		t.Errorf("default addr = %q, want 127.0.0.1:8443", addr)
	}

	noTLS, _ := serveCmd.Flags().GetBool("no-tls")
	if noTLS {
		t.Error("default no-tls should be false")
	}
}

// TestResolveAPIKey_Provided verifies that --api-key flag is used directly.
func TestResolveAPIKey_Provided(t *testing.T) {
	f := serveFlags{apiKey: "my-custom-key"}
	key, err := resolveAPIKey(f)
	if err != nil {
		t.Fatalf("resolveAPIKey: %v", err)
	}
	if key != "my-custom-key" {
		t.Errorf("key = %q, want my-custom-key", key)
	}
}

// TestResolveTLS_NoTLS verifies that --no-tls returns empty paths.
func TestResolveTLS_NoTLS(t *testing.T) {
	f := serveFlags{noTLS: true}
	cert, key, err := resolveTLS(f)
	if err != nil {
		t.Fatalf("resolveTLS: %v", err)
	}
	if cert != "" || key != "" {
		t.Errorf("expected empty paths, got cert=%q key=%q", cert, key)
	}
}

// TestResolveTLS_CustomPaths verifies that custom cert/key paths are used.
func TestResolveTLS_CustomPaths(t *testing.T) {
	f := serveFlags{tlsCert: "/custom/cert.pem", tlsKey: "/custom/key.pem"}
	cert, key, err := resolveTLS(f)
	if err != nil {
		t.Fatalf("resolveTLS: %v", err)
	}
	if cert != "/custom/cert.pem" {
		t.Errorf("cert = %q, want /custom/cert.pem", cert)
	}
	if key != "/custom/key.pem" {
		t.Errorf("key = %q, want /custom/key.pem", key)
	}
}
