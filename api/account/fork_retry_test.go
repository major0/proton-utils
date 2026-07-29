package account

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	proton "github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api"
)

// --- Task 1: bug condition exploration tests (run against UNFIXED code) ---
//
// These tests confirm the gap: neither the Bearer fork push
// (ForkSessionWithKeyPass) nor the cookie fork push (CookieFork) self-heals on
// a stale-credential 401. On the unfixed code the push returns the 401-derived
// error with no refresh and no retry, so these assertions describe the
// DEFECTIVE behavior and PASS while the defect is present. The fix-checking
// cases in task 3.3 encode the expected (recovered) behavior.
//
// Bug_Condition (from design): isBugCondition(input) =
//   authStyle IN {Bearer, CookieFork}
//   AND forkPushResult IS (or wraps) *api.Error
//   AND forkPushResult.Status == 401

// TestForkBearer_BugCondition_No401Recovery confirms the Bearer fork push does
// not refresh-and-retry on a stale-access-token 401: ForkSessionWithKeyPass
// returns an error wrapping ErrForkFailed around *api.Error{Status: 401}, the
// account refresh endpoint is never hit, and the push is attempted exactly
// once (no retry).
//
// Validates: Requirements 1.1, 1.2
func TestForkBearer_BugCondition_No401Recovery(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	var pushCount, refreshCount int32
	acctSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth/v4/sessions/forks":
			// Stale access token: the push draws a 401.
			atomic.AddInt32(&pushCount, 1)
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 401, "Error": "Invalid access token"})
		case "/auth/v4/refresh":
			// The unfixed path must never reach here.
			atomic.AddInt32(&refreshCount, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Code": 1000, "UID": "acct-uid", "AccessToken": "new-access", "RefreshToken": "new-refresh",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 404, "Error": "not found"})
		}
	}))
	defer acctSrv.Close()

	jar, _ := cookiejar.New(nil)
	acctSession := &api.Session{
		Auth: proton.Auth{
			UID:          "acct-uid",
			AccessToken:  "stale-access",
			RefreshToken: "stale-refresh",
		},
		BaseURL: acctSrv.URL,
	}
	acctSession.SetCookieJar(jar)

	svc := api.ServiceConfig{
		Name:     "drive",
		Host:     acctSrv.URL, // pull host; unreached because the push fails first
		ClientID: "web-drive",
	}

	child, keypass, err := ForkSessionWithKeyPass(context.Background(), acctSession, svc, "", []byte("test-keypass"))
	if err == nil {
		t.Fatal("expected fork to fail on stale-token 401, got nil error")
	}
	if child != nil || keypass != nil {
		t.Fatalf("expected nil child/keypass on failure, got child=%v keypass=%v", child, keypass)
	}

	// The push wraps its failure as ErrForkFailed.
	if !errors.Is(err, ErrForkFailed) {
		t.Fatalf("error does not wrap ErrForkFailed: %v", err)
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error does not wrap *api.Error: %T: %v", err, err)
	}
	if apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("wrapped *api.Error.Status = %d, want 401", apiErr.Status)
	}

	// The defect: no refresh, no retry.
	if got := atomic.LoadInt32(&refreshCount); got != 0 {
		t.Fatalf("refresh count = %d, want 0 (unfixed code must not refresh)", got)
	}
	if got := atomic.LoadInt32(&pushCount); got != 1 {
		t.Fatalf("push count = %d, want 1 (unfixed code must not retry)", got)
	}

	// Counterexample record.
	t.Logf("Bearer bug counterexample: err=%q refreshCount=%d pushCount=%d",
		err.Error(), atomic.LoadInt32(&refreshCount), atomic.LoadInt32(&pushCount))
}

