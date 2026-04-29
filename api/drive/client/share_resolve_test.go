package client_test

import (
	"context"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api/drive"
	"github.com/major0/proton-cli/api/drive/client"
)

// TestGetShareMetadata_Found verifies that GetShareMetadata returns the
// matching metadata when given a pre-fetched list.
func TestGetShareMetadata_Found(t *testing.T) {
	metas := []drive.ShareMetadata{
		drive.ShareMetadata(proton.ShareMetadata{ShareID: "share-1", VolumeID: "v1"}),
		drive.ShareMetadata(proton.ShareMetadata{ShareID: "share-2", VolumeID: "v2"}),
		drive.ShareMetadata(proton.ShareMetadata{ShareID: "share-3", VolumeID: "v3"}),
	}

	c := &client.Client{}
	got, err := c.GetShareMetadata(context.TODO(), "share-2", metas)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ShareID != "share-2" {
		t.Fatalf("ShareID = %q, want share-2", got.ShareID)
	}
	if got.VolumeID != "v2" {
		t.Fatalf("VolumeID = %q, want v2", got.VolumeID)
	}
}

// TestGetShareMetadata_NotFound verifies that GetShareMetadata returns
// a zero-value ShareMetadata when the ID is not in the list.
func TestGetShareMetadata_NotFound(t *testing.T) {
	metas := []drive.ShareMetadata{
		drive.ShareMetadata(proton.ShareMetadata{ShareID: "share-1"}),
		drive.ShareMetadata(proton.ShareMetadata{ShareID: "share-2"}),
	}

	c := &client.Client{}
	got, err := c.GetShareMetadata(context.TODO(), "nonexistent", metas)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ShareID != "" {
		t.Fatalf("expected zero ShareMetadata, got ShareID=%q", got.ShareID)
	}
}

// TestGetShareMetadata_EmptyList verifies that GetShareMetadata handles
// an empty metadata list gracefully.
func TestGetShareMetadata_EmptyList(t *testing.T) {
	c := &client.Client{}
	got, err := c.GetShareMetadata(context.TODO(), "any-id", []drive.ShareMetadata{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ShareID != "" {
		t.Fatalf("expected zero ShareMetadata, got ShareID=%q", got.ShareID)
	}
}
