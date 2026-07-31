package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-utils/api"
	"pgregory.net/rapid"
)

// encryptSymlinkXAttrT builds an armored revision XAttr whose POSIX section
// carries the given Mode and Symlink marker, encrypted with kr. It is the
// symlink-aware sibling of encryptXAttrT (which only writes Mode), used to
// stand up file links whose IsSymlink() resolves through the real
// decrypt path. A Symlink=false, Mode=0 blob writes no POSIX section
// (section-level omitempty), matching a normal file at rest.
func encryptSymlinkXAttrT(t fataler, kr *crypto.KeyRing, mode uint32, symlink bool) string {
	x := proton.RevisionXAttr{
		Common: proton.RevisionXAttrCommon{ModificationTime: "2024-01-01T00:00:00+0000"},
	}
	setPosixXAttr(&x, PosixXAttr{Mode: modePtr(mode), Symlink: symlink})
	data, err := json.Marshal(x)
	if err != nil {
		t.Fatalf("marshal symlink xattr: %v", err)
	}
	return encryptArmoredXAttrT(t, kr, kr, data)
}

// newSymlinkFileLink builds a file-type Link whose active revision XAttr marks
// it as a symlink (POSIX Symlink=true), decryptable with kr. The keyring is
// pre-cached and the XAttr pre-populated so IsSymlink() resolves without a
// network fetch. testName drives Name() so folder Lookup can find it.
func newSymlinkFileLink(t fataler, kr *crypto.KeyRing, parent *Link, share *Share, resolver LinkResolver, id, name string) *Link {
	blob := encryptSymlinkXAttrT(t, kr, 0, true)
	pLink := &proton.Link{
		LinkID:         id,
		Type:           proton.LinkTypeFile,
		State:          proton.LinkStateActive,
		SignatureEmail: "test@test.local",
		FileProperties: &proton.FileProperties{
			ActiveRevision: proton.RevisionMetadata{
				ID:             "rev-" + id,
				State:          proton.RevisionStateActive,
				Size:           1,
				XAttr:          blob,
				SignatureEmail: "test@test.local",
			},
		},
	}
	l := NewTestLink(pLink, parent, share, resolver, name)
	l.cachedKeyRing = kr
	return l
}

// newRegularFileLink builds a file-type Link that is NOT a symlink (no POSIX
// symlink marker, no active-revision XAttr). IsSymlink() resolves to false.
func newRegularFileLink(kr *crypto.KeyRing, parent *Link, share *Share, resolver LinkResolver, id, name string) *Link {
	pLink := &proton.Link{
		LinkID:         id,
		Type:           proton.LinkTypeFile,
		State:          proton.LinkStateActive,
		SignatureEmail: "test@test.local",
		// nil FileProperties → no active revision → ensureXAttr no-op → not a symlink.
	}
	l := NewTestLink(pLink, parent, share, resolver, name)
	l.cachedKeyRing = kr
	return l
}

// symlinkChainResolver is a LinkResolver that resolves children from an
// in-memory map keyed by LinkID. It supplies a single test address keyring so
// symlink markers decrypt, and serves Lookup via GetLink over cachedChildIDs.
type symlinkChainResolver struct {
	kr     *crypto.KeyRing
	addrID string
	links  map[string]*Link
}

func (r *symlinkChainResolver) ListLinkChildren(_ context.Context, _, _ string, _ bool) ([]proton.Link, error) {
	return nil, nil
}

func (r *symlinkChainResolver) NewChildLink(_ context.Context, parent *Link, pLink *proton.Link) *Link {
	return NewTestLink(pLink, parent, parent.share, r, pLink.LinkID)
}

func (r *symlinkChainResolver) GetLink(id string) *Link { return r.links[id] }

func (r *symlinkChainResolver) AddressForEmail(_ string) (proton.Address, bool) {
	if r.kr == nil {
		return proton.Address{}, false
	}
	return proton.Address{ID: r.addrID}, true
}

func (r *symlinkChainResolver) AddressKeyRing(id string) (*crypto.KeyRing, bool) {
	if r.kr == nil || id != r.addrID {
		return nil, false
	}
	return r.kr, true
}

func (r *symlinkChainResolver) Throttle() *api.Throttle                       { return nil }
func (r *symlinkChainResolver) MaxWorkers() int                               { return 1 }
func (r *symlinkChainResolver) FetchRevisionXAttr(_ context.Context, _ *Link) {}

