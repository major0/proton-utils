package account

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/major0/proton-cli/api"
	"pgregory.net/rapid"
)

// mockStore is a thread-safe in-memory SessionStore for testing.
type mockStore struct {
	config *api.SessionCredentials
}

func (m *mockStore) Load() (*api.SessionCredentials, error) {
	if m.config == nil {
		return &api.SessionCredentials{}, nil
	}
	cfg := *m.config
	return &cfg, nil
}

func (m *mockStore) Save(cfg *api.SessionCredentials) error {
	m.config = cfg
	return nil
}

func (m *mockStore) Delete() error           { return nil }
func (m *mockStore) List() ([]string, error) { return nil, nil }
func (m *mockStore) Switch(string) error     { return nil }

// errStore is a SessionStore that always returns a fixed error from Load.
type errStore struct {
	err error
}

func (s *errStore) Load() (*api.SessionCredentials, error) { return nil, s.err }
func (s *errStore) Save(*api.SessionCredentials) error     { return nil }
func (s *errStore) Delete() error                          { return nil }
func (s *errStore) List() ([]string, error)                { return nil, nil }
func (s *errStore) Switch(string) error                    { return nil }

// deleteStore tracks whether Delete was called.
type deleteStore struct {
	mockStore
	deleted bool
}

func (s *deleteStore) Delete() error {
	s.deleted = true
	return nil
}

// --- SessionFromCredentials error path tests ---

func TestSessionFromCredentials(t *testing.T) {
	tests := []struct {
		name    string
		config  *api.SessionCredentials
		wantErr error
	}{
		{
			name:    "missing UID",
			config:  &api.SessionCredentials{AccessToken: "a", RefreshToken: "r"},
			wantErr: api.ErrMissingUID,
		},
		{
			name:    "missing access token",
			config:  &api.SessionCredentials{UID: "u", RefreshToken: "r"},
			wantErr: api.ErrMissingAccessToken,
		},
		{
			name:    "missing refresh token",
			config:  &api.SessionCredentials{UID: "u", AccessToken: "a"},
			wantErr: api.ErrMissingRefreshToken,
		},
		{
			name:    "all fields empty",
			config:  &api.SessionCredentials{},
			wantErr: api.ErrMissingUID,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SessionFromCredentials(context.Background(), nil, tt.config, nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// --- ReadySession tests ---

func TestReadySessionStoreError(t *testing.T) {
	store := &mockStore{}
	_, err := ReadySession(context.Background(), nil, store, nil, nil)
	if err == nil {
		t.Fatal("expected error from ReadySession with empty store")
	}
}

func TestReadySessionNotLoggedIn(t *testing.T) {
	store := &errStore{err: api.ErrKeyNotFound}
	_, err := ReadySession(context.Background(), nil, store, nil, nil)
	if !errors.Is(err, api.ErrNotLoggedIn) {
		t.Fatalf("expected ErrNotLoggedIn, got %v", err)
	}
}

// --- SessionList tests ---

func TestSessionList(t *testing.T) {
	store := &mockStore{}
	got, err := SessionList(store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d accounts, want 0", len(got))
	}
}

// --- SessionRevoke tests ---

func TestSessionRevoke(t *testing.T) {
	store := &deleteStore{}
	err := SessionRevoke(context.Background(), nil, store, false)
	if err != nil {
		t.Fatalf("SessionRevoke: %v", err)
	}
	if !store.deleted {
		t.Fatal("expected store.Delete to be called")
	}
}

// --- Staleness detection tests ---

func TestIsStale(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name           string
		accountRefresh time.Time
		serviceRefresh time.Time
		want           bool
	}{
		{"zero service is always stale", now, time.Time{}, true},
		{"account after service is stale", now, now.Add(-time.Hour), true},
		{"equal timestamps is fresh", now, now, false},
		{"service after account is fresh", now.Add(-time.Hour), now, false},
		{"both zero: service zero is stale", time.Time{}, time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsStale(tt.accountRefresh, tt.serviceRefresh)
			if got != tt.want {
				t.Fatalf("IsStale(%v, %v) = %v, want %v",
					tt.accountRefresh, tt.serviceRefresh, got, tt.want)
			}
		})
	}
}

func TestNeedsProactiveRefresh(t *testing.T) {
	tests := []struct {
		name        string
		lastRefresh time.Time
		want        bool
	}{
		{"zero always needs refresh", time.Time{}, true},
		{"30 minutes ago: no refresh", time.Now().Add(-30 * time.Minute), false},
		{"2 hours ago: needs refresh", time.Now().Add(-2 * time.Hour), true},
		{"exactly 1h1m ago: needs refresh", time.Now().Add(-61 * time.Minute), true},
		{"59 minutes ago: no refresh", time.Now().Add(-59 * time.Minute), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NeedsProactiveRefresh(tt.lastRefresh)
			if got != tt.want {
				t.Fatalf("NeedsProactiveRefresh(%v) = %v, want %v",
					tt.lastRefresh, got, tt.want)
			}
		})
	}
}

func TestNeedsCookieRefresh(t *testing.T) {
	tests := []struct {
		name        string
		lastRefresh time.Time
		want        bool
	}{
		{"zero always refreshes", time.Time{}, true},
		{"recent does not refresh", time.Now().Add(-30 * time.Minute), false},
		{"old refreshes", time.Now().Add(-2 * time.Hour), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NeedsCookieRefresh(tt.lastRefresh)
			if got != tt.want {
				t.Fatalf("NeedsCookieRefresh(%v) = %v, want %v",
					tt.lastRefresh, got, tt.want)
			}
		})
	}
}

