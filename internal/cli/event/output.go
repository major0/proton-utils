package eventCmd

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api/drive"
)

// Core event type strings (Requirement 3.2).
const (
	typeUser         = "user"
	typeMailSettings = "mail-settings"
	typeMessages     = "messages"
	typeLabels       = "labels"
	typeAddresses    = "addresses"
)

// Drive event type strings (Requirement 3.3).
const (
	typeLinkCreate   = "link-create"
	typeLinkUpdate   = "link-update"
	typeLinkDelete   = "link-delete"
	typeLinkMetadata = "link-metadata"
)

// typeRefresh is the synthetic type emitted when a full resync is required
// (Requirement 1.7). It is not user-selectable via --type; refresh lines are
// always emitted regardless of any active filter.
const typeRefresh = "refresh"

// coreTypeSet and driveTypeSet enumerate the valid --type values per mode,
// used for startup validation (Requirements 3.6, 3.7).
var (
	coreTypeSet  = map[string]bool{typeUser: true, typeMailSettings: true, typeMessages: true, typeLabels: true, typeAddresses: true}
	driveTypeSet = map[string]bool{typeLinkCreate: true, typeLinkUpdate: true, typeLinkDelete: true, typeLinkMetadata: true}
)

// eventLine is the JSONL envelope printed for each event (Requirement 2).
type eventLine struct {
	EventID    string      `json:"event_id"`
	Type       string      `json:"type"`
	Timestamp  string      `json:"timestamp"`
	CreateTime int64       `json:"create_time,omitempty"`
	VolumeID   string      `json:"volume_id,omitempty"`
	ShareID    string      `json:"share_id,omitempty"`
	LinkID     string      `json:"link_id,omitempty"`
	Refresh    bool        `json:"refresh,omitempty"`
	Payload    interface{} `json:"payload,omitempty"`
}

// linkEventTypeString maps a Drive LinkEventType to its CLI type string.
func linkEventTypeString(t proton.LinkEventType) string {
	switch t {
	case proton.LinkEventCreate:
		return typeLinkCreate
	case proton.LinkEventUpdate:
		return typeLinkUpdate
	case proton.LinkEventDelete:
		return typeLinkDelete
	case proton.LinkEventUpdateMetadata:
		return typeLinkMetadata
	default:
		return fmt.Sprintf("link-unknown(%d)", int(t))
	}
}

// printer renders event lines as JSONL to an io.Writer, applying the type
// filter and optional pretty-printing.
type printer struct {
	w      io.Writer
	pretty bool
	filter map[string]bool // empty means "emit everything"
}

// newPrinter builds a printer from the parsed flags.
func newPrinter(w io.Writer, pretty bool, types []string) *printer {
	filter := make(map[string]bool, len(types))
	for _, t := range types {
		filter[t] = true
	}
	return &printer{w: w, pretty: pretty, filter: filter}
}

// write renders one line unconditionally (bypassing the type filter).
func (p *printer) write(line eventLine) error {
	var (
		b   []byte
		err error
	)
	if p.pretty {
		b, err = json.MarshalIndent(line, "", "  ")
	} else {
		b, err = json.Marshal(line)
	}
	if err != nil {
		return fmt.Errorf("event watch: marshal event: %w", err)
	}
	if _, err := fmt.Fprintf(p.w, "%s\n", b); err != nil {
		return fmt.Errorf("event watch: write event: %w", err)
	}
	return nil
}

// emit renders one line subject to the type filter. When the filter is
// non-empty and the line's type is not selected, the line is dropped.
func (p *printer) emit(line eventLine) error {
	if len(p.filter) > 0 && !p.filter[line.Type] {
		return nil
	}
	return p.write(line)
}

// emitCoreEvent emits one JSONL line per populated category of a core event
// (Requirement 2.6). A refresh event emits a single refresh line and reports
// refresh=true so the caller can re-anchor its cursor. Timestamp is the
// client receipt time (Requirement 2.3).
func (p *printer) emitCoreEvent(ev proton.Event, now time.Time) (refreshed bool, err error) {
	ts := now.Format(time.RFC3339)

	if ev.Refresh != 0 {
		if err := p.write(eventLine{EventID: ev.EventID, Type: typeRefresh, Timestamp: ts, Refresh: true}); err != nil {
			return true, err
		}
		return true, nil
	}

	categories := []struct {
		typ       string
		populated bool
		payload   interface{}
	}{
		{typeUser, ev.User != nil, ev.User},
		{typeMailSettings, ev.MailSettings != nil, ev.MailSettings},
		{typeMessages, len(ev.Messages) > 0, ev.Messages},
		{typeLabels, len(ev.Labels) > 0, ev.Labels},
		{typeAddresses, len(ev.Addresses) > 0, ev.Addresses},
	}
	for _, c := range categories {
		if !c.populated {
			continue
		}
		if err := p.emit(eventLine{EventID: ev.EventID, Type: c.typ, Timestamp: ts, Payload: c.payload}); err != nil {
			return false, err
		}
	}
	return false, nil
}

// emitDriveEvent emits one JSONL line per LinkEvent in a Drive batch, or a
// single refresh line when the batch signals a resync. The raw proton.Link
// is carried as the payload; it is the encrypted API object (its name and
// keys remain encrypted), so it is safe to serialize.
func (p *printer) emitDriveEvent(target drive.WatchTarget, ev proton.DriveEvent, now time.Time) error {
	ts := now.Format(time.RFC3339)

	if bool(ev.Refresh) {
		return p.write(eventLine{
			EventID:   ev.EventID,
			Type:      typeRefresh,
			Timestamp: ts,
			VolumeID:  target.VolumeID,
			ShareID:   target.ShareID,
			Refresh:   true,
		})
	}

	for _, le := range ev.Events {
		line := eventLine{
			EventID:   le.EventID,
			Type:      linkEventTypeString(le.EventType),
			Timestamp: ts,
			VolumeID:  target.VolumeID,
			ShareID:   target.ShareID,
			LinkID:    le.Link.LinkID,
			Payload:   le.Link,
		}
		if le.CreateTime != 0 {
			line.CreateTime = int64(le.CreateTime)
		}
		if err := p.emit(line); err != nil {
			return err
		}
	}
	return nil
}
