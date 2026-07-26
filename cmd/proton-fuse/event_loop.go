package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api/drive"
	fusedrv "github.com/major0/proton-utils/internal/fusemount/drive"
)

const (
	// defaultEventPollInterval is used when the configured interval is
	// non-positive.
	defaultEventPollInterval = 15 * time.Second

	// backoffEventPollInterval is the widened interval applied after
	// eventBackoffThreshold consecutive poll failures.
	backoffEventPollInterval = 60 * time.Second

	// eventBackoffThreshold is the number of consecutive poll failures that
	// triggers backoff.
	eventBackoffThreshold = 5
)

// eventConfig configures the daemon's Drive-event invalidation loop.
type eventConfig struct {
	// Interval is the normal poll interval; widened under backoff.
	Interval time.Duration
}

// startEventLoop polls Drive volume events and invalidates caches on remote
// changes, keeping the mount near-real-time consistent with other clients.
// It owns its own timer (built on the low-level PollVolumeOnce primitive, not
// the channel watcher) so it can widen the interval under backoff. It runs
// until ctx is cancelled, then persists cursors and returns. It never panics
// out or exits on API errors.
func startEventLoop(ctx context.Context, client *drive.Client, handler *fusedrv.DriveHandler, cfg eventConfig) {
	// Wire the FUSE children-invalidation hook so InvalidateParent refreshes
	// live directory listings.
	client.SetInvalidationHook(handler.OnInvalidateParent)

	volumes, err := client.ListVolumes(ctx)
	if err != nil {
		slog.Warn("event loop: list volumes failed; event invalidation disabled", "error", err)
		return
	}
	if len(volumes) == 0 {
		slog.Debug("event loop: no volumes to watch")
		return
	}

	cursorPath, cursorOK := eventCursorPath()
	cursors := make(map[string]string)
	if cursorOK {
		if loaded, lerr := loadEventCursors(cursorPath); lerr == nil {
			cursors = loaded
		} else {
			slog.Warn("event loop: loading persisted cursors", "error", lerr)
		}
	}

	// Initialize any volume without a persisted cursor to its latest event.
	for _, v := range volumes {
		vid := v.VolumeID()
		if cursors[vid] != "" {
			continue
		}
		if latest, lerr := client.LatestVolumeCursor(ctx, vid); lerr == nil {
			cursors[vid] = latest
		} else {
			slog.Warn("event loop: initializing cursor", "volume", vid, "error", lerr)
		}
	}

	interval := normalizeEventInterval(cfg.Interval)
	consecutiveFailures := 0

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if cursorOK {
				if serr := saveEventCursors(cursorPath, cursors); serr != nil {
					slog.Warn("event loop: persisting cursors on shutdown", "error", serr)
				}
			}
			return
		case <-ticker.C:
		}

		for _, v := range volumes {
			if ctx.Err() != nil {
				break
			}
			if pollVolumeAndInvalidate(ctx, client, handler, v.VolumeID(), cursors) {
				consecutiveFailures = 0
			} else {
				consecutiveFailures++
			}
		}

		// Adjust the interval for backoff / recovery.
		want := normalizeEventInterval(cfg.Interval)
		if consecutiveFailures >= eventBackoffThreshold {
			want = backoffEventPollInterval
		}
		if want != interval {
			interval = want
			ticker.Reset(interval)
			slog.Debug("event loop: interval adjusted", "interval", interval, "consecutive_failures", consecutiveFailures)
		}

		if cursorOK {
			if serr := saveEventCursors(cursorPath, cursors); serr != nil {
				slog.Debug("event loop: persisting cursors", "error", serr)
			}
		}
	}
}

// pollVolumeAndInvalidate polls one volume once and applies the resulting
// events. It returns true on success (including an empty batch or a handled
// refresh) and false on a transient failure that should count toward backoff.
// A panic in event handling is recovered so the loop never dies.
func pollVolumeAndInvalidate(ctx context.Context, client *drive.Client, handler *fusedrv.DriveHandler, volumeID string, cursors map[string]string) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("event loop: recovered panic", "volume", volumeID, "panic", r)
			ok = false
		}
	}()

	ev, err := client.PollVolumeOnce(ctx, volumeID, cursors[volumeID])
	if err != nil {
		if ctx.Err() != nil {
			return true // shutting down, not a real failure
		}
		if isStaleCursorError(err) {
			// Stale cursor: full resync, same as a Refresh.
			resyncVolume(ctx, client, handler, volumeID, cursors)
			return true
		}
		slog.Warn("event loop: poll failed", "volume", volumeID, "error", err)
		return false // leave cursor unchanged so no events are skipped
	}

	if bool(ev.Refresh) {
		resyncVolume(ctx, client, handler, volumeID, cursors)
		return true
	}

	for i := range ev.Events {
		applyLinkEvent(client, ev.Events[i])
	}
	cursors[volumeID] = ev.EventID
	return true
}

// applyLinkEvent dispatches a single LinkEvent to the client's invalidation
// seam. linkID, parentLinkID, and the block count all come from the event's
// embedded Link.
func applyLinkEvent(client *drive.Client, le proton.LinkEvent) {
	switch le.EventType {
	case proton.LinkEventCreate:
		client.InvalidateParent(le.Link.ParentLinkID)
	case proton.LinkEventDelete, proton.LinkEventUpdate, proton.LinkEventUpdateMetadata:
		blockCount := 0
		if le.Link.Type == proton.LinkTypeFile {
			blockCount = drive.BlockCount(le.Link.Size)
		}
		client.InvalidateLink(le.Link.LinkID, le.Link.ParentLinkID, blockCount)
	}
}

// resyncVolume performs a full cache clear for a Refresh / stale-cursor
// condition and re-anchors the volume's cursor to the latest event.
func resyncVolume(ctx context.Context, client *drive.Client, handler *fusedrv.DriveHandler, volumeID string, cursors map[string]string) {
	slog.Info("event loop: full resync", "volume", volumeID)
	if err := client.Clear(); err != nil {
		slog.Warn("event loop: clear caches", "error", err)
	}
	handler.InvalidateAll()
	if latest, err := client.LatestVolumeCursor(ctx, volumeID); err == nil {
		cursors[volumeID] = latest
	} else {
		slog.Warn("event loop: re-init cursor after resync", "volume", volumeID, "error", err)
	}
}

// isStaleCursorError reports whether err indicates a rejected (stale) event
// cursor — Proton returns HTTP 422 Unprocessable Entity.
func isStaleCursorError(err error) bool {
	var apiErr *proton.APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusUnprocessableEntity
}

// normalizeEventInterval clamps a non-positive configured interval to the
// default.
func normalizeEventInterval(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultEventPollInterval
	}
	return d
}
