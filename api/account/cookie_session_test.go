package account

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/major0/proton-cli/api"

	"github.com/ProtonMail/go-proton-api"
)

// testCookieSession creates a minimal CookieSession for testing.
func testCookieSession(t *testing.T) *CookieSession {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &CookieSession{
		UID:       "test-uid-123",
		cookieJar: jar,
	}
}

// --- CookieSession.DoJSON tests (1.5) ---

func TestCookieDoJSON_SuccessGet(t *testing.T) {
	type payload struct {
		Name string `json:"Name"`
		ID   int    `json:"ID"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Code": 1000,
			"Name": "test-item",
			"ID":   99,
		})
	}))
	defer srv.Close()

	cs := testCookieSession(t)
	var result payload
	err := cs.DoJSON(context.Background(), "GET", srv.URL+"/test", nil, &result)
	if err != nil {
		t.Fatalf("DoJSON GET: %v", err)
	}
	if result.Name != "test-item" || result.ID != 99 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCookieDoJSON_AuthHeaders(t *testing.T) {
	var gotUID, gotAuth, gotAppVer, gotUA string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUID = r.Header.Get("x-pm-uid")
		gotAuth = r.Header.Get("Authorization")
		gotAppVer = r.Header.Get("x-pm-appversion")
		gotUA = r.Header.Get("User-Agent")
		_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})
	}))
	defer srv.Close()

	cs := testCookieSession(t)
	cs.AppVersion = "web-account@5.2.0"
	cs.UserAgent = "proton-cli/2.0"

	_ = cs.DoJSON(context.Background(), "GET", srv.URL+"/test", nil, nil)

	if gotUID != "test-uid-123" {
		t.Fatalf("x-pm-uid = %q, want %q", gotUID, "test-uid-123")
	}
	if gotAuth != "" {
		t.Fatalf("Authorization should be empty, got %q", gotAuth)
	}
	if gotAppVer != "web-account@5.2.0" {
		t.Fatalf("x-pm-appversion = %q, want %q", gotAppVer, "web-account@5.2.0")
	}
	if gotUA != "proton-cli/2.0" {
		t.Fatalf("User-Agent = %q, want %q", gotUA, "proton-cli/2.0")
	}
}

func TestCookieDoJSON_NoBearerHeader(t *testing.T) {
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})
	}))
	defer srv.Close()

	cs := testCookieSession(t)
	_ = cs.DoJSON(context.Background(), "GET", srv.URL+"/test", nil, nil)

	if gotAuth != "" {
		t.Fatalf("CookieSession.DoJSON must not send Authorization header, got %q", gotAuth)
	}
}

func TestCookieDoJSON_CookieSending(t *testing.T) {
	var gotCookie string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("AUTH-test-uid-123")
		if err == nil {
			gotCookie = c.Value
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})
	}))
	defer srv.Close()

	cs := testCookieSession(t)
	// Inject an AUTH cookie into the jar for the test server.
	srvURL, _ := url.Parse(srv.URL)
	cs.cookieJar.SetCookies(srvURL, []*http.Cookie{
		{Name: "AUTH-test-uid-123", Value: "auth-token-xyz", Path: "/"},
	})

	err := cs.DoJSON(context.Background(), "GET", srv.URL+"/test", nil, nil)
	if err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	if gotCookie != "auth-token-xyz" {
		t.Fatalf("AUTH cookie = %q, want %q", gotCookie, "auth-token-xyz")
	}
}

func TestCookieDoJSON_EnvelopeParsing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Code":  2011,
			"Error": "resource not found",
		})
	}))
	defer srv.Close()

	cs := testCookieSession(t)
	err := cs.DoJSON(context.Background(), "GET", srv.URL+"/test", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *api.Error, got %T: %v", err, err)
	}
	if apiErr.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", apiErr.Status, http.StatusUnprocessableEntity)
	}
	if apiErr.Code != 2011 {
		t.Fatalf("code = %d, want %d", apiErr.Code, 2011)
	}
	if apiErr.Message != "resource not found" {
		t.Fatalf("message = %q, want %q", apiErr.Message, "resource not found")
	}
}

func TestCookieDoJSON_ResultPopulation(t *testing.T) {
	type user struct {
		Name  string `json:"Name"`
		Email string `json:"Email"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Code":  1000,
			"Name":  "Alice",
			"Email": "alice@proton.me",
		})
	}))
	defer srv.Close()

	cs := testCookieSession(t)
	var result user
	err := cs.DoJSON(context.Background(), "GET", srv.URL+"/users", nil, &result)
	if err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	if result.Name != "Alice" {
		t.Fatalf("Name = %q, want %q", result.Name, "Alice")
	}
	if result.Email != "alice@proton.me" {
		t.Fatalf("Email = %q, want %q", result.Email, "alice@proton.me")
	}
}

