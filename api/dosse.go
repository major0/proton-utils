package api

import (
	"context"
	"io"
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
	appVer := s.resolveAppVersion(reqURL)
	client := s.initHTTPClient()

	return ExecuteSSE(ctx, client, reqURL, s.Auth.UID, appVer, s.UserAgent, body, func(req *http.Request) {
		if s.Auth.AccessToken != "" {
			req.Header.Set("Authorization", "Bearer "+s.Auth.AccessToken)
		}
	})
}