// TestIsSymlinkDetection_Property verifies Property 2 (detection is exact):
// for any file link, IsSymlink() is true iff the resolved POSIX section's
// Symlink field is true; folder links are never symlinks (short-circuit,
// no decryption). Exercised through the real ensureXAttr/decrypt path.
//
// **Property 2: Detection is exact**
// **Validates: Requirements 4.1**
func TestIsSymlinkDetection_Property(t *testing.T) {
	// Generate the keyring once (expensive RSA keygen) — reused across iterations.
	kr := xattrTestKeyRing(t)

	rapid.Check(t, func(t *rapid.T) {
		isFolder := rapid.Bool().Draw(t, "isFolder")
		symlink := rapid.Bool().Draw(t, "symlink")
		mode := rapid.Uint32Range(0, 0o7777).Draw(t, "mode")

		blob := encryptSymlinkXAttrT(t, kr, mode, symlink)

		resolver := &xattrResolver{
			addrKR: kr,
			addrID: "addr-1",
			fetchFunc: func(link *Link) {
				link.protonLink.FileProperties.ActiveRevision.XAttr = blob
			},
		}
		share := makeXAttrShare(resolver)

		if isFolder {
			// A folder carrying a symlink-marked link-level XAttr must still
			// report false — IsSymlink short-circuits on type without decrypt.
			pLink := &proton.Link{
				LinkID:           "folder-1",
				Type:             proton.LinkTypeFolder,
				State:            proton.LinkStateActive,
				SignatureEmail:   "test@test.local",
				XAttr:            blob,
				FolderProperties: &proton.FolderProperties{},
			}
			link := NewTestLink(pLink, share.Link, share, resolver, "dir")
			link.cachedKeyRing = kr
			if link.IsSymlink() {
				t.Fatalf("folder IsSymlink() = true, want false (marker=%v)", symlink)
			}
			return
		}

		pLink := &proton.Link{
			LinkID:         "file-1",
			Type:           proton.LinkTypeFile,
			State:          proton.LinkStateActive,
			SignatureEmail: "test@test.local",
			FileProperties: &proton.FileProperties{
				ActiveRevision: proton.RevisionMetadata{
					ID:             "rev-1",
					State:          proton.RevisionStateActive,
					SignatureEmail: "test@test.local",
				},
			},
		}
		link := NewTestLink(pLink, share.Link, share, resolver, "file")
		link.cachedKeyRing = kr

		if got := link.IsSymlink(); got != symlink {
			t.Fatalf("IsSymlink() = %v, want %v (mode=%d)", got, symlink, mode)
		}
	})
}

// pathTargetGen draws a plausible, verbatim symlink target: a slash-joined
// sequence of path segments, optionally absolute (leading /) and optionally
// prefixed with ".." components (relative/dangling). All shapes are stored
// verbatim, so the round-trip must be exact regardless of shape. Generated
// targets are well under maxSymlinkTarget bytes.
func pathTargetGen(t *rapid.T) string {
	seg := rapid.StringMatching(`[a-zA-Z0-9._-]{1,12}`)
	n := rapid.IntRange(1, 6).Draw(t, "target_segments")
	parts := make([]string, 0, n)
	for i := 0; i < n; i++ {
		parts = append(parts, seg.Draw(t, fmt.Sprintf("target_seg_%d", i)))
	}
	target := strings.Join(parts, "/")

	switch rapid.IntRange(0, 2).Draw(t, "target_shape") {
	case 0:
		return "/" + target // absolute
	case 1:
		return "../" + target // relative, likely dangling
	default:
		return target // in-directory relative
	}
}

// TestReadSymlinkTargetRoundTrip_Property verifies Property 3 (verbatim
// round-trip) over a decrypt-backed FileDescriptor: reading the content of a
// symlink returns exactly the bytes stored, for arbitrary targets (relative,
// absolute, dangling). It drives readSymlinkTarget over the same reader OpenFD
// produces (NewTestFD), the split-out bounded-read tail of ReadSymlinkTarget —
// exercising the round-trip without the live-session content path.
//
// **Property 3: Target round-trip is verbatim**
// **Validates: Requirements 1.3, 2.2, 3.1**
func TestReadSymlinkTargetRoundTrip_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		target := pathTargetGen(t)

		fd, err := NewTestFD([]byte(target))
		if err != nil {
			t.Fatalf("NewTestFD: %v", err)
		}
		defer func() { _ = fd.Close() }()

		got, err := readSymlinkTarget(fd, "test-link")
		if err != nil {
			t.Fatalf("readSymlinkTarget(%q): %v", target, err)
		}
		if got != target {
			t.Fatalf("round-trip mismatch: got %q, want %q", got, target)
		}
	})
}

