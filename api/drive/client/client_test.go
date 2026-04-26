package client

import (
	"context"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-cli/api"
	"github.com/major0/proton-cli/api/drive"
)

func TestClient_AddressForEmail(t *testing.T) {
	addr := proton.Address{ID: "addr-1", Email: "user@example.com"}
	c := &Client{
		addresses: map[string]proton.Address{
			"user@example.com": addr,
		},
	}

	tests := []struct {
		name   string
		email  string
		wantOK bool
		wantID string
	}{
		{"found", "user@example.com", true, "addr-1"},
		{"not found", "other@example.com", false, ""},
		{"empty", "", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := c.AddressForEmail(tt.email)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got.ID != tt.wantID {
				t.Fatalf("ID = %q, want %q", got.ID, tt.wantID)
			}
		})
	}
}

func TestClient_AddressKeyRing(t *testing.T) {
	// Create a minimal keyring for testing.
	kr := &crypto.KeyRing{}
	c := &Client{
		addressKeyRings: map[string]*crypto.KeyRing{
			"addr-1": kr,
		},
	}

	tests := []struct {
		name      string
		addressID string
		wantOK    bool
	}{
		{"found", "addr-1", true},
		{"not found", "addr-2", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := c.AddressKeyRing(tt.addressID)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got != kr {
				t.Fatal("returned wrong keyring")
			}
		})
	}
}

func TestClient_Throttle(t *testing.T) {
	throttle := &api.Throttle{}
	c := &Client{
		Session: &api.Session{Throttle: throttle},
	}
	if got := c.Throttle(); got != throttle {
		t.Fatal("Throttle() returned wrong value")
	}
}

func TestClient_MaxWorkers(t *testing.T) {
	c := &Client{}
	if got := c.MaxWorkers(); got != api.DefaultMaxWorkers() {
		t.Fatalf("MaxWorkers() = %d, want %d", got, api.DefaultMaxWorkers())
	}
}

func TestClient_NewChildLink(t *testing.T) {
	resolver := &mockResolver{}
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "s1"},
	}
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	root := drive.NewLink(rootPLink, nil, nil, resolver)
	share := drive.NewShare(pShare, nil, root, resolver, "")
	root = drive.NewLink(rootPLink, nil, share, resolver)
	share.Link = root

	c := &Client{}
	childPLink := &proton.Link{LinkID: "child-1", Type: proton.LinkTypeFile}
	child := c.NewChildLink(context.Background(), root, childPLink)

	if child.LinkID() != "child-1" {
		t.Fatalf("child LinkID = %q, want %q", child.LinkID(), "child-1")
	}
}