func TestCookieDoJSON_BaseURLOverride(t *testing.T) {
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})
	}))
	defer srv.Close()

	cs := testCookieSession(t)
	cs.BaseURL = srv.URL

	err := cs.DoJSON(context.Background(), "GET", "/core/v4/users", nil, nil)
	if err != nil {
		t.Fatalf("DoJSON with BaseURL: %v", err)
	}
	if gotPath != "/core/v4/users" {
		t.Fatalf("path = %q, want %q", gotPath, "/core/v4/users")
	}
}

func TestCookieDoJSON_PostWithBody(t *testing.T) {
	type reqBody struct {
		Prompt string `json:"Prompt"`
	}

	var gotBody reqBody
	var gotCT string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		gotCT = r.Header.Get("Content-Type")
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})
	}))
	defer srv.Close()

	cs := testCookieSession(t)
	err := cs.DoJSON(context.Background(), "POST", srv.URL+"/chat", reqBody{Prompt: "hello"}, nil)
	if err != nil {
		t.Fatalf("DoJSON POST: %v", err)
	}
	if gotCT != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", gotCT, "application/json")
	}
	if gotBody.Prompt != "hello" {
		t.Fatalf("Prompt = %q, want %q", gotBody.Prompt, "hello")
	}
}

func TestCookieDoJSON_MarshalError(t *testing.T) {
	cs := testCookieSession(t)
	err := cs.DoJSON(context.Background(), "POST", "http://localhost/test", make(chan int), nil)
	if err == nil {
		t.Fatal("expected error for unmarshalable body")
	}
	if !strings.Contains(err.Error(), "marshal body") {
		t.Fatalf("error = %v, want containing 'marshal body'", err)
	}
}

// --- CookieSession.DoSSE tests (1.6) ---

func TestCookieDoSSE_AuthHeaders(t *testing.T) {
	var gotUID, gotAuth, gotAppVer, gotUA, gotAccept string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUID = r.Header.Get("x-pm-uid")
		gotAuth = r.Header.Get("Authorization")
		gotAppVer = r.Header.Get("x-pm-appversion")
		gotUA = r.Header.Get("User-Agent")
		gotAccept = r.Header.Get("Accept")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cs := testCookieSession(t)
	cs.AppVersion = "web-lumo@1.3.3.4"
	cs.UserAgent = "proton-cli/2.0"

	rc, err := cs.DoSSE(context.Background(), srv.URL+"/ai/v1/chat", map[string]string{"key": "val"})
	if err != nil {
		t.Fatalf("DoSSE: %v", err)
	}
	_ = rc.Close()

	if gotUID != "test-uid-123" {
		t.Fatalf("x-pm-uid = %q, want %q", gotUID, "test-uid-123")
	}
	if gotAuth != "" {
		t.Fatalf("Authorization should be empty, got %q", gotAuth)
	}
	if gotAppVer != "web-lumo@1.3.3.4" {
		t.Fatalf("x-pm-appversion = %q, want %q", gotAppVer, "web-lumo@1.3.3.4")
	}
	if gotUA != "proton-cli/2.0" {
		t.Fatalf("User-Agent = %q, want %q", gotUA, "proton-cli/2.0")
	}
	if gotAccept != "text/event-stream" {
		t.Fatalf("Accept = %q, want %q", gotAccept, "text/event-stream")
	}
}

