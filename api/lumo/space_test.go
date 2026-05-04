package lumo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	pgpcrypto "github.com/ProtonMail/gopenpgp/v2/crypto"
	"pgregory.net/rapid"
)

// testCryptoChain sets up a master key and returns the raw key bytes
// and the PGP-armored version for mock server responses.
type testCryptoChain struct {
	masterKey    []byte
	armored      string
	kr           *pgpcrypto.KeyRing
	lastSpaceKey []byte
}

func newTestCryptoChain(t *testing.T) *testCryptoChain {
	t.Helper()
	_, kr := testKeyPair(t)
	mk := make([]byte, 32)
	for i := range mk {
		mk[i] = byte(i + 1)
	}
	return &testCryptoChain{
		masterKey: mk,
		armored:   pgpEncrypt(t, kr, mk),
		kr:        kr,
	}
}

// masterKeyHandler returns an HTTP handler that serves the master key.
func (tc *testCryptoChain) masterKeyHandler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, ListMasterKeysResponse{
			Code:        1000,
			Eligibility: 1,
			MasterKeys: []MasterKeyEntry{
				{ID: "mk1", IsLatest: true, Version: 1, CreateTime: "2024-01-01T00:00:00Z", MasterKey: tc.armored},
			},
		})
	}
}

// makeEncryptedSpace creates a Space with properly encrypted metadata
// using the test crypto chain.
func (tc *testCryptoChain) makeEncryptedSpace(t *testing.T, id, tag string, isProject bool) Space {
	t.Helper()
	spaceKey, err := GenerateSpaceKey()
	if err != nil {
		t.Fatalf("generate space key: %v", err)
	}
	wrapped, err := WrapSpaceKey(tc.masterKey, spaceKey)
	if err != nil {
		t.Fatalf("wrap space key: %v", err)
	}
	tc.lastSpaceKey = spaceKey
	dek, err := DeriveDataEncryptionKey(spaceKey)
	if err != nil {
		t.Fatalf("derive DEK: %v", err)
	}
	priv := SpacePriv{IsProject: &isProject}
	privJSON, _ := json.Marshal(priv)
	ad := SpaceAD(tag)
	encrypted, err := EncryptString(string(privJSON), dek, ad)
	if err != nil {
		t.Fatalf("encrypt space priv: %v", err)
	}
	return Space{
		ID:         id,
		SpaceKey:   base64.StdEncoding.EncodeToString(wrapped),
		SpaceTag:   tag,
		Encrypted:  encrypted,
		CreateTime: "2024-01-01T00:00:00Z",
	}
}

// deriveSpaceDEK derives the DEK from the last space key created by
// makeEncryptedSpace.
func (tc *testCryptoChain) deriveSpaceDEK(t *testing.T) ([]byte, error) {
	t.Helper()
	return DeriveDataEncryptionKey(tc.lastSpaceKey)
}

func TestListSpaces_HappyPath(t *testing.T) {
	spaces := []Space{
		{ID: "s1", SpaceTag: "tag1", CreateTime: "2024-01-01T00:00:00Z"},
		{ID: "s2", SpaceTag: "tag2", CreateTime: "2024-01-02T00:00:00Z"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return spaces on first call, empty on paginated calls.
		if r.URL.Query().Get("CreateTimeUntil") != "" {
			writeJSON(t, w, ListSpacesResponse{Code: 1000, Spaces: nil})
			return
		}
		writeJSON(t, w, ListSpacesResponse{Code: 1000, Spaces: spaces})
	}))
	defer srv.Close()

	sess := testSession(t)
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	got, err := c.ListSpaces(context.Background())
	if err != nil {
		t.Fatalf("ListSpaces: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d spaces, want 2", len(got))
	}
	if got[0].ID != "s1" || got[1].ID != "s2" {
		t.Fatalf("unexpected space IDs: %s, %s", got[0].ID, got[1].ID)
	}
}

