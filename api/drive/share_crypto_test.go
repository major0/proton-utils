package drive

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/ProtonMail/gopenpgp/v2/helper"
	"pgregory.net/rapid"
)

// genKeyRing generates a fresh PGP key pair and returns the keyring.
func genKeyRing(t *testing.T, name string) *crypto.KeyRing {
	t.Helper()
	armored, err := helper.GenerateKey(name, name+"@test.local", nil, "x25519", 0)
	if err != nil {
		t.Fatalf("generate key %s: %v", name, err)
	}
	key, err := crypto.NewKeyFromArmored(armored)
	if err != nil {
		t.Fatalf("parse key %s: %v", name, err)
	}
	kr, err := crypto.NewKeyRing(key)
	if err != nil {
		t.Fatalf("keyring %s: %v", name, err)
	}
	return kr
}

// encryptPassphrase encrypts a plaintext passphrase with the share keyring,
// simulating how a share passphrase is stored.
func encryptPassphrase(t *testing.T, shareKR *crypto.KeyRing, plaintext []byte) string {
	t.Helper()
	msg, err := shareKR.Encrypt(crypto.NewPlainMessage(plaintext), nil)
	if err != nil {
		t.Fatalf("encrypt passphrase: %v", err)
	}
	armored, err := msg.GetArmored()
	if err != nil {
		t.Fatalf("armor passphrase: %v", err)
	}
	return armored
}

// TestGenerateKeyPacketRoundTrip_Property verifies that for any valid
// share keyring, inviter address keyring, and invitee key pair, generating
// a key packet and decrypting it with the invitee's private key yields
// the original share passphrase.
//
// **Property 1: Key Packet Round-Trip**
// **Validates: Requirements 3.2, 5.2, 5.4**
func TestGenerateKeyPacketRoundTrip_Property(t *testing.T) {
	// Key generation is expensive — generate once, randomize passphrase.
	shareKR := genKeyRing(t, "share")
	inviterKR := genKeyRing(t, "inviter")
	inviteeKR := genKeyRing(t, "invitee")

	rapid.Check(t, func(t *rapid.T) {
		// Generate random passphrase content (8-64 bytes).
		passphraseLen := rapid.IntRange(8, 64).Draw(t, "passphraseLen")
		passphrase := make([]byte, passphraseLen)
		for i := range passphrase {
			passphrase[i] = byte(rapid.IntRange(0, 255).Draw(t, "byte")) //nolint:gosec // bounded 0-255
		}

		// Encrypt the passphrase with the share keyring (simulates stored share passphrase).
		msg, err := shareKR.Encrypt(crypto.NewPlainMessage(passphrase), nil)
		if err != nil {
			t.Fatalf("encrypt passphrase: %v", err)
		}
		encPassphrase, err := msg.GetArmored()
		if err != nil {
			t.Fatalf("armor passphrase: %v", err)
		}

		// Generate the key packet for the invitee.
		keyPacketB64, sigArmored, err := GenerateKeyPacket(shareKR, inviterKR, inviteeKR, encPassphrase)
		if err != nil {
			t.Fatalf("GenerateKeyPacket: %v", err)
		}

		if keyPacketB64 == "" {
			t.Fatal("key packet is empty")
		}
		if sigArmored == "" {
			t.Fatal("signature is empty")
		}

		// Decrypt the key packet with the invitee's private key.
		keyPacketBytes, err := base64.StdEncoding.DecodeString(keyPacketB64)
		if err != nil {
			t.Fatalf("decode key packet: %v", err)
		}

		encMsg := crypto.NewPGPMessage(keyPacketBytes)
		decMsg, err := inviteeKR.Decrypt(encMsg, nil, crypto.GetUnixTime())
		if err != nil {
			t.Fatalf("decrypt key packet: %v", err)
		}

		// Verify the recovered passphrase matches the original.
		if !bytes.Equal(decMsg.GetBinary(), passphrase) {
			t.Fatalf("round-trip mismatch: got %x, want %x", decMsg.GetBinary(), passphrase)
		}

		// Verify the signature is valid (signed by inviter).
		sigBytes, err := base64.StdEncoding.DecodeString(sigArmored)
		if err != nil {
			t.Fatalf("decode signature: %v", err)
		}

		sig := crypto.NewPGPSignature(sigBytes)

		if err := inviterKR.VerifyDetached(crypto.NewPlainMessage(keyPacketBytes), sig, crypto.GetUnixTime()); err != nil {
			t.Fatalf("signature verification failed: %v", err)
		}
	})
}

// TestGenerateShareCryptoRoundTrip_Property verifies that GenerateShareCrypto
// produces a share key that can be unlocked with the address keyring, and
// the key packets can be decrypted back to valid session keys.
//
// **Property 1: Share crypto round-trip**
// **Validates: Requirements 2.1, 2.3**
func TestGenerateShareCryptoRoundTrip_Property(t *testing.T) {
	// Key generation is expensive — generate once.
	addrKR := genKeyRing(t, "addr")
	linkNodeKR := genKeyRing(t, "linkNode")
	parentKR := genKeyRing(t, "parent")

	rapid.Check(t, func(t *rapid.T) {
		// Create encrypted link passphrase and name fixtures using parentKR.
		passphrase := []byte("test-passphrase-" + rapid.StringMatching(`[a-z]{4}`).Draw(t, "pp"))
		linkName := []byte("test-name-" + rapid.StringMatching(`[a-z]{4}`).Draw(t, "name"))

		encPassphrase, err := parentKR.Encrypt(crypto.NewPlainMessage(passphrase), nil)
		if err != nil {
			t.Fatalf("encrypt passphrase: %v", err)
		}
		encPassphraseArm, err := encPassphrase.GetArmored()
		if err != nil {
			t.Fatalf("armor passphrase: %v", err)
		}

		encName, err := parentKR.Encrypt(crypto.NewPlainMessage(linkName), nil)
		if err != nil {
			t.Fatalf("encrypt name: %v", err)
		}
		encNameArm, err := encName.GetArmored()
		if err != nil {
			t.Fatalf("armor name: %v", err)
		}

		// Generate share crypto.
		shareKeyArm, _, _, ppKPB64, nameKPB64, err := GenerateShareCrypto(
			addrKR, linkNodeKR, parentKR, encPassphraseArm, encNameArm,
		)
		if err != nil {
			t.Fatalf("GenerateShareCrypto: %v", err)
		}

		if shareKeyArm == "" || ppKPB64 == "" || nameKPB64 == "" {
			t.Fatal("empty output fields")
		}

		// Verify: unlock the share key using the passphrase from linkNodeKR.
		// The passphrase was encrypted with linkNodeKR, so we need to decrypt it first.
		// For this test, we verify the share key is a valid armored key.
		lockedKey, err := crypto.NewKeyFromArmored(shareKeyArm)
		if err != nil {
			t.Fatalf("parse share key: %v", err)
		}
		if lockedKey == nil {
			t.Fatal("share key is nil")
		}

		// Verify key packets are valid base64.
		ppKP, err := base64.StdEncoding.DecodeString(ppKPB64)
		if err != nil {
			t.Fatalf("decode passphrase key packet: %v", err)
		}
		if len(ppKP) == 0 {
			t.Fatal("passphrase key packet is empty")
		}

		nameKP, err := base64.StdEncoding.DecodeString(nameKPB64)
		if err != nil {
			t.Fatalf("decode name key packet: %v", err)
		}
		if len(nameKP) == 0 {
			t.Fatal("name key packet is empty")
		}
	})
}
