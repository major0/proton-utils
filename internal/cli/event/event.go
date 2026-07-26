// Package eventCmd implements the `proton event` subcommands, which poll the
// Proton event API and stream events to stdout as JSONL.
package eventCmd

import (
	"time"

	cli "github.com/major0/proton-utils/internal/cli"
	"github.com/spf13/cobra"
)

// watchParams holds the parsed `event watch` flags.
type watchParams struct {
	drive    bool
	share    string
	types    []string
	from     string
	interval time.Duration
	pretty   bool
}

var watchFlags watchParams

var eventCmd = &cobra.Command{
	Use:               "event",
	Short:             "Watch Proton event streams",
	Long:              "Watch Proton core and Drive event streams.",
	PersistentPreRunE: cli.ServicePreRunE("drive"),
	Run: func(cmd *cobra.Command, _ []string) {
		_ = cmd.Help()
	},
}

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Poll the event API and stream events as JSONL",
	Long: `Poll the Proton event API and print each event to stdout as a line of JSON.

By default the core event stream is watched. Use --drive to watch Drive
volume events across all volumes, or --share <shareID> to watch a single
share. The command runs until interrupted (Ctrl+C), then prints the current
resume cursor(s) to stderr.`,
	RunE: runWatch,
}

func init() {
	cli.AddCommand(eventCmd)
	eventCmd.AddCommand(watchCmd)

	f := watchCmd.Flags()
	f.BoolVar(&watchFlags.drive, "drive", false, "watch Drive volume events (all volumes)")
	f.StringVar(&watchFlags.share, "share", "", "watch a single Drive share's events")
	f.StringArrayVar(&watchFlags.types, "type", nil, "filter by event type (repeatable, OR logic)")
	f.StringVar(&watchFlags.from, "from", "", "resume from an event ID")
	f.DurationVar(&watchFlags.interval, "interval", 5*time.Second, "poll interval")
	f.BoolVar(&watchFlags.pretty, "pretty", false, "indent JSON output")
}

// AddCommand registers a subcommand under the event command group.
func AddCommand(cmd *cobra.Command) {
	eventCmd.AddCommand(cmd)
}
