package accountCmd

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/major0/proton-cli/api"
	"pgregory.net/rapid"
)

// TestBuildServiceStatus verifies buildServiceStatus for various scenarios.
func TestBuildServiceStatus(t *testing.T) {
	svc := api.ServiceConfig{
		Name:     "drive",
		Host:     "https://drive-api.proton.me/api",
		ClientID: "web-drive",
	}
	acctRefresh := time.Now()

	tests := []struct {
		name       string
		cfg        *api.SessionCredentials
		wantStatus sessionStatus
	}{
		{
			"nil config is none",
			nil,
			statusNone,
		},
		{
			"zero LastRefresh is stale",
			&api.SessionCredentials{UID: "u1"},
			statusStale,
		},
		{
			"fresh session",
			&api.SessionCredentials{UID: "u1", LastRefresh: time.Now()},
			statusFresh,
		},
		{
			"stale relative to account",
			&api.SessionCredentials{UID: "u1", LastRefresh: acctRefresh.Add(-time.Hour)},
			statusStale,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ss := buildServiceStatus(svc, tt.cfg, acctRefresh, false)
			if ss.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", ss.Status, tt.wantStatus)
			}
			if ss.Service != "drive" {
				t.Errorf("Service = %q, want %q", ss.Service, "drive")
			}
			if ss.Host != svc.Host {
				t.Errorf("Host = %q, want %q", ss.Host, svc.Host)
			}
		})
	}
}

// TestBuildServiceStatus_Verbose verifies that verbose mode includes
// ClientID and AppVersion.
func TestBuildServiceStatus_Verbose(t *testing.T) {
	svc := api.ServiceConfig{
		Name:     "lumo",
		Host:     "https://lumo.proton.me/api",
		ClientID: "web-lumo",
	}

	ss := buildServiceStatus(svc, nil, time.Time{}, true)
	if ss.ClientID != "web-lumo" {
		t.Errorf("ClientID = %q, want %q", ss.ClientID, "web-lumo")
	}
	if ss.AppVersion == "" {
		t.Error("AppVersion should be set in verbose mode")
	}
}

// TestBuildServiceStatus_AccountService verifies that the account service
// itself is not marked stale relative to itself.
func TestBuildServiceStatus_AccountService(t *testing.T) {
	svc := api.ServiceConfig{
		Name:     "account",
		Host:     "https://account-api.proton.me/api",
		ClientID: "web-account",
	}
	now := time.Now()
	cfg := &api.SessionCredentials{UID: "u1", LastRefresh: now}

	ss := buildServiceStatus(svc, cfg, now, false)
	if ss.Status != statusFresh {
		t.Errorf("account service Status = %q, want %q", ss.Status, statusFresh)
	}
}

