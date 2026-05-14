package keyring

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"testing/quick"

	"github.com/major0/proton-utils/api"
	"pgregory.net/rapid"
)

// randomString generates a non-empty alphanumeric string for use in quick.Check generators.
func randomString(r *rand.Rand, maxLen int) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	n := r.Intn(maxLen) + 1
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[r.Intn(len(chars))]
	}
	return string(b)
}

// sessionConfigGenerator produces random SessionCredentials values for property tests.
type sessionConfigGenerator struct{}

func (sessionConfigGenerator) Generate(r *rand.Rand, _ int) reflect.Value {
	cfg := api.SessionCredentials{
		UID:           randomString(r, 32),
		AccessToken:   randomString(r, 64),
		RefreshToken:  randomString(r, 64),
		SaltedKeyPass: randomString(r, 64),
	}
	return reflect.ValueOf(cfg)
}

// nonEmptyStringGenerator produces non-empty alphanumeric strings.
type nonEmptyStringGenerator struct{}

func (nonEmptyStringGenerator) Generate(r *rand.Rand, _ int) reflect.Value {
	return reflect.ValueOf(randomString(r, 32))
}

// PropertySaveLoadRoundTrip verifies that for any valid SessionCredentials, account
// name, and service name, saving then loading with the same (account, service)
// returns an equal config.
//
// **Validates: Requirements 9.3**
func TestPropertySaveLoadRoundTrip(t *testing.T) {
	cfg := &quick.Config{
		MaxCount: 100,
		Values: func(values []reflect.Value, r *rand.Rand) {
			values[0] = sessionConfigGenerator{}.Generate(r, 0)
			values[1] = nonEmptyStringGenerator{}.Generate(r, 0)
			values[2] = nonEmptyStringGenerator{}.Generate(r, 0)
		},
	}

	prop := func(session api.SessionCredentials, account string, service string) bool {
		dir := t.TempDir()
		indexPath := filepath.Join(dir, "sessions.json")
		kr := NewMockKeyring()

		store := NewSessionStore(indexPath, account, service, kr)
		if err := store.Save(&session); err != nil {
			t.Logf("Save failed: %v", err)
			return false
		}

		loaded, err := store.Load()
		if err != nil {
			t.Logf("Load failed: %v", err)
			return false
		}

		return reflect.DeepEqual(session, *loaded)
	}

	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 9 failed: %v", err)
	}
}

// nonStarStringGenerator produces non-empty alphanumeric strings that are never "*".
type nonStarStringGenerator struct{}

func (nonStarStringGenerator) Generate(r *rand.Rand, _ int) reflect.Value {
	s := randomString(r, 32)
	// Ensure the generated string is never "*".
	if s == "*" {
		s = "fallback"
	}
	return reflect.ValueOf(s)
}

// TestPropertyServiceFallbackToWildcard verifies that for any account with a
// wildcard ("*") session and no service-specific session for service S, loading
// with (account, S) returns the wildcard session's config.
//
// **Validates: Requirements 9.3**
func TestPropertyServiceFallbackToWildcard(t *testing.T) {
	cfg := &quick.Config{
		MaxCount: 100,
		Values: func(values []reflect.Value, r *rand.Rand) {
			values[0] = sessionConfigGenerator{}.Generate(r, 0)
			values[1] = nonEmptyStringGenerator{}.Generate(r, 0) // account
			values[2] = nonStarStringGenerator{}.Generate(r, 0)  // service (never "*")
		},
	}

	prop := func(session api.SessionCredentials, account string, service string) bool {
		dir := t.TempDir()
		indexPath := filepath.Join(dir, "sessions.json")
		kr := NewMockKeyring()

		// Save session under the wildcard service "*".
		wildcardStore := NewSessionStore(indexPath, account, "*", kr)
		if err := wildcardStore.Save(&session); err != nil {
			t.Logf("Save wildcard failed: %v", err)
			return false
		}

		// Load with a different, non-wildcard service name — should fall back to "*".
		serviceStore := NewSessionStore(indexPath, account, service, kr)
		loaded, err := serviceStore.Load()
		if err != nil {
			t.Logf("Load with service %q failed: %v", service, err)
			return false
		}

		return reflect.DeepEqual(session, *loaded)
	}

	if err := quick.Check(prop, cfg); err != nil {
		t.Errorf("Property 10 failed: %v", err)
	}
}

