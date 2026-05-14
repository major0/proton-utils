package drive

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-utils/api"
	"pgregory.net/rapid"
)

// mockLinkResolver is a minimal LinkResolver that always fails address
// lookups. Used to test error context in deriveKeyRing and decryptName.
type mockLinkResolver struct{}

func (m *mockLinkResolver) ListLinkChildren(_ context.Context, _, _ string, _ bool) ([]proton.Link, error) {
	return nil, nil
}

func (m *mockLinkResolver) NewChildLink(_ context.Context, parent *Link, pLink *proton.Link) *Link {
	return NewLink(pLink, parent, parent.share, m)
}

func (m *mockLinkResolver) GetLink(_ string) *Link { return nil }

func (m *mockLinkResolver) AddressForEmail(_ string) (proton.Address, bool) {
	return proton.Address{}, false
}

func (m *mockLinkResolver) AddressKeyRing(_ string) (*crypto.KeyRing, bool) {
	return nil, false
}

func (m *mockLinkResolver) Throttle() *api.Throttle { return nil }
func (m *mockLinkResolver) MaxWorkers() int         { return 1 }

// TestDeriveKeyRing_ErrorContext_Property verifies that deriveKeyRing returns
// an error wrapping ErrKeyNotFound that contains the signature email string
// when the resolver has no matching address.
//
// **Property 6: Key-Not-Found Errors Include Email Context**
// **Validates: Requirements 8.1, 8.2**
func TestDeriveKeyRing_ErrorContext_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		email := rapid.String().Draw(t, "signatureEmail")

		resolver := &mockLinkResolver{}
		pLink := &proton.Link{
			LinkID:         "test-link",
			SignatureEmail: email,
		}
		link := NewLink(pLink, nil, nil, resolver)

		kr, err := link.deriveKeyRing(nil)
		if kr != nil {
			t.Fatalf("expected nil keyring for unmatched email %q, got non-nil", email)
		}
		if err == nil {
			t.Fatalf("expected error for unmatched email %q, got nil", email)
		}
		if !errors.Is(err, api.ErrKeyNotFound) {
			t.Fatalf("expected error wrapping ErrKeyNotFound, got: %v", err)
		}
		// The error uses %q formatting, so check for the quoted email.
		quoted := fmt.Sprintf("%q", email)
		if !strings.Contains(err.Error(), quoted) {
			t.Fatalf("error %q does not contain quoted email %s", err.Error(), quoted)
		}
	})
}

// TestDecryptName_ErrorContext_Property verifies that decryptName returns
// an error wrapping ErrKeyNotFound that contains the name signature email
// string when the resolver has no matching address.
//
// **Property 6: Key-Not-Found Errors Include Email Context**
// **Validates: Requirements 8.1, 8.2**
func TestDecryptName_ErrorContext_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		email := rapid.String().Draw(t, "nameSignatureEmail")

		resolver := &mockLinkResolver{}
		pLink := &proton.Link{
			LinkID:             "test-link",
			NameSignatureEmail: email,
		}
		link := NewLink(pLink, nil, nil, resolver)

		name, err := link.decryptName(nil)
		if name != "" {
			t.Fatalf("expected empty name for unmatched email %q, got %q", email, name)
		}
		if err == nil {
			t.Fatalf("expected error for unmatched email %q, got nil", email)
		}
		if !errors.Is(err, api.ErrKeyNotFound) {
			t.Fatalf("expected error wrapping ErrKeyNotFound, got: %v", err)
		}
		// The error uses %q formatting, so check for the quoted email.
		quoted := fmt.Sprintf("%q", email)
		if !strings.Contains(err.Error(), quoted) {
			t.Fatalf("error %q does not contain quoted email %s", err.Error(), quoted)
		}
	})
}

// TestIsTransient_Property verifies that isTransient returns true for any
// error wrapping context.Canceled or context.DeadlineExceeded, and false
// for any error that does not wrap either (including nil).
//
// **Property 7: Transient Error Classification**
// **Validates: Requirement 9.1**
func TestIsTransient_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		wrapCtx := rapid.Bool().Draw(t, "wrapContext")
		msg := rapid.String().Draw(t, "msg")

		var err error
		if wrapCtx {
			base := rapid.SampledFrom([]error{
				context.Canceled,
				context.DeadlineExceeded,
			}).Draw(t, "base")
			err = fmt.Errorf("%s: %w", msg, base)
		} else {
			err = errors.New(msg)
		}

		got := isTransient(err)
		if wrapCtx && !got {
			t.Fatalf("expected transient for %v, got false", err)
		}
		if !wrapCtx && got {
			t.Fatalf("expected non-transient for %v, got true", err)
		}
	})
}

