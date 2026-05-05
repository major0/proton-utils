package drive

import (
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"pgregory.net/rapid"
)

// TestPropertyLinkPredicatesConsistentWithTypeAndState verifies that
// the POSIX-style predicates on Link are consistent with the underlying
// Type() and State() accessors for all possible enum values.
//
// **Property 1: Link predicates are consistent with Type() and State()**
// **Validates: Requirements 1.1**
func TestPropertyLinkPredicatesConsistentWithTypeAndState(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random LinkType values including known and unknown.
		linkType := proton.LinkType(rapid.IntRange(0, 5).Draw(rt, "linkType"))
		// Generate random LinkState values including known and unknown.
		linkState := proton.LinkState(rapid.IntRange(0, 6).Draw(rt, "linkState"))

		pLink := &proton.Link{
			LinkID: "test-link",
			Type:   linkType,
			State:  linkState,
		}

		resolver := &mockLinkResolver{}
		link := NewTestLink(pLink, nil, nil, resolver, "test")

		// Verify Type() and State() return the generated values.
		if link.Type() != linkType {
			rt.Fatalf("Type() = %d, want %d", link.Type(), linkType)
		}
		if link.State() != linkState {
			rt.Fatalf("State() = %d, want %d", link.State(), linkState)
		}

		// Assert IsDir() == (Type() == LinkTypeFolder).
		if link.IsDir() != (linkType == proton.LinkTypeFolder) {
			rt.Fatalf("IsDir() = %v, want %v (type=%d)",
				link.IsDir(), linkType == proton.LinkTypeFolder, linkType)
		}

		// Assert IsFile() == (Type() == LinkTypeFile).
		if link.IsFile() != (linkType == proton.LinkTypeFile) {
			rt.Fatalf("IsFile() = %v, want %v (type=%d)",
				link.IsFile(), linkType == proton.LinkTypeFile, linkType)
		}

		// Assert IsActive() == (State() == LinkStateActive).
		if link.IsActive() != (linkState == proton.LinkStateActive) {
			rt.Fatalf("IsActive() = %v, want %v (state=%d)",
				link.IsActive(), linkState == proton.LinkStateActive, linkState)
		}

		// Assert IsTrashed() == (State() == LinkStateTrashed).
		if link.IsTrashed() != (linkState == proton.LinkStateTrashed) {
			rt.Fatalf("IsTrashed() = %v, want %v (state=%d)",
				link.IsTrashed(), linkState == proton.LinkStateTrashed, linkState)
		}

		// Assert IsDraft() == (State() == LinkStateDraft).
		if link.IsDraft() != (linkState == proton.LinkStateDraft) {
			rt.Fatalf("IsDraft() = %v, want %v (state=%d)",
				link.IsDraft(), linkState == proton.LinkStateDraft, linkState)
		}
	})
}

// TestPropertyRevisionAccessorsConsistentWithFileProperties verifies that
// HasActiveRevision() and RevisionID() are consistent with the underlying
// FileProperties for all combinations of link type, file properties presence,
// revision ID, and revision state.
//
// **Property 4: Revision accessors are consistent with file properties**
// **Validates: Requirements 3.1, 3.2**
func TestPropertyRevisionAccessorsConsistentWithFileProperties(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random LinkType (file or folder or unknown).
		linkType := proton.LinkType(rapid.IntRange(0, 5).Draw(rt, "linkType"))

		// Generate FileProperties: nil, or with varying revision ID and state.
		hasFileProps := rapid.Bool().Draw(rt, "hasFileProps")
		var fileProps *proton.FileProperties
		var revID string
		var revState proton.RevisionState

		if hasFileProps {
			// Revision ID: empty or non-empty.
			revID = rapid.OneOf(
				rapid.Just(""),
				rapid.StringMatching(`[a-zA-Z0-9]{1,20}`),
			).Draw(rt, "revisionID")

			// Revision state: Active, Draft, Obsolete, or unknown.
			revState = proton.RevisionState(rapid.IntRange(0, 4).Draw(rt, "revisionState"))

			fileProps = &proton.FileProperties{
				ActiveRevision: proton.RevisionMetadata{
					ID:    revID,
					State: revState,
				},
			}
		}

		pLink := &proton.Link{
			LinkID:         "test-link",
			Type:           linkType,
			FileProperties: fileProps,
		}

		resolver := &mockLinkResolver{}
		link := NewTestLink(pLink, nil, nil, resolver, "test")

		// Assert HasActiveRevision() == (IsFile() && RevisionID() != "" && state == Active).
		isFile := linkType == proton.LinkTypeFile
		expectedHasActive := isFile && revID != "" && revState == proton.RevisionStateActive && hasFileProps
		if link.HasActiveRevision() != expectedHasActive {
			rt.Fatalf("HasActiveRevision() = %v, want %v (type=%d, hasProps=%v, revID=%q, state=%d)",
				link.HasActiveRevision(), expectedHasActive, linkType, hasFileProps, revID, revState)
		}

		// Assert RevisionID() returns the ID when FileProperties exists, else "".
		var expectedRevID string
		if hasFileProps {
			expectedRevID = revID
		}
		if link.RevisionID() != expectedRevID {
			rt.Fatalf("RevisionID() = %q, want %q (hasProps=%v, revID=%q)",
				link.RevisionID(), expectedRevID, hasFileProps, revID)
		}
	})
}
