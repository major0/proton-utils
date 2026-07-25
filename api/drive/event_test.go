package drive

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"pgregory.net/rapid"
)

// mockEventFetcher is a test double for driveEventFetcher. Each field, when
// non-nil, backs the corresponding method so individual tests wire only the
// calls they exercise.
type mockEventFetcher struct {
	getVolumeEvent         func(ctx context.Context, volumeID, eventID string) (proton.DriveEvent, error)
	getShareEvent          func(ctx context.Context, shareID, eventID string) (proton.DriveEvent, error)
	getLatestVolumeEventID func(ctx context.Context, volumeID string) (string, error)
	getLatestShareEventID  func(ctx context.Context, shareID string) (string, error)
}

func (m *mockEventFetcher) GetVolumeEvent(ctx context.Context, volumeID, eventID string) (proton.DriveEvent, error) {
	return m.getVolumeEvent(ctx, volumeID, eventID)
}

func (m *mockEventFetcher) GetShareEvent(ctx context.Context, shareID, eventID string) (proton.DriveEvent, error) {
	return m.getShareEvent(ctx, shareID, eventID)
}

func (m *mockEventFetcher) GetLatestVolumeEventID(ctx context.Context, volumeID string) (string, error) {
	return m.getLatestVolumeEventID(ctx, volumeID)
}

func (m *mockEventFetcher) GetLatestShareEventID(ctx context.Context, shareID string) (string, error) {
	return m.getLatestShareEventID(ctx, shareID)
}

// eventSeqID renders a monotonic event ID from an integer, e.g. 3 -> "e3".
func eventSeqID(n int) string { return fmt.Sprintf("e%d", n) }

// eventSeqNum parses an event ID produced by eventSeqID back to its integer.
func eventSeqNum(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "e"))
	return n
}

// readBatch reads one batch from ch with a timeout guard so a stuck watcher
// fails the test instead of hanging.
func readBatch(rt *rapid.T, ch <-chan EventBatch) EventBatch {
	rt.Helper()
	select {
	case b := <-ch:
		return b
	case <-time.After(2 * time.Second):
		rt.Fatal("timed out waiting for a batch")
		return EventBatch{}
	}
}

// TestPropertyCursorAdvancesMonotonically validates Property 1: each
// delivered batch's cursor equals the EventID returned by the underlying
// poll, and successive polls resume from the advanced cursor so no event ID
// is polled twice. The mock returns a strictly increasing sequence starting
// from the latest cursor; if the watcher failed to advance, the same ID
// would repeat.
//
// Validates: Requirements 4.1, 5 (event-watch)
func TestPropertyCursorAdvancesMonotonically(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 20).Draw(rt, "polls")

		mock := &mockEventFetcher{
			getLatestVolumeEventID: func(_ context.Context, _ string) (string, error) {
				return eventSeqID(0), nil
			},
			getVolumeEvent: func(_ context.Context, _, eventID string) (proton.DriveEvent, error) {
				return proton.DriveEvent{EventID: eventSeqID(eventSeqNum(eventID) + 1)}, nil
			},
		}
		client := &Client{eventFetcher: mock}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch := client.WatchDriveEvents(ctx, []WatchTarget{{VolumeID: "vol1"}}, time.Millisecond)

		for i := 1; i <= n; i++ {
			b := readBatch(rt, ch)
			if b.Err != nil {
				rt.Fatalf("unexpected poll error: %v", b.Err)
			}
			want := eventSeqID(i)
			if b.Target.Cursor != want {
				rt.Fatalf("batch %d: cursor = %q, want %q (cursor did not advance monotonically)", i, b.Target.Cursor, want)
			}
			if b.Event.EventID != want {
				rt.Fatalf("batch %d: event ID = %q, want %q", i, b.Event.EventID, want)
			}
		}
	})
}

// TestPropertyRefreshSurfaced validates Property 4: a DriveEvent with
// Refresh set is delivered to the caller unchanged rather than being
// swallowed by the watcher.
//
// Validates: Requirements 1.7, 5.4
func TestPropertyRefreshSurfaced(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		refresh := rapid.Bool().Draw(rt, "refresh")

		mock := &mockEventFetcher{
			getLatestVolumeEventID: func(_ context.Context, _ string) (string, error) {
				return eventSeqID(0), nil
			},
			getVolumeEvent: func(_ context.Context, _, _ string) (proton.DriveEvent, error) {
				return proton.DriveEvent{EventID: eventSeqID(1), Refresh: proton.Bool(refresh)}, nil
			},
		}
		client := &Client{eventFetcher: mock}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch := client.WatchDriveEvents(ctx, []WatchTarget{{VolumeID: "vol1"}}, time.Millisecond)

		b := readBatch(rt, ch)
		if b.Err != nil {
			rt.Fatalf("unexpected poll error: %v", b.Err)
		}
		if got := bool(b.Event.Refresh); got != refresh {
			rt.Fatalf("delivered Refresh = %v, want %v (refresh was not surfaced)", got, refresh)
		}
	})
}