func TestCreateSpace_RequestBody(t *testing.T) {
	tc := newTestCryptoChain(t)

	var capturedReq CreateSpaceReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/lumo/v1/masterkeys":
			tc.masterKeyHandler(t)(w, r)
		case "/api/lumo/v1/spaces":
			if err := json.NewDecoder(r.Body).Decode(&capturedReq); err != nil {
				t.Errorf("decode request: %v", err)
			}
			writeJSON(t, w, GetSpaceResponse{
				Code: 1000,
				Space: Space{
					ID:         "new-space-id",
					SpaceKey:   capturedReq.SpaceKey,
					SpaceTag:   capturedReq.SpaceTag,
					Encrypted:  capturedReq.Encrypted,
					CreateTime: "2024-01-01T00:00:00Z",
				},
			})
		}
	}))
	defer srv.Close()

	sess := testSession(t)
	sess.UserKeyRing = tc.kr
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	space, err := c.CreateSpace(context.Background(), "My Space", false)
	if err != nil {
		t.Fatalf("CreateSpace: %v", err)
	}

	// Verify the space was created with proper fields.
	if space.ID != "new-space-id" {
		t.Fatalf("space ID = %q, want %q", space.ID, "new-space-id")
	}
	if capturedReq.SpaceKey == "" {
		t.Fatal("SpaceKey is empty")
	}
	if capturedReq.SpaceTag == "" {
		t.Fatal("SpaceTag is empty")
	}
	if capturedReq.Encrypted == "" {
		t.Fatal("Encrypted is empty")
	}

	// Verify we can decrypt the metadata.
	wrappedKey, _ := base64.StdEncoding.DecodeString(capturedReq.SpaceKey)
	spaceKey, err := UnwrapSpaceKey(tc.masterKey, wrappedKey)
	if err != nil {
		t.Fatalf("unwrap space key: %v", err)
	}
	dek, err := DeriveDataEncryptionKey(spaceKey)
	if err != nil {
		t.Fatalf("derive DEK: %v", err)
	}
	ad := SpaceAD(capturedReq.SpaceTag)
	privJSON, err := DecryptString(capturedReq.Encrypted, dek, ad)
	if err != nil {
		t.Fatalf("decrypt metadata: %v", err)
	}
	// Simple spaces encrypt "{}" — no isProject field.
	if privJSON != "{}" {
		t.Fatalf("expected empty JSON object, got %q", privJSON)
	}
}

func TestGetDefaultSpace_FindsSimple(t *testing.T) {
	tc := newTestCryptoChain(t)

	projectSpace := tc.makeEncryptedSpace(t, "s-project", "tag-project", true)
	simpleSpace := tc.makeEncryptedSpace(t, "s-simple", "tag-simple", false)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/lumo/v1/masterkeys":
			tc.masterKeyHandler(t)(w, r)
		case "/api/lumo/v1/spaces":
			writeJSON(t, w, ListSpacesResponse{
				Code:   1000,
				Spaces: []Space{projectSpace, simpleSpace},
			})
		}
	}))
	defer srv.Close()

	sess := testSession(t)
	sess.UserKeyRing = tc.kr
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	got, err := c.GetDefaultSpace(context.Background())
	if err != nil {
		t.Fatalf("GetDefaultSpace: %v", err)
	}
	if got.ID != "s-simple" {
		t.Fatalf("got space ID %q, want %q", got.ID, "s-simple")
	}
}