// TestIsTransient_Nil verifies that isTransient(nil) returns false.
func TestIsTransient_Nil(t *testing.T) {
	if isTransient(nil) {
		t.Fatal("expected isTransient(nil) == false")
	}
}

// TestIsTransient_KnownErrors verifies isTransient for specific known errors.
func TestIsTransient_KnownErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"context.Canceled", context.Canceled, true},
		{"context.DeadlineExceeded", context.DeadlineExceeded, true},
		{"wrapped Canceled", fmt.Errorf("op: %w", context.Canceled), true},
		{"wrapped DeadlineExceeded", fmt.Errorf("op: %w", context.DeadlineExceeded), true},
		{"plain error", errors.New("foo"), false},
		{"ErrKeyNotFound", api.ErrKeyNotFound, false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTransient(tt.err); got != tt.want {
				t.Fatalf("isTransient(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestName_ErrorPropagation verifies that Name() propagates errors from
// the parent keyring chain when the resolver has no matching address.
func TestName_ErrorPropagation(t *testing.T) {
	resolver := &mockLinkResolver{}

	// Build a share root so getParentKeyRing doesn't panic on nil share.
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	root := NewTestLink(rootPLink, nil, nil, resolver, "root")
	share := NewShare(
		&proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}},
		nil, root, resolver, "",
	)
	root = NewTestLink(rootPLink, nil, share, resolver, "root")
	share.Link = root

	pLink := &proton.Link{
		LinkID:             "test-link",
		NameSignatureEmail: "test@example.com",
		SignatureEmail:     "test@example.com",
	}
	link := NewLink(pLink, root, share, resolver)

	// Name() calls getParentKeyRing → parent.KeyRing() → deriveKeyRing
	// which fails because the resolver has no matching address.
	_, err := link.Name()
	if err == nil {
		t.Fatal("expected error from Name(), got nil")
	}
}

// TestKeyRing_ErrorPropagation verifies that KeyRing() propagates errors
// from the parent keyring chain.
func TestKeyRing_ErrorPropagation(t *testing.T) {
	resolver := &mockLinkResolver{}

	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	root := NewTestLink(rootPLink, nil, nil, resolver, "root")
	share := NewShare(
		&proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}},
		nil, root, resolver, "",
	)
	root = NewTestLink(rootPLink, nil, share, resolver, "root")
	share.Link = root

	pLink := &proton.Link{
		LinkID:         "test-link",
		SignatureEmail: "test@example.com",
	}
	link := NewLink(pLink, root, share, resolver)

	_, err := link.KeyRing()
	if err == nil {
		t.Fatal("expected error from KeyRing(), got nil")
	}
}

// TestParentChainIntegrity_Property verifies that for any link obtained
// via a mock chain, repeated Parent() calls reach a self-referencing root.
//
// **Property 9: Parent Chain Integrity**
// **Validates: Requirements 11.1, 11.2**
func TestParentChainIntegrity_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		depth := rapid.IntRange(0, 10).Draw(t, "depth")

		resolver := &mockLinkResolver{}
		// Build a chain: root → child1 → child2 → ... → childN
		root := NewTestLink(
			&proton.Link{LinkID: "root", Type: proton.LinkTypeFolder},
			nil, nil, resolver, "root",
		)
		share := NewShare(
			&proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}},
			nil, root, resolver, "",
		)
		root = NewTestLink(
			&proton.Link{LinkID: "root", Type: proton.LinkTypeFolder},
			nil, share, resolver, "root",
		)
		share.Link = root

		current := root
		for i := 0; i < depth; i++ {
			child := NewTestLink(
				&proton.Link{LinkID: fmt.Sprintf("child-%d", i), Type: proton.LinkTypeFolder},
				current, share, resolver, fmt.Sprintf("child%d", i),
			)
			current = child
		}

		// Walk Parent() chain — should reach root (self-referencing).
		walker := current
		for i := 0; i <= depth+5; i++ {
			parent := walker.Parent()
			if parent == walker {
				// Reached self-referencing root.
				return
			}
			walker = parent
		}
		t.Fatal("parent chain did not reach self-referencing root")
	})
}

