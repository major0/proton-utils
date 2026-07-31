package drive

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"pgregory.net/rapid"
)

// canonicalizeJSON re-encodes a raw JSON value into a canonical form
// (object keys sorted by encoding/json, insignificant whitespace removed) so
// two byte-different-but-semantically-equal encodings compare equal.
func canonicalizeJSON(t *rapid.T, raw json.RawMessage) string {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("canonicalizeJSON: unmarshal %s: %v", raw, err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("canonicalizeJSON: marshal: %v", err)
	}
	return string(b)
}

// genJSONValue draws an arbitrary, valid JSON value: scalar, null, a flat
// object of integers, or an integer array. These stand in for the opaque
// sibling sections other Proton clients (Media, Camera, Location) write.
func genJSONValue(t *rapid.T, label string) json.RawMessage {
	var v interface{}
	switch rapid.IntRange(0, 6).Draw(t, label+"_shape") {
	case 0:
		v = rapid.String().Draw(t, label+"_str")
	case 1:
		v = rapid.Int64().Draw(t, label+"_int")
	case 2:
		v = rapid.Bool().Draw(t, label+"_bool")
	case 3:
		v = nil
	case 4:
		v = rapid.Float64Range(-1e9, 1e9).Draw(t, label+"_float")
	case 5:
		m := make(map[string]int64)
		keys := rapid.SliceOfN(rapid.StringMatching(`[a-z]{1,4}`), 0, 3).Draw(t, label+"_objkeys")
		for j, k := range keys {
			m[k] = rapid.Int64().Draw(t, fmt.Sprintf("%s_objval_%d", label, j))
		}
		v = m
	case 6:
		v = rapid.SliceOfN(rapid.Int64(), 0, 4).Draw(t, label+"_arr")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("genJSONValue: marshal: %v", err)
	}
	return raw
}

// genSiblingSections draws an arbitrary set of unmodeled top-level XAttr
// sections: arbitrary keys other than "POSIX" mapped to arbitrary JSON values.
func genSiblingSections(t *rapid.T) map[string]json.RawMessage {
	n := rapid.IntRange(0, 5).Draw(t, "num_siblings")
	out := make(map[string]json.RawMessage, n)
	for i := 0; i < n; i++ {
		key := rapid.StringMatching(`[A-Za-z][A-Za-z0-9]{0,7}`).Draw(t, fmt.Sprintf("sibling_key_%d", i))
		if key == posixXAttrKey {
			continue // POSIX is the section under test, not a sibling
		}
		out[key] = genJSONValue(t, fmt.Sprintf("sibling_val_%d", i))
	}
	return out
}

// modePtr returns a *uint32 for a test mode value, preserving the historical
// "0 means absent" convention these tests were written under: 0 yields nil (no
// POSIX Mode key), any non-zero value yields a pointer to it. The present-zero
// path (&0) is exercised directly by the chmod-zero-vs-unset-mode bugfix tests.
func modePtr(mode uint32) *uint32 {
	if mode == 0 {
		return nil
	}
	return &mode
}

// samePosix reports whether two PosixXAttr values are equal, comparing the
// optional Mode by presence and value rather than by pointer address.
func samePosix(a, b PosixXAttr) bool {
	if a.Symlink != b.Symlink {
		return false
	}
	if (a.Mode == nil) != (b.Mode == nil) {
		return false
	}
	return a.Mode == nil || *a.Mode == *b.Mode
}

