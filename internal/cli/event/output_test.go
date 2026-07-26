package eventCmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api/drive"
	"pgregory.net/rapid"
)

func TestLinkEventTypeString(t *testing.T) {
	cases := map[proton.LinkEventType]string{
		proton.LinkEventCreate:         typeLinkCreate,
		proton.LinkEventUpdate:         typeLinkUpdate,
		proton.LinkEventDelete:         typeLinkDelete,
		proton.LinkEventUpdateMetadata: typeLinkMetadata,
	}
	for in, want := range cases {
		if got := linkEventTypeString(in); got != want {
			t.Errorf("linkEventTypeString(%d) = %q, want %q", int(in), got, want)
		}
	}
}

var allDriveEventTypes = []proton.LinkEventType{
	proton.LinkEventCreate,
	proton.LinkEventUpdate,
	proton.LinkEventDelete,
	proton.LinkEventUpdateMetadata,
}

// splitLines returns the non-empty output lines.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

// TestPropertyTypeFilter validates Property 2: an event is printed iff its
// mapped type is in the (non-empty) filter set; an empty filter passes all.
//
// Validates: Requirements 3.1, 3.4, 3.5
func TestPropertyTypeFilter(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		filter := rapid.SliceOfDistinct(
			rapid.SampledFrom([]string{typeLinkCreate, typeLinkUpdate, typeLinkDelete, typeLinkMetadata}),
			func(s string) string { return s },
		).Draw(rt, "filter")

		evTypes := rapid.SliceOf(rapid.SampledFrom(allDriveEventTypes)).Draw(rt, "events")

		links := make([]proton.LinkEvent, len(evTypes))
		for i, et := range evTypes {
			links[i] = proton.LinkEvent{EventID: "le", EventType: et, Link: proton.Link{LinkID: "L"}}
		}

		var buf bytes.Buffer
		p := newPrinter(&buf, false, filter)
		if err := p.emitDriveEvent(drive.WatchTarget{VolumeID: "v1"}, proton.DriveEvent{EventID: "e1", Events: links}, time.Now()); err != nil {
			rt.Fatalf("emitDriveEvent: %v", err)
		}

		filterSet := make(map[string]bool, len(filter))
		for _, f := range filter {
			filterSet[f] = true
		}

		// Expected count = number of link events whose type passes the filter.
		want := 0
		for _, et := range evTypes {
			if len(filterSet) == 0 || filterSet[linkEventTypeString(et)] {
				want++
			}
		}

		lines := splitLines(buf.String())
		if len(lines) != want {
			rt.Fatalf("emitted %d lines, want %d (filter=%v, events=%v)", len(lines), want, filter, evTypes)
		}
		for _, ln := range lines {
			var got eventLine
			if err := json.Unmarshal([]byte(ln), &got); err != nil {
				rt.Fatalf("line is not valid JSON: %q: %v", ln, err)
			}
			if len(filterSet) > 0 && !filterSet[got.Type] {
				rt.Fatalf("emitted type %q not in filter %v", got.Type, filter)
			}
		}
	})
}

// TestPropertyJSONLOneLine validates Property 3: without --pretty, each
// emitted event is a single line that parses as one JSON object carrying the
// required fields.
//
// Validates: Requirements 2.1, 2.2
func TestPropertyJSONLOneLine(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		et := rapid.SampledFrom(allDriveEventTypes).Draw(rt, "eventType")
		linkID := rapid.StringMatching(`[a-zA-Z0-9]{1,12}`).Draw(rt, "linkID")

		var buf bytes.Buffer
		p := newPrinter(&buf, false, nil)
		ev := proton.DriveEvent{
			EventID: "e1",
			Events:  []proton.LinkEvent{{EventID: "le1", EventType: et, Link: proton.Link{LinkID: linkID}}},
		}
		if err := p.emitDriveEvent(drive.WatchTarget{VolumeID: "v1"}, ev, time.Now()); err != nil {
			rt.Fatalf("emitDriveEvent: %v", err)
		}

		out := buf.String()
		if strings.Count(out, "\n") != 1 {
			rt.Fatalf("expected exactly one trailing newline, got %d in %q", strings.Count(out, "\n"), out)
		}

		var got eventLine
		if err := json.Unmarshal([]byte(strings.TrimRight(out, "\n")), &got); err != nil {
			rt.Fatalf("output is not a single JSON object: %q: %v", out, err)
		}
		if got.EventID == "" || got.Type == "" || got.Timestamp == "" {
			rt.Fatalf("missing required fields in %+v", got)
		}
		if _, err := time.Parse(time.RFC3339, got.Timestamp); err != nil {
			rt.Fatalf("timestamp %q is not RFC3339: %v", got.Timestamp, err)
		}
	})
}

// TestEmitCoreEventCategories verifies one line is emitted per populated core
// category, each with that category's type (Requirement 2.6).
func TestEmitCoreEventCategories(t *testing.T) {
	var buf bytes.Buffer
	p := newPrinter(&buf, false, nil)

	ev := proton.Event{
		EventID:      "evt-1",
		User:         &proton.User{},
		MailSettings: &proton.MailSettings{},
		Messages:     []proton.MessageEvent{{}},
	}
	refreshed, err := p.emitCoreEvent(ev, time.Now())
	if err != nil {
		t.Fatalf("emitCoreEvent: %v", err)
	}
	if refreshed {
		t.Fatal("non-refresh event reported refreshed=true")
	}

	lines := splitLines(buf.String())
	gotTypes := make(map[string]bool)
	for _, ln := range lines {
		var el eventLine
		if err := json.Unmarshal([]byte(ln), &el); err != nil {
			t.Fatalf("bad JSON line %q: %v", ln, err)
		}
		gotTypes[el.Type] = true
	}
	for _, want := range []string{typeUser, typeMailSettings, typeMessages} {
		if !gotTypes[want] {
			t.Errorf("missing category line %q (got %v)", want, gotTypes)
		}
	}
	if gotTypes[typeLabels] || gotTypes[typeAddresses] {
		t.Errorf("emitted unpopulated category (got %v)", gotTypes)
	}
}

// TestEmitCoreEventRefresh verifies a refresh event emits a single refresh
// line and reports refreshed=true, bypassing the type filter.
func TestEmitCoreEventRefresh(t *testing.T) {
	var buf bytes.Buffer
	p := newPrinter(&buf, false, []string{typeMessages}) // filter that excludes "refresh"

	refreshed, err := p.emitCoreEvent(proton.Event{EventID: "evt-1", Refresh: proton.RefreshAll}, time.Now())
	if err != nil {
		t.Fatalf("emitCoreEvent: %v", err)
	}
	if !refreshed {
		t.Fatal("refresh event reported refreshed=false")
	}
	var el eventLine
	if err := json.Unmarshal([]byte(strings.TrimRight(buf.String(), "\n")), &el); err != nil {
		t.Fatalf("bad refresh JSON %q: %v", buf.String(), err)
	}
	if el.Type != typeRefresh || !el.Refresh {
		t.Fatalf("expected refresh line, got %+v", el)
	}
}
