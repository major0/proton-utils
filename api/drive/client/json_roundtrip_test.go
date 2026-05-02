package client

import (
	"encoding/json"
	"testing"

	proton "github.com/ProtonMail/go-proton-api"
)

func TestLinkJSONRoundTrip(t *testing.T) {
	original := proton.Link{ //nolint:gosec // G101: test data, not credentials
		LinkID:                  "test-link-id",
		ParentLinkID:            "test-parent-id",
		Type:                    proton.LinkTypeFolder,
		Name:                    "encrypted-name",
		NameSignatureEmail:      "user@example.com",
		Hash:                    "abc123hash",
		Size:                    12345,
		State:                   proton.LinkStateActive,
		MIMEType:                "application/octet-stream",
		CreateTime:              1000000,
		ModifyTime:              2000000,
		NodeKey:                 "node-key-data",
		NodePassphrase:          "node-passphrase-data",
		NodePassphraseSignature: "node-sig-data",
		SignatureEmail:          "signer@example.com",
		FolderProperties: &proton.FolderProperties{
			NodeHashKey: "hash-key-data",
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	t.Logf("JSON: %s", string(data))

	var restored proton.Link
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// Check critical fields.
	if restored.LinkID != original.LinkID {
		t.Errorf("LinkID: got %q, want %q", restored.LinkID, original.LinkID)
	}
	if restored.Type != original.Type {
		t.Errorf("Type: got %d, want %d", restored.Type, original.Type)
	}
	if restored.Name != original.Name {
		t.Errorf("Name: got %q, want %q", restored.Name, original.Name)
	}
	if restored.NameSignatureEmail != original.NameSignatureEmail {
		t.Errorf("NameSignatureEmail: got %q, want %q", restored.NameSignatureEmail, original.NameSignatureEmail)
	}
	if restored.SignatureEmail != original.SignatureEmail {
		t.Errorf("SignatureEmail: got %q, want %q", restored.SignatureEmail, original.SignatureEmail)
	}
	if restored.NodeKey != original.NodeKey {
		t.Errorf("NodeKey: got %q, want %q", restored.NodeKey, original.NodeKey)
	}
	if restored.NodePassphrase != original.NodePassphrase {
		t.Errorf("NodePassphrase: got %q, want %q", restored.NodePassphrase, original.NodePassphrase)
	}
	if restored.NodePassphraseSignature != original.NodePassphraseSignature {
		t.Errorf("NodePassphraseSignature: got %q, want %q", restored.NodePassphraseSignature, original.NodePassphraseSignature)
	}
	if restored.FolderProperties == nil {
		t.Fatal("FolderProperties is nil after round-trip")
	}
	if restored.FolderProperties.NodeHashKey != original.FolderProperties.NodeHashKey {
		t.Errorf("NodeHashKey: got %q, want %q", restored.FolderProperties.NodeHashKey, original.FolderProperties.NodeHashKey)
	}
}
