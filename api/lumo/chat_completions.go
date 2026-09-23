package lumo

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// ChatEndpoint is the Lumo 2.0 unified generation endpoint. It replaces the
// retired PHP path "ai/v1/chat" (which now returns 404 "Path not found") and
// speaks an OpenAI chat/completions-shaped protocol: the request carries a
// `messages` array plus a `lumo` extension holding the encryption material, and
// the response is a stream of `choices[].delta` chunks. The encryption of the
// message content and of the request key is unchanged from the legacy path.
const ChatEndpoint = "ai/v1/chat/completions"

// chatMessage is one entry of the chat/completions `messages` array. Content is
// the (encrypted) turn text; Encrypted marks it so the backend decrypts it with
// the per-request key carried in the lumo extension.
type chatMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Encrypted bool   `json:"encrypted,omitempty"`
}

// streamOptions asks the backend to emit a final usage chunk.
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// toolRef names a native Lumo tool to enable for the request.
type toolRef struct {
	Name string `json:"name"`
}

// lumoExtension is the request's `lumo` object: client metadata plus the
// end-to-end encryption material (the PGP-wrapped request key and its id).
type lumoExtension struct {
	ClientType string `json:"client_type"`
	Target     string `json:"target,omitempty"`
	RequestKey string `json:"request_key,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
}

// chatCompletionsBody is the POST body for ChatEndpoint. It mirrors the wire
// format produced by Proton's Lumo web client (toChatCompletionsBody).
type chatCompletionsBody struct {
	Model           string        `json:"model"`
	Messages        []chatMessage `json:"messages"`
	Stream          bool          `json:"stream"`
	StreamOptions   streamOptions `json:"stream_options"`
	ReasoningEffort string        `json:"reasoning_effort"`
	Tools           []toolRef     `json:"tools,omitempty"`
	ToolChoice      string        `json:"tool_choice,omitempty"`
	Lumo            lumoExtension `json:"lumo"`
}

// chatModel maps to the wire `model` value. Lumo currently exposes a single
// routed model; "auto"/default resolves to "lumo".
const chatModel = "lumo"

// chatMessageRole maps an internal Role to the chat/completions wire role.
func chatMessageRole(role Role) string {
	switch role {
	case RoleToolResult:
		return "tool"
	case RoleToolCall:
		return "lumo_tool_call"
	default:
		return string(role)
	}
}

// chatTarget maps a generation target to the single-valued lumo.target field.
func chatTarget(target GenerationTarget) string {
	if target == TargetTitle {
		return "title"
	}
	return "message"
}

// buildChatCompletionsBody assembles the ChatEndpoint POST body from
// already-encrypted turns, the PGP-wrapped request key and its id.
func buildChatCompletionsBody(turns []Turn, tools []ToolName, target GenerationTarget,
	encRequestKey, requestID string) chatCompletionsBody {
	messages := make([]chatMessage, len(turns))
	for i := range turns {
		messages[i] = chatMessage{
			Role:      chatMessageRole(turns[i].Role),
			Content:   turns[i].Content,
			Encrypted: turns[i].Encrypted,
		}
	}

	body := chatCompletionsBody{
		Model:           chatModel,
		Messages:        messages,
		Stream:          true,
		StreamOptions:   streamOptions{IncludeUsage: true},
		ReasoningEffort: "none",
		Lumo: lumoExtension{
			ClientType: "frontend",
			Target:     chatTarget(target),
			RequestKey: encRequestKey,
			RequestID:  requestID,
		},
	}

	if len(tools) > 0 {
		body.Tools = make([]toolRef, len(tools))
		for i, name := range tools {
			body.Tools[i] = toolRef{Name: string(name)}
		}
		body.ToolChoice = "auto"
	}

	return body
}

// v2Delta is the streamed delta of a chat/completions choice.
type v2Delta struct {
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content"`
	Reasoning        string `json:"reasoning"`
	Target           string `json:"target"`
	Encrypted        bool   `json:"encrypted"`
}

// v2Chunk is one parsed SSE data line of the chat/completions response.
type v2Chunk struct {
	Object string `json:"object"`
	Model  string `json:"model"`
	Target string `json:"target"`
	Error  *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
	Usage   json.RawMessage `json:"usage"`
	Choices []struct {
		FinishReason string   `json:"finish_reason"`
		Delta        *v2Delta `json:"delta"`
	} `json:"choices"`
}

// contentTarget resolves the delta's target into an internal GenerationTarget,
// defaulting to a normal message.
func contentTarget(deltaTarget, topTarget string) GenerationTarget {
	raw := deltaTarget
	if raw == "" {
		raw = topTarget
	}
	switch raw {
	case string(TargetReasoning):
		return TargetReasoning
	case string(TargetToolCall):
		return TargetToolCall
	default:
		return TargetMessage
	}
}

// parseV2Line translates one SSE line of the chat/completions stream into zero
// or more GenerationResponseMessage values, preserving the message types the
// rest of the client already understands (token_data / done / harmful / error).
// Encrypted content is left encrypted here; the caller decrypts it with the
// request key, exactly as on the legacy path.
func parseV2Line(rawLine string) []GenerationResponseMessage {
	line := strings.TrimSuffix(rawLine, "\r")
	line = strings.TrimPrefix(line, "data:")
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, ":") {
		return nil
	}
	if line == "[DONE]" {
		return []GenerationResponseMessage{{Type: "done"}}
	}

	var chunk v2Chunk
	if err := json.Unmarshal([]byte(line), &chunk); err != nil {
		slog.Debug("lumo: skipping malformed chat.completions line", "error", err)
		return nil
	}

	// Server-side tool traffic and image chunks are not surfaced by this client.
	switch chunk.Object {
	case "chat.tool_call", "chat.tool_result", "lumo.image_data":
		return nil
	}
	if chunk.Error != nil {
		return []GenerationResponseMessage{{Type: "error"}}
	}
	if len(chunk.Choices) == 0 {
		return nil // e.g. the trailing usage-only chunk
	}

	var out []GenerationResponseMessage
	choice := chunk.Choices[0]
	if choice.FinishReason == "content_filter" {
		out = append(out, GenerationResponseMessage{Type: "harmful"})
	}
	if d := choice.Delta; d != nil {
		if d.Content != "" {
			out = append(out, GenerationResponseMessage{
				Type:      "token_data",
				Target:    contentTarget(d.Target, chunk.Target),
				Content:   d.Content,
				Encrypted: d.Encrypted,
			})
		}
		if reasoning := d.ReasoningContent; reasoning != "" {
			out = append(out, GenerationResponseMessage{
				Type:      "token_data",
				Target:    TargetReasoning,
				Content:   reasoning,
				Encrypted: d.Encrypted,
			})
		} else if d.Reasoning != "" {
			out = append(out, GenerationResponseMessage{
				Type:      "token_data",
				Target:    TargetReasoning,
				Content:   d.Reasoning,
				Encrypted: d.Encrypted,
			})
		}
	}
	return out
}

// processV2Stream reads the chat/completions SSE body, delivering each decoded
// message to fn. It returns nil on a clean [DONE], the matching sentinel error
// on a terminal message (harmful/error), or ErrStreamClosed if the stream ends
// without a done marker.
func processV2Stream(ctx context.Context, r io.Reader, fn func(GenerationResponseMessage)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, msg := range parseV2Line(scanner.Text()) {
			fn(msg)
			if msg.IsTerminal() {
				return terminalError(msg.Type)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("lumo: read stream: %w", err)
	}
	return ErrStreamClosed
}