// TestReadSymlinkTargetBound_Property verifies the PATH_MAX bound of
// ReadSymlinkTarget's bounded read: content up to and including
// maxSymlinkTarget (4096) bytes is returned verbatim; content larger than the
// bound is a malformed symlink and yields an error (never a truncated target).
//
// **Property 3 (bound): content > maxSymlinkTarget is malformed**
// **Validates: Requirements 3.1, 3.2**
func TestReadSymlinkTargetBound_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Draw sizes densely around the boundary plus a few well past it.
		size := rapid.OneOf(
			rapid.IntRange(1, maxSymlinkTarget+8),
			rapid.IntRange(maxSymlinkTarget, 4*maxSymlinkTarget),
		).Draw(t, "content_size")

		content := bytes.Repeat([]byte("a"), size)
		got, err := readSymlinkTarget(bytes.NewReader(content), "test-link")

		if size > maxSymlinkTarget {
			if err == nil {
				t.Fatalf("size %d > bound %d: expected malformed error, got target of len %d", size, maxSymlinkTarget, len(got))
			}
			if !strings.Contains(err.Error(), "malformed symlink") {
				t.Fatalf("size %d: error %q does not report a malformed symlink", size, err)
			}
			return
		}

		if err != nil {
			t.Fatalf("size %d <= bound %d: unexpected error: %v", size, maxSymlinkTarget, err)
		}
		if got != string(content) {
			t.Fatalf("size %d: verbatim mismatch (got len %d)", size, len(got))
		}
	})
}

// TestCreateSymlinkEmptyTarget_EINVAL verifies that CreateSymlink rejects an
// empty target with EINVAL before any upload/commit work — POSIX rejects an
// empty target, and a zero-length target would yield a zero-block file.
//
// **Property 3 (empty): empty target → EINVAL**
// **Validates: Requirements 1.4**
func TestCreateSymlinkEmptyTarget_EINVAL(t *testing.T) {
	c := &Client{}
	// share/parent are never dereferenced — the empty-target guard returns first.
	_, err := c.CreateSymlink(context.Background(), nil, nil, "link", "")
	if !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("CreateSymlink(empty target) = %v, want EINVAL", err)
	}
}