func TestCookieDoSSE_NoBearerHeader(t *testing.T) {
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cs := testCookieSession(t)
	rc, err := cs.DoSSE(context.Background(), srv.URL+"/test", nil)
	if err != nil {
		t.Fatalf("DoSSE: %v", err)
	}
	_ = rc.Close()

	if gotAuth != "" {
		t.Fatalf("CookieSession.DoSSE must not send Authorization header, got %q", gotAuth)
	}
}

func TestCookieDoSSE_StreamingResponse(t *testing.T) {
	ssePayload := "data: {\"type\":\"token\",\"content\":\"hello\"}\n\ndata: {\"type\":\"done\"}\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssePayload))
	}))
	defer srv.Close()

	cs := testCookieSession(t)
	rc, err := cs.DoSSE(context.Background(), srv.URL+"/ai/v1/chat", map[string]string{"prompt": "hi"})
	if err != nil {
		t.Fatalf("DoSSE: %v", err)
	}
	defer func() { _ = rc.Close() }()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != ssePayload {
		t.Fatalf("body = %q, want %q", string(got), ssePayload)
	}
}

func TestCookieDoSSE_NonSuccessReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Code":  2000,
			"Error": "access denied",
		})
	}))
	defer srv.Close()

	cs := testCookieSession(t)
	rc, err := cs.DoSSE(context.Background(), srv.URL+"/test", nil)
	if rc != nil {
		_ = rc.Close()
		t.Fatal("expected nil body on error")
	}
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *api.Error, got %T: %v", err, err)
	}
	if apiErr.Status != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", apiErr.Status, http.StatusForbidden)
	}
	if apiErr.Code != 2000 {
		t.Fatalf("code = %d, want %d", apiErr.Code, 2000)
	}
}

func TestCookieDoSSE_BaseURLOverride(t *testing.T) {
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cs := testCookieSession(t)
	cs.BaseURL = srv.URL

	rc, err := cs.DoSSE(context.Background(), "/ai/v1/chat", nil)
	if err != nil {
		t.Fatalf("DoSSE with BaseURL: %v", err)
	}
	_ = rc.Close()

	if gotPath != "/ai/v1/chat" {
		t.Fatalf("path = %q, want %q", gotPath, "/ai/v1/chat")
	}
}