// TestWatchDriveEventsReinitCursorAfterRefresh verifies that after a Refresh
// batch the watcher re-anchors the target's cursor to the latest event ID
// (so the next poll resumes from a valid cursor rather than a stale delta).
func TestWatchDriveEventsReinitCursorAfterRefresh(t *testing.T) {
	var latestCalls int
	var pollFromIDs []string

	mock := &mockEventFetcher{
		getLatestVolumeEventID: func(_ context.Context, _ string) (string, error) {
			latestCalls++
			// First init -> "e0"; post-refresh re-init -> "e100".
			if latestCalls == 1 {
				return eventSeqID(0), nil
			}
			return eventSeqID(100), nil
		},
		getVolumeEvent: func(_ context.Context, _, eventID string) (proton.DriveEvent, error) {
			pollFromIDs = append(pollFromIDs, eventID)
			// First poll (from e0) refreshes; later polls do not.
			if eventID == eventSeqID(0) {
				return proton.DriveEvent{EventID: eventSeqID(1), Refresh: proton.Bool(true)}, nil
			}
			return proton.DriveEvent{EventID: eventSeqID(eventSeqNum(eventID) + 1)}, nil
		},
	}
	client := &Client{eventFetcher: mock}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := client.WatchDriveEvents(ctx, []WatchTarget{{VolumeID: "vol1"}}, time.Millisecond)

	// First batch is the refresh from e0.
	first := <-ch
	if !bool(first.Event.Refresh) {
		t.Fatalf("expected first batch to be a refresh, got %+v", first.Event)
	}

	// Second batch must poll from the re-initialized cursor (e100), not the
	// pre-refresh EventID (e1).
	second := <-ch
	if second.Target.Cursor != eventSeqID(101) {
		t.Fatalf("post-refresh cursor = %q, want %q (cursor not re-anchored to latest)", second.Target.Cursor, eventSeqID(101))
	}
	cancel()

	if len(pollFromIDs) < 2 || pollFromIDs[1] != eventSeqID(100) {
		t.Fatalf("second poll used fromID %v, want re-init to %q", pollFromIDs, eventSeqID(100))
	}
}

// TestWatchDriveEventsDeliversPollError verifies that a failed poll is
// surfaced as a batch with Err set and that the target's cursor is left
// unchanged so no events are skipped.
func TestWatchDriveEventsDeliversPollError(t *testing.T) {
	wantErr := fmt.Errorf("transient network error")
	mock := &mockEventFetcher{
		getLatestVolumeEventID: func(_ context.Context, _ string) (string, error) {
			return eventSeqID(5), nil
		},
		getVolumeEvent: func(_ context.Context, _, _ string) (proton.DriveEvent, error) {
			return proton.DriveEvent{}, wantErr
		},
	}
	client := &Client{eventFetcher: mock}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := client.WatchDriveEvents(ctx, []WatchTarget{{VolumeID: "vol1"}}, time.Millisecond)

	b := <-ch
	if b.Err == nil {
		t.Fatal("expected a poll error batch, got nil Err")
	}
	// Cursor stays at the initialized latest (e5) since the poll failed.
	if b.Target.Cursor != eventSeqID(5) {
		t.Fatalf("cursor after failed poll = %q, want %q (unchanged)", b.Target.Cursor, eventSeqID(5))
	}
}

// TestWatchDriveEventsClosesChannelOnCancel verifies the channel is closed
// when the context is cancelled.
func TestWatchDriveEventsClosesChannelOnCancel(t *testing.T) {
	mock := &mockEventFetcher{
		getLatestShareEventID: func(_ context.Context, _ string) (string, error) {
			return eventSeqID(0), nil
		},
		getShareEvent: func(_ context.Context, _, eventID string) (proton.DriveEvent, error) {
			return proton.DriveEvent{EventID: eventSeqID(eventSeqNum(eventID) + 1)}, nil
		},
	}
	client := &Client{eventFetcher: mock}

	ctx, cancel := context.WithCancel(context.Background())
	ch := client.WatchDriveEvents(ctx, []WatchTarget{{ShareID: "share1"}}, time.Millisecond)

	<-ch // consume at least one batch
	cancel()

	// The channel must close (and draining must complete) after cancel.
	done := make(chan struct{})
	go func() {
		for {
			if _, ok := <-ch; !ok {
				break
			}
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("channel was not closed after context cancellation")
	}
}