// --- shouldFork tests ---

func TestShouldFork(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name      string
		svcConfig *api.SessionCredentials
		svcErr    error
		acctCfg   *api.SessionCredentials
		service   string
		want      bool
	}{
		{
			name:    "missing session triggers fork",
			svcErr:  api.ErrKeyNotFound,
			acctCfg: &api.SessionCredentials{LastRefresh: now},
			service: "drive",
			want:    true,
		},
		{
			name:      "wildcard fallback triggers fork",
			svcConfig: &api.SessionCredentials{Service: "other", LastRefresh: now},
			acctCfg:   &api.SessionCredentials{LastRefresh: now},
			service:   "drive",
			want:      true,
		},
		{
			name:      "empty service field triggers fork",
			svcConfig: &api.SessionCredentials{Service: "", LastRefresh: now},
			acctCfg:   &api.SessionCredentials{LastRefresh: now},
			service:   "drive",
			want:      true,
		},
		{
			name:      "stale session triggers fork",
			svcConfig: &api.SessionCredentials{Service: "drive", LastRefresh: now.Add(-2 * time.Hour)},
			acctCfg:   &api.SessionCredentials{LastRefresh: now},
			service:   "drive",
			want:      true,
		},
		{
			name:      "fresh session does not fork",
			svcConfig: &api.SessionCredentials{Service: "drive", LastRefresh: now},
			acctCfg:   &api.SessionCredentials{LastRefresh: now},
			service:   "drive",
			want:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldFork(tt.svcConfig, tt.svcErr, tt.acctCfg, tt.service)
			if got != tt.want {
				t.Fatalf("shouldFork() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- RestoreServiceSession tests ---

func TestRestoreServiceSession_NoAccountSession(t *testing.T) {
	svcStore := &mockStore{}
	acctStore := &errStore{err: api.ErrKeyNotFound}

	_, err := RestoreServiceSession(
		context.Background(), "drive", nil,
		svcStore, acctStore, nil, api.DefaultVersion, nil,
	)
	if !errors.Is(err, api.ErrNotLoggedIn) {
		t.Fatalf("expected ErrNotLoggedIn, got %v", err)
	}
}

func TestRestoreServiceSession_UnknownService(t *testing.T) {
	svcStore := &mockStore{}
	acctStore := &mockStore{}

	_, err := RestoreServiceSession(
		context.Background(), "nonexistent", nil,
		svcStore, acctStore, nil, api.DefaultVersion, nil,
	)
	if !errors.Is(err, api.ErrUnknownService) {
		t.Fatalf("expected ErrUnknownService, got %v", err)
	}
}

// --- Property tests for staleness ---

func TestStalenessComparison_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		sec1 := rapid.Int64Range(0, 253402300799).Draw(t, "sec1")
		sec2 := rapid.Int64Range(0, 253402300799).Draw(t, "sec2")
		ts1 := time.Unix(sec1, 0).UTC()
		ts2 := time.Unix(sec2, 0).UTC()

		if !IsStale(ts1, time.Time{}) {
			t.Fatal("zero serviceRefresh should always be stale")
		}

		if sec1 > sec2 {
			if !IsStale(ts1, ts2) {
				t.Fatalf("expected stale: account=%v > service=%v", ts1, ts2)
			}
		}

		if sec2 >= sec1 {
			if IsStale(ts1, ts2) {
				t.Fatalf("expected fresh: account=%v <= service=%v", ts1, ts2)
			}
		}
	})
}

func TestProactiveRefreshThreshold_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		if !NeedsProactiveRefresh(time.Time{}) {
			t.Fatal("zero LastRefresh should always need refresh")
		}

		ageMinutes := rapid.IntRange(0, 180).Draw(t, "ageMinutes")
		lastRefresh := time.Now().Add(-time.Duration(ageMinutes) * time.Minute)

		got := NeedsProactiveRefresh(lastRefresh)

		if ageMinutes > 62 && !got {
			t.Fatalf("age=%dm should need refresh", ageMinutes)
		}
		if ageMinutes < 58 && got {
			t.Fatalf("age=%dm should NOT need refresh", ageMinutes)
		}
	})
}
