package drive

import (
	"context"
	"fmt"

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
