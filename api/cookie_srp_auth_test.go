package api

import (
	"context"
	pmrand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ProtonMail/go-srp"
	"pgregory.net/rapid"
)

// SRP test vectors from go-srp/srp_test.go. These are known-good parameters
// that produce valid SRP proofs with go-srp.
const (
	srpTestUsername  = "jakubqa"
	srpTestPassword  = "abc123"
	srpTestSalt      = "yKlc5/CvObfoiw=="
	srpTestEphemeral = "l13IQSVFBEV0ZZREuRQ4ZgP6OpGiIfIjbSDYQG3Yp39FkT2B/k3n1ZhwqrAdy+qvPPFq/le0b7UDtayoX4aOTJihoRvifas8Hr3icd9nAHqd0TUBbkZkT6Iy6UpzmirCXQtEhvGQIdOLuwvy+vZWh24G2ahBM75dAqwkP961EJMh67/I5PA5hJdQZjdPT5luCyVa7BS1d9ZdmuR0/VCjUOdJbYjgtIH7BQoZs+KacjhUN8gybu+fsycvTK3eC+9mCN2Y6GdsuCMuR3pFB0RF9eKae7cA6RbJfF1bjm0nNfWLXzgKguKBOeF3GEAsnCgK68q82/pq9etiUDizUlUBcA=="
	srpTestModulus   = "-----BEGIN PGP SIGNED MESSAGE-----\nHash: SHA256\n\nW2z5HBi8RvsfYzZTS7qBaUxxPhsfHJFZpu3Kd6s1JafNrCCH9rfvPLrfuqocxWPgWDH2R8neK7PkNvjxto9TStuY5z7jAzWRvFWN9cQhAKkdWgy0JY6ywVn22+HFpF4cYesHrqFIKUPDMSSIlWjBVmEJZ/MusD44ZT29xcPrOqeZvwtCffKtGAIjLYPZIEbZKnDM1Dm3q2K/xS5h+xdhjnndhsrkwm9U9oyA2wxzSXFL+pdfj2fOdRwuR5nW0J2NFrq3kJjkRmpO/Genq1UW+TEknIWAb6VzJJJA244K/H8cnSx2+nSNZO3bbo6Ys228ruV9A8m6DhxmS+bihN3ttQ==\n-----BEGIN PGP SIGNATURE-----\nVersion: ProtonMail\nComment: https://protonmail.com\n\nwl4EARYIABAFAlwB1j0JEDUFhcTpUY8mAAD8CgEAnsFnF4cF0uSHKkXa1GIa\nGO86yMV4zDZEZcDSJo0fgr8A/AlupGN9EdHlsrZLmTA1vhIx+rOgxdEff28N\nkvNM7qIK\n=q6vu\n-----END PGP SIGNATURE-----"
)

// srpAuthInfoResponse returns a JSON response body for POST /core/v4/auth/info
// using the SRP test vectors. The response matches the proton.AuthInfo struct.
func srpAuthInfoResponse() map[string]any {
	return map[string]any{
		"Code":            1000,
		"Version":         4,
		"Modulus":         srpTestModulus,
		"ServerEphemeral": srpTestEphemeral,
		"Salt":            srpTestSalt,
		"SRPSession":      "test-srp-session-id",
	}
}

// srpAuthResponse returns a JSON response body for POST /core/v4/auth with
// the given base64-encoded ServerProof. Other fields are plausible defaults.
func srpAuthResponse(serverProofB64 string) map[string]any {
	return map[string]any{
		"Code":         1000,
		"UserID":       "user-123",
		"UID":          "uid-456",
		"AccessToken":  "acc-tok",
		"RefreshToken": "ref-tok",
		"ServerProof":  serverProofB64,
		"Scope":        "full",
		"PasswordMode": 1,
	}
}

