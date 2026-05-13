package smoke

// Compile-time type assertions verifying the internal/cli package's public API
// surface. If any exported symbol is missing or has the wrong signature, this
// file fails to compile.
//
// Validates: Requirements 10.1, 10.2, 10.3, 10.4

import (
	"time"

	cli "github.com/major0/proton-utils/internal/cli"
	"github.com/spf13/cobra"
)

// Execute() — no args, no return.
var _ func() = cli.Execute

// AddCommand(*cobra.Command) — single arg, no return.
var _ func(*cobra.Command) = cli.AddCommand

// RuntimeContext.Timeout is an exported time.Duration field.
var _ time.Duration = cli.RuntimeContext{}.Timeout

// GetContext(*cobra.Command) — returns *RuntimeContext.
var _ func(*cobra.Command) *cli.RuntimeContext = cli.GetContext