// TestForkCookie_BugCondition_No401Recovery confirms the cookie fork push does
// not refresh-and-retry on a stale-cookie 401: CookieFork returns a bare
// *api.Error{Status: 401} (not ErrForkFailed-wrapped), the cookie refresh
// endpoint is never hit, and the push is attempted exactly once (no retry).
//
// Validates: Requirements 1.3, 1.4
func TestForkCookie_BugCondition_No401Recovery(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	const uid = "cookie-uid"

	var pushCount, refreshCount int32
	acctSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth/v4/sessions/forks":
			// Stale cookies: the push draws a 401.
			atomic.AddInt32(&pushCount, 1)
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 401, "Error": "Invalid session"})
		case "/auth/refresh":
			// The unfixed path must never reach here.
			atomic.AddInt32(&refreshCount, 1)
			http.SetCookie(w, &http.Cookie{Name: "AUTH-" + uid, Value: "new-auth", Path: "/"})       //nolint:gosec // G124: test cookie
			http.SetCookie(w, &http.Cookie{Name: "REFRESH-" + uid, Value: "new-refresh", Path: "/"}) //nolint:gosec // G124: test cookie
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 404, "Error": "not found"})
		}
	}))
	defer acctSrv.Close()

	// Point the account service host (used by loadOrCreateCookieSession and the
	// cookie push URL) at the test server.
	withAccountHost(t, acctSrv.URL)

	// Preload the cookie store with a CookieAuth session whose LastRefresh is
	// newer than the account config, so loadOrCreateCookieSession restores it
	// rather than re-creating (which would require a live transition).
	cookieStore := &trackingStore{creds: cookieCreds(uid, "stale-auth", "stale-refresh", time.Now())}

	jar, _ := cookiejar.New(nil)
	acctSession := &api.Session{Auth: proton.Auth{UID: uid}}
	acctSession.SetCookieJar(jar)
	acctConfig := &api.SessionCredentials{
		UID:         uid,
		CookieAuth:  true,
		LastRefresh: time.Now().Add(-2 * time.Hour),
	}

	svc := api.ServiceConfig{
		Name:     "lumo",
		Host:     acctSrv.URL, // pull host; unreached because the push fails first
		ClientID: "web-lumo",
	}

	child, keypass, err := CookieFork(context.Background(), acctSession, acctConfig, svc, "", []byte("test-keypass"), cookieStore)
	if err == nil {
		t.Fatal("expected cookie fork to fail on stale-cookie 401, got nil error")
	}
	if child != nil || keypass != nil {
		t.Fatalf("expected nil child/keypass on failure, got child=%v keypass=%v", child, keypass)
	}

	// The cookie push returns a BARE *api.Error (not ErrForkFailed-wrapped).
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not *api.Error: %T: %v", err, err)
	}
	if apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("*api.Error.Status = %d, want 401", apiErr.Status)
	}
	if errors.Is(err, ErrForkFailed) {
		t.Fatalf("cookie push envelope error should be a bare *api.Error, not ErrForkFailed-wrapped: %v", err)
	}

	// The defect: no cookie refresh, no retry.
	if got := atomic.LoadInt32(&refreshCount); got != 0 {
		t.Fatalf("cookie refresh count = %d, want 0 (unfixed code must not refresh)", got)
	}
	if got := atomic.LoadInt32(&pushCount); got != 1 {
		t.Fatalf("push count = %d, want 1 (unfixed code must not retry)", got)
	}

	// Counterexample record.
	t.Logf("Cookie bug counterexample: err=%q refreshCount=%d pushCount=%d",
		err.Error(), atomic.LoadInt32(&refreshCount), atomic.LoadInt32(&pushCount))
}

// --- Task 2: preservation baseline tests (run against UNFIXED code) ---
//
// These tests record the behavior that the fix MUST preserve for inputs where
// isBugCondition returns false (a fork push that succeeds, or fails with a
// non-401 error). They observe the UNFIXED code so the baseline is captured
// before the wrapper is introduced; the fix re-runs them unchanged (task 3.5)
// to prove no regression.
//
// Preservation (from design): success passthrough with zero refresh/retry;
// non-401 propagates unchanged (Bearer keeps its ErrForkFailed wrap, CookieFork
// keeps its bare *api.Error); diagnostic logs carry only UID/cookie names,
// never tokens or cookie values (Req 3.1-3.5).

// forkPayloadRelay is a mutex-guarded holder for the fork push Payload. The
// push handler records the ciphertext generated inside the fork call, and the
// pull handler echoes it back so DecryptForkBlob succeeds. The mutex makes the
// cross-goroutine (push handler → pull handler) handoff safe under -race.
type forkPayloadRelay struct {
	mu sync.Mutex
	v  string
}

func (p *forkPayloadRelay) set(s string) {
	p.mu.Lock()
	p.v = s
	p.mu.Unlock()
}

func (p *forkPayloadRelay) get() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.v
}

// captureForkDebugLogs redirects the default slog logger to a buffer at Debug
// level for the duration of the test, restoring the previous logger on
// cleanup. It lets the preservation tests assert that no sensitive value is
// emitted at any log level (Req 3.5).
func captureForkDebugLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// assertNoForkSecretsLogged fails the test if any of the given sensitive
// values appears in the captured log output (Req 3.5).
func assertNoForkSecretsLogged(t *testing.T, logs string, secrets ...string) {
	t.Helper()
	for _, s := range secrets {
		if s != "" && strings.Contains(logs, s) {
			t.Errorf("diagnostic logs leaked a sensitive value %q", s)
		}
	}
}