// TestPropertyServerProofMismatchRejected verifies that for any byte slice
// used as ServerProof in the auth response, if it does not equal the
// ExpectedServerProof computed by go-srp, CookieSRPAuth returns an error
// containing "server proof mismatch".
//
// The test uses real SRP parameters (from go-srp test vectors) so that
// srp.NewAuth + GenerateProofs succeed inside CookieSRPAuth. The mock server
// returns the arbitrary ServerProof in the auth response. Since the client
// ephemeral is random each call, the ExpectedServerProof is unpredictable —
// but any fixed arbitrary byte slice has negligible probability (1/2^2048) of
// matching it.
//
// **Validates: Requirements 1.4, 2.1**
func TestPropertyServerProofMismatchRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: SRP key derivation is expensive (~3 s)")
	}

	rapid.Check(t, func(t *rapid.T) {
		// Generate arbitrary bytes for the wrong ServerProof.
		// Use 1–512 bytes to cover various lengths including the correct
		// length (256 bytes for 2048-bit SRP). The content is random, so
		// collision with the actual ExpectedServerProof is negligible.
		wrongProofBytes := rapid.SliceOfN(rapid.Byte(), 1, 512).Draw(t, "wrongServerProof")
		wrongProofB64 := base64.StdEncoding.EncodeToString(wrongProofBytes)

		// Set up a test server that serves both auth/info and auth endpoints.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")

			switch r.URL.Path {
			case "/core/v4/auth/info":
				_ = json.NewEncoder(w).Encode(srpAuthInfoResponse())

			case "/core/v4/auth":
				// Consume the request body to avoid broken pipe.
				_, _ = io.ReadAll(r.Body)
				_ = json.NewEncoder(w).Encode(srpAuthResponse(wrongProofB64))

			default:
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"Code":  404,
					"Error": "not found",
				})
			}
		}))
		defer srv.Close()

		// Build a CookieSession pointing at the test server.
		jar, _ := cookiejar.New(nil)
		cs := &CookieSession{
			UID:       "test-uid",
			BaseURL:   srv.URL,
			cookieJar: jar,
		}

		// Call CookieSRPAuth — it should fail with server proof mismatch.
		_, err := CookieSRPAuth(
			context.Background(),
			cs,
			srpTestUsername,
			[]byte(srpTestPassword),
		)

		if err == nil {
			t.Fatal("expected error for mismatched server proof, got nil")
		}
		if !strings.Contains(err.Error(), "server proof mismatch") {
			t.Fatalf("expected error containing 'server proof mismatch', got: %v", err)
		}
	})
}

// errReader is an io.Reader that always returns an error. Used to make
// srp.RandReader fail so that GenerateProofs returns an error.
type errReader struct{ err error }

func (r *errReader) Read([]byte) (int, error) { return 0, r.err }

