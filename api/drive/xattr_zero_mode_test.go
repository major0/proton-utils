package drive

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"pgregory.net/rapid"
)

// observedMode extracts the persisted POSIX mode from a (decoded)
// RevisionXAttr as a (value, present) pair, reading presence from the JSON
// key itself: present is true only when the "POSIX" section exists AND
// carries a "Mode" key, mirroring the design's result.present / result.value.
// It is a test-side observation of what the write/read pipeline persisted —
// it does not touch production code. This is the single source of truth for
// the design's expectedBehavior(result): result.present == TRUE AND
// result.value == 0 for an explicit chmod 0000.
func observedMode(t fataler, x *proton.RevisionXAttr) (value uint32, present bool) {
	if x == nil || x.Extra == nil {
		return 0, false
	}
	raw, ok := x.Extra[posixXAttrKey]
	if !ok {
		return 0, false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("observedMode: unmarshal POSIX section %s: %v", raw, err)
	}
	mraw, ok := obj["Mode"]
	if !ok {
		return 0, false
	}
	var m uint32
	if err := json.Unmarshal(mraw, &m); err != nil {
		t.Fatalf("observedMode: unmarshal Mode %s: %v", mraw, err)
	}
	return m, true
}

// TestExplicitZeroModePersistsAndRoundTrips_BugCondition is the Property 1
// (Bug Condition) exploration test for the chmod-zero-vs-unset-mode bugfix.
//
// **Property 1: Bug Condition — Explicit Zero Mode Persists and Round-Trips**
// **Validates: Requirements 1.1, 1.2, 1.3, 2.1, 2.2, 2.4**
//
// It is a SCOPED property-based test: the bug is deterministic, so the input
// domain is scoped to the concrete failing case — a mode that is present and
// equal to 0 (an explicit `chmod 0000`) — crossed with the file/dir link
// types. It drives the REAL write and read helpers (buildRevisionXAttr,
// setPosixXAttr, encrypt/decrypt, decryptRevisionXAttr, Link.Mode) and asserts
// the design's expectedBehavior: after persisting an explicit 0000 the mode
// round-trips as present-and-zero, and the consumer default (0600) is applied
// only to a genuinely absent mode.
//
// This test MUST FAIL on the unfixed code — the failure confirms the bug:
// buildRevisionXAttr(mode=0) drops the whole POSIX section (the all-zero
// PosixXAttr marshals to "{}" and setPosixXAttr elides it), and the read path
// cannot distinguish a persisted present-zero from an absent section, so the
// consumer defaults 0000 up to 0600.
func TestExplicitZeroModePersistsAndRoundTrips_BugCondition(t *testing.T) {
	// Generate the keyring once (expensive RSA keygen) — reused across
	// property iterations.
	kr := xattrTestKeyRing(t)

	rapid.Check(t, func(t *rapid.T) {
		// Scope: mode is present and exactly 0 (the bug condition). Cross the
		// file/dir link types and vary the surrounding Common values so the
		// property is not a single fixed example.
		isFolder := rapid.Bool().Draw(t, "isFolder")
		size := rapid.Int64Range(0, 1<<40).Draw(t, "size")
		modTime := time.Unix(
			rapid.Int64Range(1000000000, 2000000000).Draw(t, "mtime"), 0,
		).UTC().Format("2006-01-02T15:04:05-0700")

		explicitZero := uint32(0) // chmod 0000, present

		// --- Write path: persist an explicit mode 0 via the real helper.
		// A non-nil pointer to 0 encodes "present and zero" (the bug condition),
		// distinct from a nil (absent) mode. ---
		built := buildRevisionXAttr(nil, modTime, size, nil, &explicitZero, false)

		wVal, wPresent := observedMode(t, built)
		if !wPresent {
			t.Fatalf("write path: buildRevisionXAttr(mode=0) produced NO present POSIX Mode "+
				"(section elided); want present POSIX Mode==0. built.Extra=%v", built.Extra)
		}
		if wVal != 0 {
			t.Fatalf("write path: persisted Mode = %d, want 0", wVal)
		}

		// --- End-to-end: encrypt what was persisted, then re-read it through
		// the real decrypt path on a Link of the drawn type. ---
		data, err := json.Marshal(built)
		if err != nil {
			t.Fatalf("marshal built xattr: %v", err)
		}
		blob := encryptArmoredXAttrT(t, kr, kr, data)

		resolver := &xattrResolver{addrKR: kr, addrID: "addr-1"}
		share := makeXAttrShare(resolver)

		var pLink *proton.Link
		if isFolder {
			pLink = &proton.Link{
				LinkID:         "dir-1",
				Type:           proton.LinkTypeFolder,
				State:          proton.LinkStateActive,
				SignatureEmail: "test@test.local",
				XAttr:          blob,
			}
		} else {
			pLink = &proton.Link{
				LinkID:         "file-1",
				Type:           proton.LinkTypeFile,
				State:          proton.LinkStateActive,
				SignatureEmail: "test@test.local",
				FileProperties: &proton.FileProperties{
					ActiveRevision: proton.RevisionMetadata{
						ID:             "rev-1",
						State:          proton.RevisionStateActive,
						Size:           size,
						SignatureEmail: "test@test.local",
						XAttr:          blob,
					},
				},
			}
		}
		link := NewTestLink(pLink, share.Link, share, resolver, "target")
		// Pre-cache the keyring to bypass real crypto derivation in tests.
		link.cachedKeyRing = kr

		// Drive the real accessor so the full lazy-decrypt path executes.
		_, _ = link.Mode()

		// Decrypt the round-tripped blob and observe present/value.
		nodeKR, err := link.KeyRing()
		if err != nil {
			t.Fatalf("KeyRing: %v", err)
		}
		decoded, err := link.decryptRevisionXAttr(nodeKR)
		if err != nil {
			t.Fatalf("decryptRevisionXAttr: %v", err)
		}
		rVal, rPresent := observedMode(t, decoded)
		if !rPresent {
			t.Fatalf("read path: explicit 0000 did not round-trip as present "+
				"(isFolder=%v); the persisted present-zero is indistinguishable from an "+
				"absent POSIX section. decoded=%+v", isFolder, decoded)
		}
		if rVal != 0 {
			t.Fatalf("read path: round-tripped Mode = %d, want 0", rVal)
		}

		// --- Consumer resolution: the default (0600) must apply ONLY to an
		// absent mode. A present 0000 must be used exactly. ---
		perm := uint32(0o600) // default for an ABSENT mode
		if rVal, ok := observedMode(t, decoded); ok {
			perm = rVal & 0o7777 // present — use exactly, including 0000
		}
		if perm != 0 {
			t.Fatalf("consumer resolution: reported permission = %#o, want 0 "+
				"(explicit 0000 defaulted up to 0600 — the reported bug)", perm)
		}
	})
}