func TestCookieDoSSE_CookieSending(t *testing.T) {
	var gotCookie string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("AUTH-test-uid-123")
		if err == nil {
			gotCookie = c.Value
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cs := testCookieSession(t)
	srvURL, _ := url.Parse(srv.URL)
	cs.cookieJar.SetCookies(srvURL, []*http.Cookie{
		{Name: "AUTH-test-uid-123", Value: "auth-token-xyz", Path: "/"},
	})

	rc, err := cs.DoSSE(context.Background(), srv.URL+"/test", nil)
	if err != nil {
		t.Fatalf("DoSSE: %v", err)
	}
	_ = rc.Close()

	if gotCookie != "auth-token-xyz" {
		t.Fatalf("AUTH cookie = %q, want %q", gotCookie, "auth-token-xyz")
	}
}

// --- CookieSession.buildURL tests (1.4) ---

func TestCookieBuildURL_RelativePath(t *testing.T) {
	cs := &CookieSession{BaseURL: api.AccountHost()}
	got := cs.buildURL("/core/v4/users")
	want := api.AccountHost() + "/core/v4/users"
	if got != want {
		t.Fatalf("buildURL = %q, want %q", got, want)
	}
}

func TestCookieBuildURL_AbsoluteURL(t *testing.T) {
	cs := &CookieSession{BaseURL: api.AccountHost()}
	abs := "https://lumo.proton.me/api/ai/v1/chat"
	got := cs.buildURL(abs)
	if got != abs {
		t.Fatalf("buildURL = %q, want %q", got, abs)
	}
}

func TestCookieBuildURL_EmptyBaseURL(t *testing.T) {
	cs := &CookieSession{}
	got := cs.buildURL("/core/v4/users")
	want := api.AccountHost() + "/core/v4/users"
	if got != want {
		t.Fatalf("buildURL = %q, want %q", got, want)
	}
}

// --- TransitionToCookies tests (2.3) ---

// testBearerSession creates a minimal Session for testing TransitionToCookies.
// The session's DoJSONCookie sends Bearer auth and uses the cookie jar,
// so we point it at a test server that returns Set-Cookie headers.
func testBearerSession(t *testing.T, srvURL string) *api.Session {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	s := &api.Session{
		Auth: proton.Auth{
			UID:          "test-uid-abc",
			AccessToken:  "access-tok-123",
			RefreshToken: "refresh-tok-456",
		},
		BaseURL: srvURL,
	}
	s.SetCookieJar(jar)
	return s
}

func TestTransitionToCookies_Success(t *testing.T) {
	uid := "test-uid-abc"
	var gotBody AuthCookiesReq
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/core/v4/auth/cookies" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		gotAuth = r.Header.Get("Authorization")
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &gotBody)

		// Set AUTH and REFRESH cookies in the response.
		http.SetCookie(w, &http.Cookie{Name: "AUTH-" + uid, Value: "auth-cookie-val", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "REFRESH-" + uid, Value: "refresh-cookie-val", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "Session-Id", Value: "sid-123", Path: "/"})

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})
	}))
	defer srv.Close()

	session := testBearerSession(t, srv.URL)
	cs, err := TransitionToCookies(context.Background(), session)
	if err != nil {
		t.Fatalf("TransitionToCookies: %v", err)
	}

	// Verify request body fields.
	if gotBody.UID != uid {
		t.Fatalf("UID = %q, want %q", gotBody.UID, uid)
	}
	if gotBody.RefreshToken != "refresh-tok-456" {
		t.Fatalf("RefreshToken = %q, want %q", gotBody.RefreshToken, "refresh-tok-456")
	}
	if gotBody.GrantType != "refresh_token" {
		t.Fatalf("GrantType = %q, want %q", gotBody.GrantType, "refresh_token")
	}
	if gotBody.ResponseType != "token" {
		t.Fatalf("ResponseType = %q, want %q", gotBody.ResponseType, "token")
	}
	if gotBody.RedirectURI != "https://proton.me" {
		t.Fatalf("RedirectURI = %q, want %q", gotBody.RedirectURI, "https://proton.me")
	}

	// Verify Bearer auth was sent on the transition request.
	if gotAuth != "Bearer access-tok-123" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer access-tok-123")
	}

	// Verify returned CookieSession fields.
	if cs.UID != uid {
		t.Fatalf("cs.UID = %q, want %q", cs.UID, uid)
	}
	if cs.BaseURL != srv.URL {
		t.Fatalf("cs.BaseURL = %q, want %q", cs.BaseURL, srv.URL)
	}

	// Verify cookies are in the jar.
	srvURL, _ := url.Parse(srv.URL)
	cookies := cs.cookieJar.Cookies(srvURL)
	cookieMap := make(map[string]string)
	for _, c := range cookies {
		cookieMap[c.Name] = c.Value
	}
	if cookieMap["AUTH-"+uid] != "auth-cookie-val" {
		t.Fatalf("AUTH cookie = %q, want %q", cookieMap["AUTH-"+uid], "auth-cookie-val")
	}
	if cookieMap["REFRESH-"+uid] != "refresh-cookie-val" {
		t.Fatalf("REFRESH cookie = %q, want %q", cookieMap["REFRESH-"+uid], "refresh-cookie-val")
	}
}

func TestTransitionToCookies_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Code":  10013,
			"Error": "invalid refresh token",
		})
	}))
	defer srv.Close()

	session := testBearerSession(t, srv.URL)
	_, err := TransitionToCookies(context.Background(), session)
	if err == nil {
		t.Fatal("expected error for API failure, got nil")
	}

	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *api.Error, got %T: %v", err, err)
	}
	if apiErr.Code != 10013 {
		t.Fatalf("code = %d, want %d", apiErr.Code, 10013)
	}
	if apiErr.Message != "invalid refresh token" {
		t.Fatalf("message = %q, want %q", apiErr.Message, "invalid refresh token")
	}
}

// --- extractRefreshCookie tests (3.1) ---