// TestPosixXAttrRoundTrip_Property verifies that a PosixXAttr with a non-zero
// field survives a setPosixXAttr -> posixFromXAttr round-trip unchanged, and
// that the set operation leaves every pre-existing sibling section in Extra
// byte-equivalent (compared as canonicalized JSON).
//
// **Property 2: `PosixXAttr` round-trips through `Extra["POSIX"]`**
// **Validates: Requirements 2.6, 3.4, 6.3**
func TestPosixXAttrRoundTrip_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		siblings := genSiblingSections(t)

		// Build the starting RevisionXAttr with the generated sibling sections.
		x := &proton.RevisionXAttr{}
		if len(siblings) > 0 {
			x.Extra = make(map[string]json.RawMessage, len(siblings))
			for k, v := range siblings {
				x.Extra[k] = v
			}
		}

		// Snapshot the canonicalized form of each sibling before the set op.
		before := make(map[string]string, len(siblings))
		for k, v := range siblings {
			before[k] = canonicalizeJSON(t, v)
		}

		// Apply setPosixXAttr with a generated non-zero Mode.
		mode := rapid.Uint32Range(1, math.MaxUint32).Draw(t, "mode")
		pfs := PosixXAttr{Mode: &mode}
		setPosixXAttr(x, pfs)

		// Round-trip: the value read back must equal the value written.
		got := posixFromXAttr(x)
		if got == nil {
			t.Fatalf("posixFromXAttr returned nil after setPosixXAttr with Mode=%d", mode)
			return
		}
		if !samePosix(*got, pfs) {
			t.Fatalf("round-trip mismatch: got %+v, want %+v", *got, pfs)
		}

		// Sibling preservation: every pre-existing section is byte-equivalent
		// (canonicalized) to its pre-call value.
		for k, wantCanon := range before {
			raw, ok := x.Extra[k]
			if !ok {
				t.Fatalf("sibling section %q missing after setPosixXAttr", k)
			}
			if gotCanon := canonicalizeJSON(t, raw); gotCanon != wantCanon {
				t.Fatalf("sibling %q changed: got %s, want %s", k, gotCanon, wantCanon)
			}
		}

		// The set op adds exactly the POSIX key and touches nothing else.
		if len(x.Extra) != len(before)+1 {
			t.Fatalf("Extra size = %d, want %d (siblings + POSIX)", len(x.Extra), len(before)+1)
		}
		for k := range x.Extra {
			if k == posixXAttrKey {
				continue
			}
			if _, ok := before[k]; !ok {
				t.Fatalf("unexpected new sibling section %q in Extra", k)
			}
		}
	})
}

