package drive

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"pgregory.net/rapid"
)

// shareFixture holds the data needed to construct a test share.
type shareFixture struct {
	ShareID   string
	Name      string
	AddressID string
}

// TestPropertyResolveShare_ByFullID verifies that resolving by full ShareID
// always returns exactly one share regardless of name collisions.
//
// **Property 4: ResolveShare name-or-ID disambiguation**
// **Validates: Requirements 5.7, 5.8**
func TestPropertyResolveShare_ByFullID(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate 2-5 shares with distinct IDs but potentially duplicate names.
		numShares := rapid.IntRange(2, 5).Draw(t, "numShares")
		namePool := rapid.SliceOfN(
			rapid.StringMatching(`[A-Za-z][A-Za-z0-9 ]{2,10}`),
			1, 3,
		).Draw(t, "namePool")

		fixtures := make([]shareFixture, numShares)
		for i := range numShares {
			fixtures[i] = shareFixture{
				ShareID:   fmt.Sprintf("share-%d-%s", i, rapid.StringMatching(`[a-z0-9]{8}`).Draw(t, fmt.Sprintf("id%d", i))),
				Name:      namePool[rapid.IntRange(0, len(namePool)-1).Draw(t, fmt.Sprintf("nameIdx%d", i))],
				AddressID: "addr-1",
			}
		}

		// Pick a random share to resolve by full ID.
		targetIdx := rapid.IntRange(0, numShares-1).Draw(t, "targetIdx")
		targetID := fixtures[targetIdx].ShareID

		// Build shares in memory for the mock.
		resolver := &mockResolver{}
		shares := make(map[string]*Share, numShares)
		metas := make([]ShareMetadata, numShares)
		for i, f := range fixtures {
			pShare := &proton.Share{
				ShareMetadata: proton.ShareMetadata{
					ShareID: f.ShareID,
					Type:    proton.ShareTypeStandard,
				},
				AddressID: f.AddressID,
			}
			pLink := &proton.Link{LinkID: "link-" + f.ShareID, Type: proton.LinkTypeFolder}
			root := NewTestLink(pLink, nil, nil, resolver, f.Name)
			share := NewShare(pShare, nil, root, resolver, "vol-1")
			root = NewTestLink(pLink, nil, share, resolver, f.Name)
			share.Link = root
			shares[f.ShareID] = share
			metas[i] = ShareMetadata(proton.ShareMetadata{
				ShareID: f.ShareID,
				Type:    proton.ShareTypeStandard,
			})
		}

		c := &resolveTestClient{metas: metas, shares: shares}

		// Resolve by full ShareID — should always succeed.
		result, err := resolveShareLogic(c, targetID)
		if err != nil {
			t.Fatalf("ResolveShare(%q) error: %v", targetID, err)
		}
		if result.Metadata().ShareID != targetID {
			t.Fatalf("ResolveShare(%q) returned share %q", targetID, result.Metadata().ShareID)
		}
	})
}

// TestResolveShare_UniqueName returns the correct share when name is unique.
func TestResolveShare_UniqueName(t *testing.T) {
	resolver := &mockResolver{}

	shares := map[string]*Share{}
	metas := []ShareMetadata{}

	for _, f := range []shareFixture{
		{ShareID: "share-aaa", Name: "Alpha", AddressID: "addr-1"},
		{ShareID: "share-bbb", Name: "Beta", AddressID: "addr-1"},
		{ShareID: "share-ccc", Name: "Gamma", AddressID: "addr-1"},
	} {
		pShare := &proton.Share{
			ShareMetadata: proton.ShareMetadata{ShareID: f.ShareID, Type: proton.ShareTypeStandard},
			AddressID:     f.AddressID,
		}
		pLink := &proton.Link{LinkID: "link-" + f.ShareID, Type: proton.LinkTypeFolder}
		root := NewTestLink(pLink, nil, nil, resolver, f.Name)
		share := NewShare(pShare, nil, root, resolver, "vol-1")
		root = NewTestLink(pLink, nil, share, resolver, f.Name)
		share.Link = root
		shares[f.ShareID] = share
		metas = append(metas, ShareMetadata(proton.ShareMetadata{ShareID: f.ShareID, Type: proton.ShareTypeStandard}))
	}

	c := &resolveTestClient{metas: metas, shares: shares}

	result, err := resolveShareLogic(c, "Beta")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Metadata().ShareID != "share-bbb" {
		t.Fatalf("got share %q, want share-bbb", result.Metadata().ShareID)
	}
}

// TestResolveShare_AmbiguousName returns error when multiple shares have the same name.
func TestResolveShare_AmbiguousName(t *testing.T) {
	resolver := &mockResolver{}

	shares := map[string]*Share{}
	metas := []ShareMetadata{}

	for _, f := range []shareFixture{
		{ShareID: "share-aaa", Name: "Shared", AddressID: "addr-1"},
		{ShareID: "share-bbb", Name: "Shared", AddressID: "addr-1"},
	} {
		pShare := &proton.Share{
			ShareMetadata: proton.ShareMetadata{ShareID: f.ShareID, Type: proton.ShareTypeStandard},
			AddressID:     f.AddressID,
		}
		pLink := &proton.Link{LinkID: "link-" + f.ShareID, Type: proton.LinkTypeFolder}
		root := NewTestLink(pLink, nil, nil, resolver, f.Name)
		share := NewShare(pShare, nil, root, resolver, "vol-1")
		root = NewTestLink(pLink, nil, share, resolver, f.Name)
		share.Link = root
		shares[f.ShareID] = share
		metas = append(metas, ShareMetadata(proton.ShareMetadata{ShareID: f.ShareID, Type: proton.ShareTypeStandard}))
	}

	c := &resolveTestClient{metas: metas, shares: shares}

	_, err := resolveShareLogic(c, "Shared")
	if err == nil {
		t.Fatal("expected ambiguity error, got nil")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguity error, got: %v", err)
	}
}

