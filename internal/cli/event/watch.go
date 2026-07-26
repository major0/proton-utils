package eventCmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api/drive"
	cli "github.com/major0/proton-utils/internal/cli"
	"github.com/spf13/cobra"
)

// coreEventFetcher is the subset of the proton client used by the core-event
// path. *proton.Client satisfies it; tests substitute a mock.
type coreEventFetcher interface {
	GetLatestEventID(ctx context.Context) (string, error)
	GetEvent(ctx context.Context, eventID string) ([]proton.Event, bool, error)
}

// runWatch validates flags, establishes a session, and dispatches to the
// selected watch mode. It exits cleanly (status 0) only on Ctrl+C; startup
// failures return a non-zero error (Requirement 6.3, 6.4).
func runWatch(cmd *cobra.Command, _ []string) error {
	if err := validateWatchFlags(watchFlags); err != nil {
		return err
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// Derive a cancellable context so an early return (e.g. a broken output
	// pipe) tears down the watcher goroutine rather than leaking it.
	ctx, cancel := context.WithCancel(sigCtx)
	defer cancel()

	session, err := cli.SetupSession(ctx, cmd)
	if err != nil {
		return err
	}
	dc, err := cli.NewDriveClient(ctx, session)
	if err != nil {
		return err
	}

	p := newPrinter(os.Stdout, watchFlags.pretty, watchFlags.types)

	switch {
	case watchFlags.drive:
		return runDriveWatch(ctx, dc, p)
	case watchFlags.share != "":
		return runShareWatch(ctx, dc, p)
	default:
		return runCoreWatch(ctx, dc.Session.Client, p)
	}
}

// runCoreWatch polls the core event stream, emitting one JSONL line per
// populated category. The initial latest-event-ID fetch is a startup failure
// (non-zero exit); per-poll failures are logged to stderr and retried.
func runCoreWatch(ctx context.Context, pc coreEventFetcher, p *printer) error {
	cursor := watchFlags.from
	if cursor == "" {
		latest, err := pc.GetLatestEventID(ctx)
		if err != nil {
			return fmt.Errorf("event watch: fetch latest event ID: %w", err)
		}
		cursor = latest
	}

	ticker := time.NewTicker(watchFlags.interval)
	defer ticker.Stop()

	for {
		next, err := pollCoreOnce(ctx, pc, p, cursor)
		if err != nil {
			return err // terminal output failure
		}
		cursor = next

		select {
		case <-ctx.Done():
			printErr("cursor: %s", cursor)
			return nil
		case <-ticker.C:
		}
	}
}

// pollCoreOnce performs one core poll. API errors are logged and swallowed
// (the cursor is left unchanged so no events are skipped); only output
// failures are returned. On a refresh the cursor is re-anchored to latest.
func pollCoreOnce(ctx context.Context, pc coreEventFetcher, p *printer, cursor string) (string, error) {
	events, _, err := pc.GetEvent(ctx, cursor)
	if err != nil {
		if ctx.Err() != nil {
			return cursor, nil // shutting down, not a poll failure
		}
		printErr("event watch: poll error: %v", err)
		return cursor, nil
	}

	for _, ev := range events {
		refreshed, werr := p.emitCoreEvent(ev, time.Now())
		if werr != nil {
			return cursor, werr
		}
		if refreshed {
			latest, lerr := pc.GetLatestEventID(ctx)
			if lerr != nil {
				printErr("event watch: re-init after refresh: %v", lerr)
				return ev.EventID, nil
			}
			return latest, nil
		}
		cursor = ev.EventID
	}
	return cursor, nil
}

// runDriveWatch watches Drive volume events across all volumes. --from is
// rejected when more than one volume is present (Requirement 4.2).
func runDriveWatch(ctx context.Context, dc *drive.Client, p *printer) error {
	volumes, err := dc.ListVolumes(ctx)
	if err != nil {
		return fmt.Errorf("event watch: list volumes: %w", err)
	}
	if len(volumes) == 0 {
		return fmt.Errorf("event watch: no Drive volumes to watch")
	}
	if err := validateDriveFrom(watchFlags.from, len(volumes)); err != nil {
		return err
	}

	targets := make([]drive.WatchTarget, len(volumes))
	for i := range volumes {
		targets[i] = drive.WatchTarget{VolumeID: volumes[i].VolumeID()}
	}
	if watchFlags.from != "" {
		targets[0].Cursor = watchFlags.from
	}

	ch := dc.WatchDriveEvents(ctx, targets, watchFlags.interval)
	return consumeDriveWatch(ch, p, true)
}

// runShareWatch watches a single Drive share's events. --from sets the
// initial cursor.
func runShareWatch(ctx context.Context, dc *drive.Client, p *printer) error {
	target := drive.WatchTarget{ShareID: watchFlags.share, Cursor: watchFlags.from}
	ch := dc.WatchDriveEvents(ctx, []drive.WatchTarget{target}, watchFlags.interval)
	return consumeDriveWatch(ch, p, false)
}

// consumeDriveWatch drains the watcher channel until it closes (on context
// cancellation), printing each batch and logging poll errors to stderr
// without exiting (Requirement 6.1). On exit it prints resume cursor(s) to
// stderr (Requirement 4.3).
func consumeDriveWatch(ch <-chan drive.EventBatch, p *printer, driveMode bool) error {
	cursors := make(map[string]string)

	for batch := range ch {
		if batch.Err != nil {
			printErr("event watch: poll error (%s): %v", targetLabel(batch.Target), batch.Err)
			continue
		}
		cursors[cursorKey(batch.Target)] = batch.Target.Cursor
		if err := p.emitDriveEvent(batch.Target, batch.Event, time.Now()); err != nil {
			return err
		}
	}

	printDriveCursors(driveMode, cursors)
	return nil
}

// cursorKey returns the map key for a target's cursor: the volume ID for a
// volume target, otherwise the share ID.
func cursorKey(t drive.WatchTarget) string {
	if t.VolumeID != "" {
		return t.VolumeID
	}
	return t.ShareID
}

// targetLabel returns a human-readable label for a watch target, used in
// stderr diagnostics.
func targetLabel(t drive.WatchTarget) string {
	if t.VolumeID != "" {
		return "volume " + t.VolumeID
	}
	return "share " + t.ShareID
}

// printDriveCursors prints resume cursor(s) to stderr: one volumeID=eventID
// line per volume in --drive mode, or a single cursor line otherwise
// (Requirement 4.3).
func printDriveCursors(driveMode bool, cursors map[string]string) {
	if driveMode {
		for vol, cur := range cursors {
			printErr("%s=%s", vol, cur)
		}
		return
	}
	for _, cur := range cursors {
		printErr("cursor: %s", cur)
	}
}

// printErr writes a diagnostic line to stderr, keeping stdout reserved for
// the JSONL event stream. stderr write failures are ignored (best-effort).
func printErr(format string, args ...interface{}) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
}
