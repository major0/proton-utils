package drive

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-cli/api"
)

// TestCreateShareURL_ExistingURL_ReturnsError verifies that CreateShareURL
// returns ErrShareURLExists when the share already has a ShareURL.
//
// **Property 6: ShareURL enable idempotence guard**
// **Validates: Requirements 1a.6**
func TestCreateShareURL_ExistingURL_ReturnsError(t *testing.T) {
	// Set up a test HTTP server that returns a non-empty ShareURLs list.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The first call from CreateShareURL is ListShareURLs (GET /drive/shares/{id}/urls).
		resp := ShareURLsResponse{
			Code: 1000,
			ShareURLs: []ShareURL{
				{ShareURLID: "existing-url-1", ShareID: "test-share"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	// Construct a minimal Client with a real Session pointing at the test server.
	session := &api.Session{
		BaseURL: srv.URL,
		Sem:     api.NewSemaphore(context.Background(), 4, nil),
	}

	addrKR := genKeyRing(t, "test-addr")
	c := &Client{
		Session:         session,
		addressKeyRings: map[string]*crypto.KeyRing{"addr-1": addrKR},
	}

	// Build a minimal share with the address ID matching our keyring.
	resolver := &mockResolver{}
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{
			ShareID: "test-share",
			Creator: "user@example.com",
		},
		AddressID: "addr-1",
	}
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	root := NewTestLink(rootPLink, nil, nil, resolver, "TestShare")
	share := NewShare(pShare, nil, root, resolver, "vol-1")
	root = NewTestLink(rootPLink, nil, share, resolver, "TestShare")
	share.Link = root

	// Call CreateShareURL — should return ErrShareURLExists.
	_, _, err := c.CreateShareURL(context.Background(), share)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrShareURLExists) {
		t.Fatalf("expected ErrShareURLExists, got: %v", err)
	}
}

// TestCreateShareURL_NoExistingURL_CallsPost verifies that CreateShareURL
// proceeds to the POST call when no existing URL is found.
func TestCreateShareURL_NoExistingURL_CallsPost(t *testing.T) {
	var postCalled bool

	// Set up a test HTTP server that returns empty list on GET, then
	// handles the subsequent calls (modulus + POST).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == "GET" && r.URL.Path == "/drive/shares/test-share/urls":
			// ListShareURLs: return empty list.
			resp := ShareURLsResponse{Code: 1000, ShareURLs: nil}
			_ = json.NewEncoder(w).Encode(resp)

		case r.Method == "GET" && r.URL.Path == "/core/v4/auth/modulus":
			// SRP modulus fetch.
			resp := map[string]any{
				"Code":      1000,
				"Modulus":   testSRPModulus,
				"ModulusID": "test-modulus-id",
			}
			_ = json.NewEncoder(w).Encode(resp)

		case r.Method == "POST" && r.URL.Path == "/drive/shares/test-share/urls":
			postCalled = true
			resp := map[string]any{
				"Code": 1000,
				"ShareURL": ShareURL{
					ShareURLID: "new-url-1",
					ShareID:    "test-share",
				},
			}
			_ = json.NewEncoder(w).Encode(resp)

		default:
			http.Error(w, `{"Code":404,"Error":"not found"}`, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// Construct a Client with a real Session pointing at the test server.
	session := &api.Session{
		BaseURL: srv.URL,
		Sem:     api.NewSemaphore(context.Background(), 4, nil),
	}

	addrKR := genKeyRing(t, "test-addr")
	c := &Client{
		Session:         session,
		addressKeyRings: map[string]*crypto.KeyRing{"addr-1": addrKR},
	}

	// Build a share with a valid encrypted passphrase so shareSessionKey works.
	// Encrypt a dummy passphrase with the address keyring.
	dummyPassphrase := []byte("test-share-passphrase-material")
	encMsg, err := addrKR.Encrypt(crypto.NewPlainMessage(dummyPassphrase), nil)
	if err != nil {
		t.Fatalf("encrypt passphrase: %v", err)
	}
	armoredPassphrase, err := encMsg.GetArmored()
	if err != nil {
		t.Fatalf("armor passphrase: %v", err)
	}

	resolver := &mockResolver{}
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{
			ShareID: "test-share",
			Creator: "user@example.com",
		},
		AddressID:  "addr-1",
		Passphrase: armoredPassphrase,
	}
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	root := NewTestLink(rootPLink, nil, nil, resolver, "TestShare")
	share := NewShare(pShare, addrKR, root, resolver, "vol-1")
	root = NewTestLink(rootPLink, nil, share, resolver, "TestShare")
	share.Link = root

	// Call CreateShareURL — should succeed.
	password, shareURL, err := c.CreateShareURL(context.Background(), share)
	if err != nil {
		t.Fatalf("CreateShareURL: %v", err)
	}
	if password == "" {
		t.Fatal("expected non-empty password")
	}
	if len(password) != 32 {
		t.Fatalf("password length = %d, want 32", len(password))
	}
	if shareURL == nil {
		t.Fatal("expected non-nil ShareURL")
	}
	if !postCalled {
		t.Fatal("POST to create ShareURL was not called")
	}
}

// TestDeleteShareURL_Success verifies that DeleteShareURL calls the
// correct API endpoint.
func TestDeleteShareURL_Success(t *testing.T) {
	var deleteCalled bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "DELETE" && r.URL.Path == "/drive/shares/s1/urls/url1" {
			deleteCalled = true
			resp := map[string]any{"Code": 1000}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		http.Error(w, `{"Code":404,"Error":"not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()

	session := &api.Session{
		BaseURL: srv.URL,
		Sem:     api.NewSemaphore(context.Background(), 4, nil),
	}
	c := &Client{Session: session}

	err := c.DeleteShareURL(context.Background(), "s1", "url1")
	if err != nil {
		t.Fatalf("DeleteShareURL: %v", err)
	}
	if !deleteCalled {
		t.Fatal("DELETE was not called")
	}
}

// TestUpdateShareURLPassword_NilShareURL_ReturnsError verifies that
// UpdateShareURLPassword returns ErrNoShareURL when shareURL is nil.
func TestUpdateShareURLPassword_NilShareURL_ReturnsError(t *testing.T) {
	c := &Client{}
	resolver := &mockResolver{}
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "s1"},
	}
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	root := NewTestLink(rootPLink, nil, nil, resolver, "TestShare")
	share := NewShare(pShare, nil, root, resolver, "")

	err := c.UpdateShareURLPassword(context.Background(), share, nil, "newpass")
	if !errors.Is(err, ErrNoShareURL) {
		t.Fatalf("expected ErrNoShareURL, got: %v", err)
	}
}

// TestDecryptShareURLPassword_NilShareURL_ReturnsError verifies that
// DecryptShareURLPassword returns ErrNoShareURL when shareURL is nil.
func TestDecryptShareURLPassword_NilShareURL_ReturnsError(t *testing.T) {
	c := &Client{}
	resolver := &mockResolver{}
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "s1"},
	}
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	root := NewTestLink(rootPLink, nil, nil, resolver, "TestShare")
	share := NewShare(pShare, nil, root, resolver, "")

	_, err := c.DecryptShareURLPassword(context.Background(), share, nil)
	if !errors.Is(err, ErrNoShareURL) {
		t.Fatalf("expected ErrNoShareURL, got: %v", err)
	}
}

// TestDecryptShareURLPassword_EmptyPassword_ReturnsError verifies that
// DecryptShareURLPassword returns ErrNoShareURL when the password field is empty.
func TestDecryptShareURLPassword_EmptyPassword_ReturnsError(t *testing.T) {
	c := &Client{}
	resolver := &mockResolver{}
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "s1"},
	}
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	root := NewTestLink(rootPLink, nil, nil, resolver, "TestShare")
	share := NewShare(pShare, nil, root, resolver, "")

	_, err := c.DecryptShareURLPassword(context.Background(), share, &ShareURL{Password: ""})
	if !errors.Is(err, ErrNoShareURL) {
		t.Fatalf("expected ErrNoShareURL, got: %v", err)
	}
}

// TestListShareURLs_Success verifies that ListShareURLs correctly
// parses the API response.
func TestListShareURLs_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := ShareURLsResponse{
			Code: 1000,
			ShareURLs: []ShareURL{
				{ShareURLID: "url-1", ShareID: "s1", NumAccesses: 42},
				{ShareURLID: "url-2", ShareID: "s1", NumAccesses: 7},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	session := &api.Session{
		BaseURL: srv.URL,
		Sem:     api.NewSemaphore(context.Background(), 4, nil),
	}
	c := &Client{Session: session}

	urls, err := c.ListShareURLs(context.Background(), "s1")
	if err != nil {
		t.Fatalf("ListShareURLs: %v", err)
	}
	if len(urls) != 2 {
		t.Fatalf("len(urls) = %d, want 2", len(urls))
	}
	if urls[0].ShareURLID != "url-1" {
		t.Fatalf("urls[0].ShareURLID = %q, want %q", urls[0].ShareURLID, "url-1")
	}
	if urls[0].NumAccesses != 42 {
		t.Fatalf("urls[0].NumAccesses = %d, want 42", urls[0].NumAccesses)
	}
}