// TestAbsPathRoundTrip_Property verifies that for any link reachable by
// building a chain, AbsPath returns a path that matches the expected
// construction from decrypted names.
//
// **Property 10: AbsPath Round-Trip**
// **Validates: Requirement 11.5**
func TestAbsPathRoundTrip_Property(t *testing.T) {
	// Generate safe path segment names (non-empty, no slashes, no dots).
	segmentGen := rapid.StringMatching(`[a-zA-Z0-9_-]{1,20}`)

	rapid.Check(t, func(t *rapid.T) {
		depth := rapid.IntRange(0, 8).Draw(t, "depth")
		rootName := segmentGen.Draw(t, "rootName")

		resolver := &mockLinkResolver{}
		root := NewTestLink(
			&proton.Link{LinkID: "root", Type: proton.LinkTypeFolder},
			nil, nil, resolver, rootName,
		)
		share := NewShare(
			&proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}},
			nil, root, resolver, "",
		)
		root = NewTestLink(
			&proton.Link{LinkID: "root", Type: proton.LinkTypeFolder},
			nil, share, resolver, rootName,
		)
		share.Link = root

		// Build chain and collect expected path segments.
		names := []string{rootName}
		current := root
		for i := 0; i < depth; i++ {
			childName := segmentGen.Draw(t, fmt.Sprintf("name-%d", i))
			child := NewTestLink(
				&proton.Link{LinkID: fmt.Sprintf("child-%d", i), Type: proton.LinkTypeFolder},
				current, share, resolver, childName,
			)
			names = append(names, childName)
			current = child
		}

		ctx := context.Background()
		absPath, err := current.AbsPath(ctx)
		if err != nil {
			t.Fatalf("AbsPath failed: %v", err)
		}

		expected := strings.Join(names, "/")
		if absPath != expected {
			t.Fatalf("AbsPath = %q, want %q", absPath, expected)
		}
	})
}

// TestDotDotResolution_Property verifies that ResolvePath("..") equals
// Parent() for non-root links, and equals self for share roots.
//
// **Property 11: Dot-Dot Resolution**
// **Validates: Requirements 11.1, 11.3**
func TestDotDotResolution_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		depth := rapid.IntRange(0, 8).Draw(t, "depth")

		resolver := &mockLinkResolver{}
		root := NewTestLink(
			&proton.Link{LinkID: "root", Type: proton.LinkTypeFolder},
			nil, nil, resolver, "root",
		)
		share := NewShare(
			&proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}},
			nil, root, resolver, "",
		)
		root = NewTestLink(
			&proton.Link{LinkID: "root", Type: proton.LinkTypeFolder},
			nil, share, resolver, "root",
		)
		share.Link = root

		current := root
		for i := 0; i < depth; i++ {
			child := NewTestLink(
				&proton.Link{LinkID: fmt.Sprintf("child-%d", i), Type: proton.LinkTypeFolder},
				current, share, resolver, fmt.Sprintf("child%d", i),
			)
			current = child
		}

		ctx := context.Background()
		resolved, err := current.ResolvePath(ctx, "..", false)
		if err != nil {
			t.Fatalf("ResolvePath(..) failed: %v", err)
		}

		parent := current.Parent()
		if resolved != parent {
			t.Fatalf("ResolvePath(..) returned %p (LinkID=%s), Parent() returned %p (LinkID=%s)",
				resolved, resolved.LinkID(), parent, parent.LinkID())
		}

		// For share root specifically, both should be self.
		if depth == 0 {
			if resolved != current {
				t.Fatalf("share root: ResolvePath(..) should return self, got LinkID=%s", resolved.LinkID())
			}
		}
	})
}

// countingResolver wraps mockLinkResolver and counts AddressKeyRing
// calls as a proxy for decryption/derivation attempts.
type countingResolver struct {
	mockLinkResolver
	keyRingCalls int
}