func TestExtractRefreshCookie_Found(t *testing.T) {
	cs := testCookieSession(t)
	cs.BaseURL = "http://localhost"
	srvURL, _ := url.Parse(cs.BaseURL)
	cs.cookieJar.SetCookies(srvURL, []*http.Cookie{
		{Name: "AUTH-uid-42", Value: "auth-val", Path: "/"},
		{Name: "REFRESH-uid-42", Value: "refresh-val", Path: "/"},
	})

	uid, token, err := cs.extractRefreshCookie()
	if err != nil {
		t.Fatalf("extractRefreshCookie: %v", err)
	}
	if uid != "uid-42" {
		t.Fatalf("uid = %q, want %q", uid, "uid-42")
	}
	if token != "refresh-val" {
		t.Fatalf("token = %q, want %q", token, "refresh-val")
	}
}

func TestExtractRefreshCookie_Missing(t *testing.T) {
	cs := testCookieSession(t)
	cs.BaseURL = "http://localhost"
	srvURL, _ := url.Parse(cs.BaseURL)
	cs.cookieJar.SetCookies(srvURL, []*http.Cookie{
		{Name: "AUTH-uid-42", Value: "auth-val", Path: "/"},
	})

	_, _, err := cs.extractRefreshCookie()
	if err == nil {
		t.Fatal("expected error for missing REFRESH cookie, got nil")
	}
	if !strings.Contains(err.Error(), "no REFRESH cookie") {
		t.Fatalf("error = %v, want containing 'no REFRESH cookie'", err)
	}
}

// --- RefreshCookies tests (3.4) ---

func TestRefreshCookies_Success(t *testing.T) {
	uid := "test-uid-123"
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/refresh" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")

		// Return new cookies (no request body expected — REFRESH cookie is the token).
		http.SetCookie(w, &http.Cookie{Name: "AUTH-" + uid, Value: "new-auth-token", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "REFRESH-" + uid, Value: "new-refresh-token", Path: "/"})
		_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})
	}))
	defer srv.Close()

	cs := testCookieSession(t)
	cs.BaseURL = srv.URL

	// Seed the jar with old cookies.
	srvURL, _ := url.Parse(srv.URL)
	cs.cookieJar.SetCookies(srvURL, []*http.Cookie{
		{Name: "AUTH-" + uid, Value: "old-auth-token", Path: "/"},
		{Name: "REFRESH-" + uid, Value: "old-refresh-token", Path: "/"},
	})

	err := cs.RefreshCookies(context.Background())
	if err != nil {
		t.Fatalf("RefreshCookies: %v", err)
	}

	// Verify no Bearer header was sent.
	if gotAuth != "" {
		t.Fatalf("Authorization should be empty (cookie auth only), got %q", gotAuth)
	}

	// Verify new cookies replaced old ones in the jar.
	cookies := cs.cookieJar.Cookies(srvURL)
	cookieMap := make(map[string]string)
	for _, c := range cookies {
		cookieMap[c.Name] = c.Value
	}
	if cookieMap["AUTH-"+uid] != "new-auth-token" {
		t.Fatalf("AUTH cookie = %q, want %q", cookieMap["AUTH-"+uid], "new-auth-token")
	}
	if cookieMap["REFRESH-"+uid] != "new-refresh-token" {
		t.Fatalf("REFRESH cookie = %q, want %q", cookieMap["REFRESH-"+uid], "new-refresh-token")
	}
}

func TestRefreshCookies_MissingRefreshCookie(t *testing.T) {
	cs := testCookieSession(t)
	cs.BaseURL = "http://localhost"

	// Jar has AUTH but no REFRESH cookie.
	srvURL, _ := url.Parse(cs.BaseURL)
	cs.cookieJar.SetCookies(srvURL, []*http.Cookie{
		{Name: "AUTH-test-uid-123", Value: "auth-val", Path: "/"},
	})

	err := cs.RefreshCookies(context.Background())
	if err == nil {
		t.Fatal("expected error for missing REFRESH cookie, got nil")
	}
	if !strings.Contains(err.Error(), "no REFRESH cookie") {
		t.Fatalf("error = %v, want containing 'no REFRESH cookie'", err)
	}
}

