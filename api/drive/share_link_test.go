package drive

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/ProtonMail/gopenpgp/v2/helper"
	"github.com/major0/proton-utils/api"
)

// genShareKey generates a locked share key and its passphrase encrypted
// with addrKR, suitable for constructing a mock proton.Share response.
func genShareKey(t *testing.T, addrKR *crypto.KeyRing) (keyArmored, passphraseArmored, sigArmored string) {
	t.Helper()

	// Generate a fresh key for the share.
	shareKeyArm, err := helper.GenerateKey("share", "share@test.local", nil, "x25519", 0)
	if err != nil {
		t.Fatalf("generate share key: %v", err)
	}

	// Use a fixed passphrase to lock the key.
	passphrase := []byte("share-passphrase-for-test")

	// Lock the key with the passphrase.
	key, err := crypto.NewKeyFromArmored(shareKeyArm)
	if err != nil {
		t.Fatalf("parse share key: %v", err)
	}
	lockedKey, err := key.Lock(passphrase)
	if err != nil {
		t.Fatalf("lock share key: %v", err)
	}
	lockedArmored, err := lockedKey.Armor()
	if err != nil {
		t.Fatalf("armor locked key: %v", err)
	}

	// Encrypt the passphrase with addrKR.
	encMsg, err := addrKR.Encrypt(crypto.NewPlainMessage(passphrase), nil)
	if err != nil {
		t.Fatalf("encrypt passphrase: %v", err)
	}
	passphraseArmored, err = encMsg.GetArmored()
	if err != nil {
		t.Fatalf("armor passphrase: %v", err)
	}

	// Sign the passphrase with addrKR.
	sig, err := addrKR.SignDetached(crypto.NewPlainMessage(passphrase))
	if err != nil {
		t.Fatalf("sign passphrase: %v", err)
	}
	sigArmored, err = sig.GetArmored()
	if err != nil {
		t.Fatalf("armor signature: %v", err)
	}

	return lockedArmored, passphraseArmored, sigArmored
}

// newTestProtonClient creates a proton.Client pointing at the given test server URL.
func newTestProtonClient(url string) *proton.Client {
	m := proton.New(proton.WithHostURL(url))
	return m.NewClient("test-uid", "test-acc", "test-ref")
}

// TestShareLink_AlreadyShared verifies that ShareLink returns an error
// when the link is already shared (metadata contains a matching LinkID).
func TestShareLink_AlreadyShared(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" && r.URL.Path == "/drive/shares" {
			resp := map[string]any{
				"Code": 1000,
				"Shares": []map[string]any{
					{"ShareID": "existing-share", "LinkID": "link-123", "VolumeID": "vol-1", "Type": 2},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		http.Error(w, `{"Code":404}`, http.StatusNotFound)
	}))
	defer srv.Close()

	session := &api.Session{
		Client:  newTestProtonClient(srv.URL),
		BaseURL: srv.URL,
		Sem:     api.NewSemaphore(context.Background(), 4, nil),
	}
	c := &Client{Session: session}

	// Build a link with LinkID matching the metadata.
	resolver := &mockResolver{}
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "parent-share", Type: proton.ShareTypeMain},
		AddressID:     "addr-1",
	}
	pLink := &proton.Link{LinkID: "link-123", Type: proton.LinkTypeFolder}
	root := NewTestLink(pLink, nil, nil, resolver, "TestFolder")
	share := NewShare(pShare, nil, root, resolver, "vol-1")
	root = NewTestLink(pLink, nil, share, resolver, "TestFolder")
	share.Link = root

	_, err := c.ShareLink(context.Background(), root, "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "already shared") {
		t.Fatalf("expected 'already shared' error, got: %v", err)
	}
}

