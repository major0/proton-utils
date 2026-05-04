package lumoCmd

import (
	"testing"

	"github.com/major0/proton-cli/api/lumo"
	"pgregory.net/rapid"
)

// genOpenAIMessage generates an arbitrary OpenAI message with a known role.
func genOpenAIMessage(t *rapid.T) lumo.OAIMessage {
	return lumo.OAIMessage{
		Role:    rapid.SampledFrom([]string{"system", "user", "assistant"}).Draw(t, "role"),
		Content: rapid.String().Draw(t, "content"),
	}
}

// genOpenAIMessages generates a non-empty slice of OpenAI messages.
func genOpenAIMessages(t *rapid.T) []lumo.OAIMessage {
	n := rapid.IntRange(1, 20).Draw(t, "num_messages")
	msgs := make([]lumo.OAIMessage, n)
	for i := range msgs {
		msgs[i] = genOpenAIMessage(t)
	}
	return msgs
}

// expectedRole returns the expected Lumo role for an OpenAI role string.
func expectedRole(role string) lumo.Role {
	switch role {
	case "system":
		return lumo.RoleSystem
	case "user":
		return lumo.RoleUser
	case "assistant":
		return lumo.RoleAssistant
	default:
		return lumo.RoleUser
	}
}

// TestMessagesToTurns_RoleMapping_Property verifies that for any sequence of
// OpenAI messages with roles in {system, user, assistant}, MessagesToTurns
// produces Lumo turns where each turn's role matches the mapping and content
// is preserved.
//
// Feature: lumo-serve, Property 2: Message-to-turn role mapping and content preservation
//
// **Validates: Requirements 4.2**
func TestMessagesToTurns_RoleMapping_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		msgs := genOpenAIMessages(t)
		turns := MessagesToTurns(msgs)

		if len(turns) != len(msgs) {
			t.Fatalf("len(turns) = %d, want %d", len(turns), len(msgs))
		}

		for i, msg := range msgs {
			turn := turns[i]
			want := expectedRole(msg.Role)
			if turn.Role != want {
				t.Fatalf("turn[%d].Role = %q, want %q (from OpenAI role %q)", i, turn.Role, want, msg.Role)
			}
			if turn.Content != msg.Content {
				t.Fatalf("turn[%d].Content = %q, want %q", i, turn.Content, msg.Content)
			}
		}
	})
}

// TestChunkToSSEEvent_Property verifies that for any Lumo token_data message
// with non-empty content, ChunkToSSEEvent produces a ChatCompletionChunk where
// choices[0].delta.content equals the message content, object is
// "chat.completion.chunk", and model and ID match the provided arguments.
//
// Feature: lumo-serve, Property 3: Chunk-to-SSE-event conversion
//
// **Validates: Requirements 4.3**
func TestChunkToSSEEvent_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		content := rapid.StringMatching(`.+`).Draw(t, "content")
		id := rapid.StringMatching(`chatcmpl-[a-zA-Z0-9]{10}`).Draw(t, "id")
		model := rapid.StringMatching(`[a-z0-9-]{1,32}`).Draw(t, "model")

		msg := lumo.GenerationResponseMessage{
			Type:    "token_data",
			Content: content,
		}

		chunk, ok := ChunkToSSEEvent(msg, id, model)
		if !ok {
			t.Fatal("ChunkToSSEEvent returned false for token_data with non-empty content")
		}

		if chunk.Object != "chat.completion.chunk" {
			t.Fatalf("Object = %q, want %q", chunk.Object, "chat.completion.chunk")
		}
		if chunk.ID != id {
			t.Fatalf("ID = %q, want %q", chunk.ID, id)
		}
		if chunk.Model != model {
			t.Fatalf("Model = %q, want %q", chunk.Model, model)
		}
		if len(chunk.Choices) != 1 {
			t.Fatalf("len(Choices) = %d, want 1", len(chunk.Choices))
		}
		if chunk.Choices[0].Delta == nil {
			t.Fatal("Choices[0].Delta is nil")
		}
		if chunk.Choices[0].Delta.Content != content {
			t.Fatalf("Delta.Content = %q, want %q", chunk.Choices[0].Delta.Content, content)
		}
	})
}

// TestChunkToSSEEvent_NonTokenData_Property verifies that non-token_data
// messages and empty-content token_data messages produce no output.
func TestChunkToSSEEvent_NonTokenData_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		msgType := rapid.SampledFrom([]string{"queued", "ingesting", "done", "timeout", "error", "rejected", "harmful"}).Draw(t, "type")
		msg := lumo.GenerationResponseMessage{
			Type:    msgType,
			Content: rapid.String().Draw(t, "content"),
		}
		_, ok := ChunkToSSEEvent(msg, "id", "model")
		if ok {
			t.Fatalf("ChunkToSSEEvent returned true for type %q", msgType)
		}
	})
}

// TestAccumulateResponse_Property verifies that for any non-empty content
// string, AccumulateResponse produces a ChatCompletionResponse where
// choices[0].message.content equals the input, object is "chat.completion",
// finish_reason is "stop", and model and ID match the provided arguments.
//
// Feature: lumo-serve, Property 4: Accumulated response correctness
//
// **Validates: Requirements 4.4**
func TestAccumulateResponse_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		content := rapid.StringMatching(`.+`).Draw(t, "content")
		id := rapid.StringMatching(`chatcmpl-[a-zA-Z0-9]{10}`).Draw(t, "id")
		model := rapid.StringMatching(`[a-z0-9-]{1,32}`).Draw(t, "model")

		resp := AccumulateResponse(content, id, model)

		if resp.Object != "chat.completion" {
			t.Fatalf("Object = %q, want %q", resp.Object, "chat.completion")
		}
		if resp.ID != id {
			t.Fatalf("ID = %q, want %q", resp.ID, id)
		}
		if resp.Model != model {
			t.Fatalf("Model = %q, want %q", resp.Model, model)
		}
		if len(resp.Choices) != 1 {
			t.Fatalf("len(Choices) = %d, want 1", len(resp.Choices))
		}
		choice := resp.Choices[0]
		if choice.Message == nil {
			t.Fatal("Choices[0].Message is nil")
		}
		if choice.Message.Content != content {
			t.Fatalf("Message.Content = %q, want %q", choice.Message.Content, content)
		}
		if choice.FinishReason == nil {
			t.Fatal("FinishReason is nil")
		}
		if *choice.FinishReason != "stop" {
			t.Fatalf("FinishReason = %q, want %q", *choice.FinishReason, "stop")
		}
	})
}