func TestRefreshCookies_APIError(t *testing.T) {
	uid := "test-uid-123"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Code":  10013,
			"Error": "invalid refresh token",
		})
	}))
	defer srv.Close()

	cs := testCookieSession(t)
	cs.BaseURL = srv.URL
	srvURL, _ := url.Parse(srv.URL)
	cs.cookieJar.SetCookies(srvURL, []*http.Cookie{
		{Name: "REFRESH-" + uid, Value: "bad-token", Path: "/"},
	})

	err := cs.RefreshCookies(context.Background())
	if err == nil {
		t.Fatal("expected error for API failure, got nil")
	}

	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *api.Error, got %T: %v", err, err)
	}
	if apiErr.Code != 10013 {
		t.Fatalf("code = %d, want %d", apiErr.Code, 10013)
	}
}

func TestRefreshCookies_Headers(t *testing.T) {
	uid := "test-uid-123"
	var gotUID, gotAppVer, gotUA, gotCT string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUID = r.Header.Get("x-pm-uid")
		gotAppVer = r.Header.Get("x-pm-appversion")
		gotUA = r.Header.Get("User-Agent")
		gotCT = r.Header.Get("Content-Type")
		http.SetCookie(w, &http.Cookie{Name: "AUTH-" + uid, Value: "a", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "REFRESH-" + uid, Value: "r", Path: "/"})
		_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})
	}))
	defer srv.Close()

	cs := testCookieSession(t)
	cs.BaseURL = srv.URL
	cs.AppVersion = "web-account@5.2.0"
	cs.UserAgent = "proton-cli/2.0"
	srvURL, _ := url.Parse(srv.URL)
	cs.cookieJar.SetCookies(srvURL, []*http.Cookie{
		{Name: "REFRESH-" + uid, Value: "old-refresh", Path: "/"},
	})

	_ = cs.RefreshCookies(context.Background())

	if gotUID != uid {
		t.Fatalf("x-pm-uid = %q, want %q", gotUID, uid)
	}
	if gotAppVer != "web-account@5.2.0" {
		t.Fatalf("x-pm-appversion = %q, want %q", gotAppVer, "web-account@5.2.0")
	}
	if gotUA != "proton-cli/2.0" {
		t.Fatalf("User-Agent = %q, want %q", gotUA, "proton-cli/2.0")
	}
	if gotCT != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", gotCT, "application/json")
	}
}

// --- 401-retry flow tests (3.5) ---

func TestDoJSON_401RetrySuccess(t *testing.T) {
	uid := "test-uid-123"
	callCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/test":
			callCount++
			if callCount == 1 {
				// First call: return 401.
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"Code":  401,
					"Error": "access token expired",
				})
				return
			}
			// Second call (retry): return success.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Code": 1000,
				"Name": "retried",
			})

		case "/auth/refresh":
			// Refresh endpoint: return new cookies (no request body expected).
			http.SetCookie(w, &http.Cookie{Name: "AUTH-" + uid, Value: "new-auth", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "REFRESH-" + uid, Value: "new-refresh", Path: "/"})
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})

		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	cs := testCookieSession(t)
	cs.BaseURL = srv.URL
	srvURL, _ := url.Parse(srv.URL)
	cs.cookieJar.SetCookies(srvURL, []*http.Cookie{
		{Name: "AUTH-" + uid, Value: "expired-auth", Path: "/"},
		{Name: "REFRESH-" + uid, Value: "valid-refresh", Path: "/"},
	})

	type result struct {
		Name string `json:"Name"`
	}
	var res result
	err := cs.DoJSON(context.Background(), "GET", "/test", nil, &res)
	if err != nil {
		t.Fatalf("DoJSON 401-retry: %v", err)
	}
	if res.Name != "retried" {
		t.Fatalf("Name = %q, want %q", res.Name, "retried")
	}
	if callCount != 2 {
		t.Fatalf("callCount = %d, want 2 (initial + retry)", callCount)
	}
}

