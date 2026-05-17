package drive

import (
	"context"
	"strings"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"pgregory.net/rapid"
)

// TestLink_Accessors is a table-driven test covering all 0% accessor methods
// on Link: State, ExpirationTime, MIMEType, LinkID, ProtonLink.
func TestLink_Accessors(t *testing.T) {
	resolver := &mockLinkResolver{}
	pLink := &proton.Link{
		LinkID:         "test-id",
		Type:           proton.LinkTypeFile,
		State:          proton.LinkStateActive,
		CreateTime:     1000,
		ModifyTime:     2000,
		ExpirationTime: 3000,
		MIMEType:       "text/plain",
	}
	link := NewTestLink(pLink, nil, nil, resolver, "test-file")

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"State", link.State(), proton.LinkStateActive},
		{"ExpirationTime", link.ExpirationTime(), int64(3000)},
		{"MIMEType", link.MIMEType(), "text/plain"},
		{"LinkID", link.LinkID(), "test-id"},
		{"ProtonLink_LinkID", link.ProtonLink().LinkID, "test-id"},
		{"ProtonLink_Type", link.ProtonLink().Type, proton.LinkTypeFile},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %v, want %v", tt.got, tt.want)
			}
		})
	}
}

// TestLink_ShareAndVolumeID verifies Link.Share() and Link.VolumeID()
// return the correct share and volume ID.
func TestLink_ShareAndVolumeID(t *testing.T) {
	resolver := &mockLinkResolver{}
	pShare := &proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s1"}}
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	root := NewTestLink(rootPLink, nil, nil, resolver, "root")
	share := NewShare(pShare, nil, root, resolver, "vol-123")
	root = NewTestLink(rootPLink, nil, share, resolver, "root")
	share.Link = root

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"Share", root.Share(), share},
		{"VolumeID", root.VolumeID(), "vol-123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %v, want %v", tt.got, tt.want)
			}
		})
	}
}

// TestLink_VolumeID_NilShare verifies VolumeID returns "" when share is nil.
func TestLink_VolumeID_NilShare(t *testing.T) {
	resolver := &mockLinkResolver{}
	pLink := &proton.Link{LinkID: "orphan", Type: proton.LinkTypeFile}
	link := NewTestLink(pLink, nil, nil, resolver, "orphan")

	if got := link.VolumeID(); got != "" {
		t.Errorf("VolumeID() = %q, want empty string for nil share", got)
	}
}

// TestSameDevice is a table-driven test for the SameDevice function.
func TestSameDevice(t *testing.T) {
	resolver := &mockLinkResolver{}

	makeLink := func(volumeID string) *Link {
		pShare := &proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s-" + volumeID}}
		rootPLink := &proton.Link{LinkID: "root-" + volumeID, Type: proton.LinkTypeFolder}
		root := NewTestLink(rootPLink, nil, nil, resolver, "root")
		share := NewShare(pShare, nil, root, resolver, volumeID)
		root = NewTestLink(rootPLink, nil, share, resolver, "root")
		share.Link = root
		return root
	}

	tests := []struct {
		name string
		volA string
		volB string
		want bool
	}{
		{"same volume", "vol-1", "vol-1", true},
		{"different volumes", "vol-1", "vol-2", false},
		{"empty volumes", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := makeLink(tt.volA)
			b := makeLink(tt.volB)
			if got := SameDevice(a, b); got != tt.want {
				t.Errorf("SameDevice(%q, %q) = %v, want %v", tt.volA, tt.volB, got, tt.want)
			}
		})
	}
}

// TestVolume_VolumeID verifies Volume.VolumeID() returns the correct ID.
func TestVolume_VolumeID(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{"normal ID", "vol-abc"},
		{"empty ID", ""},
		{"long ID", "vol-" + strings.Repeat("x", 100)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := Volume{ProtonVolume: proton.Volume{VolumeID: tt.id}}
			if got := v.VolumeID(); got != tt.id {
				t.Errorf("VolumeID() = %q, want %q", got, tt.id)
			}
		})
	}
}

// TestShare_GetName verifies Share.GetName returns the root link's name.
func TestShare_GetName(t *testing.T) {
	resolver := &mockLinkResolver{}
	pShare := &proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s1", Type: proton.ShareTypeStandard}}
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	root := NewTestLink(rootPLink, nil, nil, resolver, "my-share")
	share := NewShare(pShare, nil, root, resolver, "vol-1")
	root = NewTestLink(rootPLink, nil, share, resolver, "my-share")
	share.Link = root

	name, err := share.GetName(context.Background())
	if err != nil {
		t.Fatalf("GetName() error: %v", err)
	}
	if name != "my-share" {
		t.Errorf("GetName() = %q, want %q", name, "my-share")
	}
}

// TestShare_Metadata verifies Share.Metadata returns the correct metadata.
func TestShare_Metadata(t *testing.T) {
	resolver := &mockLinkResolver{}
	meta := proton.ShareMetadata{
		ShareID: "s1",
		Type:    proton.ShareTypeStandard,
	}
	pShare := &proton.Share{ShareMetadata: meta}
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	root := NewTestLink(rootPLink, nil, nil, resolver, "root")
	share := NewShare(pShare, nil, root, resolver, "vol-1")

	got := share.Metadata()
	if got.ShareID != "s1" {
		t.Errorf("Metadata().ShareID = %q, want %q", got.ShareID, "s1")
	}
	if got.Type != proton.ShareTypeStandard {
		t.Errorf("Metadata().Type = %d, want %d", got.Type, proton.ShareTypeStandard)
	}
}

