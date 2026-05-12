package cli

import "github.com/spf13/cobra"

// ServicePreRunE returns a PersistentPreRunE closure that chains the
// root command's PersistentPreRunE and then calls SetServiceCmd with
// the given service name.
func ServicePreRunE(service string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if p := cmd.Root(); p != nil && p.PersistentPreRunE != nil {
			if err := p.PersistentPreRunE(p, args); err != nil {
				return err
			}
		}
		SetServiceCmd(cmd, service)
		return nil
	}
}
