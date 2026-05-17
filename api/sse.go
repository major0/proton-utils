package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// ExecuteSSE performs an SSE POST request with standard Proton headers.
// The caller is responsible for auth headers — pass a decorator function
// that modifies the request before execution, or nil for cookie-only auth.
func ExecuteSSE(ctx context.Context, client *http.Client, reqURL, uid, appVersion, userAgent string, body any, decorate func(*http.Request)) (io.ReadCloser, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("sse: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("sse: new request: %w", err)
	}

	req.Header.Set("x-pm-uid", uid)
	if appVersion != "" {
		req.Header.Set("x-pm-appversion", appVersion)
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "text/event-stream")

	if decorate != nil {
		decorate(req)
	}

	slog.Debug("sse.request", "url", reqURL, "appversion", appVersion)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sse: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxJSONResponseSize))
		if readErr != nil {
			return nil, &Error{Status: resp.StatusCode}
		}
		var envelope Envelope
		if json.Unmarshal(respBody, &envelope) == nil && envelope.Code != 0 {
			slog.Debug("sse.error", "url", reqURL, "status", resp.StatusCode, "code", envelope.Code, "message", envelope.Error)
			return nil, &Error{Status: resp.StatusCode, Code: envelope.Code, Message: envelope.Error, Details: envelope.Details}
		}
		return nil, &Error{Status: resp.StatusCode}
	}

	return resp.Body, nil
}
