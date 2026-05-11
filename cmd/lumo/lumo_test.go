package lumoCmd

import (
	"context"
	"strings"
	"testing"

	common "github.com/major0/proton-cli/api"
	cli "github.com/major0/proton-cli/internal/cli"
	"github.com/spf13/cobra"
)

// mockStore is a minimal SessionStore for testing.
type mockStore struct {
	config  *common.SessionCredentials
	loadErr error
}

func (m *mockStore) Load() (*common.SessionCredentials, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	return m.config, nil
}

func (m *mockStore) Save(_ *common.SessionCredentials) error { return nil }
func (m *mockStore) Delete() error                           { return nil }
func (m *mockStore) List() ([]string, error)                 { return nil, nil }
func (m *mockStore) Switch(_ string) error                   { return nil }

// newTestCmd creates a cobra.Command with a RuntimeContext attached.
func newTestCmd(acctStore common.SessionStore) *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.SetContext(context.Background())
	cli.SetContext(cmd, &cli.RuntimeContext{
		AccountStore: acctStore,
	})
	return cmd
}

// TestRestoreClient_BearerSessionRejected verifies that restoreClient
// returns an error when the account session uses Bearer auth.
func TestRestoreClient_BearerSessionRejected(t *testing.T) {
	cmd := newTestCmd(&mockStore{
		config: &common.SessionCredentials{
			UID:        "test-uid",
			CookieAuth: false,
		},
	})

	_, err := restoreClient(cmd)
	if err == nil {
		t.Fatal("expected error for Bearer session, got nil")
	}
	if !strings.Contains(err.Error(), "cookie-based authentication") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestRestoreClient_NoSession verifies that restoreClient returns an
// error when no account session exists.
func TestRestoreClient_NoSession(t *testing.T) {
	cmd := newTestCmd(&mockStore{
		loadErr: common.ErrKeyNotFound,
	})

	_, err := restoreClient(cmd)
	if err == nil {
		t.Fatal("expected error for missing session, got nil")
	}
	if !strings.Contains(err.Error(), "no active session") {
		t.Fatalf("unexpected error: %v", err)
	}
}
