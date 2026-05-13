package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	proton "github.com/ProtonMail/go-proton-api"
)

// DoSSE executes an authenticated POST and returns the raw response body for
// SSE streaming. The caller is responsible for closing the returned
// io.ReadCloser. Sets the same auth headers as DoJSON (x-pm-uid,
// Authorization, x-pm-appversion, User-Agent) plus Accept: text/event-stream.
// Returns an *Error on non-2xx HTTP status.
func (s *Session) DoSSE(ctx context.Context, path string, body any) (io.ReadCloser, error) {
	reqURL := path
	if !strings.HasPrefix(path, "http") {
		base := s.BaseURL
		if base == "" {
			base = proton.DefaultHostURL
		}
		reqURL = base + path
	}

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("doSSE: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("doSSE: new request: %w", err)
	}

	req.Header.Set("x-pm-uid", s.Auth.UID)
	if s.Auth.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.Auth.AccessToken)
	}
	appVer := s.resolveAppVersion(reqURL)
	if appVer != "" {
		req.Header.Set("x-pm-appversion", appVer)
	}
	if s.UserAgent != "" {
		req.Header.Set("User-Agent", s.UserAgent)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "text/event-stream")

	slog.Debug("doSSE.request", "url", reqURL, "appversion", appVer)

	httpClient := s.initHTTPClient()
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("doSSE: POST %s: %w", path, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxJSONResponseSize))
		if readErr != nil {
			return nil, &Error{Status: resp.StatusCode}
		}
		// Try to extract an API error message from the response body.
		var envelope apiEnvelope
		if json.Unmarshal(respBody, &envelope) == nil && envelope.Code != 0 {
			slog.Debug("doSSE.error", "url", reqURL, "status", resp.StatusCode, "code", envelope.Code, "message", envelope.Error)
			return nil, &Error{
				Status:  resp.StatusCode,
				Code:    envelope.Code,
				Message: envelope.Error,
			}
		}
		return nil, &Error{Status: resp.StatusCode}
	}

	return resp.Body, nil
}