// TestShareLink_HappyPath_WithName verifies that ShareLink creates a share
// and renames it when a name is provided.
func TestShareLink_HappyPath_WithName(t *testing.T) {
	addrKR := genKeyRing(t, "addr")
	linkNodeKR := genKeyRing(t, "linkNode")
	parentKR := genKeyRing(t, "parent")

	// Create encrypted link passphrase and name using parentKR.
	passphrase := []byte("test-link-passphrase")
	encPassphrase, err := parentKR.Encrypt(crypto.NewPlainMessage(passphrase), nil)
	if err != nil {
		t.Fatalf("encrypt passphrase: %v", err)
	}
	encPassphraseArm, err := encPassphrase.GetArmored()
	if err != nil {
		t.Fatalf("armor passphrase: %v", err)
	}

	linkName := []byte("original-name")
	encName, err := parentKR.Encrypt(crypto.NewPlainMessage(linkName), nil)
	if err != nil {
		t.Fatalf("encrypt name: %v", err)
	}
	encNameArm, err := encName.GetArmored()
	if err != nil {
		t.Fatalf("armor name: %v", err)
	}

	// Generate a valid share key for the GetShare response.
	shareKeyArm, sharePassArm, shareSigArm := genShareKey(t, addrKR)

	var createCalled, getShareCalled, renameCalled bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == "GET" && r.URL.Path == "/drive/shares" && r.URL.RawQuery == "ShowAll=1":
			// ListSharesMetadata: no existing share for this link.
			resp := map[string]any{
				"Code":   1000,
				"Shares": []map[string]any{},
			}
			_ = json.NewEncoder(w).Encode(resp)

		case r.Method == "POST" && r.URL.Path == "/drive/volumes/vol-1/shares":
			createCalled = true
			resp := map[string]any{
				"Code":  1000,
				"Share": map[string]any{"ID": "new-share-id"},
			}
			_ = json.NewEncoder(w).Encode(resp)

		case r.Method == "GET" && r.URL.Path == "/drive/shares/new-share-id":
			getShareCalled = true
			resp := map[string]any{
				"Code":                1000,
				"ShareID":             "new-share-id",
				"Type":                2, // Standard
				"State":               1,
				"LinkID":              "link-456",
				"VolumeID":            "vol-1",
				"AddressID":           "addr-1",
				"Key":                 shareKeyArm,
				"Passphrase":          sharePassArm,
				"PassphraseSignature": shareSigArm,
				"Creator":             "addr@test.local",
			}
			_ = json.NewEncoder(w).Encode(resp)

		case r.Method == "GET" && r.URL.Path == "/drive/shares/new-share-id/links/link-456":
			resp := map[string]any{
				"Code":   1000,
				"LinkID": "link-456",
				"Type":   1,
			}
			_ = json.NewEncoder(w).Encode(resp)

		case r.Method == "PUT" && strings.Contains(r.URL.Path, "/rename"):
			renameCalled = true
			resp := map[string]any{"Code": 1000}
			_ = json.NewEncoder(w).Encode(resp)

		default:
			http.Error(w, `{"Code":404,"Error":"not found: `+r.URL.Path+`"}`, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	session := &api.Session{
		Client:  newTestProtonClient(srv.URL),
		BaseURL: srv.URL,
		Sem:     api.NewSemaphore(context.Background(), 4, nil),
	}

	c := &Client{
		Session:         session,
		addressKeyRings: map[string]*crypto.KeyRing{"addr-1": addrKR},
		addresses:       map[string]proton.Address{"addr@test.local": {ID: "addr-1", Email: "addr@test.local"}},
		linkTable:       make(map[string]*Link),
	}

	// Build the link with real encrypted fields.
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "parent-share", Type: proton.ShareTypeMain, Creator: "addr@test.local"},
		AddressID:     "addr-1",
	}
	shareObj := NewShare(pShare, parentKR, nil, c, "vol-1")

	pLink := &proton.Link{
		LinkID:         "link-456",
		Type:           proton.LinkTypeFolder,
		NodePassphrase: encPassphraseArm,
		Name:           encNameArm,
		SignatureEmail: "addr@test.local",
	}
	// Create a link with the share as parent (root link, no parent link).
	link := NewLink(pLink, nil, shareObj, c)
	link.testName = "TestFolder"
	shareObj.Link = link

	// Inject the linkNodeKR into the link's cached keyring.
	link.cachedKeyRing = linkNodeKR

	result, err := c.ShareLink(context.Background(), link, "MyShare")
	if err != nil {
		t.Fatalf("ShareLink: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil share")
	}
	if !createCalled {
		t.Fatal("CreateShareFromLink was not called")
	}
	if !getShareCalled {
		t.Fatal("GetShare was not called")
	}
	if !renameCalled {
		t.Fatal("ShareRename was not called")
	}
}

