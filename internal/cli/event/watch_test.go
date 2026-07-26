package eventCmd

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api/drive"
)

// mockCoreFetcher is a test double for coreEventFetcher.
type mockCoreFetcher struct {
	latest    func(ctx context.Context) (string, error)
	getEvent  func(ctx context.Context, eventID string) ([]proton.Event, bool, error)
	latestHit int
}

func (m *mockCoreFetcher) GetLatestEventID(ctx context.Context) (string, error) {
	m.latestHit++
	return m.latest(ctx)
}

func (m *mockCoreFetcher) GetEvent(ctx context.Context, eventID string) ([]proton.Event, bool, error) {
	return m.getEvent(ctx, eventID)
}

// TestConsumeDriveWatchContinuesOnPollError verifies that a poll-error batch
// is logged (to stderr) without stopping consumption: a subsequent good batch
// is still printed (Requirement 6.1).
func TestConsumeDriveWatchContinuesOnPollError(t *testing.T) {
	ch := make(chan drive.EventBatch, 3)
	ch <- drive.EventBatch{Target: drive.WatchTarget{VolumeID: "v1"}, Err: fmt.Errorf("transient")}
	ch <- drive.EventBatch{
		Target: drive.WatchTarget{VolumeID: "v1", Cursor: "e5"},
		Event: proton.DriveEvent{
			EventID: "e5",
			Events:  []proton.LinkEvent{{EventID: "le1", EventType: proton.LinkEventCreate, Link: proton.Link{LinkID: "L1"}}},
		},
	}
	close(ch)

	var buf bytes.Buffer
	p := newPrinter(&buf, false, nil)
	if err := consumeDriveWatch(ch, p, true); err != nil {
		t.Fatalf("consumeDriveWatch: %v", err)
	}

	if !strings.Contains(buf.String(), typeLinkCreate) || !strings.Contains(buf.String(), "L1") {
		t.Fatalf("good batch after poll error was not printed: %q", buf.String())
	}
}

// TestPollCoreOnceAdvancesCursor verifies successful polls emit populated
// categories and advance the cursor to the last event's ID.
func TestPollCoreOnceAdvancesCursor(t *testing.T) {
	mock := &mockCoreFetcher{
		latest: func(_ context.Context) (string, error) { return "e0", nil },
		getEvent: func(_ context.Context, _ string) ([]proton.Event, bool, error) {
			return []proton.Event{
				{EventID: "e1", Messages: []proton.MessageEvent{{}}},
				{EventID: "e2"},
			}, false, nil
		},
	}
	var buf bytes.Buffer
	p := newPrinter(&buf, false, nil)

	next, err := pollCoreOnce(context.Background(), mock, p, "e0")
	if err != nil {
		t.Fatalf("pollCoreOnce: %v", err)
	}
	if next != "e2" {
		t.Fatalf("cursor = %q, want e2", next)
	}
	if !strings.Contains(buf.String(), typeMessages) {
		t.Fatalf("messages category not emitted: %q", buf.String())
	}
}

// TestPollCoreOnceSwallowsAPIError verifies a poll API error is not returned
// (logged and swallowed) and leaves the cursor unchanged (Requirement 6.1).
func TestPollCoreOnceSwallowsAPIError(t *testing.T) {
	mock := &mockCoreFetcher{
		latest: func(_ context.Context) (string, error) { return "e0", nil },
		getEvent: func(_ context.Context, _ string) ([]proton.Event, bool, error) {
			return nil, false, fmt.Errorf("network hiccup")
		},
	}
	var buf bytes.Buffer
	p := newPrinter(&buf, false, nil)

	next, err := pollCoreOnce(context.Background(), mock, p, "e7")
	if err != nil {
		t.Fatalf("pollCoreOnce should swallow API errors, got %v", err)
	}
	if next != "e7" {
		t.Fatalf("cursor changed on error: got %q, want e7", next)
	}
}

// TestPollCoreOnceRefreshReinits verifies a core refresh event re-anchors the
// cursor to the latest event ID.
func TestPollCoreOnceRefreshReinits(t *testing.T) {
	mock := &mockCoreFetcher{
		latest: func(_ context.Context) (string, error) { return "e99", nil },
		getEvent: func(_ context.Context, _ string) ([]proton.Event, bool, error) {
			return []proton.Event{{EventID: "e1", Refresh: proton.RefreshAll}}, false, nil
		},
	}
	var buf bytes.Buffer
	p := newPrinter(&buf, false, nil)

	next, err := pollCoreOnce(context.Background(), mock, p, "e0")
	if err != nil {
		t.Fatalf("pollCoreOnce: %v", err)
	}
	if next != "e99" {
		t.Fatalf("cursor after refresh = %q, want re-init to e99", next)
	}
	if !strings.Contains(buf.String(), typeRefresh) {
		t.Fatalf("refresh line not emitted: %q", buf.String())
	}
}
