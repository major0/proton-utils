package api

import (
	"encoding/json"
	"fmt"
	"net/http/cookiejar"
	"testing"

	"pgregory.net/rapid"
)

// TestPropertyCookiePersistenceRoundTrip verifies that for any set of
// SerialCookie values with non-empty Name and Value, serializing them into a
// SessionCredentials, persisting to a mock store, loading back, and injecting into
// a fresh cookie jar produces a jar where querying the account service URL
// returns cookies with matching Name and Value for every original cookie.
//
// Domain and Path are not preserved by net/http/cookiejar.Cookies() — the jar
// manages domain/path matching internally. The round-trip therefore preserves
// only Name and Value.
//
// **Validates: Requirements 4.3, 4.4**
func TestPropertyCookiePersistenceRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 20).Draw(t, "numCookies")

		// Generate cookies with unique names to avoid jar deduplication.
		seen := make(map[string]bool, n)
		cookies := make([]SerialCookie, 0, n)
		for i := 0; i < n; i++ {
			name := genCookieName(t, fmt.Sprintf("name%d", i))
			if seen[name] {
				continue
			}
			seen[name] = true
			value := genCookieValue(t, fmt.Sprintf("value%d", i))
			cookies = append(cookies, SerialCookie{
				Name:  name,
				Value: value,
			})
		}

		// Serialize into a SessionCredentials.
		config := &SessionCredentials{
			UID:        "roundtrip-uid",
			Cookies:    cookies,
			CookieAuth: true,
		}

		// Persist to mock store via JSON marshal/unmarshal (simulates store save/load).
		//nolint:gosec // G117: property test intentionally marshals SessionCredentials.
		data, err := json.Marshal(config)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		var loaded SessionCredentials
		if err := json.Unmarshal(data, &loaded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		// Inject into a fresh cookie jar scoped to the account service host.
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatalf("cookiejar.New: %v", err)
		}
		u := cookieQueryURL(AccountHost())
		u.Path = "/"
		LoadCookies(jar, loaded.Cookies, u)

		// Query the jar and assert Name+Value match for every original cookie.
		queriedCookies := jar.Cookies(cookieQueryURL(AccountHost()))

		type nv struct{ Name, Value string }
		want := make(map[nv]bool, len(cookies))
		for _, c := range cookies {
			want[nv{c.Name, c.Value}] = true
		}

		got := make(map[nv]bool, len(queriedCookies))
		for _, c := range queriedCookies {
			got[nv{c.Name, c.Value}] = true
		}

		for k := range want {
			if !got[k] {
				t.Fatalf("missing cookie after round-trip: Name=%q Value=%q", k.Name, k.Value)
			}
		}
	})
}
