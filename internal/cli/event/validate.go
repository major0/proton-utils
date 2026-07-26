package eventCmd

import (
	"fmt"
	"time"
)

// minInterval is the smallest permitted poll interval (Requirement 1.6). A
// smaller value would hammer the API and fight the session Throttle.
const minInterval = time.Second

// validateWatchFlags performs startup validation that must pass before any
// polling begins (Requirements 1.6, 3.6, 3.7). It does not cover the
// multi-volume --from rejection (Requirement 4.2), which depends on the
// resolved volume count and is checked in the --drive path.
func validateWatchFlags(p watchParams) error {
	if p.drive && p.share != "" {
		return fmt.Errorf("--drive and --share are mutually exclusive")
	}

	if p.interval < minInterval {
		return fmt.Errorf("--interval must be at least %s (got %s)", minInterval, p.interval)
	}

	driveMode := p.drive || p.share != ""
	for _, t := range p.types {
		isCore := coreTypeSet[t]
		isDrive := driveTypeSet[t]
		switch {
		case !isCore && !isDrive:
			return fmt.Errorf("unknown --type %q (core types: user, mail-settings, messages, labels, addresses; drive types: link-create, link-update, link-delete, link-metadata)", t)
		case driveMode && isCore:
			return fmt.Errorf("--type %q is a core event type but --drive/--share selects Drive events", t)
		case !driveMode && isDrive:
			return fmt.Errorf("--type %q is a Drive event type and is only valid with --drive or --share", t)
		}
	}

	return nil
}

// validateDriveFrom rejects --from when watching more than one volume
// (Requirement 4.2): a single event ID is not meaningful across independent
// per-volume cursors. It is checked in the --drive path once the volume count
// is known.
func validateDriveFrom(from string, volumeCount int) error {
	if from != "" && volumeCount > 1 {
		return fmt.Errorf("--from cannot be used with --drive across %d volumes; a single event ID is not meaningful across independent per-volume cursors", volumeCount)
	}
	return nil
}
