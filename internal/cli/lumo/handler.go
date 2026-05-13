package lumoCmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/major0/proton-utils/api/lumo"
)

// chatHandler returns an http.HandlerFunc that proxies OpenAI-format chat
// completion requests to Lumo via the provided client.
func chatHandler(client *lumo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req lumo.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON: "+err.Error())
			return
		}

		turns := MessagesToTurns(req.Messages)
		id := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
		model := "lumo"

		if req.Stream {
			streamResponse(r.Context(), w, client, turns, id, model)
		} else {
			nonStreamResponse(r.Context(), w, client, turns, id, model)
		}
	}
}

// streamResponse handles streaming (SSE) chat completions.
func streamResponse(ctx context.Context, w http.ResponseWriter, client *lumo.Client, turns []lumo.Turn, id, model string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "server_error", "Streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	err := client.Generate(ctx, turns, lumo.GenerateOpts{
		ChunkCallback: func(msg lumo.GenerationResponseMessage) {
			chunk, ok := ChunkToSSEEvent(msg, id, model)
			if !ok {
				return
			}
			data, err := json.Marshal(chunk)
			if err != nil {
				return
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		},
	})

	if err != nil {
		// Mid-stream error: write error as final SSE event.
		status, errType, message := mapLumoError(err)
		errResp := lumo.OAIErrorResponse{
			Error: lumo.OAIErrorBody{
				Message: message,
				Type:    errType,
			},
		}
		data, _ := json.Marshal(errResp)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		slog.Error("stream error", "status", status, "type", errType)
		return
	}

	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// nonStreamResponse handles non-streaming chat completions.
func nonStreamResponse(ctx context.Context, w http.ResponseWriter, client *lumo.Client, turns []lumo.Turn, id, model string) {
	var content strings.Builder

	err := client.Generate(ctx, turns, lumo.GenerateOpts{
		ChunkCallback: func(msg lumo.GenerationResponseMessage) {
			if msg.Type == "token_data" && msg.Content != "" {
				content.WriteString(msg.Content)
			}
		},
	})

	if err != nil {
		status, errType, message := mapLumoError(err)
		writeError(w, status, errType, message)
		return
	}

	resp := AccumulateResponse(content.String(), id, model)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// modelsHandler returns an http.HandlerFunc that serves the static model list.
func modelsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		resp := lumo.OAIModelList{
			Object: "list",
			Data: []lumo.OAIModel{{
				ID:      "lumo",
				Object:  "model",
				Created: 0,
				OwnedBy: "proton",
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// loggingMiddleware logs method, path, status, and latency for each request.
// Never logs request or response bodies.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		slog.LogAttrs(r.Context(), slog.LevelInfo, "request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", sw.status),
			slog.Duration("latency", time.Since(start)),
		)
	})
}

// statusWriter wraps http.ResponseWriter to capture the status code.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

// Flush implements http.Flusher for streaming support.
func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// writeError writes an OpenAI-format error response.
func writeError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(lumo.OAIErrorResponse{
		Error: lumo.OAIErrorBody{
			Message: message,
			Type:    errType,
		},
	})
}

// mapLumoError maps a Lumo error to (HTTP status, OpenAI error type, message).
func mapLumoError(err error) (int, string, string) {
	switch {
	case errors.Is(err, lumo.ErrRejected):
		return http.StatusBadRequest, "invalid_request_error", "Request rejected by Lumo"
	case errors.Is(err, lumo.ErrHarmful):
		return http.StatusBadRequest, "content_filter", "Content flagged by Lumo"
	case errors.Is(err, lumo.ErrTimeout):
		return http.StatusGatewayTimeout, "timeout", "Lumo generation timed out"
	case errors.Is(err, lumo.ErrStreamClosed):
		return http.StatusBadGateway, "server_error", "Upstream connection closed"
	case errors.Is(err, context.Canceled):
		return 499, "cancelled", "Request cancelled"
	default:
		return http.StatusInternalServerError, "server_error", "Internal server error"
	}
}