// TestBuildRevisionXAttrPreservesSiblings_Property is the headline
// non-destructive-commit guarantee for the write path: given a prior revision
// whose XAttr carries arbitrary sibling sections (Media, Location, ...) plus a
// stale Common, buildRevisionXAttr produces a RevisionXAttr that
//
//   - preserves every sibling section byte-equivalently (canonicalized JSON),
//   - replaces Common with the NEW revision's values (never the stale prior),
//   - reflects the new mode in POSIX when a non-zero mode is given, and
//   - leaves an inherited POSIX section untouched when the new mode is 0.
//
// It also verifies the prior blob is not mutated and that the result survives
// a marshal->unmarshal round-trip through the fork's RevisionXAttr codec.
//
// **Property: non-destructive read-modify-write commit (upload clobber gap)**
// **Validates: Requirements 1.4, 2.2, 2.3, 3.4**
func TestBuildRevisionXAttrPreservesSiblings_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Arbitrary sibling sections written by other Proton clients. Drop any
		// "Common" key: the typed Common owns that section and the fork codec
		// discards a stray Extra["Common"], so it is not a valid sibling.
		siblings := genSiblingSections(t)
		delete(siblings, "Common")

		// Prior revision: stale Common plus the sibling sections, and — some of
		// the time — an inherited POSIX section from an earlier mode.
		prior := &proton.RevisionXAttr{
			Common: proton.RevisionXAttrCommon{
				ModificationTime: "2000-01-01T00:00:00+0000",
				Size:             rapid.Int64Range(0, 1<<30).Draw(t, "priorSize"),
				BlockSizes:       []int64{1, 2, 3},
			},
		}
		if len(siblings) > 0 {
			prior.Extra = make(map[string]json.RawMessage, len(siblings)+1)
			for k, v := range siblings {
				prior.Extra[k] = v
			}
		}
		priorMode := rapid.Uint32Range(0, 0o7777).Draw(t, "priorMode")
		if priorMode != 0 {
			setPosixXAttr(prior, PosixXAttr{Mode: &priorMode})
		}

		// Snapshot canonicalized sibling values before the merge.
		before := make(map[string]string, len(siblings))
		for k, v := range siblings {
			before[k] = canonicalizeJSON(t, v)
		}

		// New revision values.
		newMTime := "2024-06-01T12:30:00+0000"
		newSize := rapid.Int64Range(0, 1<<40).Draw(t, "newSize")
		newBlocks := rapid.SliceOfN(rapid.Int64Range(0, 1<<20), 0, 5).Draw(t, "newBlocks")
		newMode := rapid.Uint32().Draw(t, "newMode")

		// The write path treats a mode whose low 12 bits are 0 as mode-less
		// (nil), matching the pre-fix "0 means absent" convention this test was
		// written under; any other value is a present mode.
		var newModeArg *uint32
		if newMode&0o7777 != 0 {
			newModeArg = &newMode
		}
		got := buildRevisionXAttr(prior, newMTime, newSize, newBlocks, newModeArg, false)

		// Common reflects the NEW revision, never the stale prior.
		if got.Common.ModificationTime != newMTime {
			t.Fatalf("Common.ModificationTime = %q, want %q", got.Common.ModificationTime, newMTime)
		}
		if got.Common.Size != newSize {
			t.Fatalf("Common.Size = %d, want %d", got.Common.Size, newSize)
		}
		if len(got.Common.BlockSizes) != len(newBlocks) {
			t.Fatalf("Common.BlockSizes len = %d, want %d", len(got.Common.BlockSizes), len(newBlocks))
		}
		for i, bs := range newBlocks {
			if got.Common.BlockSizes[i] != bs {
				t.Fatalf("Common.BlockSizes[%d] = %d, want %d", i, got.Common.BlockSizes[i], bs)
			}
		}

		// Every sibling section is preserved byte-equivalently.
		for k, wantCanon := range before {
			raw, ok := got.Extra[k]
			if !ok {
				t.Fatalf("sibling section %q dropped by buildRevisionXAttr", k)
			}
			if gotCanon := canonicalizeJSON(t, raw); gotCanon != wantCanon {
				t.Fatalf("sibling %q changed: got %s, want %s", k, gotCanon, wantCanon)
			}
		}

		// POSIX semantics: a non-zero new mode replaces the section; a zero new
		// mode leaves any inherited POSIX section untouched (must not wipe it).
		effNew := newMode & 0o7777
		pfs := posixFromXAttr(got)
		switch {
		case effNew != 0:
			if pfs == nil || pfs.Mode == nil || *pfs.Mode != effNew {
				t.Fatalf("POSIX mode = %v, want %d (new mode)", pfs, effNew)
			}
		case priorMode != 0:
			if pfs == nil || pfs.Mode == nil || *pfs.Mode != priorMode {
				t.Fatalf("inherited POSIX mode = %v, want %d (preserved)", pfs, priorMode)
			}
		default:
			if pfs != nil {
				t.Fatalf("POSIX section present (%v) but neither new nor prior mode set", *pfs)
			}
		}

		// The prior blob must not be mutated by the merge.
		for k, wantCanon := range before {
			if gotCanon := canonicalizeJSON(t, prior.Extra[k]); gotCanon != wantCanon {
				t.Fatalf("prior sibling %q mutated by buildRevisionXAttr", k)
			}
		}

		// The result survives a round-trip through the fork's RevisionXAttr
		// codec (the actual on-the-wire encode/decode path).
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("marshal built xattr: %v", err)
		}
		var decoded proton.RevisionXAttr
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("unmarshal built xattr: %v", err)
		}
		for k, wantCanon := range before {
			raw, ok := decoded.Extra[k]
			if !ok {
				t.Fatalf("sibling %q lost across marshal round-trip", k)
			}
			if gotCanon := canonicalizeJSON(t, raw); gotCanon != wantCanon {
				t.Fatalf("sibling %q changed across round-trip: got %s, want %s", k, gotCanon, wantCanon)
			}
		}
		if decoded.Common.Size != newSize || decoded.Common.ModificationTime != newMTime {
			t.Fatalf("Common not preserved across round-trip: %+v", decoded.Common)
		}
	})
}