func TestGetDefaultSpace_CreatesWhenNone(t *testing.T) {
	tc := newTestCryptoChain(t)

	projectSpace := tc.makeEncryptedSpace(t, "s-project", "tag-project", true)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/lumo/v1/masterkeys":
			tc.masterKeyHandler(t)(w, r)
		case r.URL.Path == "/api/lumo/v1/spaces" && r.Method == "GET":
			writeJSON(t, w, ListSpacesResponse{
				Code:   1000,
				Spaces: []Space{projectSpace},
			})
		case r.URL.Path == "/api/lumo/v1/spaces" && r.Method == "POST":
			var req CreateSpaceReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode: %v", err)
			}
			writeJSON(t, w, GetSpaceResponse{
				Code: 1000,
				Space: Space{
					ID:         "new-default",
					SpaceKey:   req.SpaceKey,
					SpaceTag:   req.SpaceTag,
					Encrypted:  req.Encrypted,
					CreateTime: "2024-01-01T00:00:00Z",
				},
			})
		}
	}))
	defer srv.Close()

	sess := testSession(t)
	sess.UserKeyRing = tc.kr
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	got, err := c.GetDefaultSpace(context.Background())
	if err != nil {
		t.Fatalf("GetDefaultSpace: %v", err)
	}
	if got.ID != "new-default" {
		t.Fatalf("got space ID %q, want %q", got.ID, "new-default")
	}
}

func TestDeleteSpace_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		writeJSON(t, w, map[string]any{"Code": 2501, "Error": "resource deleted"})
	}))
	defer srv.Close()

	sess := testSession(t)
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	err := c.DeleteSpace(context.Background(), "deleted-id")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func TestCreateSpace_Conflict(t *testing.T) {
	tc := newTestCryptoChain(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/lumo/v1/masterkeys":
			tc.masterKeyHandler(t)(w, r)
		case "/api/lumo/v1/spaces":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			writeJSON(t, w, map[string]any{"Code": 2000, "Error": "duplicate"})
		}
	}))
	defer srv.Close()

	sess := testSession(t)
	sess.UserKeyRing = tc.kr
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	_, err := c.CreateSpace(context.Background(), "dup", false)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got: %v", err)
	}
}

func TestUpdateSpace_Success(t *testing.T) {
	var capturedMethod string
	var capturedPath string
	var capturedReq UpdateSpaceReq

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&capturedReq); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writeJSON(t, w, map[string]any{"Code": 1000})
	}))
	defer srv.Close()

	sess := testSession(t)
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	req := UpdateSpaceReq{Encrypted: "some-encrypted-data"}
	err := c.UpdateSpace(context.Background(), "space-42", req)
	if err != nil {
		t.Fatalf("UpdateSpace: %v", err)
	}
	if capturedMethod != "PUT" {
		t.Fatalf("method = %q, want PUT", capturedMethod)
	}
	if capturedPath != "/api/lumo/v1/spaces/space-42" {
		t.Fatalf("path = %q, want /api/lumo/v1/spaces/space-42", capturedPath)
	}
	if capturedReq.Encrypted != "some-encrypted-data" {
		t.Fatalf("Encrypted = %q, want %q", capturedReq.Encrypted, "some-encrypted-data")
	}
}

func TestDecryptSpacePriv_Success(t *testing.T) {
	tc := newTestCryptoChain(t)

	isProject := true
	space := tc.makeEncryptedSpace(t, "s1", "tag1", true)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		tc.masterKeyHandler(t)(w, nil)
	}))
	defer srv.Close()

	sess := testSession(t)
	sess.UserKeyRing = tc.kr
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	priv, err := c.DecryptSpacePriv(context.Background(), &space)
	if err != nil {
		t.Fatalf("DecryptSpacePriv: %v", err)
	}
	if priv.IsProject == nil || *priv.IsProject != isProject {
		t.Fatalf("IsProject = %v, want %v", priv.IsProject, &isProject)
	}
}

func TestDecryptSpacePriv_EmptyEncrypted(t *testing.T) {
	space := Space{ID: "s1", SpaceTag: "tag1"}

	sess := testSession(t)
	c := NewClient(sess)

	priv, err := c.DecryptSpacePriv(context.Background(), &space)
	if err != nil {
		t.Fatalf("DecryptSpacePriv: %v", err)
	}
	if priv.IsProject != nil {
		t.Fatalf("expected nil IsProject, got %v", priv.IsProject)
	}
	if priv.ProjectName != "" {
		t.Fatalf("expected empty ProjectName, got %q", priv.ProjectName)
	}
}

