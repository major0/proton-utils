package cli

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"
)

func TestServicePreRunE_ChainsRootAndSetsService(t *testing.T) {
	rootCalled := false
	root := &cobra.Command{
		Use: "root",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			rootCalled = true
			// Simulate what the real root PersistentPreRunE does:
			// set up a RuntimeContext so SetServiceCmd can read it.
			rc := &RuntimeContext{ServiceName: "*"}
			SetContext(cmd, rc)
			return nil
		},
	}

	child := &cobra.Command{Use: "child"}
	root.AddCommand(child)

	fn := ServicePreRunE("drive")
	if err := fn(child, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !rootCalled {
		t.Fatal("root PersistentPreRunE was not called")
	}

	rc := GetContext(child)
	if rc == nil {
		t.Fatal("RuntimeContext not set")
	}
	if rc.ServiceName != "drive" {
		t.Fatalf("expected ServiceName %q, got %q", "drive", rc.ServiceName)
	}
}

func TestServicePreRunE_PropagatesRootError(t *testing.T) {
	wantErr := errors.New("root failed")
	root := &cobra.Command{
		Use: "root",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return wantErr
		},
	}

	child := &cobra.Command{Use: "child"}
	root.AddCommand(child)

	fn := ServicePreRunE("drive")
	err := fn(child, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected error %v, got %v", wantErr, err)
	}
}