// TestPosixXAttrOmitEmpty_Property verifies section-level omitempty: an
// all-zero PosixXAttr (Mode == 0) is a no-op for setPosixXAttr. It must never
// add, modify, or clear the POSIX key.
//
//   - Starting from a RevisionXAttr with NO POSIX section (arbitrary sibling
//     sections), setPosixXAttr(x, PosixXAttr{}) leaves Extra without a POSIX
//     key and every sibling byte-equivalent (canonicalized JSON).
//   - Starting from a RevisionXAttr that ALREADY carries a POSIX section,
//     setPosixXAttr(x, PosixXAttr{}) leaves that section untouched — neither
//     cleared nor modified.
//
// **Property 3: No metadata produces no `POSIX` section (omitempty)**
// **Validates: Requirements 3.2, 3.3, 6.4**
func TestPosixXAttrOmitEmpty_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		siblings := genSiblingSections(t)

		// Build the starting RevisionXAttr with the generated sibling sections.
		x := &proton.RevisionXAttr{}
		if len(siblings) > 0 {
			x.Extra = make(map[string]json.RawMessage, len(siblings))
			for k, v := range siblings {
				x.Extra[k] = v
			}
		}

		// Snapshot the canonicalized form of each sibling before the set op.
		before := make(map[string]string, len(siblings))
		for k, v := range siblings {
			before[k] = canonicalizeJSON(t, v)
		}

		// Optionally seed an existing POSIX section with a non-zero mode; the
		// empty set op must leave it exactly as-is.
		hasPriorPosix := rapid.Bool().Draw(t, "has_prior_posix")
		var priorPosixCanon string
		if hasPriorPosix {
			priorMode := rapid.Uint32Range(1, 0o7777).Draw(t, "prior_mode")
			setPosixXAttr(x, PosixXAttr{Mode: &priorMode})
			priorPosixCanon = canonicalizeJSON(t, x.Extra[posixXAttrKey])
		}

		// Apply the empty (all-zero) PosixXAttr — this must be a no-op.
		setPosixXAttr(x, PosixXAttr{})

		if hasPriorPosix {
			// The pre-existing POSIX section is left untouched.
			raw, ok := x.Extra[posixXAttrKey]
			if !ok {
				t.Fatalf("existing POSIX section was cleared by empty setPosixXAttr")
			}
			if gotCanon := canonicalizeJSON(t, raw); gotCanon != priorPosixCanon {
				t.Fatalf("existing POSIX section modified by empty setPosixXAttr: got %s, want %s", gotCanon, priorPosixCanon)
			}
		} else {
			// No POSIX key is added.
			if _, ok := x.Extra[posixXAttrKey]; ok {
				t.Fatalf("empty setPosixXAttr added a POSIX section: %s", x.Extra[posixXAttrKey])
			}
		}

		// Sibling sections are byte-equivalent (canonicalized) to their
		// pre-call values — the empty set op touches nothing else.
		for k, wantCanon := range before {
			raw, ok := x.Extra[k]
			if !ok {
				t.Fatalf("sibling section %q missing after empty setPosixXAttr", k)
			}
			if gotCanon := canonicalizeJSON(t, raw); gotCanon != wantCanon {
				t.Fatalf("sibling %q changed by empty setPosixXAttr: got %s, want %s", k, gotCanon, wantCanon)
			}
		}

		// The empty set op adds no sibling and drops none. Expected size is the
		// sibling count plus the prior POSIX section (if any).
		want := len(before)
		if hasPriorPosix {
			want++
		}
		if len(x.Extra) != want {
			t.Fatalf("Extra size = %d, want %d", len(x.Extra), want)
		}
	})
}