// TestSessionIndex_MissingIndexFile verifies that loading from a non-existent
// index file returns ErrKeyNotFound (empty index, no account).
func TestSessionIndex_MissingIndexFile(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "nonexistent", "sessions.json")
	kr := NewMockKeyring()

	store := NewSessionStore(indexPath, "alice", "drive", kr)
	_, err := store.Load()
	if err == nil {
		t.Fatal("Load on missing index file: expected error, got nil")
	}
	if !errors.Is(err, api.ErrKeyNotFound) {
		t.Errorf("Load error = %v, want wrapped api.ErrKeyNotFound", err)
	}
}

// TestSessionIndex_CorruptIndexFile verifies that loading from an index file
// containing invalid JSON returns an error (not silently discarded).
func TestSessionIndex_CorruptIndexFile(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "sessions.json")

	if err := os.WriteFile(indexPath, []byte("{not valid json!!!"), 0600); err != nil {
		t.Fatalf("write corrupt index: %v", err)
	}

	kr := NewMockKeyring()
	store := NewSessionStore(indexPath, "alice", "drive", kr)

	_, err := store.Load()
	if err == nil {
		t.Fatal("Load on corrupt index file: expected error, got nil")
	}
	// The error should mention the account/service context.
	if !strings.Contains(err.Error(), "alice") || !strings.Contains(err.Error(), "drive") {
		t.Errorf("error lacks context: %v", err)
	}
}

// TestSessionIndex_KeyringUnavailable verifies that when the keyring backend
// returns an error, Load surfaces a clear error after a successful Save.
func TestSessionIndex_KeyringUnavailable(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "sessions.json")
	kr := NewMockKeyring()

	store := NewSessionStore(indexPath, "alice", "drive", kr)
	session := &api.SessionCredentials{
		UID:           "uid-1",
		AccessToken:   "at-1",
		RefreshToken:  "rt-1",
		SaltedKeyPass: "skp-1",
	}

	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Simulate keyring becoming unavailable after save.
	kr.ErrGet = fmt.Errorf("keyring unavailable: no secret service provider")

	_, err := store.Load()
	if err == nil {
		t.Fatal("Load with unavailable keyring: expected error, got nil")
	}
	if !errors.Is(err, api.ErrKeyNotFound) {
		t.Errorf("Load error = %v, want wrapped api.ErrKeyNotFound", err)
	}
}

// TestSessionIndex_StaleEntryCleanup verifies that when a keyring entry is
// deleted externally, Load returns ErrKeyNotFound and removes the stale
// entry from the on-disk index.
func TestSessionIndex_StaleEntryCleanup(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "sessions.json")
	kr := NewMockKeyring()

	store := NewSessionStore(indexPath, "alice", "drive", kr)
	session := &api.SessionCredentials{
		UID:           "uid-1",
		AccessToken:   "at-1",
		RefreshToken:  "rt-1",
		SaltedKeyPass: "skp-1",
	}

	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Delete the keyring entry directly, simulating external removal.
	//nolint:gosec // G304: test code reading test fixture by constructed path.
	idxData, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var idx SessionIndexData
	if err := json.Unmarshal(idxData, &idx); err != nil {
		t.Fatalf("unmarshal index: %v", err)
	}
	uuid := idx.Accounts["alice"].Sessions["drive"]
	if err := kr.Delete(KeyringService, uuid); err != nil {
		t.Fatalf("delete keyring entry: %v", err)
	}

	// Load should return ErrKeyNotFound.
	_, err = store.Load()
	if err == nil {
		t.Fatal("Load after keyring entry deleted: expected error, got nil")
	}
	if !errors.Is(err, api.ErrKeyNotFound) {
		t.Errorf("Load error = %v, want wrapped api.ErrKeyNotFound", err)
	}

	// Verify the stale entry was cleaned up from the index file.
	//nolint:gosec // G304: test code reading test fixture by constructed path.
	idxData, err = os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index after cleanup: %v", err)
	}
	var cleaned SessionIndexData
	if err := json.Unmarshal(idxData, &cleaned); err != nil {
		t.Fatalf("unmarshal cleaned index: %v", err)
	}

	if acct, ok := cleaned.Accounts["alice"]; ok {
		if _, ok := acct.Sessions["drive"]; ok {
			t.Error("stale entry for alice/drive still present in index after cleanup")
		}
	}
}