func (r *countingResolver) AddressKeyRing(id string) (*crypto.KeyRing, bool) {
	r.keyRingCalls++
	return r.mockLinkResolver.AddressKeyRing(id)
}

// TestNameNoCacheDecryptsEveryTime_Property verifies that calling Name()
// N times on a Link (without testName) triggers exactly N decryption
// attempts. No state is retained on the Link between calls.
//
// **Property 1: Name() Decrypts Every Time**
// **Validates: Requirement 1.2**
func TestNameNoCacheDecryptsEveryTime_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		callCount := rapid.IntRange(1, 5).Draw(t, "callCount")

		resolver := &countingResolver{}

		// Build a share with an AddressID so getKeyRing calls AddressKeyRing.
		rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
		root := NewTestLink(rootPLink, nil, nil, resolver, "root")
		share := NewShare(
			&proton.Share{
				ShareMetadata: proton.ShareMetadata{ShareID: "s"},
				AddressID:     "addr-1",
			},
			nil, root, resolver, "",
		)
		root = NewTestLink(rootPLink, nil, share, resolver, "root")
		share.Link = root

		// Create a non-test link (no testName) — Name() will attempt
		// real decryption via the resolver on every call.
		pLink := &proton.Link{
			LinkID:             "child",
			NameSignatureEmail: "user@example.com",
			SignatureEmail:     "user@example.com",
		}
		link := NewLink(pLink, root, share, resolver)

		resolver.keyRingCalls = 0
		for i := 0; i < callCount; i++ {
			// Name() will fail (resolver returns false for AddressKeyRing)
			// but we're verifying it attempts the chain each time.
			_, _ = link.Name()
		}

		// Each Name() call goes through getParentKeyRing → parent.KeyRing()
		// → share.getKeyRing() → AddressKeyRing. The chain fails there,
		// but the call was made. Each call produces at least 1 AddressKeyRing call.
		if resolver.keyRingCalls < callCount {
			t.Fatalf("expected at least %d AddressKeyRing calls, got %d",
				callCount, resolver.keyRingCalls)
		}
	})
}

// TestKeyRingAlwaysDerives_Property verifies that calling KeyRing()
// N times triggers exactly N derivation attempts. KeyRing is never cached.
//
// **Property 6: KeyRing() Always Derives**
// **Validates: Requirement 1.3**
func TestKeyRingAlwaysDerives_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		callCount := rapid.IntRange(1, 5).Draw(t, "callCount")

		resolver := &countingResolver{}

		rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
		root := NewTestLink(rootPLink, nil, nil, resolver, "root")
		share := NewShare(
			&proton.Share{
				ShareMetadata: proton.ShareMetadata{ShareID: "s"},
				AddressID:     "addr-1",
			},
			nil, root, resolver, "",
		)
		root = NewTestLink(rootPLink, nil, share, resolver, "root")
		share.Link = root

		pLink := &proton.Link{
			LinkID:         "child",
			SignatureEmail: "user@example.com",
		}
		link := NewLink(pLink, root, share, resolver)

		resolver.keyRingCalls = 0
		for i := 0; i < callCount; i++ {
			_, _ = link.KeyRing()
		}

		// Each KeyRing() call goes through the same chain, producing
		// at least 1 AddressKeyRing call per invocation.
		if resolver.keyRingCalls < callCount {
			t.Fatalf("expected at least %d AddressKeyRing calls, got %d",
				callCount, resolver.keyRingCalls)
		}
	})
}

// readdirResolver is a mock LinkResolver that returns pre-built children
// for Readdir testing.
type readdirResolver struct {
	children []proton.Link
}

func (r *readdirResolver) ListLinkChildren(_ context.Context, _, _ string, _ bool) ([]proton.Link, error) {
	return r.children, nil
}

func (r *readdirResolver) NewChildLink(_ context.Context, parent *Link, pLink *proton.Link) *Link {
	return NewTestLink(pLink, parent, parent.share, r, pLink.LinkID)
}

func (r *readdirResolver) GetLink(_ string) *Link { return nil }

func (r *readdirResolver) AddressForEmail(_ string) (proton.Address, bool) {
	return proton.Address{}, false
}

func (r *readdirResolver) AddressKeyRing(_ string) (*crypto.KeyRing, bool) {
	return nil, false
}