// TestForkBearer_Preservation_SuccessFirstAttempt observes the Bearer fork
// success path on unfixed code: a push that returns envelope Code 1000 yields
// the child session on the first attempt, with zero refreshes and a single
// push. The pull handler echoes the recorded push Payload so the fork blob
// decrypts and the child is built.
//
// Validates: Requirements 3.1, 3.5
func TestForkBearer_Preservation_SuccessFirstAttempt(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	logs := captureForkDebugLogs(t)

	const (
		childUID     = "child-uid-bearer"
		childAT      = "child-access-bearer"
		childRT      = "child-refresh-bearer"
		staleAccess  = "stale-access-secret"
		staleRefresh = "stale-refresh-secret"
	)
	var pushCount, refreshCount int32
	var payload forkPayloadRelay

	// A single server handles push (account) and pull (target): the fork uses
	// parent.BaseURL for the push and targetService.Host for the pull, both
	// pointed here. The refresh endpoint counts calls (must stay 0).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/auth/v4/sessions/forks":
			atomic.AddInt32(&pushCount, 1)
			var req ForkPushReq
			_ = json.NewDecoder(r.Body).Decode(&req)
			payload.set(req.Payload)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000, "Selector": "sel-bearer"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/auth/v4/sessions/forks/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Code": 1000, "UID": childUID, "AccessToken": childAT, "RefreshToken": childRT,
				"Payload": payload.get(),
			})
		case r.URL.Path == "/auth/v4/refresh":
			atomic.AddInt32(&refreshCount, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Code": 1000, "UID": "acct-uid", "AccessToken": "unused", "RefreshToken": "unused",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 404, "Error": "not found"})
		}
	}))
	defer srv.Close()

	jar, _ := cookiejar.New(nil)
	acctSession := &api.Session{
		Auth:    proton.Auth{UID: "acct-uid", AccessToken: staleAccess, RefreshToken: staleRefresh},
		BaseURL: srv.URL,
	}
	acctSession.SetCookieJar(jar)

	svc := api.ServiceConfig{Name: "drive", Host: srv.URL, ClientID: "web-drive"}

	child, keypass, err := ForkSessionWithKeyPass(context.Background(), acctSession, svc, "", []byte("test-keypass"))
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if child == nil {
		t.Fatal("expected non-nil child session")
	}
	defer child.Stop()

	if string(keypass) != "test-keypass" {
		t.Fatalf("keypass = %q, want %q", keypass, "test-keypass")
	}
	if child.Auth.UID != childUID {
		t.Fatalf("child UID = %q, want %q", child.Auth.UID, childUID)
	}
	if got := atomic.LoadInt32(&pushCount); got != 1 {
		t.Fatalf("push count = %d, want 1 (single attempt)", got)
	}
	if got := atomic.LoadInt32(&refreshCount); got != 0 {
		t.Fatalf("refresh count = %d, want 0 (success path must not refresh)", got)
	}

	assertNoForkSecretsLogged(t, logs.String(), staleAccess, staleRefresh)
}

// TestForkBearer_Preservation_Non401Propagates observes the Bearer non-401
// error path on unfixed code: a push failing with HTTP 403 (Code 9100)
// propagates the error unchanged, wrapping ErrForkFailed around
// *api.Error{Status: 403}, with zero refreshes and a single push.
//
// Validates: Requirements 3.2
func TestForkBearer_Preservation_Non401Propagates(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	var pushCount, refreshCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth/v4/sessions/forks":
			atomic.AddInt32(&pushCount, 1)
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 9100, "Error": "insufficient scope"})
		case "/auth/v4/refresh":
			atomic.AddInt32(&refreshCount, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 404, "Error": "not found"})
		}
	}))
	defer srv.Close()

	jar, _ := cookiejar.New(nil)
	acctSession := &api.Session{
		Auth:    proton.Auth{UID: "acct-uid", AccessToken: "acc", RefreshToken: "ref"},
		BaseURL: srv.URL,
	}
	acctSession.SetCookieJar(jar)

	svc := api.ServiceConfig{Name: "drive", Host: srv.URL, ClientID: "web-drive"}

	child, keypass, err := ForkSessionWithKeyPass(context.Background(), acctSession, svc, "", []byte("test-keypass"))
	if err == nil {
		t.Fatal("expected error on non-401 push failure")
	}
	if child != nil || keypass != nil {
		t.Fatalf("expected nil child/keypass, got child=%v keypass=%v", child, keypass)
	}

	if !errors.Is(err, ErrForkFailed) {
		t.Fatalf("error does not wrap ErrForkFailed: %v", err)
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error does not wrap *api.Error: %T: %v", err, err)
	}
	if apiErr.Status != http.StatusForbidden {
		t.Fatalf("*api.Error.Status = %d, want 403", apiErr.Status)
	}

	if got := atomic.LoadInt32(&refreshCount); got != 0 {
		t.Fatalf("refresh count = %d, want 0 (non-401 must not refresh)", got)
	}
	if got := atomic.LoadInt32(&pushCount); got != 1 {
		t.Fatalf("push count = %d, want 1 (non-401 must not retry)", got)
	}
}

