package lumoCmd

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"pgregory.net/rapid"
)

// okHandler is a simple handler that returns 200 OK.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// TestAuthMiddleware_TokenValidation_Property verifies that the auth
// middleware returns 200 iff the provided bearer token matches the expected
// key, and 401 for mismatch, empty, missing, or malformed headers.
//
// Feature: lumo-serve, Property 5: Auth middleware token validation
//
// **Validates: Requirements 2.4, 2.5**
func TestAuthMiddleware_TokenValidation_Property(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		expected := rapid.StringMatching(`[a-f0-9]{64}`).Draw(rt, "expected")
		provided := rapid.StringMatching(`[a-f0-9]{64}`).Draw(rt, "provided")

		handler := authMiddleware(expected, okHandler)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+provided)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if expected == provided {
			if rec.Code != http.StatusOK {
				rt.Fatalf("matching tokens: status = %d, want 200", rec.Code)
			}
		} else {
			if rec.Code != http.StatusUnauthorized {
				rt.Fatalf("mismatched tokens: status = %d, want 401", rec.Code)
			}
		}
	})
}

// TestAuthMiddleware_MissingHeader verifies 401 when Authorization is absent.
func TestAuthMiddleware_MissingHeader(t *testing.T) {
	handler := authMiddleware("testkey", okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing header: status = %d, want 401", rec.Code)
	}
}

// TestAuthMiddleware_MalformedHeader verifies 401 for non-Bearer auth.
func TestAuthMiddleware_MalformedHeader(t *testing.T) {
	handler := authMiddleware("testkey", okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("malformed header: status = %d, want 401", rec.Code)
	}
}

// TestAuthMiddleware_EmptyBearer verifies 401 for empty bearer token.
func TestAuthMiddleware_EmptyBearer(t *testing.T) {
	handler := authMiddleware("testkey", okHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("empty bearer: status = %d, want 401", rec.Code)
	}
}
