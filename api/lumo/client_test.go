package lumo

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	proton "github.com/ProtonMail/go-proton-api"
	pgpcrypto "github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/ProtonMail/gopenpgp/v2/helper"
	"github.com/major0/proton-utils/api"
)

// testKeyPair generates a fresh PGP keypair for testing. Returns the
// armored public key and the private KeyRing for decryption.
func testKeyPair(t *testing.T) (string, *pgpcrypto.KeyRing) {
	t.Helper()
	armored, err := helper.GenerateKey("Test", "test@test.com", []byte(""), "x25519", 0)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	key, err := pgpcrypto.NewKeyFromArmored(armored)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	unlockedKey, err := key.Unlock([]byte(""))
	if err != nil {
		t.Fatalf("unlock key: %v", err)
	}
	kr, err := pgpcrypto.NewKeyRing(unlockedKey)
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	pubKey, err := key.GetArmoredPublicKey()
	if err != nil {
		t.Fatalf("public key: %v", err)
	}
	return pubKey, kr
}

// decryptRequestKey extracts the raw AES key from the PGP-encrypted,
// base64-encoded request key sent by the client.
func decryptRequestKey(t *testing.T, encoded string, kr *pgpcrypto.KeyRing) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("base64 decode request key: %v", err)
	}
	msg := pgpcrypto.NewPGPMessage(raw)
	plain, err := kr.Decrypt(msg, nil, 0)
	if err != nil {
		t.Fatalf("pgp decrypt request key: %v", err)
	}
	return plain.GetBinary()
}

// testEncryptAESGCM encrypts plaintext with AES-GCM and returns base64.
// Mirrors the wire format: nonce || ciphertext || tag.
func testEncryptAESGCM(t *testing.T, plaintext, key, ad []byte) string {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("nonce: %v", err)
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, ad)
	return base64.StdEncoding.EncodeToString(sealed)
}

// testSession creates a minimal api.Session for testing.
func testSession(t *testing.T) *api.Session {
	t.Helper()
	return &api.Session{
		Auth: proton.Auth{
			UID:         "test-uid-123",
			AccessToken: "test-token-abc",
		},
		AppVersion: "cli@2.0.0",
		UserAgent:  "proton-cli/test",
	}
}

// TestGenerate_MockServer drives the Lumo 2.0 chat/completions path end to end:
// it asserts the request lands on the new endpoint in the expected shape, then
// replies with an encrypted delta and [DONE], and checks the delta is decrypted.
func TestGenerate_MockServer(t *testing.T) {
	pubKey, privKR := testKeyPair(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/api/"+ChatEndpoint {
			t.Errorf("path = %q, want %q", got, "/api/"+ChatEndpoint)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token-abc" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer test-token-abc")
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Errorf("Accept = %q, want %q", got, "text/event-stream")
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var req chatCompletionsBody
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		if req.Model != chatModel {
			t.Errorf("model = %q, want %q", req.Model, chatModel)
		}
		if !req.Stream {
			t.Errorf("stream = false, want true")
		}
		if len(req.Messages) != 1 || !req.Messages[0].Encrypted {
			t.Errorf("messages = %+v, want one encrypted message", req.Messages)
		}

		requestID := req.Lumo.RequestID
		aesKey := decryptRequestKey(t, req.Lumo.RequestKey, privKR)
		responseAD := []byte(ResponseAD(requestID))
		encContent := testEncryptAESGCM(t, []byte("Hello from Lumo"), aesKey, responseAD)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q,\"encrypted\":true}}]}\n\n", encContent)
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	sess := testSession(t)
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	var mu sync.Mutex
	var messages []GenerationResponseMessage

	err := c.Generate(context.Background(), []Turn{
		{Role: RoleUser, Content: "Hi"},
	}, GenerateOpts{
		ChunkCallback: func(msg GenerationResponseMessage) {
			mu.Lock()
			messages = append(messages, msg)
			mu.Unlock()
		},
		LumoPubKey: pubKey,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(messages) != 2 {
		t.Fatalf("got %d messages, want 2 (token_data, done)", len(messages))
	}
	token := messages[0]
	if token.Type != "token_data" {
		t.Fatalf("message[0].Type = %q, want token_data", token.Type)
	}
	if token.Content != "Hello from Lumo" {
		t.Fatalf("decrypted content = %q, want %q", token.Content, "Hello from Lumo")
	}
	if token.Encrypted {
		t.Fatal("token_data should be decrypted (Encrypted=false)")
	}
	if messages[1].Type != "done" {
		t.Fatalf("message[1].Type = %q, want done", messages[1].Type)
	}
}

// TestGenerate_Harmful maps a content_filter finish_reason to ErrHarmful.
func TestGenerate_Harmful(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"finish_reason\":\"content_filter\",\"delta\":{}}]}\n\n")
	}))
	defer srv.Close()

	pubKey, _ := testKeyPair(t)
	c := NewClient(testSession(t))
	c.BaseURL = srv.URL + "/api"

	err := c.Generate(context.Background(), []Turn{
		{Role: RoleUser, Content: "bad"},
	}, GenerateOpts{LumoPubKey: pubKey})
	if !errors.Is(err, ErrHarmful) {
		t.Fatalf("err = %v, want ErrHarmful", err)
	}
}

// TestGenerate_ErrorEvent maps an error chunk to ErrStreamClosed.
func TestGenerate_ErrorEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "data: {\"error\":{\"message\":\"boom\",\"code\":\"server_error\"}}\n\n")
	}))
	defer srv.Close()

	pubKey, _ := testKeyPair(t)
	c := NewClient(testSession(t))
	c.BaseURL = srv.URL + "/api"

	err := c.Generate(context.Background(), []Turn{
		{Role: RoleUser, Content: "x"},
	}, GenerateOpts{LumoPubKey: pubKey})
	if !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("err = %v, want ErrStreamClosed", err)
	}
}

// TestGenerate_StreamClosed reports ErrStreamClosed when the stream ends without
// a [DONE] marker.
func TestGenerate_StreamClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
	}))
	defer srv.Close()

	pubKey, _ := testKeyPair(t)
	c := NewClient(testSession(t))
	c.BaseURL = srv.URL + "/api"

	err := c.Generate(context.Background(), []Turn{
		{Role: RoleUser, Content: "x"},
	}, GenerateOpts{LumoPubKey: pubKey})
	if !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("err = %v, want ErrStreamClosed", err)
	}
}