// TestForkCookie_Preservation_SuccessFirstAttempt observes the CookieFork
// success path on unfixed code: a push that returns envelope Code 1000 yields
// the child cookie session on the first attempt, with zero cookie refreshes,
// zero store saves (the fresh cookie session is restored, not re-created), and
// a single push. The target server serves the pull (echoing the recorded push
// Payload) and the child cookie transition.
//
// Validates: Requirements 3.3, 3.5
func TestForkCookie_Preservation_SuccessFirstAttempt(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	logs := captureForkDebugLogs(t)

	const (
		uid                = "cookie-uid"
		childUID           = "child-uid-cookie"
		staleAuthCookie    = "stale-auth-cookie-secret"
		staleRefreshCookie = "stale-refresh-cookie-secret"
		childAuthCookie    = "child-auth-cookie-secret"
		childRefreshCookie = "child-refresh-cookie-secret"
	)
	var payload forkPayloadRelay
	var pushCount, refreshCount int32

	// Target server: fork pull + child cookie transition.
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/auth/v4/sessions/forks/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Code": 1000, "UID": childUID, "AccessToken": "child-at", "RefreshToken": "child-rt",
				"Payload": payload.get(),
			})
		case r.Method == http.MethodPost && r.URL.Path == "/core/v4/auth/cookies":
			http.SetCookie(w, &http.Cookie{Name: "AUTH-" + childUID, Value: childAuthCookie, Path: "/"})       //nolint:gosec // G124: test cookie
			http.SetCookie(w, &http.Cookie{Name: "REFRESH-" + childUID, Value: childRefreshCookie, Path: "/"}) //nolint:gosec // G124: test cookie
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 404, "Error": "not found"})
		}
	}))
	defer targetSrv.Close()

	// Account server: cookie fork push + cookie refresh (must stay unused).
	acctSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth/v4/sessions/forks":
			atomic.AddInt32(&pushCount, 1)
			var req ForkPushReq
			_ = json.NewDecoder(r.Body).Decode(&req)
			payload.set(req.Payload)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000, "Selector": "sel-cookie"})
		case "/auth/refresh":
			atomic.AddInt32(&refreshCount, 1)
			http.SetCookie(w, &http.Cookie{Name: "AUTH-" + uid, Value: "rotated-auth", Path: "/"})       //nolint:gosec // G124: test cookie
			http.SetCookie(w, &http.Cookie{Name: "REFRESH-" + uid, Value: "rotated-refresh", Path: "/"}) //nolint:gosec // G124: test cookie
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 404, "Error": "not found"})
		}
	}))
	defer acctSrv.Close()

	// Point loadOrCreateCookieSession and the cookie push URL at the account
	// server.
	withAccountHost(t, acctSrv.URL)

	// Preloaded cookie session, fresh (LastRefresh newer than acctConfig) so it
	// is restored rather than re-created.
	cookieStore := &trackingStore{creds: cookieCreds(uid, staleAuthCookie, staleRefreshCookie, time.Now())}

	jar, _ := cookiejar.New(nil)
	acctSession := &api.Session{Auth: proton.Auth{UID: uid}}
	acctSession.SetCookieJar(jar)
	acctConfig := &api.SessionCredentials{
		UID:         uid,
		CookieAuth:  true,
		LastRefresh: time.Now().Add(-2 * time.Hour),
	}

	svc := api.ServiceConfig{Name: "lumo", Host: targetSrv.URL, ClientID: "web-lumo"}

	child, keypass, err := CookieFork(context.Background(), acctSession, acctConfig, svc, "", []byte("test-keypass"), cookieStore)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if child == nil {
		t.Fatal("expected non-nil child cookie session")
	}
	defer child.Stop()

	if string(keypass) != "test-keypass" {
		t.Fatalf("keypass = %q, want %q", keypass, "test-keypass")
	}
	if got := atomic.LoadInt32(&pushCount); got != 1 {
		t.Fatalf("push count = %d, want 1 (single attempt)", got)
	}
	if got := atomic.LoadInt32(&refreshCount); got != 0 {
		t.Fatalf("cookie refresh count = %d, want 0 (success path must not refresh)", got)
	}
	if got := cookieStore.saves(); got != 0 {
		t.Fatalf("cookie store saves = %d, want 0 (restore path must not persist)", got)
	}

	assertNoForkSecretsLogged(t, logs.String(), staleAuthCookie, staleRefreshCookie, childAuthCookie, childRefreshCookie)
}

// TestForkCookie_Preservation_Non401Propagates observes the CookieFork non-401
// error path on unfixed code: a push failing with HTTP 403 (Code 9100)
// propagates the BARE *api.Error (Status == 403, not ErrForkFailed-wrapped)
// unchanged, with zero cookie refreshes, zero store saves, and a single push.
//
// Validates: Requirements 3.4
func TestForkCookie_Preservation_Non401Propagates(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	const uid = "cookie-uid"
	var pushCount, refreshCount int32
	acctSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth/v4/sessions/forks":
			atomic.AddInt32(&pushCount, 1)
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 9100, "Error": "insufficient scope"})
		case "/auth/refresh":
			atomic.AddInt32(&refreshCount, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 404, "Error": "not found"})
		}
	}))
	defer acctSrv.Close()

	withAccountHost(t, acctSrv.URL)

	cookieStore := &trackingStore{creds: cookieCreds(uid, "stale-auth", "stale-refresh", time.Now())}

	jar, _ := cookiejar.New(nil)
	acctSession := &api.Session{Auth: proton.Auth{UID: uid}}
	acctSession.SetCookieJar(jar)
	acctConfig := &api.SessionCredentials{
		UID:         uid,
		CookieAuth:  true,
		LastRefresh: time.Now().Add(-2 * time.Hour),
	}

	svc := api.ServiceConfig{Name: "lumo", Host: acctSrv.URL, ClientID: "web-lumo"}

	child, keypass, err := CookieFork(context.Background(), acctSession, acctConfig, svc, "", []byte("test-keypass"), cookieStore)
	if err == nil {
		t.Fatal("expected error on non-401 push failure")
	}
	if child != nil || keypass != nil {
		t.Fatalf("expected nil child/keypass, got child=%v keypass=%v", child, keypass)
	}

	// The cookie push returns a BARE *api.Error (not ErrForkFailed-wrapped).
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not *api.Error: %T: %v", err, err)
	}
	if apiErr.Status != http.StatusForbidden {
		t.Fatalf("*api.Error.Status = %d, want 403", apiErr.Status)
	}
	if errors.Is(err, ErrForkFailed) {
		t.Fatalf("cookie push envelope error should be a bare *api.Error, not ErrForkFailed-wrapped: %v", err)
	}

	if got := atomic.LoadInt32(&refreshCount); got != 0 {
		t.Fatalf("cookie refresh count = %d, want 0 (non-401 must not refresh)", got)
	}
	if got := cookieStore.saves(); got != 0 {
		t.Fatalf("cookie store saves = %d, want 0 (non-401 must not persist)", got)
	}
	if got := atomic.LoadInt32(&pushCount); got != 1 {
		t.Fatalf("push count = %d, want 1 (non-401 must not retry)", got)
	}
}

