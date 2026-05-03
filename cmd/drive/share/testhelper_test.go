package shareCmd

import (
	"fmt"

	"github.com/ProtonMail/go-proton-api"
	commonAPI "github.com/major0/proton-cli/api"
)

// failingStore is a SessionStore that always returns an error on Load.
type failingStore struct {
	err error
}

func (f *failingStore) Load() (*commonAPI.SessionCredentials, error) { return nil, f.err }
func (f *failingStore) Save(_ *commonAPI.SessionCredentials) error   { return nil }
func (f *failingStore) Delete() error                                { return nil }
func (f *failingStore) List() ([]string, error)                      { return nil, f.err }
func (f *failingStore) Switch(_ string) error                        { return nil }

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