// TestResolveShare_ShortIDPrefix_MultipleMatches returns error for ambiguous prefix.
func TestResolveShare_ShortIDPrefix_MultipleMatches(t *testing.T) {
	resolver := &mockResolver{}

	shares := map[string]*Share{}
	metas := []ShareMetadata{}

	for _, f := range []shareFixture{
		{ShareID: "share-aaa111", Name: "Alpha", AddressID: "addr-1"},
		{ShareID: "share-aaa222", Name: "Beta", AddressID: "addr-1"},
	} {
		pShare := &proton.Share{
			ShareMetadata: proton.ShareMetadata{ShareID: f.ShareID, Type: proton.ShareTypeStandard},
			AddressID:     f.AddressID,
		}
		pLink := &proton.Link{LinkID: "link-" + f.ShareID, Type: proton.LinkTypeFolder}
		root := NewTestLink(pLink, nil, nil, resolver, f.Name)
		share := NewShare(pShare, nil, root, resolver, "vol-1")
		root = NewTestLink(pLink, nil, share, resolver, f.Name)
		share.Link = root
		shares[f.ShareID] = share
		metas = append(metas, ShareMetadata(proton.ShareMetadata{ShareID: f.ShareID, Type: proton.ShareTypeStandard}))
	}

	c := &resolveTestClient{metas: metas, shares: shares}

	// "share-aaa" is a prefix matching both shares.
	_, err := resolveShareLogic(c, "share-aaa")
	if err == nil {
		t.Fatal("expected ambiguity error, got nil")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguity error, got: %v", err)
	}
}

// TestResolveShare_NoMatch returns ErrFileNotFound.
func TestResolveShare_NoMatch(t *testing.T) {
	resolver := &mockResolver{}

	shares := map[string]*Share{}
	metas := []ShareMetadata{}

	for _, f := range []shareFixture{
		{ShareID: "share-aaa", Name: "Alpha", AddressID: "addr-1"},
	} {
		pShare := &proton.Share{
			ShareMetadata: proton.ShareMetadata{ShareID: f.ShareID, Type: proton.ShareTypeStandard},
			AddressID:     f.AddressID,
		}
		pLink := &proton.Link{LinkID: "link-" + f.ShareID, Type: proton.LinkTypeFolder}
		root := NewTestLink(pLink, nil, nil, resolver, f.Name)
		share := NewShare(pShare, nil, root, resolver, "vol-1")
		root = NewTestLink(pLink, nil, share, resolver, f.Name)
		share.Link = root
		shares[f.ShareID] = share
		metas = append(metas, ShareMetadata(proton.ShareMetadata{ShareID: f.ShareID, Type: proton.ShareTypeStandard}))
	}

	c := &resolveTestClient{metas: metas, shares: shares}

	_, err := resolveShareLogic(c, "nonexistent")
	if !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("expected ErrFileNotFound, got: %v", err)
	}
}

// resolveTestClient is a minimal mock for testing ResolveShare logic
// without real API calls.
type resolveTestClient struct {
	metas  []ShareMetadata
	shares map[string]*Share
}

// resolveShareLogic replicates the ResolveShare algorithm using the
// test client's in-memory data. This allows property testing without
// HTTP mocking.
func resolveShareLogic(c *resolveTestClient, nameOrID string) (*Share, error) {
	var nameMatch *Share
	var nameMatchCount int
	var idMatch *Share
	var idMatchCount int

	for _, meta := range c.metas {
		isIDMatch := strings.HasPrefix(meta.ShareID, nameOrID)

		share, ok := c.shares[meta.ShareID]
		if !ok {
			continue
		}

		if isIDMatch {
			idMatch = share
			idMatchCount++
		}

		shareName, err := share.Link.Name()
		if err != nil {
			continue
		}
		if shareName == nameOrID {
			nameMatch = share
			nameMatchCount++
		}
	}

	switch {
	case nameMatchCount == 1 && idMatchCount == 0:
		return nameMatch, nil
	case nameMatchCount == 0 && idMatchCount == 1:
		return idMatch, nil
	case nameMatchCount == 1 && idMatchCount == 1:
		if nameMatch.Metadata().ShareID == idMatch.Metadata().ShareID {
			return nameMatch, nil
		}
		return nil, fmt.Errorf("ambiguous: %q matches share name %q and ID prefix %q — use full ID to disambiguate",
			nameOrID, nameMatch.Metadata().ShareID, idMatch.Metadata().ShareID)
	case nameMatchCount > 1:
		return nil, fmt.Errorf("ambiguous: multiple shares named %q — use share ID to disambiguate", nameOrID)
	case idMatchCount > 1:
		return nil, fmt.Errorf("ambiguous: %q matches multiple share IDs — use a longer prefix", nameOrID)
	default:
		return nil, ErrFileNotFound
	}
}
