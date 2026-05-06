package drive

import (
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"pgregory.net/rapid"
)

// TestPropertySharePredicatesConsistentWithShareType verifies that
// IsSystemShare() and IsShared() are consistent with the underlying
// share type for all possible ShareType values.
//
// **Property 2: Share predicates are consistent with share type**
// **Validates: Requirements 2.2**
func TestPropertySharePredicatesConsistentWithShareType(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random ShareType values including known (1-4) and unknown (0, 5+).
		shareType := proton.ShareType(rapid.IntRange(0, 10).Draw(rt, "shareType"))

		pShare := &proton.Share{
			ShareMetadata: proton.ShareMetadata{Type: shareType},
		}
		share := NewShare(pShare, nil, nil, nil, "")

		// Assert IsSystemShare() iff type in {Main, Photos, Device}.
		expectedSystem := shareType == proton.ShareTypeMain ||
			shareType == ShareTypePhotos ||
			shareType == proton.ShareTypeDevice
		if share.IsSystemShare() != expectedSystem {
			rt.Fatalf("IsSystemShare() = %v, want %v (type=%d)",
				share.IsSystemShare(), expectedSystem, shareType)
		}

		// Assert IsShared() iff type == Standard.
		expectedShared := shareType == proton.ShareTypeStandard
		if share.IsShared() != expectedShared {
			rt.Fatalf("IsShared() = %v, want %v (type=%d)",
				share.IsShared(), expectedShared, shareType)
		}
	})
}

// TestPropertyTypeNameEquivalenceWithFormatShareType verifies that
// TypeName() returns the same string as FormatShareType(share.ProtonShare().Type)
// for all possible ShareType values.
//
// **Property 3: TypeName equivalence with FormatShareType**
// **Validates: Requirements 2.3**
func TestPropertyTypeNameEquivalenceWithFormatShareType(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random ShareType values including known (1-4) and unknown (0, 5+).
		shareType := proton.ShareType(rapid.IntRange(0, 10).Draw(rt, "shareType"))

		pShare := &proton.Share{
			ShareMetadata: proton.ShareMetadata{Type: shareType},
		}
		share := NewShare(pShare, nil, nil, nil, "")

		// Assert TypeName() == FormatShareType(share.ProtonShare().Type).
		expected := FormatShareType(share.ProtonShare().Type)
		got := share.TypeName()
		if got != expected {
			rt.Fatalf("TypeName() = %q, want %q (type=%d)",
				got, expected, shareType)
		}
	})
}