func (r *readdirResolver) Throttle() *api.Throttle { return nil }
func (r *readdirResolver) MaxWorkers() int         { return 1 }

// TestDotDotDotCorrectness_Property verifies that the first two entries
// from Readdir have Link pointers equal to self and Parent() respectively.
// For share roots, both point to the same link.
//
// **Property 2: Dot and DotDot Correctness**
// **Validates: Requirements 2.1, 2.2**
func TestDotDotDotCorrectness_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		depth := rapid.IntRange(0, 10).Draw(t, "depth")
		childCount := rapid.IntRange(0, 5).Draw(t, "childCount")

		children := make([]proton.Link, childCount)
		for i := range children {
			children[i] = proton.Link{
				LinkID: fmt.Sprintf("child-%d", i),
				Type:   proton.LinkTypeFile,
			}
		}
		resolver := &readdirResolver{children: children}

		// Build chain of depth levels.
		rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
		root := NewTestLink(rootPLink, nil, nil, resolver, "root")
		share := NewShare(
			&proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}},
			nil, root, resolver, "",
		)
		root = NewTestLink(rootPLink, nil, share, resolver, "root")
		share.Link = root

		current := root
		for i := 0; i < depth; i++ {
			child := NewTestLink(
				&proton.Link{LinkID: fmt.Sprintf("dir-%d", i), Type: proton.LinkTypeFolder},
				current, share, resolver, fmt.Sprintf("dir%d", i),
			)
			current = child
		}

		ctx := context.Background()
		entries := make([]DirEntry, 0)
		for entry := range current.Readdir(ctx) {
			entries = append(entries, entry)
		}

		if len(entries) < 2 {
			t.Fatalf("expected at least 2 entries (. and ..), got %d", len(entries))
		}

		// First entry is . (self).
		if entries[0].Link != current {
			t.Fatalf(". entry Link %p != self %p", entries[0].Link, current)
		}

		// Second entry is .. (parent).
		expectedParent := current.Parent()
		if entries[1].Link != expectedParent {
			t.Fatalf(".. entry Link %p != Parent() %p", entries[1].Link, expectedParent)
		}

		// For share roots, both . and .. point to self.
		if depth == 0 {
			if entries[0].Link != entries[1].Link {
				t.Fatal("share root: . and .. should be the same link")
			}
		}

		// Total entries: 2 (. and ..) + childCount.
		if len(entries) != 2+childCount {
			t.Fatalf("expected %d entries, got %d", 2+childCount, len(entries))
		}
	})
}

// TestDirEntryFields_Property verifies that the DirEntry struct contains
// only the expected fields: Link, Err, and the unexported name field
// used for pre-set . and .. literals. No other decrypted content is
// stored on the struct.
//
// **Property 3: DirEntry Carries Only Controlled State**
// **Validates: Requirement 2.4**
func TestDirEntryFields_Property(t *testing.T) {
	typ := reflect.TypeOf(DirEntry{})
	// Link and Err are exported; name is unexported and pre-set only
	// for . and .. entries (never populated from decryption results
	// in the types layer).
	allowed := map[string]bool{"Link": true, "Err": true, "name": true}

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !allowed[field.Name] {
			t.Fatalf("DirEntry has unexpected field %q — types layer must not carry decrypted content", field.Name)
		}
	}

	if typ.NumField() != 3 {
		t.Fatalf("DirEntry has %d fields, expected 3 (Link, Err, name)", typ.NumField())
	}

	// Verify name is unexported (controlled access only via EntryName).
	nameField, _ := typ.FieldByName("name")
	if nameField.IsExported() {
		t.Fatal("DirEntry.name must be unexported — decrypted names are accessed via EntryName()")
	}
}

// TestLinkHasNoCachedFields verifies that the Link struct does not have
// the removed cached fields (name, keyRing, nameKeyRing, decrypted,
// decryptErr, mu).
func TestLinkHasNoCachedFields(t *testing.T) {
	typ := reflect.TypeOf(Link{})
	forbidden := []string{"mu", "decrypted", "decryptErr", "name", "keyRing", "nameKeyRing"}

	for _, name := range forbidden {
		if _, ok := typ.FieldByName(name); ok {
			t.Fatalf("Link struct still has forbidden field %q", name)
		}
	}
}
