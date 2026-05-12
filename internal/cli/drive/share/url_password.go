package shareCmd

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-cli/api/drive"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var urlPasswordFlags struct {
	random  bool
	stdin   bool
	disable bool
}

var shareURLPasswordCmd = &cobra.Command{
	Use:     "password <share-name>",
	Aliases: []string{"passwd"},
	Short:   "Change the password on a share's public URL",
	Long:    "Set, regenerate, or disable the password on an existing share public URL.",
	Args:    cobra.ExactArgs(1),
	RunE:    runShareURLPassword,
}

func init() {
	shareURLCmd.AddCommand(shareURLPasswordCmd)
	shareURLPasswordCmd.Flags().BoolVar(&urlPasswordFlags.random, "random", false, "Generate a new 32-character random password")
	shareURLPasswordCmd.Flags().BoolVar(&urlPasswordFlags.stdin, "stdin", false, "Read password from stdin")
	shareURLPasswordCmd.Flags().BoolVar(&urlPasswordFlags.disable, "disable", false, "Remove password (URL becomes truly public)")
}

// updateShareURLPasswordFn is a test seam for UpdateShareURLPassword.
var updateShareURLPasswordFn = func(ctx context.Context, dc *drive.Client, share *drive.Share, shareURL *drive.ShareURL, password string) error {
	return dc.UpdateShareURLPassword(ctx, share, shareURL, password)
}

func runShareURLPassword(cmd *cobra.Command, args []string) error {
	name := args[0]

	// Mutual exclusivity check.
	flagCount := 0
	if urlPasswordFlags.random {
		flagCount++
	}
	if urlPasswordFlags.stdin {
		flagCount++
	}
	if urlPasswordFlags.disable {
		flagCount++
	}
	if flagCount > 1 {
		return fmt.Errorf("share url password: --random, --stdin, and --disable are mutually exclusive")
	}

	ctx := context.Background()

	session, err := setupSessionFn(ctx, cmd)
	if err != nil {
		return err
	}

	dc, err := newDriveClientFn(ctx, session)
	if err != nil {
		return err
	}

	resolved, err := resolveShareFn(ctx, dc, name)
	if err != nil {
		return fmt.Errorf("share url password: %s: share not found", name)
	}

	shareID := resolved.Metadata().ShareID

	urls, err := listShareURLsFn(ctx, dc, shareID)
	if err != nil {
		return fmt.Errorf("share url password: %s: %w", name, err)
	}
	if len(urls) == 0 {
		return fmt.Errorf("share url password: %s: no public URL exists (use 'share url enable' first)", name)
	}

	var password string
	switch {
	case urlPasswordFlags.disable:
		password = "" // empty = disable
	case urlPasswordFlags.random:
		// Generate random password (same as enable).
		randBytes, err := crypto.RandomToken(32)
		if err != nil {
			return fmt.Errorf("share url password: random: %w", err)
		}
		password = base64.RawURLEncoding.EncodeToString(randBytes)[:32]
	case urlPasswordFlags.stdin:
		// Read from stdin.
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() {
			return fmt.Errorf("share url password: failed to read from stdin")
		}
		password = strings.TrimRight(scanner.Text(), "\n\r")
	default:
		// Interactive prompt.
		if !term.IsTerminal(int(os.Stdin.Fd())) { //nolint:gosec // standard pattern for term.IsTerminal
			return fmt.Errorf("share url password: stdin is not a terminal (use --random, --stdin, or --disable)")
		}
		fmt.Fprint(os.Stderr, "New password: ")
		pwBytes, err := term.ReadPassword(int(os.Stdin.Fd())) //nolint:gosec // standard pattern for term.ReadPassword
		fmt.Fprintln(os.Stderr)                               // newline after hidden input
		if err != nil {
			return fmt.Errorf("share url password: read password: %w", err)
		}
		password = string(pwBytes)
	}

	if err := updateShareURLPasswordFn(ctx, dc, resolved, &urls[0], password); err != nil {
		return fmt.Errorf("share url password: %s: %w", name, err)
	}

	// Output the new password (unless disabled).
	if password != "" {
		fmt.Println(password)
	} else {
		fmt.Fprintf(os.Stderr, "Password disabled for %s\n", name)
	}
	return nil
}