func TestDoJSON_401RetryStillFails(t *testing.T) {
	uid := "test-uid-123"
	callCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/test":
			callCount++
			// Always return 401.
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Code":  401,
				"Error": "access token expired",
			})

		case "/auth/refresh":
			// Refresh succeeds but the retry still gets 401.
			http.SetCookie(w, &http.Cookie{Name: "AUTH-" + uid, Value: "new-auth", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "REFRESH-" + uid, Value: "new-refresh", Path: "/"})
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})

		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	cs := testCookieSession(t)
	cs.BaseURL = srv.URL
	srvURL, _ := url.Parse(srv.URL)
	cs.cookieJar.SetCookies(srvURL, []*http.Cookie{
		{Name: "AUTH-" + uid, Value: "expired-auth", Path: "/"},
		{Name: "REFRESH-" + uid, Value: "valid-refresh", Path: "/"},
	})

	err := cs.DoJSON(context.Background(), "GET", "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error after double 401, got nil")
	}

	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *api.Error, got %T: %v", err, err)
	}
	if apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", apiErr.Status, http.StatusUnauthorized)
	}
	if callCount != 2 {
		t.Fatalf("callCount = %d, want 2 (initial + retry)", callCount)
	}
}

func TestDoJSON_Non401ErrorNoRetry(t *testing.T) {
	callCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Code":  2000,
			"Error": "forbidden",
		})
	}))
	defer srv.Close()

	cs := testCookieSession(t)
	cs.BaseURL = srv.URL

	err := cs.DoJSON(context.Background(), "GET", "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *api.Error, got %T: %v", err, err)
	}
	if apiErr.Status != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", apiErr.Status, http.StatusForbidden)
	}
	// Non-401 errors should NOT trigger a retry.
	if callCount != 1 {
		t.Fatalf("callCount = %d, want 1 (no retry for non-401)", callCount)
	}
}

func TestDoJSON_401RefreshFails(t *testing.T) {
	uid := "test-uid-123"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/test":
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Code":  401,
				"Error": "access token expired",
			})

		case "/auth/refresh":
			// Refresh itself fails.
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Code":  10013,
				"Error": "invalid refresh token",
			})

		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	cs := testCookieSession(t)
	cs.BaseURL = srv.URL
	srvURL, _ := url.Parse(srv.URL)
	cs.cookieJar.SetCookies(srvURL, []*http.Cookie{
		{Name: "AUTH-" + uid, Value: "expired-auth", Path: "/"},
		{Name: "REFRESH-" + uid, Value: "bad-refresh", Path: "/"},
	})

	err := cs.DoJSON(context.Background(), "GET", "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error when refresh fails, got nil")
	}
	if !strings.Contains(err.Error(), "cookie refresh after 401") {
		t.Fatalf("error = %v, want containing 'cookie refresh after 401'", err)
	}
}

// --- Persistence tests (4.1–4.4) ---

func TestCookieSessionConfig_RoundTrip(t *testing.T) {
	uid := "test-uid-123"

	cs := testCookieSession(t)
	cs.BaseURL = "http://localhost"
	srvURL, _ := url.Parse(cs.BaseURL)
	cs.cookieJar.SetCookies(srvURL, []*http.Cookie{
		{Name: "AUTH-" + uid, Value: "auth-token-abc", Path: "/"},
		{Name: "REFRESH-" + uid, Value: "refresh-token-xyz", Path: "/"},
		{Name: "Session-Id", Value: "sid-42", Path: "/"},
	})

	config := cs.Config()

	// Verify config fields.
	if config.UID != uid {
		t.Fatalf("UID = %q, want %q", config.UID, uid)
	}
	if config.LastRefresh.IsZero() {
		t.Fatal("LastRefresh should be set")
	}
	if len(config.Cookies) != 3 {
		t.Fatalf("len(Cookies) = %d, want 3", len(config.Cookies))
	}

	// Restore from config.
	restored := CookieSessionFromConfig(config, "http://localhost")
	if restored.UID != uid {
		t.Fatalf("restored UID = %q, want %q", restored.UID, uid)
	}

	// Verify cookies are in the restored jar.
	restoredCookies := restored.cookieJar.Cookies(cookieQueryURL("http://localhost"))
	cookieMap := make(map[string]string)
	for _, c := range restoredCookies {
		cookieMap[c.Name] = c.Value
	}
	if cookieMap["AUTH-"+uid] != "auth-token-abc" {
		t.Fatalf("AUTH cookie = %q, want %q", cookieMap["AUTH-"+uid], "auth-token-abc")
	}
	if cookieMap["REFRESH-"+uid] != "refresh-token-xyz" {
		t.Fatalf("REFRESH cookie = %q, want %q", cookieMap["REFRESH-"+uid], "refresh-token-xyz")
	}
	if cookieMap["Session-Id"] != "sid-42" {
		t.Fatalf("Session-Id cookie = %q, want %q", cookieMap["Session-Id"], "sid-42")
	}
}