// TestPosixXAttrSymlinkRoundTrip_Property verifies Property 1 (POSIX marker
// round-trip) of the protonfs-symlinks spec: for any PosixXAttr carrying
// Symlink=true, a setPosixXAttr -> posixFromXAttr round-trip preserves the
// Symlink flag (alongside an arbitrary Mode), while normal-file blobs — those
// with Symlink absent — stay byte-identical:
//
//   - Symlink=true (any Mode) round-trips: the value read back equals the
//     value written, and every pre-existing sibling section is preserved.
//   - Symlink=false with Mode==0 is the normal-file case: setPosixXAttr is a
//     no-op — no POSIX section is added and the blob is byte-identical.
//   - Symlink=false with a non-zero Mode writes a POSIX section that carries
//     no "Symlink" key (omitempty), so a normal file's POSIX section is
//     byte-identical to the pre-symlink format.
//
// **Property 1: POSIX marker round-trip**
// **Validates: Requirements 1.2**
func TestPosixXAttrSymlinkRoundTrip_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		siblings := genSiblingSections(t)

		// Build the starting RevisionXAttr with the generated sibling sections.
		x := &proton.RevisionXAttr{}
		if len(siblings) > 0 {
			x.Extra = make(map[string]json.RawMessage, len(siblings))
			for k, v := range siblings {
				x.Extra[k] = v
			}
		}

		// Snapshot the canonicalized form of each sibling before the set op.
		before := make(map[string]string, len(siblings))
		for k, v := range siblings {
			before[k] = canonicalizeJSON(t, v)
		}

		symlink := rapid.Bool().Draw(t, "symlink")
		mode := rapid.Uint32Range(0, 0o7777).Draw(t, "mode")
		pfs := PosixXAttr{Mode: modePtr(mode), Symlink: symlink}
		setPosixXAttr(x, pfs)

		writesSection := symlink || mode != 0

		if writesSection {
			// Round-trip: the value read back must equal the value written,
			// preserving Symlink (and Mode).
			got := posixFromXAttr(x)
			if got == nil {
				t.Fatalf("posixFromXAttr returned nil after setPosixXAttr with %+v", pfs)
				return
			}
			if !samePosix(*got, pfs) {
				t.Fatalf("round-trip mismatch: got %+v, want %+v", *got, pfs)
			}

			// Normal-file byte-identity: when Symlink is absent (false), the
			// serialized POSIX section must not contain a "Symlink" key, so a
			// normal file's blob matches the pre-symlink format exactly.
			if !symlink {
				var raw map[string]json.RawMessage
				if err := json.Unmarshal(x.Extra[posixXAttrKey], &raw); err != nil {
					t.Fatalf("unmarshal POSIX section: %v", err)
				}
				if _, ok := raw["Symlink"]; ok {
					t.Fatalf("normal-file POSIX section carries a Symlink key: %s", x.Extra[posixXAttrKey])
				}
			}
		} else {
			// Normal file with no POSIX metadata: setPosixXAttr is a no-op —
			// no POSIX key is added (byte-identical to a blob without POSIX).
			if _, ok := x.Extra[posixXAttrKey]; ok {
				t.Fatalf("normal-file setPosixXAttr added a POSIX section: %s", x.Extra[posixXAttrKey])
			}
		}

		// Sibling preservation: every pre-existing section is byte-equivalent
		// (canonicalized) to its pre-call value — nothing else is touched.
		for k, wantCanon := range before {
			raw, ok := x.Extra[k]
			if !ok {
				t.Fatalf("sibling section %q missing after setPosixXAttr", k)
			}
			if gotCanon := canonicalizeJSON(t, raw); gotCanon != wantCanon {
				t.Fatalf("sibling %q changed: got %s, want %s", k, gotCanon, wantCanon)
			}
		}
	})
}
