package cli

import (
	"log/slog"
	"strings"

	"github.com/ProtonMail/go-proton-api"
	"github.com/go-resty/resty/v2"
)

// sensitiveHeaders lists HTTP headers whose values must be redacted in debug output.
var sensitiveHeaders = map[string]bool{
	"authorization":                 true,
	"x-pm-uid":                      true,
	"x-pm-human-verification-token": true,
	"set-cookie":                    true,
	"cookie":                        true,
}

// sensitiveBodyFields lists JSON field names whose values must be redacted.
var sensitiveBodyFields = []string{
	"Password", "SRPSession", "ClientProof", "ClientEphemeral",
	"ServerProof", "AccessToken", "RefreshToken", "TokenType",
	"UID", "SaltedKeyPass", "KeySalt",
}

// redactHeader returns the header value or "<redacted>" if the header is sensitive.
func redactHeader(key, value string) string {
	if sensitiveHeaders[strings.ToLower(key)] {
		return "<redacted>"
	}
	return value
}

// redactBody replaces known sensitive field values in a body string with <redacted>.
// This is a best-effort approach for debug logging, not a security boundary.
func redactBody(body string) string {
	for _, field := range sensitiveBodyFields {
		// Match "FieldName":"value" patterns in JSON.
		prefix := `"` + field + `":"`
		var result strings.Builder
		remaining := body
		for {
			idx := strings.Index(remaining, prefix)
			if idx < 0 {
				result.WriteString(remaining)
				break
			}
			start := idx + len(prefix)
			end := strings.Index(remaining[start:], `"`)
			if end < 0 {
				result.WriteString(remaining)
				break
			}
			result.WriteString(remaining[:start])
			result.WriteString("<redacted>")
			remaining = remaining[start+end:]
		}
		body = result.String()
	}
	return body
}

// InstallDebugHooks adds pre-request and post-response logging hooks to the
// proton manager. Called when verbosity >= 3.
func InstallDebugHooks(m *proton.Manager) {
	m.AddPreRequestHook(func(_ *resty.Client, req *resty.Request) error {
		attrs := []any{
			"method", req.Method,
			"url", req.URL,
		}

		for key, values := range req.Header {
			attrs = append(attrs, "req."+key, redactHeader(key, strings.Join(values, ", ")))
		}

		if req.Body != nil {
			if s, ok := req.Body.(string); ok {
				attrs = append(attrs, "req.body", redactBody(s))
			} else {
				attrs = append(attrs, "req.body", "<non-string body>")
			}
		}

		slog.Debug("http.request", attrs...)
		return nil
	})

	m.AddPostRequestHook(func(_ *resty.Client, resp *resty.Response) error {
		attrs := []any{
			"status", resp.StatusCode(),
			"method", resp.Request.Method,
			"url", resp.Request.URL,
		}

		for key, values := range resp.Header() {
			attrs = append(attrs, "resp."+key, redactHeader(key, strings.Join(values, ", ")))
		}

		body := resp.String()
		if len(body) > 0 {
			attrs = append(attrs, "resp.body", redactBody(body))
		}

		slog.Debug("http.response", attrs...)
		return nil
	})
}
