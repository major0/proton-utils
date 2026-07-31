package drive

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"pgregory.net/rapid"
)

// This file holds Property 2 (Preservation) for the chmod-zero-vs-unset-mode
// bugfix. It is the companion to the Property 1 (Bug Condition) exploration
// test in xattr_zero_mode_test.go and reuses the same helpers (observedMode,
// xattrTestKeyRing, encryptArmoredXAttrT, NewTestLink, xattrResolver,
// makeXAttrShare, genSiblingSections, canonicalizeJSON).
//
// Methodology: observation-first. These tests were written and run against the
// UNFIXED code — they capture the persisted XAttr bytes and the resolved mode
// that the current write/read pipeline produces for every input OUTSIDE the
// bug condition (mode absent, or present and non-zero), and assert them. They
// PASS on the unfixed code (establishing the baseline to preserve) and MUST
// keep passing after the fix — that equality is the F(input) = F'(input)
// preservation guarantee. observedMode (a JSON-level reader) is the single
// source of truth for present/value, so these assertions are independent of
// the Link.Mode() signature change the fix introduces.

// TestNonBuggyModesUnchanged_Preservation is Property 2 (Preservation): for
// every input where the bug condition does NOT hold, the persisted XAttr and
// the resolved mode are exactly what the unfixed pipeline produces today.
//
// **Property 2: Preservation — Non-Buggy Modes Unchanged**
// **Validates: Requirements 2.3, 3.1, 3.2, 3.4, 3.5**
//
// The input domain is NOT isBugCondition(X): the mode is either absent (a
// plain / mode-less commit, unixMode == 0 in the current write path) or
// present and non-zero (chmod with a value in 1..0o7777). It is crossed with
// the symlink flag, the file/dir link type, a random set of sibling XAttr
// sections, and an optionally-inherited prior POSIX section. For each input it
// drives the real write helper (buildRevisionXAttr) and the real read path
// (encrypt -> decryptRevisionXAttr) and asserts:
//
//   - present non-zero mode -> persists and round-trips {"Mode":N}
//   - absent mode, no symlink, no inherited POSIX -> no POSIX Mode persisted;
//     the consumer default (0600 file / 0700 dir) applies
//   - absent mode, no symlink, inherited prior POSIX -> the inherited section
//     is left untouched (a mode-less commit must not wipe it)
//   - symlink=true persists and reads back the Symlink marker regardless of
//     the mode value
//   - every sibling XAttr section is left byte-equivalent by the mode write
func TestNonBuggyModesUnchanged_Preservation(t *testing.T) {
	// Generate the keyring once (expensive RSA keygen) — reused across
	// property iterations.
	kr := xattrTestKeyRing(t)

	rapid.Check(t, func(t *rapid.T) {
		// --- Draw the non-bug-condition domain. ---
		// absent == true  -> mode-less commit (unixMode 0 in the write path).
		// absent == false -> a present, non-zero mode (never 0: that is the
		// bug condition, covered by Property 1).
		absent := rapid.Bool().Draw(t, "absent")
		var unixMode uint32
		if !absent {
			unixMode = rapid.Uint32Range(1, 0o7777).Draw(t, "mode")
		}
		symlink := rapid.Bool().Draw(t, "symlink")
		isFolder := rapid.Bool().Draw(t, "isFolder")

		// An optionally-inherited prior POSIX section (0 => none). This
		// exercises the mode-less-commit preservation guarantee.
		priorMode := rapid.Uint32Range(0, 0o7777).Draw(t, "priorMode")

		size := rapid.Int64Range(0, 1<<40).Draw(t, "size")
		blockSizes := rapid.SliceOfN(rapid.Int64Range(0, 1<<20), 0, 4).Draw(t, "blockSizes")
		modTime := time.Unix(
			rapid.Int64Range(1000000000, 2000000000).Draw(t, "mtime"), 0,
		).UTC().Format(time.RFC3339)

		// Arbitrary sibling sections written by other Proton clients. Drop
		// "Common" (owned by the typed Common member) and any "POSIX" (the
		// section under test) so the remainder are genuine siblings.
		siblings := genSiblingSections(t)
		delete(siblings, "Common")
		delete(siblings, posixXAttrKey)

		// Prior revision: siblings plus, some of the time, an inherited POSIX
		// section from an earlier non-zero mode.
		prior := &proton.RevisionXAttr{}
		if len(siblings) > 0 {
			prior.Extra = make(map[string]json.RawMessage, len(siblings)+1)
			for k, v := range siblings {
				prior.Extra[k] = v
			}
		}
		if priorMode != 0 {
			setPosixXAttr(prior, PosixXAttr{Mode: &priorMode})
		}

		// Snapshot canonicalized sibling values before the write.
		before := make(map[string]string, len(siblings))
		for k, v := range siblings {
			before[k] = canonicalizeJSON(t, v)
		}

		// --- Expected persisted state (the unfixed baseline). ---
		//   present non-zero        -> Mode key present, value == unixMode
		//   mode 0 + symlink        -> POSIX section written, but NO Mode key
		//   mode 0, no symlink, prior POSIX -> inherited Mode key preserved
		//   mode 0, no symlink, no prior    -> no POSIX section at all
		var wantPresent bool
		var wantVal uint32
		switch {
		case unixMode != 0:
			wantPresent, wantVal = true, unixMode
		case symlink:
			wantPresent, wantVal = false, 0
		case priorMode != 0:
			wantPresent, wantVal = true, priorMode
		default:
			wantPresent, wantVal = false, 0
		}

		// --- Write path: assemble the revision XAttr via the real helper. ---
		built := buildRevisionXAttr(prior, modTime, size, blockSizes, modePtr(unixMode), symlink)

		if gotVal, gotPresent := observedMode(t, built); gotPresent != wantPresent || gotVal != wantVal {
			t.Fatalf("write path: observedMode = (%d, %v), want (%d, %v) "+
				"[absent=%v symlink=%v priorMode=%d unixMode=%d]",
				gotVal, gotPresent, wantVal, wantPresent, absent, symlink, priorMode, unixMode)
		}

		// The symlink marker is written whenever symlink==true, regardless of
		// the mode value; a normal file never carries the marker.
		if pfs := posixFromXAttr(built); symlink {
			if pfs == nil || !pfs.Symlink {
				t.Fatalf("write path: symlink marker missing (pfs=%v)", pfs)
			}
		} else if pfs != nil && pfs.Symlink {
			t.Fatalf("write path: unexpected symlink marker on a normal file (pfs=%v)", pfs)
		}

		// Sibling sections are left byte-equivalent by the mode write.
		assertSiblingsPreserved(t, before, built.Extra)

		// --- Read path: encrypt what was persisted, then re-read it through
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

		// Drive the real accessor path (Stat internally resolves Mode as a
		// side effect) so the full lazy-decrypt pipeline executes. Its return
		// is not inspected — observedMode below is the source of truth.
		_ = link.Stat()

		// Decrypt the round-tripped blob and observe present/value.
		nodeKR, err := link.KeyRing()
		if err != nil {
			t.Fatalf("KeyRing: %v", err)
		}
		decoded, err := link.decryptRevisionXAttr(nodeKR)
		if err != nil {
			t.Fatalf("decryptRevisionXAttr: %v", err)
		}

		if gotVal, gotPresent := observedMode(t, decoded); gotPresent != wantPresent || gotVal != wantVal {
			t.Fatalf("read path: observedMode = (%d, %v), want (%d, %v) "+
				"[absent=%v symlink=%v priorMode=%d unixMode=%d]",
				gotVal, gotPresent, wantVal, wantPresent, absent, symlink, priorMode, unixMode)
		}

		// Symlink marker survives the round trip.
		if pfs := posixFromXAttr(decoded); symlink {
			if pfs == nil || !pfs.Symlink {
				t.Fatalf("read path: symlink marker lost across round trip (pfs=%v)", pfs)
			}
		} else if pfs != nil && pfs.Symlink {
			t.Fatalf("read path: unexpected symlink marker on a normal file (pfs=%v)", pfs)
		}

		// Sibling sections survive the round trip byte-equivalently.
		assertSiblingsPreserved(t, before, decoded.Extra)

		// --- Consumer resolution: the type default (0600 file / 0700 dir)
		// applies ONLY to a genuinely absent mode; a present mode is used
		// exactly. This mirrors FileNode.Getattr's default-on-absence logic. ---
		wantPerm := uint32(0o600)
		if isFolder {
			wantPerm = 0o700
		}
		if wantPresent {
			wantPerm = wantVal & 0o7777
		}

		perm := uint32(0o600)
		if isFolder {
			perm = 0o700
		}
		if v, ok := observedMode(t, decoded); ok {
			perm = v & 0o7777
		}
		if perm != wantPerm {
			t.Fatalf("consumer resolution: perm = %#o, want %#o "+
				"[absent=%v symlink=%v priorMode=%d unixMode=%d isFolder=%v]",
				perm, wantPerm, absent, symlink, priorMode, unixMode, isFolder)
		}
	})
}