// --- Task 3.3: fix-checking cases (run against the FIXED wrappers) ---
//
// These cases exercise the refresh-and-retry wrappers introduced by the fix
// (forkBearerWithRefreshRetry / forkCookieWithRefreshRetry). They encode the
// expected (recovered) behavior for inputs where isBugCondition is true (a
// stale-credential 401) and confirm the preserved behavior for the non-401
// branch. The first-vs-retry outcome is made deterministic by keying the push
// handler on the rotated credential being presented: the rotated Bearer access
// token for the Bearer style, the rotated AUTH-<uid> cookie for the cookie
// style.

// TestForkBearer_Fix_RefreshRetryOn401 (Case 1): the Bearer fork push draws a
// 401 until the rotated access token is presented. forkBearerWithRefreshRetry
// refreshes the account token under the lock exactly once, applies the rotated
// tokens to the account session (rebuilding its client), retries the fork, and
// the rotated refresh token is persisted to the store.
//
// Validates: Requirements 2.1, 2.2
func TestForkBearer_Fix_RefreshRetryOn401(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	const (
		uid        = "acct-uid"
		oldAccess  = "old-access"
		oldRefresh = "old-refresh"
		newAccess  = "new-access"
		newRefresh = "new-refresh"
		childUID   = "child-uid-bearer"
	)
	var payload forkPayloadRelay
	var pushCount, refreshCount int32

	// Refresh server (manager host): rotates old→new when the stored (old)
	// refresh token is presented.
	refreshSrv := bearerRefreshServer(t, &refreshCount, oldRefresh, newAccess, newRefresh)
	defer refreshSrv.Close()

	// Push+pull server: the push draws a 401 until the rotated access token is
	// presented in the Bearer header, then returns Code 1000; the pull echoes
	// the recorded push Payload so the fork blob decrypts.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/auth/v4/sessions/forks":
			atomic.AddInt32(&pushCount, 1)
			if r.Header.Get("Authorization") != "Bearer "+newAccess {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{"Code": 401, "Error": "Invalid access token"})
				return
			}
			var req ForkPushReq
			_ = json.NewDecoder(r.Body).Decode(&req)
			payload.set(req.Payload)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000, "Selector": "sel-bearer"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/auth/v4/sessions/forks/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Code": 1000, "UID": childUID, "AccessToken": "child-at", "RefreshToken": "child-rt",
				"Payload": payload.get(),
			})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 404, "Error": "not found"})
		}
	}))
	defer srv.Close()

	store := &trackingStore{creds: &api.SessionCredentials{
		UID:          uid,
		AccessToken:  oldAccess,
		RefreshToken: oldRefresh,
		LastRefresh:  time.Now().Add(-2 * time.Hour),
	}}

	jar, _ := cookiejar.New(nil)
	acctSession := &api.Session{
		Auth:    proton.Auth{UID: uid, AccessToken: oldAccess, RefreshToken: oldRefresh},
		BaseURL: srv.URL,
	}
	acctSession.SetCookieJar(jar)
	acctSession.SetManager(bearerManager(t, refreshSrv.URL))

	acctConfig := &api.SessionCredentials{UID: uid, AccessToken: oldAccess, RefreshToken: oldRefresh}
	svc := api.ServiceConfig{Name: "drive", Host: srv.URL, ClientID: "web-drive"}

	child, keypass, err := forkBearerWithRefreshRetry(context.Background(), acctSession, acctConfig, svc, "", []byte("test-keypass"), store)
	if err != nil {
		t.Fatalf("expected success after refresh+retry, got error: %v", err)
	}
	if child == nil {
		t.Fatal("expected non-nil child session")
	}
	defer child.Stop()

	if string(keypass) != "test-keypass" {
		t.Fatalf("keypass = %q, want %q", keypass, "test-keypass")
	}
	if child.Auth.UID != childUID {
		t.Fatalf("child UID = %q, want %q", child.Auth.UID, childUID)
	}
	if got := atomic.LoadInt32(&refreshCount); got != 1 {
		t.Fatalf("refresh count = %d, want 1 (exactly one coordinated refresh)", got)
	}
	if got := atomic.LoadInt32(&pushCount); got != 2 {
		t.Fatalf("push count = %d, want 2 (401 then retry)", got)
	}
	if saved := store.snapshot(); saved == nil || saved.RefreshToken != newRefresh {
		t.Fatalf("stored refresh token = %v, want %q", saved, newRefresh)
	}
	if acctSession.Auth.AccessToken != newAccess {
		t.Fatalf("acctSession access token = %q, want rotated %q", acctSession.Auth.AccessToken, newAccess)
	}
	if acctSession.Client == nil {
		t.Fatal("acctSession.Client is nil, want rebuilt after refresh")
	}
}