// TestJSONStatusRoundTrip_Property verifies that for any set of service
// session states, the JSON output parses back into a structure containing
// all services with correct status fields.
//
// **Validates: Requirements 7.8**
// Tag: Feature: session-fork, Property 8: JSON status output round-trip
func TestJSONStatusRoundTrip_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate random service statuses.
		n := rapid.IntRange(1, 10).Draw(t, "numServices")
		original := make([]serviceStatus, n)

		for i := range original {
			status := rapid.SampledFrom([]sessionStatus{
				statusFresh, statusWarn, statusExpired, statusStale, statusNone,
			}).Draw(t, "status")

			// Generate a timestamp truncated to second precision for JSON round-trip.
			sec := rapid.Int64Range(0, 253402300799).Draw(t, "unixSec")
			ts := time.Unix(sec, 0).UTC()

			original[i] = serviceStatus{
				Service:     rapid.StringMatching(`[a-z]{2,10}`).Draw(t, "service"),
				Host:        rapid.StringMatching(`https://[a-z]{3,12}\\.proton\\.me/api`).Draw(t, "host"),
				Status:      status,
				UID:         rapid.StringMatching(`[a-zA-Z0-9]{0,32}`).Draw(t, "uid"),
				LastRefresh: ts,
				Age:         rapid.StringMatching(`[0-9]{1,5}[hms]`).Draw(t, "age"),
				ExpiresIn:   rapid.StringMatching(`[0-9]{1,5}[hms]`).Draw(t, "expiresIn"),
			}
		}

		// Marshal to JSON.
		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		// Unmarshal back.
		var restored []serviceStatus
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		if len(restored) != len(original) {
			t.Fatalf("count: got %d, want %d", len(restored), len(original))
		}

		for i, orig := range original {
			got := restored[i]
			if got.Service != orig.Service {
				t.Fatalf("[%d] Service: got %q, want %q", i, got.Service, orig.Service)
			}
			if got.Host != orig.Host {
				t.Fatalf("[%d] Host: got %q, want %q", i, got.Host, orig.Host)
			}
			if got.Status != orig.Status {
				t.Fatalf("[%d] Status: got %q, want %q", i, got.Status, orig.Status)
			}
			if got.UID != orig.UID {
				t.Fatalf("[%d] UID: got %q, want %q", i, got.UID, orig.UID)
			}
			if !got.LastRefresh.Equal(orig.LastRefresh) {
				t.Fatalf("[%d] LastRefresh: got %v, want %v", i, got.LastRefresh, orig.LastRefresh)
			}
			if got.Age != orig.Age {
				t.Fatalf("[%d] Age: got %q, want %q", i, got.Age, orig.Age)
			}
			if got.ExpiresIn != orig.ExpiresIn {
				t.Fatalf("[%d] ExpiresIn: got %q, want %q", i, got.ExpiresIn, orig.ExpiresIn)
			}
		}
	})
}

// TestBuildServiceStatus_ExpiredSession verifies expired session detection.
func TestBuildServiceStatus_ExpiredSession(t *testing.T) {
	svc := api.ServiceConfig{
		Name:     "account",
		Host:     "https://account-api.proton.me/api",
		ClientID: "web-account",
	}

	cfg := &api.SessionCredentials{
		UID:         "u1",
		LastRefresh: time.Now().Add(-25 * time.Hour),
	}

	ss := buildServiceStatus(svc, cfg, time.Time{}, false)
	if ss.Status != statusExpired {
		t.Errorf("Status = %q, want %q", ss.Status, statusExpired)
	}
	if ss.ExpiresIn != "expired" {
		t.Errorf("ExpiresIn = %q, want %q", ss.ExpiresIn, "expired")
	}
}

// TestBuildServiceStatus_WarnSession verifies warn-age session detection.
func TestBuildServiceStatus_WarnSession(t *testing.T) {
	svc := api.ServiceConfig{
		Name:     "account",
		Host:     "https://account-api.proton.me/api",
		ClientID: "web-account",
	}

	cfg := &api.SessionCredentials{
		UID:         "u1",
		LastRefresh: time.Now().Add(-21 * time.Hour),
	}

	ss := buildServiceStatus(svc, cfg, time.Time{}, false)
	if ss.Status != statusWarn {
		t.Errorf("Status = %q, want %q", ss.Status, statusWarn)
	}
}

// TestServiceStatusAllRegistered verifies that buildServiceStatus works
// for all registered services.
func TestServiceStatusAllRegistered(t *testing.T) {
	for name, svc := range api.Services {
		t.Run(name, func(t *testing.T) {
			ss := buildServiceStatus(svc, nil, time.Time{}, false)
			if ss.Service != name {
				t.Errorf("Service = %q, want %q", ss.Service, name)
			}
			if ss.Status != statusNone {
				t.Errorf("Status = %q, want %q", ss.Status, statusNone)
			}
			if ss.Host != svc.Host {
				t.Errorf("Host = %q, want %q", ss.Host, svc.Host)
			}
		})
	}
}
