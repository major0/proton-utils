package drive

import (
	"context"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-utils/api"
)

// readdirEvictResolver is a LinkResolver whose GetLink is backed by an
// explicit table (so a child can be made to "disappear" mid-Readdir, as a
// concurrent event invalidation would) and whose ListLinkChildren returns a
// fixed set (the API fallthrough) while counting how often it is called.
type readdirEvictResolver struct {
	table       map[string]*Link // GetLink source; a missing key models an evicted child
	apiChildren []proton.Link    // ListLinkChildren result (the fallthrough listing)
	listCalls   int              // number of ListLinkChildren invocations
}

func (r *readdirEvictResolver) ListLinkChildren(_ context.Context, _, _ string, _ bool) ([]proton.Link, error) {
	r.listCalls++
	return r.apiChildren, nil
}

func (r *readdirEvictResolver) NewChildLink(_ context.Context, parent *Link, pLink *proton.Link) *Link {
	return NewTestLink(pLink, parent, parent.Share(), r, pLink.LinkID)
}

func (r *readdirEvictResolver) GetLink(id string) *Link { return r.table[id] }

func (r *readdirEvictResolver) AddressForEmail(_ string) (proton.Address, bool) {
	return proton.Address{}, false
}

func (r *readdirEvictResolver) AddressKeyRing(_ string) (*crypto.KeyRing, bool) {
	return nil, false
}

func (r *readdirEvictResolver) Throttle() *api.Throttle                       { return nil }
func (r *readdirEvictResolver) MaxWorkers() int                               { return 2 }
func (r *readdirEvictResolver) FetchRevisionXAttr(_ context.Context, _ *Link) {}

// newReaddirEvictFixture builds a share-root folder whose cachedChildIDs is
// pre-populated with childIDs. Every child is registered in the resolver's
// GetLink table and in the ListLinkChildren fallthrough set, except those
// named in evicted, which are absent from the table (GetLink returns nil)
// to model a concurrent eviction.
func newReaddirEvictFixture(childIDs []string, evicted map[string]bool) (*Link, *readdirEvictResolver) {
	r := &readdirEvictResolver{table: make(map[string]*Link)}

	pShare := &proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}}
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	root := NewTestLink(rootPLink, nil, nil, r, "root")
	share := NewShare(pShare, nil, root, r, "")
	share.MemoryCacheLevel = api.CacheMetadata
	root = NewTestLink(rootPLink, nil, share, r, "root")
	share.Link = root

	for _, id := range childIDs {
		pl := proton.Link{LinkID: id, Type: proton.LinkTypeFile, State: proton.LinkStateActive}
		r.apiChildren = append(r.apiChildren, pl)
		if !evicted[id] {
			plCopy := pl
			r.table[id] = NewTestLink(&plCopy, root, share, r, id)
		}
	}

	root.cachedChildIDs = append([]string(nil), childIDs...)
	return root, r
}

// collectReaddirNames drains Readdir and returns the child entry names,
// excluding the synthetic . and .. entries.
func collectReaddirNames(t *testing.T, l *Link) []string {
	t.Helper()
	var names []string
	for de := range l.Readdir(context.Background()) {
		if de.Err != nil {
			t.Fatalf("Readdir stream error: %v", de.Err)
		}
		name, err := de.EntryName()
		if err != nil {
			t.Fatalf("EntryName: %v", err)
		}
		if name == "." || name == ".." {
			continue
		}
		names = append(names, name)
	}
	return names
}

// assertExactNames fails if got contains any duplicate or does not match want
// as a set.
func assertExactNames(t *testing.T, got, want []string) {
	t.Helper()
	counts := make(map[string]int, len(got))
	for _, n := range got {
		counts[n]++
	}
	for n, c := range counts {
		if c > 1 {
			t.Errorf("entry %q appeared %d times, want 1", n, c)
		}
	}
	if len(got) != len(want) {
		t.Errorf("entry count = %d %v, want %d %v", len(got), got, len(want), want)
	}
	for _, w := range want {
		if counts[w] == 0 {
			t.Errorf("missing expected entry %q (got %v)", w, got)
		}
	}
}

// TestReaddirCacheEvictionYieldsNoDuplicates is the regression test for the
// dirent duplication bug. When a child is evicted from the link table while
// Readdir is streaming from cachedChildIDs (as a concurrent event
// invalidation does via deleteLink), the cache path must discard its attempt
// WITHOUT having emitted a partial set, then fall through to the API listing
// exactly once. Emitting during resolution and then falling through would
// re-yield the already-sent entries, duplicating them. The eviction position
// is varied because the pre-fix duplication count depended on it (an eviction
// near the end duplicated nearly every entry).
func TestReaddirCacheEvictionYieldsNoDuplicates(t *testing.T) {
	all := []string{"c1", "c2", "c3"}
	for _, evict := range all {
		t.Run("evict_"+evict, func(t *testing.T) {
			root, r := newReaddirEvictFixture(all, map[string]bool{evict: true})

			names := collectReaddirNames(t, root)

			assertExactNames(t, names, all)
			if r.listCalls != 1 {
				t.Errorf("ListLinkChildren calls = %d, want 1 (stale cache must fall through once)", r.listCalls)
			}
		})
	}
}

// TestReaddirCacheHitSkipsAPI verifies the happy path is preserved: when
// every cached child resolves, Readdir yields them once from cache and never
// calls the API.
func TestReaddirCacheHitSkipsAPI(t *testing.T) {
	all := []string{"c1", "c2", "c3"}
	root, r := newReaddirEvictFixture(all, nil)

	names := collectReaddirNames(t, root)

	assertExactNames(t, names, all)
	if r.listCalls != 0 {
		t.Errorf("ListLinkChildren calls = %d, want 0 (cache hit must not call the API)", r.listCalls)
	}
}

// TestInvalidateParentClearsCachedChildIDs verifies that InvalidateParent
// clears the parent's cachedChildIDs on the retained Link object, not just
// its link-table entry — so a subsequent Readdir re-lists from the API rather
// than serving a stale child set (e.g. one still listing a remotely removed
// child, or missing a remotely created one).
func TestInvalidateParentClearsCachedChildIDs(t *testing.T) {
	c, _, _ := newInvalidateTestClient(t)
	parent := &Link{cachedChildIDs: []string{"c1", "c2"}}
	c.putLink("parent-1", parent)

	c.InvalidateParent("parent-1")

	if parent.cachedChildIDs != nil {
		t.Errorf("parent cachedChildIDs = %v, want nil", parent.cachedChildIDs)
	}
}

// TestInvalidateLinkClearsParentCachedChildIDs verifies that InvalidateLink
// clears the parent's cachedChildIDs so the parent's next listing re-lists
// after a child is evicted.
func TestInvalidateLinkClearsParentCachedChildIDs(t *testing.T) {
	c, _, _ := newInvalidateTestClient(t)
	parent := &Link{cachedChildIDs: []string{"child-1"}}
	c.putLink("parent-1", parent)
	c.putLink("child-1", &Link{})

	c.InvalidateLink("child-1", "parent-1", 0)

	if parent.cachedChildIDs != nil {
		t.Errorf("parent cachedChildIDs = %v, want nil", parent.cachedChildIDs)
	}
}