// TestForkBearer_Fix_Non401Propagates (Case 2): a Bearer fork push failing
// with a non-401 error (HTTP 403, Code 9100) propagates unchanged, wrapping
// ErrForkFailed, with no refresh and no store save.
//
// Validates: Requirements 3.2
func TestForkBearer_Fix_Non401Propagates(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	var pushCount, refreshCount int32
	refreshSrv := bearerRefreshServer(t, &refreshCount, "unused", "x", "y")
	defer refreshSrv.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth/v4/sessions/forks":
			atomic.AddInt32(&pushCount, 1)
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 9100, "Error": "insufficient scope"})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 404, "Error": "not found"})
		}
	}))
	defer srv.Close()

	store := &trackingStore{creds: &api.SessionCredentials{
		UID: "acct-uid", AccessToken: "acc", RefreshToken: "ref", LastRefresh: time.Now(),
	}}

	jar, _ := cookiejar.New(nil)
	acctSession := &api.Session{
		Auth:    proton.Auth{UID: "acct-uid", AccessToken: "acc", RefreshToken: "ref"},
		BaseURL: srv.URL,
	}
	acctSession.SetCookieJar(jar)
	acctSession.SetManager(bearerManager(t, refreshSrv.URL))

	acctConfig := &api.SessionCredentials{UID: "acct-uid", AccessToken: "acc", RefreshToken: "ref"}
	svc := api.ServiceConfig{Name: "drive", Host: srv.URL, ClientID: "web-drive"}

	child, keypass, err := forkBearerWithRefreshRetry(context.Background(), acctSession, acctConfig, svc, "", []byte("test-keypass"), store)
	if err == nil {
		t.Fatal("expected error on non-401 push failure")
	}
	if child != nil || keypass != nil {
		t.Fatalf("expected nil child/keypass, got child=%v keypass=%v", child, keypass)
	}
	if !errors.Is(err, ErrForkFailed) {
		t.Fatalf("error does not wrap ErrForkFailed: %v", err)
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error does not wrap *api.Error: %T: %v", err, err)
	}
	if apiErr.Status != http.StatusForbidden {
		t.Fatalf("*api.Error.Status = %d, want 403", apiErr.Status)
	}
	if got := atomic.LoadInt32(&refreshCount); got != 0 {
		t.Fatalf("refresh count = %d, want 0 (non-401 must not refresh)", got)
	}
	if store.saves() != 0 {
		t.Fatalf("store saves = %d, want 0 (non-401 must not persist)", store.saves())
	}
}

// TestForkBearer_Fix_DeauthNoRetry (Case 3): a Bearer fork push draws a 401
// but the account is genuinely de-authed — the refresh rejects the presented
// refresh token with 422. forkBearerWithRefreshRetry returns ErrAccountDeauthed
// without retrying the push, and the store is left unchanged.
//
// Validates: Requirements 2.3
func TestForkBearer_Fix_DeauthNoRetry(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	var pushCount, refreshCount int32
	// The caller presents "stale-refresh" but the server only accepts
	// "some-other" — so the presented token draws a 422 (dead credential).
	refreshSrv := bearerRefreshServer(t, &refreshCount, "some-other", "x", "y")
	defer refreshSrv.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth/v4/sessions/forks":
			atomic.AddInt32(&pushCount, 1)
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 401, "Error": "Invalid access token"})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 404, "Error": "not found"})
		}
	}))
	defer srv.Close()

	store := &trackingStore{creds: &api.SessionCredentials{
		UID: "acct-uid", AccessToken: "stale-access", RefreshToken: "stale-refresh", LastRefresh: time.Now(),
	}}

	jar, _ := cookiejar.New(nil)
	acctSession := &api.Session{
		Auth:    proton.Auth{UID: "acct-uid", AccessToken: "stale-access", RefreshToken: "stale-refresh"},
		BaseURL: srv.URL,
	}
	acctSession.SetCookieJar(jar)
	acctSession.SetManager(bearerManager(t, refreshSrv.URL))

	acctConfig := &api.SessionCredentials{UID: "acct-uid", AccessToken: "stale-access", RefreshToken: "stale-refresh"}
	svc := api.ServiceConfig{Name: "drive", Host: srv.URL, ClientID: "web-drive"}

	child, keypass, err := forkBearerWithRefreshRetry(context.Background(), acctSession, acctConfig, svc, "", []byte("test-keypass"), store)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if child != nil || keypass != nil {
		t.Fatalf("expected nil child/keypass, got child=%v keypass=%v", child, keypass)
	}
	if !errors.Is(err, ErrAccountDeauthed) {
		t.Fatalf("error %v does not wrap ErrAccountDeauthed", err)
	}
	if got := atomic.LoadInt32(&pushCount); got != 1 {
		t.Fatalf("push count = %d, want 1 (deauth must not retry)", got)
	}
	if store.saves() != 0 {
		t.Fatalf("store saves = %d, want 0 (deauth must not persist)", store.saves())
	}
}

