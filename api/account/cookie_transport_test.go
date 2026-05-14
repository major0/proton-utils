package account

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestCookieTransport_StripsBearerHeader(t *testing.T) {
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ct := &CookieTransport{Base: http.DefaultTransport}
	client := &http.Client{Transport: ct}

	req, _ := http.NewRequest("GET", srv.URL+"/test", nil)
	req.Header.Set("Authorization", "Bearer some-token")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if gotAuth != "" {
		t.Fatalf("expected no Authorization header, got %q", gotAuth)
	}
}

func TestCookieTransport_SendsCookies(t *testing.T) {
	var gotCookie string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("AUTH-uid-123")
		if err == nil {
			gotCookie = c.Value
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	jar, _ := cookiejar.New(nil)
	srvURL, _ := url.Parse(srv.URL)
	jar.SetCookies(srvURL, []*http.Cookie{
		{Name: "AUTH-uid-123", Value: "cookie-token", Path: "/"}, //nolint:gosec // G124: test cookie — security attributes not relevant here
	})

	ct := &CookieTransport{Base: http.DefaultTransport}
	client := &http.Client{Transport: ct, Jar: jar}

	req, _ := http.NewRequest("GET", srv.URL+"/test", nil)
	req.Header.Set("Authorization", "Bearer dead-token")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if gotCookie != "cookie-token" {
		t.Fatalf("AUTH cookie = %q, want %q", gotCookie, "cookie-token")
	}
}

func TestCookieTransport_PreservesNonBearerAuth(t *testing.T) {
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ct := &CookieTransport{Base: http.DefaultTransport}
	client := &http.Client{Transport: ct}

	req, _ := http.NewRequest("GET", srv.URL+"/test", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if gotAuth != "Basic dXNlcjpwYXNz" {
		t.Fatalf("expected Basic auth preserved, got %q", gotAuth)
	}
}

func TestCookieTransport_NilBase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ct := &CookieTransport{} // nil Base
	client := &http.Client{Transport: ct}

	req, _ := http.NewRequest("GET", srv.URL+"/test", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestCookieTransport_PreservesOtherHeaders(t *testing.T) {
	var gotUID, gotAppVer string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUID = r.Header.Get("x-pm-uid")
		gotAppVer = r.Header.Get("x-pm-appversion")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ct := &CookieTransport{Base: http.DefaultTransport}
	client := &http.Client{Transport: ct}

	req, _ := http.NewRequest("GET", srv.URL+"/test", nil)
	req.Header.Set("Authorization", "Bearer dead-token")
	req.Header.Set("x-pm-uid", "uid-123")
	req.Header.Set("x-pm-appversion", "web-lumo@1.3.3.4")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if gotUID != "uid-123" {
		t.Fatalf("x-pm-uid = %q, want %q", gotUID, "uid-123")
	}
	if gotAppVer != "web-lumo@1.3.3.4" {
		t.Fatalf("x-pm-appversion = %q, want %q", gotAppVer, "web-lumo@1.3.3.4")
	}
}