// testSession returns a minimal SessionCredentials for use in table-driven tests.
func testSession(uid string) *api.SessionCredentials {
	return &api.SessionCredentials{
		UID:           uid,
		AccessToken:   "at-" + uid,
		RefreshToken:  "rt-" + uid,
		SaltedKeyPass: "skp-" + uid,
	}
}

func TestSessionIndex_SaveAndLoad(t *testing.T) {
	tests := []struct {
		name    string
		account string
		service string
		session *api.SessionCredentials
		wantErr string
	}{
		{
			name:    "basic save and load",
			account: "alice",
			service: "drive",
			session: testSession("u1"),
		},
		{
			name:    "wildcard service",
			account: "bob",
			service: "*",
			session: testSession("u2"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			indexPath := filepath.Join(dir, "sessions.json")
			kr := NewMockKeyring()

			store := NewSessionStore(indexPath, tt.account, tt.service, kr)
			if err := store.Save(tt.session); err != nil {
				t.Fatalf("Save: %v", err)
			}

			got, err := store.Load()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !reflect.DeepEqual(*got, *tt.session) {
				t.Errorf("Load = %+v, want %+v", *got, *tt.session)
			}
		})
	}
}

func TestSessionIndex_SaveReusesUUID(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "sessions.json")
	kr := NewMockKeyring()

	store := NewSessionStore(indexPath, "alice", "drive", kr)

	s1 := testSession("u1")
	if err := store.Save(s1); err != nil {
		t.Fatalf("Save s1: %v", err)
	}

	// Read the UUID assigned on first save.
	data, _ := os.ReadFile(indexPath) //nolint:gosec // G304: test code reading test fixture.
	var idx1 SessionIndexData
	_ = json.Unmarshal(data, &idx1)
	uuid1 := idx1.Accounts["alice"].Sessions["drive"]

	// Save again — UUID should be reused.
	s2 := testSession("u2")
	if err := store.Save(s2); err != nil {
		t.Fatalf("Save s2: %v", err)
	}

	data, _ = os.ReadFile(indexPath) //nolint:gosec // G304: test code reading test fixture.
	var idx2 SessionIndexData
	_ = json.Unmarshal(data, &idx2)
	uuid2 := idx2.Accounts["alice"].Sessions["drive"]

	if uuid1 != uuid2 {
		t.Errorf("UUID changed on second save: %q → %q", uuid1, uuid2)
	}

	// Verify the loaded session is the updated one.
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.UID != "u2" {
		t.Errorf("Load.UID = %q, want %q", got.UID, "u2")
	}
}

func TestSessionIndex_SaveKeyringError(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "sessions.json")
	kr := NewMockKeyring()
	kr.ErrSet = fmt.Errorf("keyring locked")

	store := NewSessionStore(indexPath, "alice", "drive", kr)
	err := store.Save(testSession("u1"))
	if err == nil {
		t.Fatal("Save with keyring error: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "keyring locked") {
		t.Errorf("error = %v, want containing %q", err, "keyring locked")
	}
}

func TestSessionIndex_LoadNoAccount(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "sessions.json")
	kr := NewMockKeyring()

	// Save under "alice", load under "bob".
	store := NewSessionStore(indexPath, "alice", "drive", kr)
	if err := store.Save(testSession("u1")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	store2 := NewSessionStore(indexPath, "bob", "drive", kr)
	_, err := store2.Load()
	if err == nil {
		t.Fatal("Load missing account: expected error, got nil")
	}
	if !errors.Is(err, api.ErrKeyNotFound) {
		t.Errorf("error = %v, want wrapped ErrKeyNotFound", err)
	}
}

func TestSessionIndex_LoadNoServiceNoWildcard(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "sessions.json")
	kr := NewMockKeyring()

	// Save under "drive", load under "mail" — no wildcard fallback.
	store := NewSessionStore(indexPath, "alice", "drive", kr)
	if err := store.Save(testSession("u1")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	store2 := NewSessionStore(indexPath, "alice", "mail", kr)
	_, err := store2.Load()
	if err == nil {
		t.Fatal("Load missing service: expected error, got nil")
	}
	if !errors.Is(err, api.ErrKeyNotFound) {
		t.Errorf("error = %v, want wrapped ErrKeyNotFound", err)
	}
}

