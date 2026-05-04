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
	"strings"
	"sync"
	"testing"

	proton "github.com/ProtonMail/go-proton-api"
	pgpcrypto "github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/ProtonMail/gopenpgp/v2/helper"
	"github.com/major0/proton-cli/api"
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

func TestGenerate_MockServer(t *testing.T) {
	pubKey, privKR := testKeyPair(t)

	// The mock server decrypts the request key from the incoming request,
	// then encrypts response token_data with that key + response AD.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth headers.
		if got := r.Header.Get("x-pm-uid"); got != "test-uid-123" {
			t.Errorf("x-pm-uid = %q, want %q", got, "test-uid-123")
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token-abc" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer test-token-abc")
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Errorf("Accept = %q, want %q", got, "text/event-stream")
		}

		// Parse the request body to extract the request key and ID.
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		var req ChatEndpointGenerationRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("unmarshal: %v", err)
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		requestID := req.Prompt.RequestID
		aesKey := decryptRequestKey(t, req.Prompt.RequestKey, privKR)
		responseAD := []byte(ResponseAD(requestID))

		// Encrypt a known plaintext for the response.
		encContent := testEncryptAESGCM(t, []byte("Hello from Lumo"), aesKey, responseAD)

		// Write SSE stream: queued → ingesting → encrypted token_data → done.
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		lines := []string{
			sseData(t, GenerationResponseMessage{Type: "queued"}),
			sseData(t, GenerationResponseMessage{Type: "ingesting"}),
			sseData(t, GenerationResponseMessage{
				Type:      "token_data",
				Target:    TargetMessage,
				Content:   encContent,
				Encrypted: true,
			}),
			sseData(t, GenerationResponseMessage{Type: "done"}),
		}
		_, _ = w.Write([]byte(strings.Join(lines, "")))
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

	// Verify we got all 4 messages.
	if len(messages) != 4 {
		t.Fatalf("got %d messages, want 4", len(messages))
	}

	// Verify the token_data was decrypted.
	tokenMsg := messages[2]
	if tokenMsg.Type != "token_data" {
		t.Fatalf("message[2].Type = %q, want %q", tokenMsg.Type, "token_data")
	}
	if tokenMsg.Content != "Hello from Lumo" {
		t.Fatalf("decrypted content = %q, want %q", tokenMsg.Content, "Hello from Lumo")
	}
	if tokenMsg.Encrypted {
		t.Fatal("token_data should be decrypted (Encrypted=false)")
	}
}

func TestGenerate_Rejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "data: {\"type\":\"rejected\"}\n\n")
	}))
	defer srv.Close()

	pubKey, _ := testKeyPair(t)
	sess := testSession(t)
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	err := c.Generate(context.Background(), []Turn{
		{Role: RoleUser, Content: "bad"},
	}, GenerateOpts{LumoPubKey: pubKey})

	if !errors.Is(err, ErrRejected) {
		t.Fatalf("err = %v, want ErrRejected", err)
	}
}

func TestGenerate_Harmful(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "data: {\"type\":\"harmful\"}\n\n")
	}))
	defer srv.Close()

	pubKey, _ := testKeyPair(t)
	sess := testSession(t)
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	err := c.Generate(context.Background(), []Turn{
		{Role: RoleUser, Content: "bad"},
	}, GenerateOpts{LumoPubKey: pubKey})

	if !errors.Is(err, ErrHarmful) {
		t.Fatalf("err = %v, want ErrHarmful", err)
	}
}

func TestGenerate_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "data: {\"type\":\"timeout\"}\n\n")
	}))
	defer srv.Close()

	pubKey, _ := testKeyPair(t)
	sess := testSession(t)
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	err := c.Generate(context.Background(), []Turn{
		{Role: RoleUser, Content: "slow"},
	}, GenerateOpts{LumoPubKey: pubKey})

	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
}

// sseData serializes a message to an SSE data: line.
func sseData(t *testing.T, msg GenerationResponseMessage) string {
	t.Helper()
	b, err := MarshalSSE(msg)
	if err != nil {
		t.Fatalf("MarshalSSE: %v", err)
	}
	return string(b)
}