// TestPropertyErrorWrappingPreservesContext verifies Property 2: for each of
// the four fallible steps in CookieSRPAuth, the returned error contains the
// step-specific prefix and the original error is recoverable via errors.Unwrap.
//
// Steps 1 and 4 use rapid to generate arbitrary error messages injected via
// the mock HTTP server. Steps 2 and 3 trigger real errors from go-srp by
// providing invalid parameters or a failing random reader.
//
// **Validates: Requirements 6.1, 6.2, 6.3, 6.4**
func TestPropertyErrorWrappingPreservesContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: SRP key derivation is expensive (~5 s)")
	}

	// Step 1: auth/info DoJSON error — arbitrary error message from server.
	// Req 6.1: prefix "cookie srp: auth info:"
	t.Run("step1_auth_info", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			msg := rapid.StringMatching(`[a-zA-Z0-9 _-]{1,64}`).Draw(t, "errMsg")

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"Code":  2000,
					"Error": msg,
				})
			}))
			defer srv.Close()

			jar, _ := cookiejar.New(nil)
			cs := &CookieSession{UID: "test-uid", BaseURL: srv.URL, cookieJar: jar}

			_, err := CookieSRPAuth(context.Background(), cs, srpTestUsername, []byte(srpTestPassword))

			if err == nil {
				t.Fatal("expected error, got nil")
			}
			const prefix = "cookie srp: auth info:"
			if !strings.Contains(err.Error(), prefix) {
				t.Fatalf("error %q does not contain prefix %q", err.Error(), prefix)
			}
			if errors.Unwrap(err) == nil {
				t.Fatalf("errors.Unwrap returned nil; original error not recoverable")
			}
		})
	})

	// Step 2: srp.NewAuth error — server returns an invalid (unsigned) modulus
	// so that NewAuth fails with a signature error.
	// Req 6.2: prefix "cookie srp: new auth:"
	t.Run("step2_new_auth", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			// Any non-empty string that is not a valid PGP signed message will
			// cause srp.NewAuth to fail. We use a fixed invalid value here
			// because the property is about wrapping, not about the specific
			// error message from go-srp.
			badModulus := rapid.StringMatching(`[a-zA-Z0-9]{8,32}`).Draw(t, "badModulus")

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				resp := srpAuthInfoResponse()
				resp["Modulus"] = badModulus // invalid: not a PGP signed message
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer srv.Close()

			jar, _ := cookiejar.New(nil)
			cs := &CookieSession{UID: "test-uid", BaseURL: srv.URL, cookieJar: jar}

			_, err := CookieSRPAuth(context.Background(), cs, srpTestUsername, []byte(srpTestPassword))

			if err == nil {
				t.Fatal("expected error, got nil")
			}
			const prefix = "cookie srp: new auth:"
			if !strings.Contains(err.Error(), prefix) {
				t.Fatalf("error %q does not contain prefix %q", err.Error(), prefix)
			}
			if errors.Unwrap(err) == nil {
				t.Fatalf("errors.Unwrap returned nil; original error not recoverable")
			}
		})
	})

	// Step 3: GenerateProofs error — replace srp.RandReader with a failing
	// reader so that generateClientEphemeral returns an error. The auth/info
	// response uses valid SRP parameters so that NewAuth succeeds.
	// Req 6.3: prefix "cookie srp: generate proofs:"
	t.Run("step3_generate_proofs", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			randErrMsg := rapid.StringMatching(`[a-zA-Z0-9 ]{1,32}`).Draw(t, "randErrMsg")
			randErr := errors.New(randErrMsg)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(srpAuthInfoResponse())
			}))
			defer srv.Close()

			// Replace the global random reader with one that always fails.
			// Restore it after the test to avoid polluting other tests.
			origReader := srp.RandReader
			srp.RandReader = &errReader{err: randErr}
			defer func() { srp.RandReader = origReader }()

			jar, _ := cookiejar.New(nil)
			cs := &CookieSession{UID: "test-uid", BaseURL: srv.URL, cookieJar: jar}

			_, err := CookieSRPAuth(context.Background(), cs, srpTestUsername, []byte(srpTestPassword))

			if err == nil {
				t.Fatal("expected error, got nil")
			}
			const prefix = "cookie srp: generate proofs:"
			if !strings.Contains(err.Error(), prefix) {
				t.Fatalf("error %q does not contain prefix %q", err.Error(), prefix)
			}
			if errors.Unwrap(err) == nil {
				t.Fatalf("errors.Unwrap returned nil; original error not recoverable")
			}
		})
	})

	// Step 4: auth POST DoJSON error — server returns success for auth/info
	// but an error for the auth POST. Uses rapid to generate arbitrary error
	// messages.
	// Req 6.4: prefix "cookie srp: auth:"
	t.Run("step4_auth", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			msg := rapid.StringMatching(`[a-zA-Z0-9 _-]{1,64}`).Draw(t, "errMsg")

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/core/v4/auth/info":
					_ = json.NewEncoder(w).Encode(srpAuthInfoResponse())
				case "/core/v4/auth":
					_, _ = io.ReadAll(r.Body)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"Code":  2000,
						"Error": msg,
					})
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()

			jar, _ := cookiejar.New(nil)
			cs := &CookieSession{UID: "test-uid", BaseURL: srv.URL, cookieJar: jar}

			_, err := CookieSRPAuth(context.Background(), cs, srpTestUsername, []byte(srpTestPassword))

			if err == nil {
				t.Fatal("expected error, got nil")
			}
			const prefix = "cookie srp: auth:"
			if !strings.Contains(err.Error(), prefix) {
				t.Fatalf("error %q does not contain prefix %q", err.Error(), prefix)
			}
			if errors.Unwrap(err) == nil {
				t.Fatalf("errors.Unwrap returned nil; original error not recoverable")
			}
		})
	})
}