// TestShareLink_EmptyName_NoRename verifies that ShareLink creates a share
// without calling rename when name is empty.
func TestShareLink_EmptyName_NoRename(t *testing.T) {
	addrKR := genKeyRing(t, "addr")
	linkNodeKR := genKeyRing(t, "linkNode")
	parentKR := genKeyRing(t, "parent")

	// Create encrypted link passphrase and name using parentKR.
	passphrase := []byte("test-link-passphrase")
	encPassphrase, err := parentKR.Encrypt(crypto.NewPlainMessage(passphrase), nil)
	if err != nil {
		t.Fatalf("encrypt passphrase: %v", err)
	}
	encPassphraseArm, err := encPassphrase.GetArmored()
	if err != nil {
		t.Fatalf("armor passphrase: %v", err)
	}

	linkName := []byte("original-name")
	encName, err := parentKR.Encrypt(crypto.NewPlainMessage(linkName), nil)
	if err != nil {
		t.Fatalf("encrypt name: %v", err)
	}
	encNameArm, err := encName.GetArmored()
	if err != nil {
		t.Fatalf("armor name: %v", err)
	}

	// Generate a valid share key for the GetShare response.
	shareKeyArm, sharePassArm, shareSigArm := genShareKey(t, addrKR)

	var createCalled, renameCalled bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == "GET" && r.URL.Path == "/drive/shares" && r.URL.RawQuery == "ShowAll=1":
			resp := map[string]any{
				"Code":   1000,
				"Shares": []map[string]any{},
			}
			_ = json.NewEncoder(w).Encode(resp)

		case r.Method == "POST" && r.URL.Path == "/drive/volumes/vol-1/shares":
			createCalled = true
			resp := map[string]any{
				"Code":  1000,
				"Share": map[string]any{"ID": "new-share-id"},
			}
			_ = json.NewEncoder(w).Encode(resp)

		case r.Method == "GET" && r.URL.Path == "/drive/shares/new-share-id":
			resp := map[string]any{
				"Code":                1000,
				"ShareID":             "new-share-id",
				"Type":                2,
				"State":               1,
				"LinkID":              "link-789",
				"VolumeID":            "vol-1",
				"AddressID":           "addr-1",
				"Key":                 shareKeyArm,
				"Passphrase":          sharePassArm,
				"PassphraseSignature": shareSigArm,
				"Creator":             "addr@test.local",
			}
			_ = json.NewEncoder(w).Encode(resp)

		case r.Method == "GET" && r.URL.Path == "/drive/shares/new-share-id/links/link-789":
			resp := map[string]any{
				"Code":   1000,
				"LinkID": "link-789",
				"Type":   1,
			}
			_ = json.NewEncoder(w).Encode(resp)

		case r.Method == "PUT":
			renameCalled = true
			resp := map[string]any{"Code": 1000}
			_ = json.NewEncoder(w).Encode(resp)

		default:
			http.Error(w, `{"Code":404,"Error":"not found: `+r.URL.Path+`"}`, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	session := &api.Session{
		Client:  newTestProtonClient(srv.URL),
		BaseURL: srv.URL,
		Sem:     api.NewSemaphore(context.Background(), 4, nil),
	}

	c := &Client{
		Session:         session,
		addressKeyRings: map[string]*crypto.KeyRing{"addr-1": addrKR},
		addresses:       map[string]proton.Address{"addr@test.local": {ID: "addr-1", Email: "addr@test.local"}},
		linkTable:       make(map[string]*Link),
	}

	// Build the link with real encrypted fields.
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "parent-share", Type: proton.ShareTypeMain, Creator: "addr@test.local"},
		AddressID:     "addr-1",
	}
	shareObj := NewShare(pShare, parentKR, nil, c, "vol-1")

	pLink := &proton.Link{
		LinkID:         "link-789",
		Type:           proton.LinkTypeFolder,
		NodePassphrase: encPassphraseArm,
		Name:           encNameArm,
		SignatureEmail: "addr@test.local",
	}
	link := NewLink(pLink, nil, shareObj, c)
	link.testName = "TestFolder"
	shareObj.Link = link

	// Inject the linkNodeKR into the link's cached keyring.
	link.cachedKeyRing = linkNodeKR

	result, err := c.ShareLink(context.Background(), link, "")
	if err != nil {
		t.Fatalf("ShareLink: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil share")
	}
	if !createCalled {
		t.Fatal("CreateShareFromLink was not called")
	}
	if renameCalled {
		t.Fatal("ShareRename should NOT have been called for empty name")
	}
}
