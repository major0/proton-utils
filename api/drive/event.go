package drive

import (
	"context"
	"fmt"
	"time"

	"github.com/ProtonMail/go-proton-api"
)

// PollVolumeOnce fetches the next batch of events for a volume starting at
// fromEventID. go-proton-api's GetVolumeEvent auto-paginates internally, so
// the returned DriveEvent already contains all buffered events; its EventID
// is the new cursor to poll from next. The DriveEvent is returned unchanged
// (including its Refresh flag) — the caller decides what to do.
func (c *Client) PollVolumeOnce(ctx context.Context, volumeID, fromEventID string) (proton.DriveEvent, error) {
	event, err := c.eventFetcher.GetVolumeEvent(ctx, volumeID, fromEventID)
	if err != nil {
		return proton.DriveEvent{}, fmt.Errorf("drive.PollVolumeOnce %s: %w", volumeID, err)
	}
	return event, nil
}

// PollShareOnce is the share-scoped equivalent of PollVolumeOnce. It fetches
// the next batch of events for a share starting at fromEventID and returns
// the DriveEvent unchanged (including its Refresh flag).
func (c *Client) PollShareOnce(ctx context.Context, shareID, fromEventID string) (proton.DriveEvent, error) {
	event, err := c.eventFetcher.GetShareEvent(ctx, shareID, fromEventID)
	if err != nil {
		return proton.DriveEvent{}, fmt.Errorf("drive.PollShareOnce %s: %w", shareID, err)
	}
	return event, nil
}

// LatestVolumeCursor returns the latest event ID for a volume, used to
// initialize a cursor before polling begins.
func (c *Client) LatestVolumeCursor(ctx context.Context, volumeID string) (string, error) {
	eventID, err := c.eventFetcher.GetLatestVolumeEventID(ctx, volumeID)
	if err != nil {
		return "", fmt.Errorf("drive.LatestVolumeCursor %s: %w", volumeID, err)
	}
	return eventID, nil
}

// LatestShareCursor returns the latest event ID for a share, used to
// initialize a cursor before polling begins.
func (c *Client) LatestShareCursor(ctx context.Context, shareID string) (string, error) {
	eventID, err := c.eventFetcher.GetLatestShareEventID(ctx, shareID)
	if err != nil {
		return "", fmt.Errorf("drive.LatestShareCursor %s: %w", shareID, err)
	}
	return eventID, nil
}

// WatchTarget identifies one volume or share to watch, together with
// its polling cursor. VolumeID and ShareID are mutually exclusive: a
// non-empty ShareID selects share-scoped polling, otherwise VolumeID is
// used. An empty Cursor means "start from the latest event ID", which the
// watcher resolves on the first poll.
type WatchTarget struct {
	VolumeID string
	ShareID  string
	Cursor   string
}

// EventBatch is one poll result for one target, delivered on the
// channel returned by WatchDriveEvents. Target carries the cursor as
// advanced by this batch (the EventID to poll from next), so callers can
// persist or print it. Err is non-nil when the poll failed; in that case
// Event is zero and the target's cursor is left unchanged so no events are
// skipped.
type EventBatch struct {
	Target WatchTarget
	Event  proton.DriveEvent
	Err    error
}

// WatchDriveEvents polls the given targets every interval and delivers each
// poll result on the returned channel until ctx is cancelled, at which
// point the channel is closed. Targets with an empty Cursor are initialized
// to the latest event ID before their first poll. Cursors advance
// internally; each delivered batch carries the target with its post-batch
// cursor. A batch whose Event has Refresh set is delivered as-is, after
// which the watcher re-initializes that target's cursor to latest. A failed
// poll is delivered with Err set and leaves the cursor unchanged.
//
// Targets are polled sequentially within each interval tick. The watcher
// does not persist cursors — that is the caller's concern.
func (c *Client) WatchDriveEvents(ctx context.Context, targets []WatchTarget, interval time.Duration) <-chan EventBatch {
	ch := make(chan EventBatch)

	// Copy targets so the caller's slice is not mutated as cursors advance.
	ts := make([]WatchTarget, len(targets))
	copy(ts, targets)

	go func() {
		defer close(ch)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			for i := range ts {
				if ctx.Err() != nil {
					return
				}
				c.pollWatchTarget(ctx, &ts[i], ch)
			}

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	return ch
}

// pollWatchTarget performs one poll for a single target: it initializes an
// empty cursor to latest, polls, advances the cursor, and delivers the
// batch. On a Refresh result it re-initializes the cursor to latest for the
// next poll. On any error it delivers an error batch and leaves the cursor
// unchanged.
func (c *Client) pollWatchTarget(ctx context.Context, t *WatchTarget, ch chan<- EventBatch) {
	if t.Cursor == "" {
		cursor, err := c.latestCursor(ctx, *t)
		if err != nil {
			deliverBatch(ctx, ch, EventBatch{Target: *t, Err: err})
			return
		}
		t.Cursor = cursor
	}

	event, err := c.pollTargetOnce(ctx, *t, t.Cursor)
	if err != nil {
		deliverBatch(ctx, ch, EventBatch{Target: *t, Err: err})
		return
	}

	t.Cursor = event.EventID
	deliverBatch(ctx, ch, EventBatch{Target: *t, Event: event})

	// After a refresh the delta is unavailable; re-anchor to latest so the
	// next poll resumes from a valid cursor.
	if bool(event.Refresh) {
		if cursor, err := c.latestCursor(ctx, *t); err == nil {
			t.Cursor = cursor
		}
	}
}

// latestCursor resolves the latest event ID for a target, dispatching to the
// share- or volume-scoped primitive.
func (c *Client) latestCursor(ctx context.Context, t WatchTarget) (string, error) {
	if t.ShareID != "" {
		return c.LatestShareCursor(ctx, t.ShareID)
	}
	return c.LatestVolumeCursor(ctx, t.VolumeID)
}

// pollTargetOnce polls a target once from the given cursor, dispatching to
// the share- or volume-scoped primitive.
func (c *Client) pollTargetOnce(ctx context.Context, t WatchTarget, cursor string) (proton.DriveEvent, error) {
	if t.ShareID != "" {
		return c.PollShareOnce(ctx, t.ShareID, cursor)
	}
	return c.PollVolumeOnce(ctx, t.VolumeID, cursor)
}

// deliverBatch sends a batch on the channel unless ctx is cancelled first,
// so a cancelled watch never blocks on a consumer that has stopped reading.
func deliverBatch(ctx context.Context, ch chan<- EventBatch, batch EventBatch) {
	select {
	case ch <- batch:
	case <-ctx.Done():
	}
}
