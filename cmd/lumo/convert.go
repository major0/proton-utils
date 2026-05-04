// Package lumoCmd implements the lumo subcommands for proton-cli.
package lumoCmd

import (
	"time"

	"github.com/major0/proton-cli/api/lumo"
)

// MessagesToTurns converts OpenAI messages to Lumo turns.
// Role mapping: system→RoleSystem, user→RoleUser, assistant→RoleAssistant.
// Unknown roles default to RoleUser.
func MessagesToTurns(msgs []lumo.OAIMessage) []lumo.Turn {
	turns := make([]lumo.Turn, len(msgs))
	for i, m := range msgs {
		turns[i] = lumo.Turn{
			Role:    mapRole(m.Role),
			Content: m.Content,
		}
	}
	return turns
}

// mapRole maps an OpenAI role string to a Lumo Role.
func mapRole(role string) lumo.Role {
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

// ChunkToSSEEvent converts a Lumo response message to an OpenAI streaming
// chunk. Returns the chunk and whether the message produced output
// (token_data with non-empty content).
func ChunkToSSEEvent(msg lumo.GenerationResponseMessage, id, model string) (lumo.ChatCompletionChunk, bool) {
	if msg.Type != "token_data" || msg.Content == "" {
		return lumo.ChatCompletionChunk{}, false
	}
	return lumo.ChatCompletionChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []lumo.Choice{{
			Index: 0,
			Delta: &lumo.OAIMessage{
				Role:    "assistant",
				Content: msg.Content,
			},
		}},
	}, true
}

// AccumulateResponse builds a complete ChatCompletionResponse from
// collected content after all chunks have been received.
func AccumulateResponse(content, id, model string) lumo.ChatCompletionResponse {
	stop := "stop"
	return lumo.ChatCompletionResponse{
		ID:      id,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []lumo.Choice{{
			Index: 0,
			Message: &lumo.OAIMessage{
				Role:    "assistant",
				Content: content,
			},
			FinishReason: &stop,
		}},
	}
}