func TestSessionIndex_LoadCorruptKeyringData(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "sessions.json")
	kr := NewMockKeyring()

	store := NewSessionStore(indexPath, "alice", "drive", kr)
	if err := store.Save(testSession("u1")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Corrupt the keyring value to invalid JSON.
	data, _ := os.ReadFile(indexPath) //nolint:gosec // G304: test code reading test fixture.
	var idx SessionIndexData
	_ = json.Unmarshal(data, &idx)
	uuid := idx.Accounts["alice"].Sessions["drive"]
	_ = kr.Set(KeyringService, uuid, "not-json!!!")

	_, err := store.Load()
	if err == nil {
		t.Fatal("Load with corrupt keyring data: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "alice") {
		t.Errorf("error lacks context: %v", err)
	}
}

func TestSessionIndex_Delete(t *testing.T) {
	tests := []struct {
		name          string
		setupAccounts map[string][]string // account → []services to save
		deleteAccount string
		deleteService string
		wantAccounts  []string // accounts remaining after delete
		wantErr       string
	}{
		{
			name:          "delete existing session",
			setupAccounts: map[string][]string{"alice": {"drive"}},
			deleteAccount: "alice",
			deleteService: "drive",
			wantAccounts:  nil,
		},
		{
			name:          "delete one service keeps others",
			setupAccounts: map[string][]string{"alice": {"drive", "mail"}},
			deleteAccount: "alice",
			deleteService: "drive",
			wantAccounts:  []string{"alice"},
		},
		{
			name:          "delete missing account is idempotent",
			setupAccounts: map[string][]string{"alice": {"drive"}},
			deleteAccount: "bob",
			deleteService: "drive",
			wantAccounts:  []string{"alice"},
		},
		{
			name:          "delete missing service is idempotent",
			setupAccounts: map[string][]string{"alice": {"drive"}},
			deleteAccount: "alice",
			deleteService: "mail",
			wantAccounts:  []string{"alice"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			indexPath := filepath.Join(dir, "sessions.json")
			kr := NewMockKeyring()

			// Setup: save sessions.
			for acct, services := range tt.setupAccounts {
				for _, svc := range services {
					s := NewSessionStore(indexPath, acct, svc, kr)
					if err := s.Save(testSession(acct + "-" + svc)); err != nil {
						t.Fatalf("setup Save %s/%s: %v", acct, svc, err)
					}
				}
			}

			// Delete.
			store := NewSessionStore(indexPath, tt.deleteAccount, tt.deleteService, kr)
			err := store.Delete()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Delete: %v", err)
			}

			// Verify remaining accounts.
			lister := NewSessionStore(indexPath, "", "", kr)
			names, err := lister.List()
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			sort.Strings(names)
			sort.Strings(tt.wantAccounts)
			if !reflect.DeepEqual(names, tt.wantAccounts) {
				// Handle nil vs empty slice.
				if len(names) == 0 && len(tt.wantAccounts) == 0 {
					return
				}
				t.Errorf("remaining accounts = %v, want %v", names, tt.wantAccounts)
			}
		})
	}
}

