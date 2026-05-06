package lumo

import (
	"crypto/rand"
	"errors"
	"testing"
)

func TestSpaceAD_KnownOutput(t *testing.T) {
	got := SpaceAD("abc-123")
	want := `{"app":"lumo","id":"abc-123","type":"space"}`
	if got != want {
		t.Fatalf("SpaceAD:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestConversationAD_KnownOutput(t *testing.T) {
	got := ConversationAD("conv-456", "space-789")
	want := `{"app":"lumo","id":"conv-456","spaceId":"space-789","type":"conversation"}`
	if got != want {
		t.Fatalf("ConversationAD:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestMessageAD_KnownOutput(t *testing.T) {
	got := MessageAD("msg-001", "user", "msg-000", "conv-456")
	want := `{"app":"lumo","conversationId":"conv-456","id":"msg-001","parentId":"msg-000","role":"user","type":"message"}`
	if got != want {
		t.Fatalf("MessageAD:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestMessageAD_EmptyParentID(t *testing.T) {
	got := MessageAD("msg-001", "assistant", "", "conv-456")
	want := `{"app":"lumo","conversationId":"conv-456","id":"msg-001","role":"assistant","type":"message"}`
	if got != want {
		t.Fatalf("MessageAD (empty parentID):\ngot:  %s\nwant: %s", got, want)
	}
}

func TestUnwrapSpaceKey_WrongKey(t *testing.T) {
	mk := make([]byte, 32)
	sk := make([]byte, 32)
	if _, err := rand.Read(mk); err != nil {
		t.Fatal(err)
	}
	if _, err := rand.Read(sk); err != nil {
		t.Fatal(err)
	}

	wrapped, err := WrapSpaceKey(mk, sk)
	if err != nil {
		t.Fatalf("WrapSpaceKey: %v", err)
	}

	// Use a different key to unwrap.
	wrongKey := make([]byte, 32)
	if _, err := rand.Read(wrongKey); err != nil {
		t.Fatal(err)
	}

	_, err = UnwrapSpaceKey(wrongKey, wrapped)
	if !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("expected ErrDecryptionFailed, got: %v", err)
	}
}

func TestDecryptString_WrongDEK(t *testing.T) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatal(err)
	}

	encrypted, err := EncryptString("hello", dek, "ad")
	if err != nil {
		t.Fatalf("EncryptString: %v", err)
	}

	wrongDEK := make([]byte, 32)
	if _, err := rand.Read(wrongDEK); err != nil {
		t.Fatal(err)
	}

	_, err = DecryptString(encrypted, wrongDEK, "ad")
	if !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("expected ErrDecryptionFailed, got: %v", err)
	}
}

func TestDecryptString_WrongAD(t *testing.T) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatal(err)
	}

	encrypted, err := EncryptString("hello", dek, "correct-ad")
	if err != nil {
		t.Fatalf("EncryptString: %v", err)
	}

	_, err = DecryptString(encrypted, dek, "wrong-ad")
	if !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("expected ErrDecryptionFailed, got: %v", err)
	}
}

func TestDeriveDataEncryptionKey_InvalidSize(t *testing.T) {
	_, err := DeriveDataEncryptionKey([]byte("short"))
	if err == nil {
		t.Fatal("expected error for invalid key size")
	}
}

func TestWrapSpaceKey_InvalidSizes(t *testing.T) {
	_, err := WrapSpaceKey([]byte("short"), make([]byte, 32))
	if err == nil {
		t.Fatal("expected error for short master key")
	}

	_, err = WrapSpaceKey(make([]byte, 32), []byte("short"))
	if err == nil {
		t.Fatal("expected error for short space key")
	}
}
