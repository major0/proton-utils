package configCmd

import (
	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api/drive"
)

// testShareWithID creates a minimal *drive.Share with the given share ID
// for testing purposes.
func testShareWithID(id string) *drive.Share {
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{
			ShareID: id,
			Type:    proton.ShareTypeStandard,
		},
	}
	return drive.NewShare(pShare, nil, nil, nil, "")
}
