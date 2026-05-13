package lumoCmd

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/major0/proton-utils/api/lumo"
)

// TestMapLumoError_Table verifies the error mapping table from Lumo sentinel
// errors to (HTTP status, OpenAI error type).
func TestMapLumoError_Table(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantType   string
	}{
		{"ErrRejected", lumo.ErrRejected, 400, "invalid_request_error"},
		{"ErrHarmful", lumo.ErrHarmful, 400, "content_filter"},
		{"ErrTimeout", lumo.ErrTimeout, 504, "timeout"},
		{"ErrStreamClosed", lumo.ErrStreamClosed, 502, "server_error"},
		{"context.Canceled", context.Canceled, 499, "cancelled"},
		{"unknown", context.DeadlineExceeded, 500, "server_error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, errType, _ := mapLumoError(tt.err)
			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}
			if errType != tt.wantType {
				t.Errorf("type = %q, want %q", errType, tt.wantType)
			}
		})
	}
}

// TestModelsHandler verifies the models endpoint returns the expected
// response structure with a lumo model entry.
func TestModelsHandler(t *testing.T) {
	handler := modelsHandler()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}

	var resp lumo.OAIModelList
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Object != "list" {
		t.Errorf("Object = %q, want list", resp.Object)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("len(Data) = %d, want 1", len(resp.Data))
	}
	if resp.Data[0].ID != "lumo" {
		t.Errorf("Data[0].ID = %q, want lumo", resp.Data[0].ID)
	}
	if resp.Data[0].OwnedBy != "proton" {
		t.Errorf("Data[0].OwnedBy = %q, want proton", resp.Data[0].OwnedBy)
	}
}

// TestLoggingMiddleware verifies that the logging middleware logs method,
// path, and latency without logging content.
func TestLoggingMiddleware(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	defer slog.SetDefault(slog.Default())

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := loggingMiddleware(inner)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"secret":"data"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	logOutput := buf.String()
	if !strings.Contains(logOutput, "POST") {
		t.Error("log missing method")
	}
	if !strings.Contains(logOutput, "/v1/chat/completions") {
		t.Error("log missing path")
	}
	if !strings.Contains(logOutput, "latency") {
		t.Error("log missing latency")
	}
	// Must not log request body content.
	if strings.Contains(logOutput, "secret") {
		t.Error("log contains request body content")
	}
}