// TestShare_ProtonShare verifies Share.ProtonShare returns the raw share.
func TestShare_ProtonShare(t *testing.T) {
	resolver := &mockLinkResolver{}
	pShare := &proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s1"}}
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	root := NewTestLink(rootPLink, nil, nil, resolver, "root")
	share := NewShare(pShare, nil, root, resolver, "vol-1")

	if got := share.ProtonShare(); got != pShare {
		t.Error("ProtonShare() did not return the original share pointer")
	}
}

// TestShare_KeyRingValue verifies Share.KeyRingValue returns the keyring.
func TestShare_KeyRingValue(t *testing.T) {
	resolver := &mockLinkResolver{}
	pShare := &proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s1"}}
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	root := NewTestLink(rootPLink, nil, nil, resolver, "root")
	// nil keyring — just verify the accessor works.
	share := NewShare(pShare, nil, root, resolver, "vol-1")

	if got := share.KeyRingValue(); got != nil {
		t.Errorf("KeyRingValue() = %v, want nil", got)
	}
}

// TestShare_VolumeID verifies Share.VolumeID returns the volume ID.
func TestShare_VolumeID(t *testing.T) {
	tests := []struct {
		name     string
		volumeID string
	}{
		{"normal", "vol-abc"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := &mockLinkResolver{}
			pShare := &proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s1"}}
			rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
			root := NewTestLink(rootPLink, nil, nil, resolver, "root")
			share := NewShare(pShare, nil, root, resolver, tt.volumeID)

			if got := share.VolumeID(); got != tt.volumeID {
				t.Errorf("VolumeID() = %q, want %q", got, tt.volumeID)
			}
		})
	}
}

// TestLink_Lookup verifies Lookup for ".", "..", and child names.
func TestLink_Lookup(t *testing.T) {
	children := []proton.Link{
		{LinkID: "child-a", Type: proton.LinkTypeFile, State: proton.LinkStateActive},
		{LinkID: "child-b", Type: proton.LinkTypeFolder, State: proton.LinkStateActive},
	}
	resolver := &readdirResolver{children: children}

	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	root := NewTestLink(rootPLink, nil, nil, resolver, "root")
	share := NewShare(
		&proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}},
		nil, root, resolver, "",
	)
	root = NewTestLink(rootPLink, nil, share, resolver, "root")
	share.Link = root

	ctx := context.Background()

	tests := []struct {
		name       string
		lookupName string
		wantLinkID string
		wantNil    bool
	}{
		{"dot returns self", ".", "root", false},
		{"dotdot returns parent (self for root)", "..", "root", false},
		{"existing child", "child-a", "child-a", false},
		{"missing child", "nonexistent", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := root.Lookup(ctx, tt.lookupName)
			if err != nil {
				t.Fatalf("Lookup(%q) error: %v", tt.lookupName, err)
			}
			if tt.wantNil {
				if got != nil {
					t.Errorf("Lookup(%q) = %v, want nil", tt.lookupName, got.LinkID())
				}
				return
			}
			if got == nil {
				t.Fatalf("Lookup(%q) = nil, want LinkID=%q", tt.lookupName, tt.wantLinkID)
			}
			if got.LinkID() != tt.wantLinkID {
				t.Errorf("Lookup(%q).LinkID() = %q, want %q", tt.lookupName, got.LinkID(), tt.wantLinkID)
			}
		})
	}
}

// TestSameDevice_Property verifies that SameDevice is reflexive and
// symmetric, and that links on the same volume always return true.
//
// **Validates: Requirements 2.1**
func TestSameDevice_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		volA := rapid.StringMatching(`vol-[a-z0-9]{1,10}`).Draw(t, "volA")
		volB := rapid.StringMatching(`vol-[a-z0-9]{1,10}`).Draw(t, "volB")

		resolver := &mockLinkResolver{}

		makeLink := func(vol string) *Link {
			pShare := &proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s-" + vol}}
			pLink := &proton.Link{LinkID: "l-" + vol, Type: proton.LinkTypeFile}
			root := NewTestLink(pLink, nil, nil, resolver, "root")
			share := NewShare(pShare, nil, root, resolver, vol)
			root = NewTestLink(pLink, nil, share, resolver, "root")
			share.Link = root
			return root
		}

		a := makeLink(volA)
		b := makeLink(volB)

		// Reflexive: SameDevice(a, a) is always true.
		if !SameDevice(a, a) {
			t.Fatal("SameDevice(a, a) should be true")
		}

		// Symmetric: SameDevice(a, b) == SameDevice(b, a).
		if SameDevice(a, b) != SameDevice(b, a) {
			t.Fatal("SameDevice should be symmetric")
		}

		// Correct: same volume → true, different → false.
		if volA == volB && !SameDevice(a, b) {
			t.Fatalf("same volume %q but SameDevice returned false", volA)
		}
		if volA != volB && SameDevice(a, b) {
			t.Fatalf("different volumes %q/%q but SameDevice returned true", volA, volB)
		}
	})
}