// TestCookieSRPAuth_Success performs a full SRP exchange using go-srp on both
// client and server sides. The mock HTTP server uses srp.Server to compute a
// real ServerProof from the client's ephemeral and proof, so the verification
// in CookieSRPAuth succeeds. Verifies:
//   - auth/info request body contains Username
//   - auth request body contains Username, ClientEphemeral, ClientProof, SRPSession
//   - returned proton.Auth has expected fields (UserID, UID, tokens, PasswordMode)
func TestCookieSRPAuth_Success(t *testing.T) {
	// Use real randomness for server-side SRP (restore after test).
	origReader := srp.RandReader
	srp.RandReader = pmrand.Reader
	defer func() { srp.RandReader = origReader }()

	const bits = 2048

	// Generate a verifier from the test password, matching the test vectors.
	rawSalt, err := base64.StdEncoding.DecodeString(srpTestSalt)
	if err != nil {
		t.Fatalf("decode salt: %v", err)
	}
	verifierAuth, err := srp.NewAuthForVerifier([]byte(srpTestPassword), srpTestModulus, rawSalt)
	if err != nil {
		t.Fatalf("NewAuthForVerifier: %v", err)
	}
	verifier, err := verifierAuth.GenerateVerifier(bits)
	if err != nil {
		t.Fatalf("GenerateVerifier: %v", err)
	}

	// Create a server-side SRP instance and generate the challenge.
	server, err := srp.NewServerFromSigned(srpTestModulus, verifier, bits)
	if err != nil {
		t.Fatalf("NewServerFromSigned: %v", err)
	}
	challenge, err := server.GenerateChallenge()
	if err != nil {
		t.Fatalf("GenerateChallenge: %v", err)
	}
	serverEphemeralB64 := base64.StdEncoding.EncodeToString(challenge)

	// Track request bodies for verification.
	var capturedInfoBody authInfoReq
	var capturedAuthBody srpAuthReq

	const (
		wantUserID       = "user-success-123"
		wantUID          = "uid-success-456"
		wantAccessToken  = "acc-success"
		wantRefreshToken = "ref-success"
		wantScope        = "full"
		wantPasswordMode = 1
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/core/v4/auth/info":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &capturedInfoBody)

			resp := map[string]any{
				"Code":            1000,
				"Version":         4,
				"Modulus":         srpTestModulus,
				"ServerEphemeral": serverEphemeralB64,
				"Salt":            srpTestSalt,
				"SRPSession":      "test-srp-session-id",
			}
			_ = json.NewEncoder(w).Encode(resp)

		case "/core/v4/auth":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &capturedAuthBody)

			// Decode client ephemeral and proof from the request.
			clientEph, err := base64.StdEncoding.DecodeString(capturedAuthBody.ClientEphemeral)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"Code": 2000, "Error": "bad client ephemeral"})
				return
			}
			clientProof, err := base64.StdEncoding.DecodeString(capturedAuthBody.ClientProof)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"Code": 2000, "Error": "bad client proof"})
				return
			}

			// Compute the real server proof using go-srp's server.
			serverProof, err := server.VerifyProofs(clientEph, clientProof)
			if err != nil {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{"Code": 2000, "Error": "verify proofs: " + err.Error()})
				return
			}

			resp := map[string]any{
				"Code":         1000,
				"UserID":       wantUserID,
				"UID":          wantUID,
				"AccessToken":  wantAccessToken,
				"RefreshToken": wantRefreshToken,
				"ServerProof":  base64.StdEncoding.EncodeToString(serverProof),
				"Scope":        wantScope,
				"PasswordMode": wantPasswordMode,
			}
			_ = json.NewEncoder(w).Encode(resp)

		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 404, "Error": "not found"})
		}
	}))
	defer srv.Close()

	jar, _ := cookiejar.New(nil)
	cs := &CookieSession{
		UID:       "test-uid",
		BaseURL:   srv.URL,
		cookieJar: jar,
	}

	auth, err := CookieSRPAuth(context.Background(), cs, srpTestUsername, []byte(srpTestPassword))
	if err != nil {
		t.Fatalf("CookieSRPAuth returned unexpected error: %v", err)
	}

	// Verify auth/info request body (Req 7.1).
	if capturedInfoBody.Username != srpTestUsername {
		t.Errorf("auth/info Username = %q, want %q", capturedInfoBody.Username, srpTestUsername)
	}

	// Verify auth request body (Req 7.2).
	if capturedAuthBody.Username != srpTestUsername {
		t.Errorf("auth Username = %q, want %q", capturedAuthBody.Username, srpTestUsername)
	}
	if capturedAuthBody.ClientEphemeral == "" {
		t.Error("auth ClientEphemeral is empty")
	}
	if capturedAuthBody.ClientProof == "" {
		t.Error("auth ClientProof is empty")
	}
	if capturedAuthBody.SRPSession != "test-srp-session-id" {
		t.Errorf("auth SRPSession = %q, want %q", capturedAuthBody.SRPSession, "test-srp-session-id")
	}

	// Verify returned Auth fields (Req 1.5).
	if auth.UserID != wantUserID {
		t.Errorf("Auth.UserID = %q, want %q", auth.UserID, wantUserID)
	}
	if auth.UID != wantUID {
		t.Errorf("Auth.UID = %q, want %q", auth.UID, wantUID)
	}
	if auth.AccessToken != wantAccessToken {
		t.Errorf("Auth.AccessToken = %q, want %q", auth.AccessToken, wantAccessToken)
	}
	if auth.RefreshToken != wantRefreshToken {
		t.Errorf("Auth.RefreshToken = %q, want %q", auth.RefreshToken, wantRefreshToken)
	}
	if auth.Scope != wantScope {
		t.Errorf("Auth.Scope = %q, want %q", auth.Scope, wantScope)
	}
	if int(auth.PasswordMode) != wantPasswordMode {
		t.Errorf("Auth.PasswordMode = %d, want %d", auth.PasswordMode, wantPasswordMode)
	}
}