func TestEncryptSpacePriv_RoundTrip(t *testing.T) {
	tc := newTestCryptoChain(t)

	space := tc.makeEncryptedSpace(t, "s1", "tag1", false)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		tc.masterKeyHandler(t)(w, nil)
	}))
	defer srv.Close()

	sess := testSession(t)
	sess.UserKeyRing = tc.kr
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	isProject := true
	original := &SpacePriv{
		IsProject:           &isProject,
		ProjectName:         "My Project",
		ProjectInstructions: "Do the thing",
		ProjectIcon:         "rocket",
	}

	encrypted, err := c.EncryptSpacePriv(context.Background(), &space, original)
	if err != nil {
		t.Fatalf("EncryptSpacePriv: %v", err)
	}
	if encrypted == "" {
		t.Fatal("encrypted is empty")
	}

	// Decrypt and verify round-trip.
	space.Encrypted = encrypted
	got, err := c.DecryptSpacePriv(context.Background(), &space)
	if err != nil {
		t.Fatalf("DecryptSpacePriv: %v", err)
	}
	if got.ProjectName != original.ProjectName {
		t.Fatalf("ProjectName = %q, want %q", got.ProjectName, original.ProjectName)
	}
	if got.ProjectInstructions != original.ProjectInstructions {
		t.Fatalf("ProjectInstructions = %q, want %q", got.ProjectInstructions, original.ProjectInstructions)
	}
	if got.ProjectIcon != original.ProjectIcon {
		t.Fatalf("ProjectIcon = %q, want %q", got.ProjectIcon, original.ProjectIcon)
	}
	if got.IsProject == nil || !*got.IsProject {
		t.Fatal("IsProject should be true")
	}
}

// TestEncryptSpacePriv_DecryptSpacePriv_RoundTrip_Property verifies that
// for any valid SpacePriv, encrypting then decrypting produces an equal
// value.
//
// **Validates: Requirements 3.5**
func TestEncryptSpacePriv_DecryptSpacePriv_RoundTrip_Property(t *testing.T) {
	tc := newTestCryptoChain(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		tc.masterKeyHandler(t)(w, nil)
	}))
	defer srv.Close()

	sess := testSession(t)
	sess.UserKeyRing = tc.kr
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	// Create a space once — reuse for all iterations.
	space := tc.makeEncryptedSpace(t, "prop-space", "prop-tag", false)

	rapid.Check(t, func(rt *rapid.T) {
		orig := genSpacePrivForClient(rt)

		encrypted, err := c.EncryptSpacePriv(context.Background(), &space, &orig)
		if err != nil {
			rt.Fatalf("EncryptSpacePriv: %v", err)
		}

		s := space
		s.Encrypted = encrypted
		got, err := c.DecryptSpacePriv(context.Background(), &s)
		if err != nil {
			rt.Fatalf("DecryptSpacePriv: %v", err)
		}
		if !reflect.DeepEqual(orig, *got) {
			rt.Fatalf("round-trip mismatch:\norig: %+v\ngot:  %+v", orig, *got)
		}
	})
}

// genSpacePrivForClient generates a random SpacePriv for property testing.
func genSpacePrivForClient(t *rapid.T) SpacePriv {
	sp := SpacePriv{}
	switch rapid.IntRange(0, 2).Draw(t, "is_project_case") {
	case 0:
		// nil — omitted
	case 1:
		f := false
		sp.IsProject = &f
	case 2:
		tr := true
		sp.IsProject = &tr
		sp.ProjectName = rapid.StringMatching(`[a-zA-Z0-9 ]{0,16}`).Draw(t, "project_name")
		sp.ProjectInstructions = rapid.StringMatching(`[a-zA-Z0-9 ]{0,32}`).Draw(t, "project_instructions")
		sp.ProjectIcon = rapid.StringMatching(`[a-z]{0,8}`).Draw(t, "project_icon")
	}
	return sp
}