func TestCookieSessionConfig_JSONRoundTrip(t *testing.T) {
	config := &CookieSessionConfig{
		UID: "uid-json-test",
		Cookies: []api.SerialCookie{
			{Name: "AUTH-uid-json-test", Value: "auth-val"},
			{Name: "REFRESH-uid-json-test", Value: "refresh-val"},
		},
		LastRefresh: time.Date(2025, 7, 1, 12, 0, 0, 0, time.UTC),
		Service:     "lumo",
	}

	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored CookieSessionConfig
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if restored.UID != config.UID {
		t.Fatalf("UID = %q, want %q", restored.UID, config.UID)
	}
	if restored.Service != config.Service {
		t.Fatalf("Service = %q, want %q", restored.Service, config.Service)
	}
	if !restored.LastRefresh.Equal(config.LastRefresh) {
		t.Fatalf("LastRefresh = %v, want %v", restored.LastRefresh, config.LastRefresh)
	}
	if len(restored.Cookies) != len(config.Cookies) {
		t.Fatalf("len(Cookies) = %d, want %d", len(restored.Cookies), len(config.Cookies))
	}
	for i, c := range restored.Cookies {
		if c.Name != config.Cookies[i].Name || c.Value != config.Cookies[i].Value {
			t.Fatalf("cookie[%d] = %+v, want %+v", i, c, config.Cookies[i])
		}
	}
}

func TestCookieSessionFromConfig_DoJSON(t *testing.T) {
	uid := "restored-uid"
	var gotCookie string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("AUTH-" + uid)
		if err == nil {
			gotCookie = c.Value
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})
	}))
	defer srv.Close()

	// Build a config with cookies scoped to CookieURL.
	config := &CookieSessionConfig{
		UID: uid,
		Cookies: []api.SerialCookie{
			{Name: "AUTH-" + uid, Value: "restored-auth-token"},
			{Name: "REFRESH-" + uid, Value: "restored-refresh-token"},
		},
		LastRefresh: time.Now(),
	}

	restored := CookieSessionFromConfig(config, srv.URL)

	err := restored.DoJSON(context.Background(), "GET", srv.URL+"/test", nil, nil)
	if err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	if gotCookie != "restored-auth-token" {
		t.Fatalf("AUTH cookie = %q, want %q", gotCookie, "restored-auth-token")
	}
}

func TestCookieSessionConfig_EmptyCookies(t *testing.T) {
	cs := testCookieSession(t)
	// No cookies in jar.
	config := cs.Config()

	if config.UID != "test-uid-123" {
		t.Fatalf("UID = %q, want %q", config.UID, "test-uid-123")
	}
	if len(config.Cookies) != 0 {
		t.Fatalf("len(Cookies) = %d, want 0", len(config.Cookies))
	}

	// Restore from empty config.
	restored := CookieSessionFromConfig(config, "")
	if restored.UID != "test-uid-123" {
		t.Fatalf("restored UID = %q, want %q", restored.UID, "test-uid-123")
	}
	cookies := restored.cookieJar.Cookies(cookieQueryURL(""))
	if len(cookies) != 0 {
		t.Fatalf("restored cookies = %d, want 0", len(cookies))
	}
}

func TestCookieSessionConfig_ServicePreserved(t *testing.T) {
	cs := testCookieSession(t)
	config := cs.Config()
	config.Service = "lumo"

	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored CookieSessionConfig
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if restored.Service != "lumo" {
		t.Fatalf("Service = %q, want %q", restored.Service, "lumo")
	}
}