// TestForkCookie_Fix_RefreshRetryOn401 (Case 4): the cookie fork push draws a
// 401 until the rotated AUTH-<uid> cookie is presented. forkCookieWithRefreshRetry
// refreshes the cookies under the lock exactly once (rotating AUTH-/REFRESH-<uid>
// and persisting them to the cookie store), retries the push, and returns the
// child cookie session.
//
// Validates: Requirements 2.4, 2.5
func TestForkCookie_Fix_RefreshRetryOn401(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	const (
		uid                = "cookie-uid"
		childUID           = "child-uid-cookie"
		staleAuth          = "stale-auth"
		staleRefresh       = "stale-refresh"
		rotatedAuth        = "rotated-auth"
		rotatedRefresh     = "rotated-refresh"
		childAuthCookie    = "child-auth"
		childRefreshCookie = "child-refresh"
	)
	var payload forkPayloadRelay
	var pushCount, refreshCount int32

	// Target server: fork pull (echoing the recorded push Payload) + child
	// cookie transition.
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/auth/v4/sessions/forks/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Code": 1000, "UID": childUID, "AccessToken": "child-at", "RefreshToken": "child-rt",
				"Payload": payload.get(),
			})
		case r.Method == http.MethodPost && r.URL.Path == "/core/v4/auth/cookies":
			http.SetCookie(w, &http.Cookie{Name: "AUTH-" + childUID, Value: childAuthCookie, Path: "/"})       //nolint:gosec // G124: test cookie
			http.SetCookie(w, &http.Cookie{Name: "REFRESH-" + childUID, Value: childRefreshCookie, Path: "/"}) //nolint:gosec // G124: test cookie
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 404, "Error": "not found"})
		}
	}))
	defer targetSrv.Close()

	// Account server: cookie fork push (401 until the rotated AUTH cookie is
	// presented) + cookie refresh (rotates AUTH-/REFRESH-<uid>).
	acctSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth/v4/sessions/forks":
			atomic.AddInt32(&pushCount, 1)
			c, _ := r.Cookie("AUTH-" + uid)
			if c == nil || c.Value != rotatedAuth {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{"Code": 401, "Error": "Invalid session"})
				return
			}
			var req ForkPushReq
			_ = json.NewDecoder(r.Body).Decode(&req)
			payload.set(req.Payload)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000, "Selector": "sel-cookie"})
		case "/auth/refresh":
			atomic.AddInt32(&refreshCount, 1)
			http.SetCookie(w, &http.Cookie{Name: "AUTH-" + uid, Value: rotatedAuth, Path: "/"})       //nolint:gosec // G124: test cookie
			http.SetCookie(w, &http.Cookie{Name: "REFRESH-" + uid, Value: rotatedRefresh, Path: "/"}) //nolint:gosec // G124: test cookie
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 404, "Error": "not found"})
		}
	}))
	defer acctSrv.Close()

	withAccountHost(t, acctSrv.URL)

	cookieStore := &trackingStore{creds: cookieCreds(uid, staleAuth, staleRefresh, time.Now())}

	jar, _ := cookiejar.New(nil)
	acctSession := &api.Session{Auth: proton.Auth{UID: uid}}
	acctSession.SetCookieJar(jar)
	acctConfig := &api.SessionCredentials{
		UID:         uid,
		CookieAuth:  true,
		LastRefresh: time.Now().Add(-2 * time.Hour),
	}

	svc := api.ServiceConfig{Name: "lumo", Host: targetSrv.URL, ClientID: "web-lumo"}

	child, keypass, err := forkCookieWithRefreshRetry(context.Background(), acctSession, acctConfig, svc, "", []byte("test-keypass"), cookieStore)
	if err != nil {
		t.Fatalf("expected success after cookie refresh+retry, got error: %v", err)
	}
	if child == nil {
		t.Fatal("expected non-nil child cookie session")
	}
	defer child.Stop()

	if string(keypass) != "test-keypass" {
		t.Fatalf("keypass = %q, want %q", keypass, "test-keypass")
	}
	if got := atomic.LoadInt32(&refreshCount); got != 1 {
		t.Fatalf("cookie refresh count = %d, want 1 (exactly one coordinated refresh)", got)
	}
	if got := atomic.LoadInt32(&pushCount); got != 2 {
		t.Fatalf("push count = %d, want 2 (401 then retry)", got)
	}
	saved := cookieStore.snapshot()
	if saved == nil {
		t.Fatal("cookie store snapshot is nil")
	}
	if rc := rotatingCredential(saved); rc != rotatedRefresh {
		t.Fatalf("stored REFRESH cookie = %q, want rotated %q", rc, rotatedRefresh)
	}
}

