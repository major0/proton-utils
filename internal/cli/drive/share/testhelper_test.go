package shareCmd

import (
	"fmt"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/internal/cli/testutil"
)

// newFailingStore returns a MockSessionStore that always returns the given error on Load.
func newFailingStore(err error) *testutil.MockSessionStore {
	return &testutil.MockSessionStore{LoadErr: err}
}

// testShareMetadata creates a ShareMetadata for testing.
func testShareMetadata(shareID string, st proton.ShareType) proton.ShareMetadata {
	return proton.ShareMetadata{
		ShareID:      shareID,
		Type:         st,
		Creator:      "test@proton.me",
		CreationTime: 1705276800,
	}
}

// formatErr returns a formatted error string or empty if nil.
func formatErr(err error) string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%q", err.Error())
}
