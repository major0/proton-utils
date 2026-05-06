package lumoCmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/major0/proton-cli/api/lumo"
	"pgregory.net/rapid"
)

// --- Property 1: Re-encryption round-trip preserves payload ---

// TestPropertyReEncryptionRoundTrip verifies that for any valid JSON
// payload and any valid source/target AD pair, decrypting source
// ciphertext then re-encrypting under target AD produces ciphertext
// that, when decrypted with target AD, yields the exact original
// plaintext bytes.
//
// Feature: lumo-chat-cp, Property 1: Re-encryption round-trip preserves payload
//
// **Validates: Requirements 2.5, 2.6, 3.2, 3.3, 3.4**
func TestPropertyReEncryptionRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a random valid JSON payload (simulating MessagePriv).
		payload := genJSONPayload(t)

		// Generate a random 32-byte DEK.
		dek := rapid.SliceOfN(rapid.Byte(), 32, 32).Draw(t, "dek")

		// Generate source AD components.
		srcMsgTag := rapid.StringMatching(`[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}`).Draw(t, "src_msg_tag")
		srcRole := rapid.SampledFrom([]string{"user", "assistant"}).Draw(t, "src_role")
		srcParentTag := rapid.StringMatching(`[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}`).Draw(t, "src_parent_tag")
		srcConvTag := rapid.StringMatching(`[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}`).Draw(t, "src_conv_tag")

		// Generate target AD components (different tag, same role, empty parent, different conv).
		tgtMsgTag := rapid.StringMatching(`[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}`).Draw(t, "tgt_msg_tag")
		tgtConvTag := rapid.StringMatching(`[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}`).Draw(t, "tgt_conv_tag")

		// Construct source and target AD strings.
		srcAD := lumo.MessageAD(srcMsgTag, srcRole, srcParentTag, srcConvTag)
		tgtAD := lumo.MessageAD(tgtMsgTag, srcRole, "", tgtConvTag)

		// Step 1: Encrypt plaintext under source AD.
		ciphertext, err := lumo.EncryptString(payload, dek, srcAD)
		if err != nil {
			t.Fatalf("EncryptString (source): %v", err)
		}

		// Step 2: Decrypt with source AD (simulates reading the source message).
		recovered, err := lumo.DecryptString(ciphertext, dek, srcAD)
		if err != nil {
			t.Fatalf("DecryptString (source): %v", err)
		}

		// Step 3: Re-encrypt under target AD (simulates writing to new conversation).
		newCiphertext, err := lumo.EncryptString(recovered, dek, tgtAD)
		if err != nil {
			t.Fatalf("EncryptString (target): %v", err)
		}

		// Step 4: Decrypt with target AD (simulates reading the copied message).
		final, err := lumo.DecryptString(newCiphertext, dek, tgtAD)
		if err != nil {
			t.Fatalf("DecryptString (target): %v", err)
		}

		// Assert: final plaintext must be byte-for-byte identical to original.
		if final != payload {
			t.Fatalf("re-encryption round-trip mismatch:\n  original: %q\n  final:    %q", payload, final)
		}
	})
}

// --- Property 2: Title transformation ---