// TestForkCookie_Fix_Non401Propagates (Case 5): a cookie fork push failing
// with a non-401 error (HTTP 403, Code 9100) propagates the bare *api.Error
// (Status 403) unchanged, with no cookie refresh and no store save.
//
// Validates: Requirements 3.4
func TestForkCookie_Fix_Non401Propagates(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	const uid = "cookie-uid"
	var pushCount, refreshCount int32
	acctSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth/v4/sessions/forks":
			atomic.AddInt32(&pushCount, 1)
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 9100, "Error": "insufficient scope"})
		case "/auth/refresh":
			atomic.AddInt32(&refreshCount, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 404, "Error": "not found"})
		}
	}))
	defer acctSrv.Close()

	withAccountHost(t, acctSrv.URL)

	cookieStore := &trackingStore{creds: cookieCreds(uid, "stale-auth", "stale-refresh", time.Now())}

	jar, _ := cookiejar.New(nil)
	acctSession := &api.Session{Auth: proton.Auth{UID: uid}}
	acctSession.SetCookieJar(jar)
	acctConfig := &api.SessionCredentials{
		UID:         uid,
		CookieAuth:  true,
		LastRefresh: time.Now().Add(-2 * time.Hour),
	}

	svc := api.ServiceConfig{Name: "lumo", Host: acctSrv.URL, ClientID: "web-lumo"}

	child, keypass, err := forkCookieWithRefreshRetry(context.Background(), acctSession, acctConfig, svc, "", []byte("test-keypass"), cookieStore)
	if err == nil {
		t.Fatal("expected error on non-401 push failure")
	}
	if child != nil || keypass != nil {
		t.Fatalf("expected nil child/keypass, got child=%v keypass=%v", child, keypass)
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not *api.Error: %T: %v", err, err)
	}
	if apiErr.Status != http.StatusForbidden {
		t.Fatalf("*api.Error.Status = %d, want 403", apiErr.Status)
	}
	if errors.Is(err, ErrForkFailed) {
		t.Fatalf("cookie push envelope error should be a bare *api.Error, not ErrForkFailed-wrapped: %v", err)
	}
	if got := atomic.LoadInt32(&refreshCount); got != 0 {
		t.Fatalf("cookie refresh count = %d, want 0 (non-401 must not refresh)", got)
	}
	if got := cookieStore.saves(); got != 0 {
		t.Fatalf("cookie store saves = %d, want 0 (non-401 must not persist)", got)
	}
	if got := atomic.LoadInt32(&pushCount); got != 1 {
		t.Fatalf("push count = %d, want 1 (non-401 must not retry)", got)
	}
}

// TestForkCookie_Fix_DeauthNoRetry (Case 6): a cookie fork push draws a 401
// but the cookie session is genuinely de-authed — the cookie refresh endpoint
// returns 422 (dead REFRESH-<uid>). forkCookieWithRefreshRetry returns
// ErrAccountDeauthed without retrying the push, and no rotated cookies are
// persisted.
//
// Validates: Requirements 2.6
func TestForkCookie_Fix_DeauthNoRetry(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	const uid = "cookie-uid"
	var pushCount, refreshCount int32
	acctSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth/v4/sessions/forks":
			atomic.AddInt32(&pushCount, 1)
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 401, "Error": "Invalid session"})
		case "/auth/refresh":
			atomic.AddInt32(&refreshCount, 1)
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 10013, "Error": "Invalid refresh token"})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 404, "Error": "not found"})
		}
	}))
	defer acctSrv.Close()

	withAccountHost(t, acctSrv.URL)

	cookieStore := &trackingStore{creds: cookieCreds(uid, "stale-auth", "dead-refresh", time.Now())}

	jar, _ := cookiejar.New(nil)
	acctSession := &api.Session{Auth: proton.Auth{UID: uid}}
	acctSession.SetCookieJar(jar)
	acctConfig := &api.SessionCredentials{
		UID:         uid,
		CookieAuth:  true,
		LastRefresh: time.Now().Add(-2 * time.Hour),
	}

	svc := api.ServiceConfig{Name: "lumo", Host: acctSrv.URL, ClientID: "web-lumo"}

	child, keypass, err := forkCookieWithRefreshRetry(context.Background(), acctSession, acctConfig, svc, "", []byte("test-keypass"), cookieStore)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if child != nil || keypass != nil {
		t.Fatalf("expected nil child/keypass, got child=%v keypass=%v", child, keypass)
	}
	if !errors.Is(err, ErrAccountDeauthed) {
		t.Fatalf("error %v does not wrap ErrAccountDeauthed", err)
	}
	if got := atomic.LoadInt32(&pushCount); got != 1 {
		t.Fatalf("push count = %d, want 1 (deauth must not retry)", got)
	}
	if got := cookieStore.saves(); got != 0 {
		t.Fatalf("cookie store saves = %d, want 0 (deauth must not persist)", got)
	}
}
