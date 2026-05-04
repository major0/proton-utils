package lumo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	pgpcrypto "github.com/ProtonMail/gopenpgp/v2/crypto"
)

// pgpEncrypt encrypts raw bytes with the given keyring and returns
// the armored PGP message.
func pgpEncrypt(t *testing.T, kr *pgpcrypto.KeyRing, data []byte) string {
	t.Helper()
	msg, err := kr.Encrypt(pgpcrypto.NewPlainMessage(data), nil)
	if err != nil {
		t.Fatalf("pgp encrypt: %v", err)
	}
	armored, err := msg.GetArmored()
	if err != nil {
		t.Fatalf("armor: %v", err)
	}
	return armored
}

// writeJSON writes a JSON response to the http.ResponseWriter.
func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("write JSON: %v", err)
	}
}

func TestGetMasterKey_HappyPath(t *testing.T) {
	_, kr := testKeyPair(t)
	rawKey := make([]byte, 32)
	for i := range rawKey {
		rawKey[i] = byte(i)
	}
	armored := pgpEncrypt(t, kr, rawKey)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, ListMasterKeysResponse{
			Code:        1000,
			Eligibility: 1,
			MasterKeys: []MasterKeyEntry{
				{ID: "mk1", IsLatest: true, Version: 1, CreateTime: "2024-01-01T00:00:00Z", MasterKey: armored},
			},
		})
	}))
	defer srv.Close()

	sess := testSession(t)
	sess.UserKeyRing = kr
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	got, err := c.GetMasterKey(context.Background())
	if err != nil {
		t.Fatalf("GetMasterKey: %v", err)
	}
	for i, b := range got {
		if b != byte(i) {
			t.Fatalf("key byte %d = %d, want %d", i, b, i)
		}
	}
}

func TestGetMasterKey_CreatePath(t *testing.T) {
	_, kr := testKeyPair(t)

	var postCalled atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			writeJSON(t, w, ListMasterKeysResponse{
				Code:        1000,
				Eligibility: 1,
				MasterKeys:  nil,
			})
		case "POST":
			postCalled.Store(true)
			var req CreateMasterKeyReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode POST body: %v", err)
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			if req.MasterKey == "" {
				t.Error("POST body missing MasterKey")
			}
			// The key is now base64-encoded binary PGP, not armored.
			raw, err := base64.StdEncoding.DecodeString(req.MasterKey)
			if err != nil {
				t.Errorf("decode base64 key: %v", err)
				http.Error(w, "bad key encoding", http.StatusBadRequest)
				return
			}
			msg := pgpcrypto.NewPGPMessage(raw)
			if _, err = kr.Decrypt(msg, nil, 0); err != nil {
				t.Errorf("decrypt posted key: %v", err)
			}
			writeJSON(t, w, map[string]int{"Code": 1000})
		}
	}))
	defer srv.Close()

	sess := testSession(t)
	sess.UserKeyRing = kr
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	key, err := c.GetMasterKey(context.Background())
	if err != nil {
		t.Fatalf("GetMasterKey: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("key length = %d, want 32", len(key))
	}
	if !postCalled.Load() {
		t.Fatal("POST was not called for key creation")
	}
}

func TestGetMasterKey_Caching(t *testing.T) {
	_, kr := testKeyPair(t)
	rawKey := make([]byte, 32)
	for i := range rawKey {
		rawKey[i] = 0xAB
	}
	armored := pgpEncrypt(t, kr, rawKey)

	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount.Add(1)
		writeJSON(t, w, ListMasterKeysResponse{
			Code:        1000,
			Eligibility: 1,
			MasterKeys: []MasterKeyEntry{
				{ID: "mk1", IsLatest: true, Version: 1, CreateTime: "2024-01-01T00:00:00Z", MasterKey: armored},
			},
		})
	}))
	defer srv.Close()

	sess := testSession(t)
	sess.UserKeyRing = kr
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	if _, err := c.GetMasterKey(context.Background()); err != nil {
		t.Fatalf("GetMasterKey (1): %v", err)
	}
	if _, err := c.GetMasterKey(context.Background()); err != nil {
		t.Fatalf("GetMasterKey (2): %v", err)
	}
	if got := callCount.Load(); got != 1 {
		t.Fatalf("API called %d times, want 1 (caching)", got)
	}
}

func TestGetMasterKey_NotEligible(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, ListMasterKeysResponse{
			Code:        1000,
			Eligibility: 0,
			MasterKeys:  nil,
		})
	}))
	defer srv.Close()

	sess := testSession(t)
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	_, err := c.GetMasterKey(context.Background())
	if !errors.Is(err, ErrNotEligible) {
		t.Fatalf("expected ErrNotEligible, got: %v", err)
	}
}

func TestGetMasterKey_BestKeySelection(t *testing.T) {
	_, kr := testKeyPair(t)

	keyA := make([]byte, 32)
	keyB := make([]byte, 32)
	for i := range keyA {
		keyA[i] = 0x11
	}
	for i := range keyB {
		keyB[i] = 0x22
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, ListMasterKeysResponse{
			Code:        1000,
			Eligibility: 1,
			MasterKeys: []MasterKeyEntry{
				{ID: "a", IsLatest: false, Version: 1, CreateTime: "2024-01-01T00:00:00Z", MasterKey: pgpEncrypt(t, kr, keyA)},
				{ID: "b", IsLatest: true, Version: 1, CreateTime: "2024-01-01T00:00:00Z", MasterKey: pgpEncrypt(t, kr, keyB)},
			},
		})
	}))
	defer srv.Close()

	sess := testSession(t)
	sess.UserKeyRing = kr
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	got, err := c.GetMasterKey(context.Background())
	if err != nil {
		t.Fatalf("GetMasterKey: %v", err)
	}
	for i, b := range got {
		if b != 0x22 {
			t.Fatalf("key byte %d = %02x, want 0x22 (keyB)", i, b)
		}
	}
}