// TestMalformedPosixDegrades_Preservation is the degradation half of Property
// 2: a POSIX section that is malformed, or an XAttr blob that cannot be
// decrypted, resolves to the consumer default without error or panic — exactly
// as the unfixed code behaves today.
//
// **Property 2: Preservation — Malformed / Undecryptable Degrades to Default**
// **Validates: Requirements 3.3**
//
// decryptXAttr (stable signature) is the observation point: it returns a nil
// *PosixXAttr for both a malformed section and an undecryptable blob, which is
// what makes the consumer fall back to the default permission. The full Stat()
// path is exercised to confirm no panic.
func TestMalformedPosixDegrades_Preservation(t *testing.T) {
	// kr encrypts/decrypts the well-formed cases; wrongKR forces an
	// undecryptable blob.
	kr := xattrTestKeyRing(t)
	wrongKR := xattrTestKeyRing(t)

	// Valid top-level JSON values that are NOT a decodable PosixXAttr object,
	// so posixFromXAttr returns nil (non-fatal). null/{} are excluded — they
	// decode to a zero PosixXAttr rather than nil.
	malformedPOSIX := []string{
		`123`,
		`12.5`,
		`"not-an-object"`,
		`true`,
		`[1,2,3]`,
		`{"Mode":"not-a-number"}`,
		`{"Mode":[1,2]}`,
	}

	rapid.Check(t, func(t *rapid.T) {
		scenario := rapid.SampledFrom([]string{
			"malformed",
			"undecryptable_wrong_keyring",
			"undecryptable_no_address",
		}).Draw(t, "scenario")

		size := rapid.Int64Range(0, 1<<40).Draw(t, "size")
		modTime := time.Unix(
			rapid.Int64Range(1000000000, 2000000000).Draw(t, "mtime"), 0,
		).UTC().Format(time.RFC3339)

		common := &proton.RevisionXAttrCommon{
			ModificationTime: modTime,
			Size:             size,
		}

		var blob string
		nodeKR := kr
		resolver := &xattrResolver{addrKR: kr, addrID: "addr-1"}

		switch scenario {
		case "malformed":
			// Valid Common + an unparseable POSIX section.
			bad := rapid.SampledFrom(malformedPOSIX).Draw(t, "malformedPOSIX")
			commonRaw, err := json.Marshal(map[string]any{
				"ModificationTime": modTime,
				"Size":             size,
			})
			if err != nil {
				t.Fatalf("marshal Common: %v", err)
			}
			top := map[string]json.RawMessage{
				"Common": commonRaw,
				"POSIX":  json.RawMessage(bad),
			}
			raw, err := json.Marshal(top)
			if err != nil {
				t.Fatalf("marshal malformed blob: %v", err)
			}
			blob = encryptArmoredXAttrT(t, kr, kr, raw)
		case "undecryptable_wrong_keyring":
			// Encrypted to wrongKR but resolved with kr — decryption fails.
			x := &proton.RevisionXAttr{Common: *common}
			setPosixXAttr(x, PosixXAttr{Mode: modePtr(0o644)})
			raw, err := json.Marshal(x)
			if err != nil {
				t.Fatalf("marshal xattr: %v", err)
			}
			blob = encryptArmoredXAttrT(t, wrongKR, kr, raw)
		case "undecryptable_no_address":
			// Blob encrypts fine, but the resolver supplies no address keyring.
			x := &proton.RevisionXAttr{Common: *common}
			setPosixXAttr(x, PosixXAttr{Mode: modePtr(0o644)})
			raw, err := json.Marshal(x)
			if err != nil {
				t.Fatalf("marshal xattr: %v", err)
			}
			blob = encryptArmoredXAttrT(t, kr, kr, raw)
			resolver.addrKR = nil // AddressForEmail returns not-ok
		}

		share := makeXAttrShare(resolver)
		pLink := &proton.Link{
			LinkID:         "degraded-file",
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
		link := NewTestLink(pLink, share.Link, share, resolver, "degraded")
		link.cachedKeyRing = nodeKR

		// Exercise the full accessor path — must not panic.
		_ = link.Stat()

		// The POSIX section never resolves: decryptXAttr returns a nil
		// *PosixXAttr, so the consumer falls back to the default. This holds
		// for both a malformed section and an undecryptable blob.
		if _, pfs := link.decryptXAttr(nodeKR); pfs != nil {
			t.Fatalf("[%s] decryptXAttr returned a non-nil POSIX section (%v); "+
				"want nil (degrade to default)", scenario, *pfs)
		}
	})
}

// assertSiblingsPreserved verifies every snapshotted sibling section is present
// in extra and byte-equivalent (canonicalized JSON) to its pre-write value.
func assertSiblingsPreserved(t *rapid.T, before map[string]string, extra map[string]json.RawMessage) {
	t.Helper()
	for k, wantCanon := range before {
		raw, ok := extra[k]
		if !ok {
			t.Fatalf("sibling section %q missing", k)
		}
		if gotCanon := canonicalizeJSON(t, raw); gotCanon != wantCanon {
			t.Fatalf("sibling %q changed: got %s, want %s", k, gotCanon, wantCanon)
		}
	}
}