func TestSessionIndex_DeleteKeyringError(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "sessions.json")
	kr := NewMockKeyring()

	store := NewSessionStore(indexPath, "alice", "drive", kr)
	if err := store.Save(testSession("u1")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	kr.ErrDelete = fmt.Errorf("keyring locked")
	err := store.Delete()
	if err == nil {
		t.Fatal("Delete with keyring error: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "keyring locked") {
		t.Errorf("error = %v, want containing %q", err, "keyring locked")
	}
}

func TestSessionIndex_List(t *testing.T) {
	tests := []struct {
		name     string
		accounts []string
		want     []string
	}{
		{
			name:     "empty index",
			accounts: nil,
			want:     nil,
		},
		{
			name:     "single account",
			accounts: []string{"alice"},
			want:     []string{"alice"},
		},
		{
			name:     "multiple accounts",
			accounts: []string{"alice", "bob", "charlie"},
			want:     []string{"alice", "bob", "charlie"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			indexPath := filepath.Join(dir, "sessions.json")
			kr := NewMockKeyring()

			for _, acct := range tt.accounts {
				s := NewSessionStore(indexPath, acct, "drive", kr)
				if err := s.Save(testSession(acct)); err != nil {
					t.Fatalf("Save %s: %v", acct, err)
				}
			}

			lister := NewSessionStore(indexPath, "", "", kr)
			got, err := lister.List()
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			sort.Strings(got)
			sort.Strings(tt.want)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("List = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSessionIndex_Switch(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "sessions.json")
	kr := NewMockKeyring()

	// Save sessions for two accounts.
	s1 := NewSessionStore(indexPath, "alice", "drive", kr)
	if err := s1.Save(testSession("alice-uid")); err != nil {
		t.Fatalf("Save alice: %v", err)
	}
	s2 := NewSessionStore(indexPath, "bob", "drive", kr)
	if err := s2.Save(testSession("bob-uid")); err != nil {
		t.Fatalf("Save bob: %v", err)
	}

	// Start as alice, switch to bob, load.
	store := NewSessionStore(indexPath, "alice", "drive", kr)
	if err := store.Switch("bob"); err != nil {
		t.Fatalf("Switch: %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load after Switch: %v", err)
	}
	if got.UID != "bob-uid" {
		t.Errorf("Load.UID = %q, want %q", got.UID, "bob-uid")
	}
}

func TestSessionIndex_ReadIndexNullAccounts(t *testing.T) {
	// Verify that an index file with null "accounts" field is handled.
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "sessions.json")
	if err := os.WriteFile(indexPath, []byte(`{"accounts":null}`), 0600); err != nil {
		t.Fatalf("write index: %v", err)
	}

	kr := NewMockKeyring()
	store := NewSessionStore(indexPath, "alice", "drive", kr)
	names, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("List = %v, want empty", names)
	}
}

func TestSessionIndex_SaveCreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "nested", "deep", "sessions.json")
	kr := NewMockKeyring()

	store := NewSessionStore(indexPath, "alice", "drive", kr)
	if err := store.Save(testSession("u1")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify the file was created.
	if _, err := os.Stat(indexPath); err != nil {
		t.Errorf("index file not created: %v", err)
	}
}

func TestSessionIndex_StaleWildcardCleanup(t *testing.T) {
	// When loading with service "mail" falls back to wildcard "*",
	// and the wildcard keyring entry is missing, the "*" entry should
	// be cleaned up (not the "mail" entry which doesn't exist).
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "sessions.json")
	kr := NewMockKeyring()

	// Save under wildcard.
	store := NewSessionStore(indexPath, "alice", "*", kr)
	if err := store.Save(testSession("u1")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Delete the keyring entry directly.
	data, _ := os.ReadFile(indexPath) //nolint:gosec // G304: test code reading test fixture.
	var idx SessionIndexData
	_ = json.Unmarshal(data, &idx)
	uuid := idx.Accounts["alice"].Sessions["*"]
	_ = kr.Delete(KeyringService, uuid)

	// Load with "mail" — should fall back to "*", find it stale, clean up.
	mailStore := NewSessionStore(indexPath, "alice", "mail", kr)
	_, err := mailStore.Load()
	if !errors.Is(err, api.ErrKeyNotFound) {
		t.Fatalf("Load error = %v, want wrapped ErrKeyNotFound", err)
	}

	// Verify the wildcard entry was cleaned up.
	data, _ = os.ReadFile(indexPath) //nolint:gosec // G304: test code reading test fixture.
	var cleaned SessionIndexData
	_ = json.Unmarshal(data, &cleaned)
	if _, ok := cleaned.Accounts["alice"]; ok {
		t.Error("stale wildcard entry for alice still present after cleanup")
	}
}

func TestSessionIndex_DeleteReadIndexError(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "sessions.json")

	// Write corrupt JSON so readIndex fails.
	if err := os.WriteFile(indexPath, []byte("{corrupt"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	kr := NewMockKeyring()
	store := NewSessionStore(indexPath, "alice", "drive", kr)
	err := store.Delete()
	if err == nil {
		t.Fatal("Delete with corrupt index: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "alice") {
		t.Errorf("error lacks context: %v", err)
	}
}

func TestSessionIndex_SaveReadIndexError(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "sessions.json")

	// Write corrupt JSON so readIndex fails.
	if err := os.WriteFile(indexPath, []byte("{corrupt"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	kr := NewMockKeyring()
	store := NewSessionStore(indexPath, "alice", "drive", kr)
	err := store.Save(testSession("u1"))
	if err == nil {
		t.Fatal("Save with corrupt index: expected error, got nil")
	}
}

func TestSessionIndex_ListReadIndexError(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "sessions.json")

	if err := os.WriteFile(indexPath, []byte("{corrupt"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	kr := NewMockKeyring()
	store := NewSessionStore(indexPath, "", "", kr)
	_, err := store.List()
	if err == nil {
		t.Fatal("List with corrupt index: expected error, got nil")
	}
}

func TestSessionIndex_WriteIndexReadOnlyDir(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "sessions.json")
	kr := NewMockKeyring()

	store := NewSessionStore(indexPath, "alice", "drive", kr)
	if err := store.Save(testSession("u1")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Make directory read-only so writeIndex fails on Delete.
	if err := os.Chmod(dir, 0500); err != nil { //nolint:gosec // G302: test needs read-only dir.
		t.Skipf("cannot set read-only dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) }) //nolint:gosec // G302: restore dir perms in cleanup.

	// Use a new path in a read-only location to trigger writeIndex error.
	roPath := filepath.Join(dir, "subdir", "sessions.json")
	store2 := NewSessionStore(roPath, "alice", "drive", kr)
	err := store2.Save(testSession("u2"))
	if err == nil {
		t.Fatal("Save to read-only dir: expected error, got nil")
	}
}

// --- Property tests (rapid) ---

// genSessionConfigRapid generates an arbitrary SessionCredentials for rapid property tests.
func genSessionConfigRapid(t *rapid.T) *api.SessionCredentials {
	return &api.SessionCredentials{
		UID:           rapid.StringMatching(`[a-zA-Z0-9]{1,32}`).Draw(t, "uid"),
		AccessToken:   rapid.StringMatching(`[a-zA-Z0-9]{1,64}`).Draw(t, "accessToken"),
		RefreshToken:  rapid.StringMatching(`[a-zA-Z0-9]{1,64}`).Draw(t, "refreshToken"),
		SaltedKeyPass: rapid.StringMatching(`[a-zA-Z0-9]{1,64}`).Draw(t, "saltedKeyPass"),
	}
}

// TestPropertySessionIndexSaveLoadRoundTrip_Property verifies that for any
// valid SessionCredentials, account, and service, Save then Load returns an
// identical config.
//
// **Validates: Requirements 2.3**
func TestPropertySessionIndexSaveLoadRoundTrip_Property(t *testing.T) {
	dir := t.TempDir()
	var counter int
	rapid.Check(t, func(rt *rapid.T) {
		account := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9]{0,15}`).Draw(rt, "account")
		service := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9]{0,15}`).Draw(rt, "service")
		session := genSessionConfigRapid(rt)

		counter++
		indexPath := filepath.Join(dir, fmt.Sprintf("sessions_%d.json", counter))
		kr := NewMockKeyring()

		store := NewSessionStore(indexPath, account, service, kr)
		if err := store.Save(session); err != nil {
			rt.Fatalf("Save: %v", err)
		}

		loaded, err := store.Load()
		if err != nil {
			rt.Fatalf("Load: %v", err)
		}

		if !reflect.DeepEqual(*session, *loaded) {
			rt.Fatalf("round-trip mismatch:\n  saved:  %+v\n  loaded: %+v", *session, *loaded)
		}
	})
}

// TestPropertySessionIndexDeleteIdempotent_Property verifies that deleting
// a session that doesn't exist is a no-op (returns nil).
//
// **Validates: Requirements 2.3**
func TestPropertySessionIndexDeleteIdempotent_Property(t *testing.T) {
	dir := t.TempDir()
	var counter int
	rapid.Check(t, func(rt *rapid.T) {
		account := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9]{0,15}`).Draw(rt, "account")
		service := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9]{0,15}`).Draw(rt, "service")

		counter++
		indexPath := filepath.Join(dir, fmt.Sprintf("sessions_%d.json", counter))
		kr := NewMockKeyring()

		store := NewSessionStore(indexPath, account, service, kr)
		if err := store.Delete(); err != nil {
			rt.Fatalf("Delete on empty index: %v", err)
		}
	})
}

// TestPropertySessionIndexSaveDeleteLoad_Property verifies that after
// Save then Delete, Load returns ErrKeyNotFound.
//
// **Validates: Requirements 2.3**
func TestPropertySessionIndexSaveDeleteLoad_Property(t *testing.T) {
	dir := t.TempDir()
	var counter int
	rapid.Check(t, func(rt *rapid.T) {
		account := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9]{0,15}`).Draw(rt, "account")
		service := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9]{0,15}`).Draw(rt, "service")
		session := genSessionConfigRapid(rt)

		counter++
		indexPath := filepath.Join(dir, fmt.Sprintf("sessions_%d.json", counter))
		kr := NewMockKeyring()

		store := NewSessionStore(indexPath, account, service, kr)
		if err := store.Save(session); err != nil {
			rt.Fatalf("Save: %v", err)
		}
		if err := store.Delete(); err != nil {
			rt.Fatalf("Delete: %v", err)
		}

		_, err := store.Load()
		if err == nil {
			rt.Fatal("Load after Delete: expected error, got nil")
		}
		if !errors.Is(err, api.ErrKeyNotFound) {
			rt.Fatalf("Load error = %v, want wrapped ErrKeyNotFound", err)
		}
	})
}

// TestPropertySessionIndexListAfterSave_Property verifies that after saving
// N sessions for distinct accounts, List returns exactly those account names.
//
// **Validates: Requirements 2.3**
func TestPropertySessionIndexListAfterSave_Property(t *testing.T) {
	dir := t.TempDir()
	var counter int
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 10).Draw(rt, "numAccounts")
		accounts := make(map[string]bool, n)
		for len(accounts) < n {
			a := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9]{0,15}`).Draw(rt, "account")
			accounts[a] = true
		}

		counter++
		indexPath := filepath.Join(dir, fmt.Sprintf("sessions_%d.json", counter))
		kr := NewMockKeyring()

		for acct := range accounts {
			s := NewSessionStore(indexPath, acct, "drive", kr)
			if err := s.Save(genSessionConfigRapid(rt)); err != nil {
				rt.Fatalf("Save %s: %v", acct, err)
			}
		}

		lister := NewSessionStore(indexPath, "", "", kr)
		names, err := lister.List()
		if err != nil {
			rt.Fatalf("List: %v", err)
		}

		if len(names) != n {
			rt.Fatalf("List returned %d accounts, want %d", len(names), n)
		}

		for _, name := range names {
			if !accounts[name] {
				rt.Fatalf("List returned unexpected account %q", name)
			}
		}
	})
}

// TestWildcardFallback_Property verifies that for any SessionCredentials stored
// under the "*" wildcard service, SessionIndex.Load with any service name
// that has no exact match returns the wildcard session's config.
//
// **Validates: Requirements 11.1**
func TestWildcardFallback_Property(t *testing.T) {
	dir := t.TempDir()
	var counter int
	rapid.Check(t, func(rt *rapid.T) {
		account := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9]{0,15}`).Draw(rt, "account")
		// Service name that is never "*".
		service := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9]{0,15}`).Draw(rt, "service")
		session := genSessionConfigRapid(rt)

		counter++
		indexPath := filepath.Join(dir, fmt.Sprintf("sessions_%d.json", counter))
		kr := NewMockKeyring()

		// Save under wildcard.
		wildcardStore := NewSessionStore(indexPath, account, "*", kr)
		if err := wildcardStore.Save(session); err != nil {
			rt.Fatalf("Save wildcard: %v", err)
		}

		// Load with a non-wildcard service — should fall back to "*".
		serviceStore := NewSessionStore(indexPath, account, service, kr)
		loaded, err := serviceStore.Load()
		if err != nil {
			rt.Fatalf("Load with service %q: %v", service, err)
		}

		if !reflect.DeepEqual(*session, *loaded) {
			rt.Fatalf("wildcard fallback mismatch:\n  saved:  %+v\n  loaded: %+v", *session, *loaded)
		}
	})
}
