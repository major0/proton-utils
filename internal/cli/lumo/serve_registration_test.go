package lumoCmd

import "testing"

// TestServeCommandRegistration verifies that the serve command is
// registered with the expected flags.
func TestServeCommandRegistration(t *testing.T) {
	if serveCmd.Use != "serve" {
		t.Fatalf("Use = %q, want 'serve'", serveCmd.Use)
	}
	if serveCmd.RunE == nil {
		t.Fatal("serve command has no RunE")
	}

	flags := []string{"addr", "api-key", "new-api-key", "tls-cert", "tls-key", "no-tls"}
	for _, name := range flags {
		if serveCmd.Flags().Lookup(name) == nil {
			t.Errorf("missing flag: --%s", name)
		}
	}
}

// TestServeCommand_RequiresSession verifies that runServe uses
// cli.RestoreSession which requires a valid session store.
// This is tested indirectly via the restoreClient tests in lumo_test.go.
