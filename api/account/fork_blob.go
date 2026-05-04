package account

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// forkBlobKeySize is the AES-256 key size in bytes.
const forkBlobKeySize = 32

// forkBlobNonceSize is the standard AES-GCM nonce size (12 bytes),
// matching WebClient v3 format.
const forkBlobNonceSize = 12

// forkBlobAD is the additional authenticated data for fork blob encryption,
// matching WebClient's utf8StringToUint8Array("fork").
var forkBlobAD = []byte("fork")

// ForkBlob is the plaintext payload encrypted in the fork.
type ForkBlob struct {
	Type        string `json:"type"`        // "default"
	KeyPassword string `json:"keyPassword"` // SaltedKeyPass from parent
}

// EncryptForkBlob encrypts a ForkBlob using AES-256-GCM with a random
// 32-byte key. Returns the base64-encoded ciphertext (nonce || ciphertext)
// and the raw key. The additional data is the UTF-8 bytes of "fork",
// matching WebClient v3 format.
func EncryptForkBlob(blob *ForkBlob) (ciphertext string, key []byte, err error) {
	plaintext, err := json.Marshal(blob)
	if err != nil {
		return "", nil, fmt.Errorf("fork blob: marshal: %w", err)
	}

	key = make([]byte, forkBlobKeySize)
	if _, err := rand.Read(key); err != nil {
		return "", nil, fmt.Errorf("fork blob: generate key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", nil, fmt.Errorf("fork blob: new cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", nil, fmt.Errorf("fork blob: new gcm: %w", err)
	}

	nonce := make([]byte, forkBlobNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return "", nil, fmt.Errorf("fork blob: generate nonce: %w", err)
	}

	// Seal appends the ciphertext+tag to nonce, producing nonce || ciphertext || tag.
	sealed := gcm.Seal(nonce, nonce, plaintext, forkBlobAD)

	return base64.StdEncoding.EncodeToString(sealed), key, nil
}

// DecryptForkBlob decrypts a base64-encoded fork blob using AES-256-GCM.
// The ciphertext format is nonce (12 bytes) || ciphertext || tag (16 bytes),
// matching WebClient v3 format.
func DecryptForkBlob(ciphertextB64 string, key []byte) (*ForkBlob, error) {
	data, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return nil, fmt.Errorf("fork blob: base64 decode: %w", err)
	}

	if len(data) < forkBlobNonceSize {
		return nil, fmt.Errorf("fork blob: ciphertext too short: %d bytes", len(data))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("fork blob: new cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("fork blob: new gcm: %w", err)
	}

	nonce := data[:forkBlobNonceSize]
	sealed := data[forkBlobNonceSize:]

	plaintext, err := gcm.Open(nil, nonce, sealed, forkBlobAD)
	if err != nil {
		return nil, fmt.Errorf("fork blob: decrypt: %w", err)
	}

	var blob ForkBlob
	if err := json.Unmarshal(plaintext, &blob); err != nil {
		return nil, fmt.Errorf("fork blob: unmarshal: %w", err)
	}

	return &blob, nil
}