// TestSymlinkNoPlaintextTargetAtRest_Property verifies Property 4
// (no-plaintext-target-at-rest) per the encrypted-data-handling steering: a
// symlink's target rides only in the encrypted content block, and the only
// symlink metadata at rest is the encrypted POSIX Symlink marker.
//
// It establishes, for an arbitrary target plus arbitrary sibling sections:
//   - the target never appears in the revision XAttr at all (it is content,
//     not metadata) — neither in the plaintext form nor the encrypted request;
//   - the POSIX section carries only the boolean Symlink marker, encrypted at
//     rest — no "POSIX"/"Symlink" plaintext leaks into any request field, and
//     the marker (and siblings) come back solely via decryption;
//   - the serialized POSIX section carries "Symlink":true for a symlink and
//     never a "Symlink" key for a normal file (absence means false);
//   - the target content block is ciphertext at rest and decrypts back verbatim.
//
// The target sentinel and sibling sentinel use underscores (absent from the
// base64 armor alphabet), so their absence from the armored XAttr is
// deterministic, not probabilistic.
//
// **Property 4: No plaintext target at rest**
// **Validates: Requirements 2.2, encrypted-data-handling**
func TestSymlinkNoPlaintextTargetAtRest_Property(t *testing.T) {
	kr := xattrTestKeyRing(t)

	const (
		targetSentinel  = "SYMLINK_TARGET_SENTINEL_do_not_leak_at_rest_"
		siblingSentinel = "SIBLING_SENTINEL_MEDIA_do_not_leak_at_rest"
	)

	rapid.Check(t, func(t *rapid.T) {
		siblings := genSiblingSections(t)
		delete(siblings, "Common")
		siblings["Media"] = json.RawMessage(`{"tag":"` + siblingSentinel + `"}`)

		// An arbitrary, collision-free target and an optional POSIX mode that
		// may accompany the symlink marker.
		target := targetSentinel + rapid.StringMatching(`[a-z_]{0,24}`).Draw(t, "target_suffix")
		mode := rapid.Uint32Range(0, 0o7777).Draw(t, "mode")

		// Build the symlink revision XAttr exactly as the commit path does:
		// a fresh Common, inherited siblings, and the POSIX Symlink marker.
		x := &proton.RevisionXAttr{
			Common: proton.RevisionXAttrCommon{
				ModificationTime: "2024-06-01T12:30:00+0000",
				Size:             int64(len(target)),
				BlockSizes:       []int64{int64(len(target))},
			},
			Extra: make(map[string]json.RawMessage, len(siblings)),
		}
		for k, v := range siblings {
			x.Extra[k] = v
		}
		setPosixXAttr(x, PosixXAttr{Mode: modePtr(mode), Symlink: true})

		// Sanity: the plaintext XAttr carries the markers we claim are hidden,
		// and — crucially — never the target (the target lives in content).
		plain, err := json.Marshal(x)
		if err != nil {
			t.Fatalf("marshal plaintext xattr: %v", err)
		}
		plainStr := string(plain)
		for _, want := range []string{`"POSIX"`, `"Symlink":true`, siblingSentinel} {
			if !strings.Contains(plainStr, want) {
				t.Fatalf("fixture plaintext missing %q — negative checks would be vacuous", want)
			}
		}
		if strings.Contains(plainStr, target) {
			t.Fatalf("target leaked into the XAttr — it must ride in the content block, not metadata")
		}

		// Encrypt exactly as commitRevisionFromTokens does.
		var req proton.UpdateRevisionReq
		if err := req.SetEncXAttrString(kr, kr, x); err != nil {
			t.Fatalf("SetEncXAttrString: %v", err)
		}
		if !strings.HasPrefix(req.XAttr, "-----BEGIN PGP MESSAGE-----") {
			t.Fatalf("req.XAttr is not PGP-armored: %.48q", req.XAttr)
		}

		// No plaintext marker (incl. the target) leaks into ANY request field.
		markers := []string{`"POSIX"`, `"Symlink"`, siblingSentinel, target}
		for k := range siblings {
			markers = append(markers, `"`+k+`"`)
		}
		fields := map[string]string{
			"XAttr":             req.XAttr,
			"ManifestSignature": req.ManifestSignature,
			"SignatureAddress":  req.SignatureAddress,
		}
		for name, val := range fields {
			for _, m := range markers {
				if strings.Contains(val, m) {
					t.Fatalf("plaintext marker %q leaked into UpdateRevisionReq.%s", m, name)
				}
			}
		}

		// The Symlink marker and siblings are recoverable ONLY via decryption.
		rev := proton.RevisionMetadata{XAttr: req.XAttr}
		dec, err := rev.GetDecXAttrString(kr, kr)
		if err != nil {
			t.Fatalf("GetDecXAttrString: %v", err)
		}
		gotPosix := posixFromXAttr(dec)
		if gotPosix == nil || !gotPosix.Symlink {
			t.Fatalf("decrypted POSIX section = %+v, want Symlink=true", gotPosix)
		}
		if mode == 0 {
			if gotPosix.Mode != nil {
				t.Fatalf("decrypted POSIX Mode = %d, want absent", *gotPosix.Mode)
			}
		} else if gotPosix.Mode == nil || *gotPosix.Mode != mode {
			t.Fatalf("decrypted POSIX Mode = %v, want %d", gotPosix.Mode, mode)
		}
		media, ok := dec.Extra["Media"]
		if !ok || !strings.Contains(string(media), siblingSentinel) {
			t.Fatalf("decrypted XAttr lost the Media sibling sentinel")
		}

		// The serialized POSIX section carries "Symlink":true for a symlink,
		// and a normal file (same mode, no marker) carries no "Symlink" key.
		if !bytes.Contains(x.Extra[posixXAttrKey], []byte(`"Symlink":true`)) {
			t.Fatalf("symlink POSIX section lacks \"Symlink\":true: %s", x.Extra[posixXAttrKey])
		}
		normal := &proton.RevisionXAttr{}
		setPosixXAttr(normal, PosixXAttr{Mode: modePtr(mode | 1), Symlink: false})
		if raw, ok := normal.Extra[posixXAttrKey]; ok && bytes.Contains(raw, []byte("Symlink")) {
			t.Fatalf("normal-file POSIX section carries a Symlink key: %s", raw)
		}

		// The target content block is ciphertext at rest and decrypts verbatim.
		sk, err := crypto.GenerateSessionKey()
		if err != nil {
			t.Fatalf("GenerateSessionKey: %v", err)
		}
		ct, err := sk.Encrypt(crypto.NewPlainMessage([]byte(target)))
		if err != nil {
			t.Fatalf("session Encrypt: %v", err)
		}
		if bytes.Contains(ct, []byte(target)) {
			t.Fatalf("target appears as plaintext in the encrypted content block")
		}
		pm, err := sk.Decrypt(ct)
		if err != nil {
			t.Fatalf("session Decrypt: %v", err)
		}
		if string(pm.GetBinary()) != target {
			t.Fatalf("content block did not decrypt verbatim: got %q, want %q", pm.GetBinary(), target)
		}
	})
}