// TestCookieSRPAuth_ServerProofMismatch is a deterministic unit test verifying
// that CookieSRPAuth rejects a specific wrong ServerProof. Unlike the property
// test, this uses a fixed wrong proof value.
func TestCookieSRPAuth_ServerProofMismatch(t *testing.T) {
	// A fixed wrong server proof — 32 zero bytes, base64-encoded.
	wrongProof := base64.StdEncoding.EncodeToString(make([]byte, 32))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/core/v4/auth/info":
			_ = json.NewEncoder(w).Encode(srpAuthInfoResponse())
		case "/core/v4/auth":
			_, _ = io.ReadAll(r.Body)
			_ = json.NewEncoder(w).Encode(srpAuthResponse(wrongProof))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	jar, _ := cookiejar.New(nil)
	cs := &CookieSession{
		UID:       "test-uid",
		BaseURL:   srv.URL,
		cookieJar: jar,
	}

	_, err := CookieSRPAuth(context.Background(), cs, srpTestUsername, []byte(srpTestPassword))
	if err == nil {
		t.Fatal("expected error for mismatched server proof, got nil")
	}
	if !strings.Contains(err.Error(), "server proof mismatch") {
		t.Fatalf("expected error containing 'server proof mismatch', got: %v", err)
	}
}

// TestCookieSRPAuth_AuthInfoError verifies that when the auth/info endpoint
// returns an API error, CookieSRPAuth wraps it with the "cookie srp: auth info:"
// prefix and the original error is recoverable via errors.Unwrap.
func TestCookieSRPAuth_AuthInfoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Code":  2000,
			"Error": "username not found",
		})
	}))
	defer srv.Close()

	jar, _ := cookiejar.New(nil)
	cs := &CookieSession{
		UID:       "test-uid",
		BaseURL:   srv.URL,
		cookieJar: jar,
	}

	_, err := CookieSRPAuth(context.Background(), cs, "nonexistent", []byte("password"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	const wantPrefix = "cookie srp: auth info:"
	if !strings.Contains(err.Error(), wantPrefix) {
		t.Fatalf("error %q does not contain prefix %q", err.Error(), wantPrefix)
	}
	if errors.Unwrap(err) == nil {
		t.Fatal("errors.Unwrap returned nil; original error not recoverable")
	}
}

// TestCookieTwoFA_Success verifies that CookieTwoFA sends a POST to
// /core/v4/auth/2fa with a JSON body containing the TwoFactorCode field,
// and returns nil on a successful (Code 1000) response.
//
// Validates: Requirements 4.1, 7.3
func TestCookieTwoFA_Success(t *testing.T) {
	const wantCode = "123456"

	var capturedBody twoFAReq

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path != "/core/v4/auth/2fa" {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 404, "Error": "not found"})
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 405, "Error": "method not allowed"})
			return
		}

		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"Code":  1000,
			"Scope": "full",
		})
	}))
	defer srv.Close()

	jar, _ := cookiejar.New(nil)
	cs := &CookieSession{
		UID:       "test-uid",
		BaseURL:   srv.URL,
		cookieJar: jar,
	}

	err := CookieTwoFA(context.Background(), cs, wantCode)
	if err != nil {
		t.Fatalf("CookieTwoFA returned unexpected error: %v", err)
	}

	if capturedBody.TwoFactorCode != wantCode {
		t.Errorf("TwoFactorCode = %q, want %q", capturedBody.TwoFactorCode, wantCode)
	}
}

// TestCookieTwoFA_Error verifies that when the /core/v4/auth/2fa endpoint
// returns an API error, CookieTwoFA propagates it as a non-nil error.
//
// Validates: Requirements 4.2
func TestCookieTwoFA_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Code":  2000,
			"Error": "invalid 2fa code",
		})
	}))
	defer srv.Close()

	jar, _ := cookiejar.New(nil)
	cs := &CookieSession{
		UID:       "test-uid",
		BaseURL:   srv.URL,
		cookieJar: jar,
	}

	err := CookieTwoFA(context.Background(), cs, "000000")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