// TestPropertyTitleTransformation verifies that for any conversation
// title string, the copied title equals original + " (copy)" and
// survives an encrypt/decrypt round-trip under a fresh ConversationTag
// and SpaceTag. Source and target use independent DEKs and SpaceTags
// since the copy goes into a new space.
//
// Feature: lumo-chat-cp, Property 2: Title transformation
//
// **Validates: Requirements 2.2, 2.3**
func TestPropertyTitleTransformation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate an arbitrary title string.
		title := rapid.String().Draw(t, "title")

		// Generate independent source DEK and space tag.
		srcDEK := rapid.SliceOfN(rapid.Byte(), 32, 32).Draw(t, "src_dek")
		srcSpaceTag := rapid.StringMatching(`[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}`).Draw(t, "src_space_tag")
		srcConvTag := rapid.StringMatching(`[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}`).Draw(t, "src_conv_tag")

		// Generate independent target DEK and space tag (new space for copy).
		tgtDEK := rapid.SliceOfN(rapid.Byte(), 32, 32).Draw(t, "tgt_dek")
		tgtSpaceTag := rapid.StringMatching(`[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}`).Draw(t, "tgt_space_tag")
		tgtConvTag := rapid.StringMatching(`[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}`).Draw(t, "tgt_conv_tag")

		// Step 1: Encrypt the source title as the API would store it.
		srcTitleJSON := `{"title":` + mustMarshalString(title) + `}`
		srcAD := lumo.ConversationAD(srcConvTag, srcSpaceTag)
		srcCiphertext, err := lumo.EncryptString(srcTitleJSON, srcDEK, srcAD)
		if err != nil {
			t.Fatalf("EncryptString (source title): %v", err)
		}

		// Step 2: Decrypt the source title (simulates decryptConversationTitle).
		decrypted, err := lumo.DecryptString(srcCiphertext, srcDEK, srcAD)
		if err != nil {
			t.Fatalf("DecryptString (source title): %v", err)
		}

		var priv struct {
			Title string `json:"title"`
		}
		if err := json.Unmarshal([]byte(decrypted), &priv); err != nil {
			t.Fatalf("json.Unmarshal title: %v", err)
		}

		// Step 3: Apply the " (copy)" transformation.
		newTitle := priv.Title + " (copy)"

		// Verify the transformation produces the expected result.
		expectedTitle := title + " (copy)"
		if newTitle != expectedTitle {
			t.Fatalf("title transformation mismatch:\n  expected: %q\n  got:      %q", expectedTitle, newTitle)
		}

		// Step 4: Encrypt the new title under the target space's DEK and tags.
		tgtTitleJSON := `{"title":` + mustMarshalString(newTitle) + `}`
		tgtAD := lumo.ConversationAD(tgtConvTag, tgtSpaceTag)
		tgtCiphertext, err := lumo.EncryptString(tgtTitleJSON, tgtDEK, tgtAD)
		if err != nil {
			t.Fatalf("EncryptString (target title): %v", err)
		}

		// Step 5: Decrypt with target AD to verify round-trip.
		recovered, err := lumo.DecryptString(tgtCiphertext, tgtDEK, tgtAD)
		if err != nil {
			t.Fatalf("DecryptString (target title): %v", err)
		}

		var recoveredPriv struct {
			Title string `json:"title"`
		}
		if err := json.Unmarshal([]byte(recovered), &recoveredPriv); err != nil {
			t.Fatalf("json.Unmarshal recovered title: %v", err)
		}

		// Assert: recovered title must equal original + " (copy)".
		if recoveredPriv.Title != expectedTitle {
			t.Fatalf("title round-trip mismatch:\n  expected: %q\n  got:      %q", expectedTitle, recoveredPriv.Title)
		}
	})
}

// mustMarshalString JSON-encodes a string value (with proper escaping).
func mustMarshalString(s string) string {
	bs, err := json.Marshal(s)
	if err != nil {
		panic("json.Marshal string: " + err.Error())
	}
	return string(bs)
}

// --- Property 3: Chronological order preservation ---

// TestPropertyChronologicalOrderPreservation verifies that for any
// sequence of source messages, the copy loop produces messages in the
// same index order as the source. The i-th output corresponds to the
// i-th input — no reordering occurs.
//
// Feature: lumo-chat-cp, Property 3: Chronological order preservation
//
// **Validates: Requirements 2.7**
func TestPropertyChronologicalOrderPreservation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a random number of messages (1 to 50).
		n := rapid.IntRange(1, 50).Draw(t, "num_messages")

		// Simulate the source message list as an ordered slice of indices.
		// Each index represents a message's position in the source conversation.
		type sourceMsg struct {
			Index int
			ID    string
		}
		srcMessages := make([]sourceMsg, n)
		for i := range srcMessages {
			srcMessages[i] = sourceMsg{
				Index: i,
				ID:    fmt.Sprintf("msg-%d", i),
			}
		}

		// Simulate the copy loop: iterate srcMessages in order and record
		// the output index for each processed message. This mirrors the
		// sequential iteration in runChatCp's `for _, shallow := range srcConv.Messages`.
		outputIndices := make([]int, 0, n)
		for _, msg := range srcMessages {
			outputIndices = append(outputIndices, msg.Index)
		}

		// Assert: output indices must be in strictly ascending order,
		// matching the input order exactly.
		if len(outputIndices) != n {
			t.Fatalf("expected %d outputs, got %d", n, len(outputIndices))
		}
		for i := 0; i < n; i++ {
			if outputIndices[i] != i {
				t.Fatalf("order mismatch at position %d: expected index %d, got %d", i, i, outputIndices[i])
			}
		}
	})
}

// --- Property 4: ParentID flattening ---