// TestResolveFollowLoopDetection_Property verifies Property 5 (CLI loop
// detection terminates): for any symlink cycle in the CLI resolver, following
// terminates and returns ErrSymlinkLoop (ELOOP) — it never spins. It builds a
// ring of relative symlinks s0→s1→…→s(k-1)→s0 in one directory and confirms
// ResolveFollow surfaces ErrSymlinkLoop for cycle lengths from a self-loop up.
//
// **Property 5: CLI loop detection terminates**
// **Validates: Requirements 5.1, 5.4**
func TestResolveFollowLoopDetection_Property(t *testing.T) {
	kr := xattrTestKeyRing(t)

	rapid.Check(t, func(t *rapid.T) {
		k := rapid.IntRange(1, 8).Draw(t, "cycle_len")

		resolver := &symlinkChainResolver{kr: kr, addrID: "addr-1", links: map[string]*Link{}}
		share := makeXAttrShare(resolver)
		root := share.Link

		targets := make(map[string]string, k)
		ids := make([]string, 0, k)
		for i := 0; i < k; i++ {
			name := fmt.Sprintf("s%d", i)
			id := "sl-" + name
			l := newSymlinkFileLink(t, kr, root, share, resolver, id, name)
			resolver.links[id] = l
			// s_i points at s_{(i+1) % k} — a relative target in the same dir.
			targets[id] = fmt.Sprintf("s%d", (i+1)%k)
			ids = append(ids, id)
		}
		root.cachedChildIDs = ids

		c := &Client{
			symlinkTargetReader: func(_ context.Context, l *Link) (string, error) {
				return targets[l.LinkID()], nil
			},
		}

		_, err := c.ResolveFollow(context.Background(), share, root, "s0", false)
		if !errors.Is(err, ErrSymlinkLoop) {
			t.Fatalf("cycle of length %d: got err %v, want ErrSymlinkLoop", k, err)
		}
	})
}

// TestResolveFollowChainTerminates_Property verifies the complement of loop
// detection: a non-cyclic symlink chain of any length below the depth cap
// terminates by resolving to its final regular-file target. It builds a chain
// s0→s1→…→s(k-1)→f and confirms ResolveFollow returns f.
//
// **Property 5 (termination): a bounded chain resolves**
// **Validates: Requirements 5.1**
func TestResolveFollowChainTerminates_Property(t *testing.T) {
	kr := xattrTestKeyRing(t)

	rapid.Check(t, func(t *rapid.T) {
		k := rapid.IntRange(0, 12).Draw(t, "chain_len")

		resolver := &symlinkChainResolver{kr: kr, addrID: "addr-1", links: map[string]*Link{}}
		share := makeXAttrShare(resolver)
		root := share.Link

		targets := make(map[string]string)
		ids := make([]string, 0, k+1)

		// Terminal regular file the chain resolves to.
		const fileID = "f-target"
		fileLink := newRegularFileLink(kr, root, share, resolver, fileID, "target")
		resolver.links[fileID] = fileLink
		ids = append(ids, fileID)

		for i := 0; i < k; i++ {
			name := fmt.Sprintf("s%d", i)
			id := "sl-" + name
			l := newSymlinkFileLink(t, kr, root, share, resolver, id, name)
			resolver.links[id] = l
			if i == k-1 {
				targets[id] = "target" // last symlink points at the regular file
			} else {
				targets[id] = fmt.Sprintf("s%d", i+1)
			}
			ids = append(ids, id)
		}
		root.cachedChildIDs = ids

		c := &Client{
			symlinkTargetReader: func(_ context.Context, l *Link) (string, error) {
				return targets[l.LinkID()], nil
			},
		}

		start := "target"
		if k > 0 {
			start = "s0"
		}
		got, err := c.ResolveFollow(context.Background(), share, root, start, false)
		if err != nil {
			t.Fatalf("chain of length %d: unexpected error: %v", k, err)
		}
		if got == nil || got.LinkID() != fileID {
			t.Fatalf("chain of length %d: resolved to %v, want the regular file %q", k, got, fileID)
		}
	})
}
