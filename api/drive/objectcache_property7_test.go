package drive

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/major0/proton-utils/api"
	"pgregory.net/rapid"
)

// TestPropertyNoPlaintextMetadataOnDisk verifies that no file in the
// ObjectCache store for Drive contains plaintext metadata. The store
// contains only raw encrypted API response bytes keyed by LinkID.
//
// The test writes opaque byte slices (simulating encrypted API responses) to
// the object cache via ObjectCache.Write, then reads every on-disk file and
// asserts:
//  1. Each file's content is byte-identical to what was written.
//  2. No file contains recognizable plaintext metadata — specifically, no
//     file is valid JSON with known plaintext fields (Name, MIMEType, Hash,
//     etc.) that would indicate decrypted link metadata leaked to disk.
//
// **Validates: Requirements 7.1, 7.2**
func TestPropertyNoPlaintextMetadataOnDisk(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir := t.TempDir()
		cache := api.NewObjectCache(dir)

		// Generate 1–10 link entries with opaque encrypted bytes.
		numLinks := rapid.IntRange(1, 10).Draw(rt, "numLinks")
		written := make(map[string][]byte, numLinks)
		for len(written) < numLinks {
			linkID := rapid.StringMatching(`[a-zA-Z0-9_\-]{4,32}`).Draw(rt, "linkID")
			if _, exists := written[linkID]; exists {
				continue
			}
			// Simulate encrypted API response bytes — opaque, not
			// valid JSON with plaintext fields.
			data := rapid.SliceOfN(rapid.Byte(), 1, 1024).Draw(rt, "encryptedData")
			written[linkID] = data
		}

		// Write all entries via ObjectCache.Write (same path used by
		// the Drive client's GetLink flow).
		for linkID, data := range written {
			if err := cache.Write(linkID, data); err != nil {
				rt.Fatalf("ObjectCache.Write(%q): %v", linkID, err)
			}
		}

		// Walk every file in the cache directory and verify contents.
		entries, err := os.ReadDir(dir)
		if err != nil {
			rt.Fatalf("os.ReadDir(%q): %v", dir, err)
		}

		for _, entry := range entries {
			if entry.IsDir() {
				// Skip the .tmp directory used by diskv for atomic writes.
				continue
			}

			name := entry.Name()
			path := filepath.Join(dir, name)
			ondisk, err := os.ReadFile(path) //nolint:gosec // test reads from t.TempDir()
			if err != nil {
				rt.Fatalf("os.ReadFile(%q): %v", path, err)
			}

			// 1. The file must correspond to a key we wrote.
			expected, ok := written[name]
			if !ok {
				rt.Fatalf("unexpected file %q in cache directory — not a key we wrote", name)
			}

			// 2. On-disk content must be byte-identical to what was written.
			if !bytes.Equal(ondisk, expected) {
				rt.Fatalf("file %q: on-disk content (%d bytes) differs from written data (%d bytes)",
					name, len(ondisk), len(expected))
			}

			// 3. The file must NOT contain recognizable plaintext metadata.
			//    If the content happens to be valid JSON, check for known
			//    plaintext fields that would indicate a decrypted Link
			//    object leaked to disk.
			assertNoPlaintextMetadata(rt, name, ondisk)
		}
	})
}

// assertNoPlaintextMetadata checks that raw bytes do not contain
// recognizable plaintext link metadata fields. If the bytes happen to
// parse as JSON, we inspect for known decrypted-only fields.
func assertNoPlaintextMetadata(rt *rapid.T, key string, data []byte) {
	rt.Helper()

	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		// Not valid JSON — cannot be plaintext metadata. This is the
		// expected case for encrypted API response bytes.
		return
	}

	// If it IS valid JSON, check for fields that only appear in
	// decrypted/plaintext link metadata. The encrypted proton.Link
	// envelope uses fields like "LinkID", "ParentLinkID", "Type",
	// "State" — those are fine (they're part of the encrypted API
	// response). But decrypted names, MIME types, and hashes should
	// never appear.
	plaintextFields := []string{
		"DecryptedName",
		"PlaintextName",
		"ClearName",
		"MIMEType",
		"Hash",
		"ModifyTime",
		"CreateTime",
		"Size",
		"BlockSizes",
		"DigestValue",
	}

	for _, field := range plaintextFields {
		if _, found := obj[field]; found {
			rt.Fatalf("file %q contains plaintext metadata field %q — "+
				"decrypted link data must never be written to disk", key, field)
		}
	}
}