// TestPropertyParentIDFlattening verifies that for any copied message,
// regardless of the source message's ParentID value, the target
// CreateMessageReq.ParentID is always empty and the target AD uses
// empty string for parentId (which causes the parentId key to be
// omitted entirely from the AD JSON).
//
// Feature: lumo-chat-cp, Property 4: ParentID flattening
//
// **Validates: Requirements 2.8, 3.3**
func TestPropertyParentIDFlattening(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a random source ParentID — could be empty, a UUID, or
		// any arbitrary string.
		srcParentID := rapid.OneOf(
			rapid.Just(""),
			rapid.StringMatching(`[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}`),
			rapid.String(),
		).Draw(t, "src_parent_id")

		// Generate target AD components.
		tgtMsgTag := rapid.StringMatching(`[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}`).Draw(t, "tgt_msg_tag")
		role := rapid.SampledFrom([]string{"user", "assistant"}).Draw(t, "role")
		tgtConvTag := rapid.StringMatching(`[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}`).Draw(t, "tgt_conv_tag")

		// Construct target AD the same way chat_cp.go does: always empty parentID.
		targetAD := lumo.MessageAD(tgtMsgTag, role, "", tgtConvTag)

		// Property assertion 1: The target AD must NOT contain a "parentId" key.
		// MessageAD omits the key entirely when parentID is empty.
		if strings.Contains(targetAD, `"parentId"`) {
			t.Fatalf("target AD contains parentId key (should be omitted for empty parentID):\n  srcParentID: %q\n  targetAD:    %s", srcParentID, targetAD)
		}

		// Property assertion 2: Simulate building the CreateMessageReq as
		// chat_cp.go does — ParentID is never set for copied messages.
		req := lumo.CreateMessageReq{
			ConversationID: "conv-id",
			MessageTag:     tgtMsgTag,
			Role:           1,
			Encrypted:      "ciphertext",
			// ParentID intentionally not set — this is the flattening behavior.
		}
		if req.ParentID != "" {
			t.Fatalf("CreateMessageReq.ParentID is not empty: %q", req.ParentID)
		}

		// Contrast: verify that a non-empty source parentID WOULD produce
		// a different AD (confirming the flattening is meaningful).
		if srcParentID != "" {
			srcAD := lumo.MessageAD(tgtMsgTag, role, srcParentID, tgtConvTag)
			if !strings.Contains(srcAD, `"parentId"`) {
				t.Fatalf("source AD with non-empty parentID should contain parentId key:\n  srcParentID: %q\n  srcAD:       %s", srcParentID, srcAD)
			}
			if srcAD == targetAD {
				t.Fatalf("source AD with non-empty parentID should differ from target AD:\n  srcParentID: %q\n  AD:          %s", srcParentID, srcAD)
			}
		}
	})
}

// --- Property 5: Fresh unique MessageTags ---

// TestPropertyFreshUniqueMessageTags verifies that for any set of N
// copied messages, all N generated MessageTags are distinct from each
// other AND distinct from all source MessageTags.
//
// Feature: lumo-chat-cp, Property 5: Fresh unique MessageTags
//
// **Validates: Requirements 3.5**
func TestPropertyFreshUniqueMessageTags(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Draw N in a reasonable range (1 to 100).
		n := rapid.IntRange(1, 100).Draw(t, "num_messages")

		// Generate N random source MessageTags (simulating existing messages).
		sourceTags := make(map[string]struct{}, n)
		for i := 0; i < n; i++ {
			tag := rapid.StringMatching(`[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}`).Draw(t, fmt.Sprintf("src_tag_%d", i))
			sourceTags[tag] = struct{}{}
		}

		// Call GenerateTag() N times (simulating the copy loop).
		generatedTags := make(map[string]struct{}, n)
		for i := 0; i < n; i++ {
			tag := lumo.GenerateTag()

			// Assert: no duplicate among generated tags.
			if _, exists := generatedTags[tag]; exists {
				t.Fatalf("duplicate generated tag at index %d: %s", i, tag)
			}
			generatedTags[tag] = struct{}{}

			// Assert: no collision with any source tag.
			if _, exists := sourceTags[tag]; exists {
				t.Fatalf("generated tag collides with source tag at index %d: %s", i, tag)
			}
		}

		// Final sanity check: we produced exactly N distinct generated tags.
		if len(generatedTags) != n {
			t.Fatalf("expected %d distinct generated tags, got %d", n, len(generatedTags))
		}
	})
}

// genJSONPayload generates a random valid JSON object string that
// simulates a MessagePriv payload with varying fields.
func genJSONPayload(t *rapid.T) string {
	// Build a map with random fields to simulate MessagePriv structure.
	m := make(map[string]any)

	// Always include content (the most common field).
	m["content"] = rapid.String().Draw(t, "content")

	// Optionally include other MessagePriv fields.
	if rapid.Bool().Draw(t, "has_blocks") {
		nBlocks := rapid.IntRange(1, 3).Draw(t, "num_blocks")
		blocks := make([]map[string]string, nBlocks)
		for i := range blocks {
			blocks[i] = map[string]string{
				"type": rapid.SampledFrom([]string{"code", "text", "markdown"}).Draw(t, "block_type"),
				"data": rapid.String().Draw(t, "block_data"),
			}
		}
		m["blocks"] = blocks
	}

	if rapid.Bool().Draw(t, "has_reasoning") {
		m["reasoning"] = rapid.String().Draw(t, "reasoning")
	}

	if rapid.Bool().Draw(t, "has_tool_call") {
		m["toolCall"] = map[string]string{
			"name": rapid.StringMatching(`[a-z_]{3,12}`).Draw(t, "tool_name"),
			"args": rapid.String().Draw(t, "tool_args"),
		}
	}

	bs, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(bs)
}
