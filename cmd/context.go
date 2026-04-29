package cli

import (
	"context"
	"time"

	"github.com/ProtonMail/go-proton-api"
	common "github.com/major0/proton-cli/api"
	"github.com/spf13/cobra"
)

// contextKey is the unexported key type for storing RuntimeContext in
// cobra's command context. Using a private type prevents collisions.
type contextKey struct{}

// RuntimeContext bundles the per-invocation state that was previously
// spread across package-level variables. Stored on the cobra command
// context by PersistentPreRunE, retrieved by subcommands via GetContext.
type RuntimeContext struct {
	// Timeout is the global request timeout duration (from --timeout).
	Timeout time.Duration

	// DebugHTTP is true when verbosity >= 3, enabling HTTP debug logging.
	DebugHTTP bool

	// ProtonOpts holds the base Proton API options (host, app version, user agent).
	ProtonOpts []proton.Option

	// SessionStore handles loading/saving session data for the current service.
	SessionStore common.SessionStore

	// AccountStore handles loading/saving the account session data.
	// Used by RestoreServiceSession as the fork source.
	AccountStore common.SessionStore

	// CookieStore handles loading/saving cookie-based session data.
	CookieStore common.SessionStore

	// Account holds the current --account flag value.
	Account string

	// ServiceName holds the current service context ("drive", "lumo", "account", or "*").
	ServiceName string

	// AppVersionOverride holds the --app-version flag value.
	AppVersionOverride string

	// Config holds the loaded application config.
	Config *common.Config

	// SessionFile is the resolved path to the sessions.db file.
	SessionFile string
}

// SetContext stores a RuntimeContext on the cobra command's context.
func SetContext(cmd *cobra.Command, rc *RuntimeContext) {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	cmd.SetContext(context.WithValue(ctx, contextKey{}, rc))
}

// GetContext retrieves the RuntimeContext from a cobra command's context.
// Panics if the context is missing — this indicates a programming error
// (PersistentPreRunE was not called).
func GetContext(cmd *cobra.Command) *RuntimeContext {
	ctx := cmd.Context()
	if ctx == nil {
		panic("proton-cli: command has no context (PersistentPreRunE not called)")
	}
	rc, ok := ctx.Value(contextKey{}).(*RuntimeContext)
	if !ok || rc == nil {
		panic("proton-cli: RuntimeContext not found in command context")
	}
	return rc
}
