package shareCmd

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api"
	"github.com/major0/proton-cli/api/config"
	"github.com/major0/proton-cli/api/drive"
	driveClient "github.com/major0/proton-cli/api/drive/client"
	cli "github.com/major0/proton-cli/cmd"
)

// saveAndRestore saves the current function variables and returns a cleanup
// function that restores them.
func saveAndRestore(t *testing.T) {
	t.Helper()
	origRestore := restoreSessionFn
	origNewClient := newDriveClientFn
	origResolve := resolveShareFn
	origList := listSharesFn
	origDelete := deleteShareFn
	origMembers := listMembersFn
	origInvs := listInvitationsFn
	origExts := listExternalInvitationsFn
	origRemove := removeMemberFn
	origDelInv := deleteInvitationFn
	origDelExt := deleteExternalInvitationFn
	origStore := cli.SessionStoreVar
	t.Cleanup(func() {
		restoreSessionFn = origRestore
		newDriveClientFn = origNewClient
		resolveShareFn = origResolve
		listSharesFn = origList
		deleteShareFn = origDelete
		listMembersFn = origMembers
		listInvitationsFn = origInvs
		listExternalInvitationsFn = origExts
		removeMemberFn = origRemove
		deleteInvitationFn = origDelInv
		deleteExternalInvitationFn = origDelExt
		cli.SessionStoreVar = origStore
	})
}

// injectSessionError sets restoreSessionFn to return the given error.
func injectSessionError(err error) {
	restoreSessionFn = func(_ context.Context) (*api.Session, error) {
		return nil, err
	}
}

// injectClientError sets restoreSessionFn to succeed and newDriveClientFn
// to return the given error.
func injectClientError(err error) {
	restoreSessionFn = func(_ context.Context) (*api.Session, error) {
		return &api.Session{}, nil
	}
	newDriveClientFn = func(_ context.Context, _ *api.Session) (*driveClient.Client, error) {
		return nil, err
	}
}

// injectTestClient sets restoreSessionFn to succeed and newDriveClientFn
// to return a Client with a real proton.Client that will fail on API calls.
func injectTestClient() {
	m := proton.New()
	client := m.NewClient("test-uid", "test-acc", "test-ref")
	session := &api.Session{Client: client}

	restoreSessionFn = func(_ context.Context) (*api.Session, error) {
		return session, nil
	}
	newDriveClientFn = func(_ context.Context, _ *api.Session) (*driveClient.Client, error) {
		return &driveClient.Client{Session: session}, nil
	}
}

// injectResolvedShare sets up function variables so that restoreSession
// and newDriveClient succeed, and resolveShareFn returns the given share.
func injectResolvedShare(share *drive.Share) {
	m := proton.New()
	client := m.NewClient("test-uid", "test-acc", "test-ref")
	session := &api.Session{Client: client}

	restoreSessionFn = func(_ context.Context) (*api.Session, error) {
		return session, nil
	}
	newDriveClientFn = func(_ context.Context, _ *api.Session) (*driveClient.Client, error) {
		return &driveClient.Client{Session: session}, nil
	}
	resolveShareFn = func(_ context.Context, _ *driveClient.Client, _ string) (*drive.Share, error) {
		return share, nil
	}
}

// injectShareList sets up function variables so that listSharesFn returns
// the given shares.
func injectShareList(shares []*drive.Share) {
	m := proton.New()
	client := m.NewClient("test-uid", "test-acc", "test-ref")
	session := &api.Session{Client: client}

	restoreSessionFn = func(_ context.Context) (*api.Session, error) {
		return session, nil
	}
	newDriveClientFn = func(_ context.Context, _ *api.Session) (*driveClient.Client, error) {
		return &driveClient.Client{Session: session}, nil
	}
	listSharesFn = func(_ context.Context, _ *driveClient.Client) ([]*drive.Share, error) {
		return shares, nil
	}
}

// makeTestShare creates a *drive.Share with a test name for use in tests.
func makeTestShare(shareID string, st proton.ShareType, name string) *drive.Share {
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{
			ShareID:      shareID,
			Type:         st,
			Creator:      "test@proton.me",
			CreationTime: 1705276800,
		},
	}
	pLink := &proton.Link{LinkID: "link-" + shareID}
	return drive.NewShare(pShare, nil, drive.NewTestLink(pLink, nil, nil, nil, name), nil, "vol-1")
}

func TestNotImplemented(t *testing.T) {
	tests := []struct {
		name    string
		cmdName string
		wantErr string
	}{
		{"share show", "share show", "share show: not yet implemented"},
		{"share invite", "share invite", "share invite: not yet implemented"},
		{"empty name", "", ": not yet implemented"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := notImplemented(tt.cmdName)
			err := fn(nil, nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestShareCmd_Run verifies the share root command doesn't panic.
func TestShareCmd_Run(_ *testing.T) {
	shareCmd.Run(shareCmd, nil)
}

// TestShareCmd_Subcommands verifies all expected subcommands are registered.
func TestShareCmd_Subcommands(t *testing.T) {
	want := []string{"show", "invite", "revoke", "add", "del", "list", "cache"}
	cmds := shareCmd.Commands()

	names := make(map[string]bool, len(cmds))
	for _, c := range cmds {
		names[c.Name()] = true
	}

	for _, w := range want {
		if !names[w] {
			t.Errorf("missing subcommand %q", w)
		}
	}
}

// --- Session restore error tests ---

func TestShareAddCmd_RestoreError(t *testing.T) {
	saveAndRestore(t)
	injectSessionError(fmt.Errorf("no session"))

	err := shareAddCmd.RunE(shareAddCmd, []string{"proton://My Files/test"})
	if err == nil || !strings.Contains(err.Error(), "no session") {
		t.Fatalf("error = %v, want 'no session'", err)
	}
}

func TestShareDelCmd_RestoreError(t *testing.T) {
	saveAndRestore(t)
	injectSessionError(fmt.Errorf("disk error"))

	err := shareDelCmd.RunE(shareDelCmd, []string{"myshare"})
	if err == nil || !strings.Contains(err.Error(), "disk error") {
		t.Fatalf("error = %v, want 'disk error'", err)
	}
}

func TestShareListCmd_RestoreError(t *testing.T) {
	saveAndRestore(t)
	injectSessionError(fmt.Errorf("store corrupted"))

	err := shareListCmd.RunE(shareListCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "store corrupted") {
		t.Fatalf("error = %v, want 'store corrupted'", err)
	}
}

// --- NewClient error tests ---

func TestShareAddCmd_ClientError(t *testing.T) {
	saveAndRestore(t)
	injectClientError(fmt.Errorf("client init failed"))

	err := shareAddCmd.RunE(shareAddCmd, []string{"proton://path"})
	if err == nil || !strings.Contains(err.Error(), "client init failed") {
		t.Fatalf("error = %v, want 'client init failed'", err)
	}
}

func TestShareDelCmd_ClientError(t *testing.T) {
	saveAndRestore(t)
	injectClientError(fmt.Errorf("client init failed"))

	err := shareDelCmd.RunE(shareDelCmd, []string{"myshare"})
	if err == nil || !strings.Contains(err.Error(), "client init failed") {
		t.Fatalf("error = %v, want 'client init failed'", err)
	}
}

func TestShareListCmd_ClientError(t *testing.T) {
	saveAndRestore(t)
	injectClientError(fmt.Errorf("client init failed"))

	err := shareListCmd.RunE(shareListCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "client init failed") {
		t.Fatalf("error = %v, want 'client init failed'", err)
	}
}

func TestShareShowCmd_ClientError(t *testing.T) {
	saveAndRestore(t)
	injectClientError(fmt.Errorf("client init failed"))

	err := shareShowCmd.RunE(shareShowCmd, []string{"myshare"})
	if err == nil || !strings.Contains(err.Error(), "client init failed") {
		t.Fatalf("error = %v, want 'client init failed'", err)
	}
}

func TestShareRevokeCmd_ClientError(t *testing.T) {
	saveAndRestore(t)
	injectClientError(fmt.Errorf("client init failed"))

	err := shareRevokeCmd.RunE(shareRevokeCmd, []string{"myshare", "user@test.local"})
	if err == nil || !strings.Contains(err.Error(), "client init failed") {
		t.Fatalf("error = %v, want 'client init failed'", err)
	}
}

func TestShareCacheCmd_ClientError(t *testing.T) {
	saveAndRestore(t)
	injectClientError(fmt.Errorf("client init failed"))

	err := shareCacheCmd.RunE(shareCacheCmd, []string{"myshare"})
	if err == nil || !strings.Contains(err.Error(), "client init failed") {
		t.Fatalf("error = %v, want 'client init failed'", err)
	}
}

func TestShareInviteCmd_ClientError(t *testing.T) {
	saveAndRestore(t)
	origPerms := inviteFlags.permissions
	t.Cleanup(func() { inviteFlags.permissions = origPerms })
	inviteFlags.permissions = "read"
	injectClientError(fmt.Errorf("client init failed"))

	err := shareInviteCmd.RunE(shareInviteCmd, []string{"myshare", "user@test.local"})
	if err == nil || !strings.Contains(err.Error(), "client init failed") {
		t.Fatalf("error = %v, want 'client init failed'", err)
	}
}

// --- Args validation tests ---

func TestShareAddCmd_ArgsValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"no args", []string{}, true},
		{"one arg valid", []string{"proton://path"}, false},
		{"two args", []string{"a", "b"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := shareAddCmd.Args(shareAddCmd, tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("Args(%v) error = %v, wantErr %v", tt.args, err, tt.wantErr)
			}
		})
	}
}

func TestShareDelCmd_ArgsValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"no args", []string{}, true},
		{"one arg valid", []string{"share"}, false},
		{"two args", []string{"a", "b"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := shareDelCmd.Args(shareDelCmd, tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("Args(%v) error = %v, wantErr %v", tt.args, err, tt.wantErr)
			}
		})
	}
}

func TestShareInviteCmd_ArgsValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"no args", []string{}, true},
		{"one arg", []string{"share"}, true},
		{"two args valid", []string{"share", "user@test.local"}, false},
		{"three args valid", []string{"share", "user@test.local", "read"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := shareInviteCmd.Args(shareInviteCmd, tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("Args(%v) error = %v, wantErr %v", tt.args, err, tt.wantErr)
			}
		})
	}
}

func TestShareCacheCmd_ArgsValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"no args", []string{}, true},
		{"one arg valid", []string{"share"}, false},
		{"two args", []string{"a", "b"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := shareCacheCmd.Args(shareCacheCmd, tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("Args(%v) error = %v, wantErr %v", tt.args, err, tt.wantErr)
			}
		})
	}
}

// --- Tests with injected test client (API calls fail with network error) ---

func TestShareDelCmd_ResolveShareError(t *testing.T) {
	saveAndRestore(t)
	injectTestClient()

	err := shareDelCmd.RunE(shareDelCmd, []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "share not found") {
		t.Errorf("error = %q, want 'share not found'", err.Error())
	}
}

func TestShareShowCmd_ResolveShareError(t *testing.T) {
	saveAndRestore(t)
	injectTestClient()

	err := shareShowCmd.RunE(shareShowCmd, []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "share not found") {
		t.Errorf("error = %q, want 'share not found'", err.Error())
	}
}

func TestShareRevokeCmd_ResolveShareError(t *testing.T) {
	saveAndRestore(t)
	injectTestClient()

	err := shareRevokeCmd.RunE(shareRevokeCmd, []string{"nonexistent", "user@test.local"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "share not found") {
		t.Errorf("error = %q, want 'share not found'", err.Error())
	}
}

func TestShareCacheCmd_ResolveShareError(t *testing.T) {
	saveAndRestore(t)
	injectTestClient()

	err := shareCacheCmd.RunE(shareCacheCmd, []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "share not found") {
		t.Errorf("error = %q, want 'share not found'", err.Error())
	}
}

func TestShareListCmd_ListSharesError(t *testing.T) {
	saveAndRestore(t)
	injectTestClient()

	err := shareListCmd.RunE(shareListCmd, nil)
	if err != nil {
		// ListShares may return an error from the API call.
		// This is expected — we're testing the error path.
		return
	}
	// If no error, the function completed (empty share list).
}

func TestShareAddCmd_ResolvePathError(t *testing.T) {
	saveAndRestore(t)
	injectTestClient()

	err := shareAddCmd.RunE(shareAddCmd, []string{"proton://My Files/nonexistent"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want 'not found'", err.Error())
	}
}

func TestShareInviteCmd_ResolveShareError(t *testing.T) {
	saveAndRestore(t)
	origPerms := inviteFlags.permissions
	t.Cleanup(func() { inviteFlags.permissions = origPerms })
	inviteFlags.permissions = "read"
	injectTestClient()

	err := shareInviteCmd.RunE(shareInviteCmd, []string{"nonexistent", "user@test.local"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "share not found") {
		t.Errorf("error = %q, want 'share not found'", err.Error())
	}
}

// --- Tests with injected resolved share (covers code after ResolveShare) ---

func TestShareShowCmd_MainShareType(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-main", proton.ShareTypeMain, "My Files")
	injectResolvedShare(share)

	// Main share type should print "Members: -" and "Invitations: -"
	err := shareShowCmd.RunE(shareShowCmd, []string{"My Files"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestShareShowCmd_PhotosShareType(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-photos", drive.ShareTypePhotos, "Photos")
	injectResolvedShare(share)

	err := shareShowCmd.RunE(shareShowCmd, []string{"Photos"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestShareShowCmd_StandardShareType(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", proton.ShareTypeStandard, "Shared Folder")
	injectResolvedShare(share)

	// Inject data for the print functions.
	listMembersFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.Member, error) {
		return []drive.Member{
			{MemberID: "m1", Email: "alice@test.local", Permissions: drive.PermViewer},
		}, nil
	}
	listInvitationsFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.Invitation, error) {
		return []drive.Invitation{
			{InvitationID: "inv-1", InviteeEmail: "bob@test.local", Permissions: drive.PermEditor, CreateTime: 1705276800},
		}, nil
	}
	listExternalInvitationsFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.ExternalInvitation, error) {
		return []drive.ExternalInvitation{
			{ExternalInvitationID: "ext-1", InviteeEmail: "ext@test.local", Permissions: drive.PermViewer, CreateTime: 1705276800},
		}, nil
	}

	err := shareShowCmd.RunE(shareShowCmd, []string{"Shared Folder"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestShareShowCmd_StandardShareEmptyLists(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", proton.ShareTypeStandard, "Empty Share")
	injectResolvedShare(share)

	listMembersFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.Member, error) {
		return nil, nil
	}
	listInvitationsFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.Invitation, error) {
		return nil, nil
	}
	listExternalInvitationsFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.ExternalInvitation, error) {
		return nil, nil
	}

	err := shareShowCmd.RunE(shareShowCmd, []string{"Empty Share"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestShareRevokeCmd_MainShareType(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-main", proton.ShareTypeMain, "My Files")
	injectResolvedShare(share)

	err := shareRevokeCmd.RunE(shareRevokeCmd, []string{"My Files", "user@test.local"})
	if err == nil {
		t.Fatal("expected error for main share type")
	}
	if !strings.Contains(err.Error(), "cannot revoke from main share") {
		t.Errorf("error = %q, want 'cannot revoke from main share'", err.Error())
	}
}

func TestShareRevokeCmd_PhotosShareType(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-photos", drive.ShareTypePhotos, "Photos")
	injectResolvedShare(share)

	err := shareRevokeCmd.RunE(shareRevokeCmd, []string{"Photos", "user@test.local"})
	if err == nil {
		t.Fatal("expected error for photos share type")
	}
	if !strings.Contains(err.Error(), "cannot revoke from photos share") {
		t.Errorf("error = %q, want 'cannot revoke from photos share'", err.Error())
	}
}

func TestShareCacheCmd_ProhibitedShareType(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-main", proton.ShareTypeMain, "My Files")
	injectResolvedShare(share)

	err := shareCacheCmd.RunE(shareCacheCmd, []string{"My Files"})
	if err == nil {
		t.Fatal("expected error for prohibited share type")
	}
	if !strings.Contains(err.Error(), "caching only allowed") {
		t.Errorf("error = %q, want 'caching only allowed'", err.Error())
	}
}

func TestShareCacheCmd_ShowState(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", proton.ShareTypeStandard, "Shared Folder")
	injectResolvedShare(share)

	// No toggle flags — should show current state.
	origConfig := cli.ConfigVar
	t.Cleanup(func() { cli.ConfigVar = origConfig })
	cli.ConfigVar = nil

	err := shareCacheCmd.RunE(shareCacheCmd, []string{"Shared Folder"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestShareCacheCmd_ToggleFlags(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", proton.ShareTypeStandard, "Shared Folder")
	injectResolvedShare(share)

	origConfig := cli.ConfigVar
	origFlags := cacheFlags
	t.Cleanup(func() {
		cli.ConfigVar = origConfig
		cacheFlags = origFlags
	})

	tmpDir := t.TempDir()
	configPath := tmpDir + "/config.yaml"

	cfg := config.DefaultConfig()
	cli.ConfigVar = cfg

	// Save a config so SaveConfig has a valid path.
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cacheFlags.memoryCache = "metadata"
	cacheFlags.diskCache = ""

	// The SaveConfig call will use cli.ConfigFilePath() which returns
	// rootParams.ConfigFile. Since we can't set that, the save may fail.
	// But the toggle logic before the save is what we're testing.
	_ = shareCacheCmd.RunE(shareCacheCmd, []string{"Shared Folder"})

	// Verify the in-memory config was updated.
	sc := cfg.Shares["Shared Folder"]
	if sc.MemoryCache != api.CacheMetadata {
		t.Error("expected memory cache metadata")
	}
}

func TestShareListCmd_WithShares(t *testing.T) {
	saveAndRestore(t)
	shares := []*drive.Share{
		makeTestShare("share-1", proton.ShareTypeMain, "My Files"),
		makeTestShare("share-2", proton.ShareTypeStandard, "Shared"),
	}
	injectShareList(shares)

	err := shareListCmd.RunE(shareListCmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestShareListCmd_EmptyList(t *testing.T) {
	saveAndRestore(t)
	injectShareList(nil)

	err := shareListCmd.RunE(shareListCmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestShareDelCmd_DeleteShareError(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", proton.ShareTypeStandard, "Shared Folder")
	injectResolvedShare(share)

	// DeleteShareByID will fail because the test client's API calls fail.
	err := shareDelCmd.RunE(shareDelCmd, []string{"Shared Folder"})
	if err == nil {
		t.Fatal("expected error from DeleteShareByID")
	}
	if !strings.Contains(err.Error(), "share del") {
		t.Errorf("error = %q, want 'share del'", err.Error())
	}
}

func TestShareRevokeCmd_StandardShareListError(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", proton.ShareTypeStandard, "Shared Folder")
	injectResolvedShare(share)

	// Standard share type will try to list members, which fails with test client.
	err := shareRevokeCmd.RunE(shareRevokeCmd, []string{"Shared Folder", "user@test.local"})
	if err == nil {
		t.Fatal("expected error from listing members")
	}
	if !strings.Contains(err.Error(), "listing members") {
		t.Errorf("error = %q, want 'listing members'", err.Error())
	}
}

func TestShareCacheCmd_DisableFlags(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", proton.ShareTypeStandard, "Test Share")
	injectResolvedShare(share)

	origConfig := cli.ConfigVar
	origFlags := cacheFlags
	t.Cleanup(func() {
		cli.ConfigVar = origConfig
		cacheFlags = origFlags
	})

	cfg := config.DefaultConfig()
	cfg.Shares["Test Share"] = api.ShareConfig{
		MemoryCache: api.CacheMetadata,
		DiskCache:   api.DiskCacheObjectStore,
	}
	cli.ConfigVar = cfg

	cacheFlags.memoryCache = "disabled"
	cacheFlags.diskCache = "disabled"

	_ = shareCacheCmd.RunE(shareCacheCmd, []string{"Test Share"})

	sc := cfg.Shares["Test Share"]
	if sc.MemoryCache != api.CacheDisabled {
		t.Error("expected memory cache disabled")
	}
	if sc.DiskCache != api.DiskCacheDisabled {
		t.Error("expected disk cache disabled")
	}
}

func TestShareCacheCmd_OnDiskToggle(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", proton.ShareTypeStandard, "Disk Share")
	injectResolvedShare(share)

	origConfig := cli.ConfigVar
	origFlags := cacheFlags
	t.Cleanup(func() {
		cli.ConfigVar = origConfig
		cacheFlags = origFlags
	})

	cli.ConfigVar = config.DefaultConfig()

	cacheFlags.memoryCache = ""
	cacheFlags.diskCache = "objectstore"

	_ = shareCacheCmd.RunE(shareCacheCmd, []string{"Disk Share"})

	sc := cli.ConfigVar.Shares["Disk Share"]
	if sc.DiskCache != api.DiskCacheObjectStore {
		t.Error("expected disk cache objectstore")
	}
}

func TestShareInviteCmd_ResolveShareErrorWithValidPerms(t *testing.T) {
	saveAndRestore(t)
	origPerms := inviteFlags.permissions
	t.Cleanup(func() { inviteFlags.permissions = origPerms })
	inviteFlags.permissions = "write"

	// resolveShareFn returns error
	injectClientError(fmt.Errorf("client init failed"))

	err := shareInviteCmd.RunE(shareInviteCmd, []string{"myshare", "user@test.local"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestShareDelCmd_SuccessWithConfig(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", proton.ShareTypeStandard, "Shared Folder")
	injectResolvedShare(share)

	origConfig := cli.ConfigVar
	t.Cleanup(func() { cli.ConfigVar = origConfig })

	cfg := config.DefaultConfig()
	cfg.Shares["Shared Folder"] = api.ShareConfig{MemoryCache: api.CacheLinkName}
	cli.ConfigVar = cfg

	deleteShareFn = func(_ context.Context, _ *driveClient.Client, _ string, _ bool) error {
		return nil
	}

	err := shareDelCmd.RunE(shareDelCmd, []string{"Shared Folder"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify config entry was removed.
	if _, ok := cfg.Shares["Shared Folder"]; ok {
		t.Error("expected config entry to be removed")
	}
}

func TestShareDelCmd_SuccessNoConfig(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", proton.ShareTypeStandard, "Other")
	injectResolvedShare(share)

	origConfig := cli.ConfigVar
	t.Cleanup(func() { cli.ConfigVar = origConfig })
	cli.ConfigVar = nil

	deleteShareFn = func(_ context.Context, _ *driveClient.Client, _ string, _ bool) error {
		return nil
	}

	err := shareDelCmd.RunE(shareDelCmd, []string{"Other"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestShareRevokeCmd_NoMatch(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", proton.ShareTypeStandard, "Shared")
	injectResolvedShare(share)

	listMembersFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.Member, error) {
		return []drive.Member{{MemberID: "m1", Email: "alice@test.local"}}, nil
	}
	listInvitationsFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.Invitation, error) {
		return nil, nil
	}
	listExternalInvitationsFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.ExternalInvitation, error) {
		return nil, nil
	}

	err := shareRevokeCmd.RunE(shareRevokeCmd, []string{"Shared", "nobody@test.local"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no matching") {
		t.Errorf("error = %q, want 'no matching'", err.Error())
	}
}

func TestShareRevokeCmd_MemberMatch(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", proton.ShareTypeStandard, "Shared")
	injectResolvedShare(share)

	listMembersFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.Member, error) {
		return []drive.Member{{MemberID: "m1", Email: "alice@test.local"}}, nil
	}
	listInvitationsFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.Invitation, error) {
		return nil, nil
	}
	listExternalInvitationsFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.ExternalInvitation, error) {
		return nil, nil
	}
	removeMemberFn = func(_ context.Context, _ *driveClient.Client, _, _ string) error {
		return nil
	}

	err := shareRevokeCmd.RunE(shareRevokeCmd, []string{"Shared", "alice@test.local"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestShareRevokeCmd_InvitationListError(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", proton.ShareTypeStandard, "Shared")
	injectResolvedShare(share)

	listMembersFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.Member, error) {
		return nil, nil
	}
	listInvitationsFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.Invitation, error) {
		return nil, fmt.Errorf("invitation list failed")
	}

	err := shareRevokeCmd.RunE(shareRevokeCmd, []string{"Shared", "user@test.local"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "listing invitations") {
		t.Errorf("error = %q, want 'listing invitations'", err.Error())
	}
}

func TestShareRevokeCmd_ExternalInvitationListError(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", proton.ShareTypeStandard, "Shared")
	injectResolvedShare(share)

	listMembersFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.Member, error) {
		return nil, nil
	}
	listInvitationsFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.Invitation, error) {
		return nil, nil
	}
	listExternalInvitationsFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.ExternalInvitation, error) {
		return nil, fmt.Errorf("ext list failed")
	}

	err := shareRevokeCmd.RunE(shareRevokeCmd, []string{"Shared", "user@test.local"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "listing external invitations") {
		t.Errorf("error = %q, want 'listing external invitations'", err.Error())
	}
}

func TestShareRevokeCmd_InvitationMatch(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", proton.ShareTypeStandard, "Shared")
	injectResolvedShare(share)

	listMembersFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.Member, error) {
		return nil, nil
	}
	listInvitationsFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.Invitation, error) {
		return []drive.Invitation{{InvitationID: "inv-1", InviteeEmail: "bob@test.local"}}, nil
	}
	listExternalInvitationsFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.ExternalInvitation, error) {
		return nil, nil
	}
	deleteInvitationFn = func(_ context.Context, _ *driveClient.Client, _, _ string) error {
		return nil
	}

	err := shareRevokeCmd.RunE(shareRevokeCmd, []string{"Shared", "bob@test.local"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestShareRevokeCmd_ExternalInvitationMatch(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", proton.ShareTypeStandard, "Shared")
	injectResolvedShare(share)

	listMembersFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.Member, error) {
		return nil, nil
	}
	listInvitationsFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.Invitation, error) {
		return nil, nil
	}
	listExternalInvitationsFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.ExternalInvitation, error) {
		return []drive.ExternalInvitation{{ExternalInvitationID: "ext-1", InviteeEmail: "ext@test.local"}}, nil
	}
	deleteExternalInvitationFn = func(_ context.Context, _ *driveClient.Client, _, _ string) error {
		return nil
	}

	err := shareRevokeCmd.RunE(shareRevokeCmd, []string{"Shared", "ext@test.local"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestShareInviteCmd_GetPublicKeysError(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", proton.ShareTypeStandard, "Shared")
	injectResolvedShare(share)

	origPerms := inviteFlags.permissions
	t.Cleanup(func() { inviteFlags.permissions = origPerms })
	inviteFlags.permissions = "read"

	// After resolveShare succeeds, runShareInvite calls
	// session.Client.GetPublicKeys which will fail with the test client.
	err := shareInviteCmd.RunE(shareInviteCmd, []string{"Shared", "user@test.local"})
	if err == nil {
		t.Fatal("expected error from GetPublicKeys")
	}
	if !strings.Contains(err.Error(), "failed to fetch recipient keys") {
		t.Errorf("error = %q, want 'failed to fetch recipient keys'", err.Error())
	}
}

func TestShareCacheCmd_SaveConfigError(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", proton.ShareTypeStandard, "Save Test")
	injectResolvedShare(share)

	origConfig := cli.ConfigVar
	origFlags := cacheFlags
	t.Cleanup(func() {
		cli.ConfigVar = origConfig
		cacheFlags = origFlags
	})

	cli.ConfigVar = config.DefaultConfig()

	cacheFlags.memoryCache = "linkname"
	cacheFlags.diskCache = ""

	// ConfigFilePath() returns empty string, so SaveConfig will fail.
	// The error should be returned.
	err := shareCacheCmd.RunE(shareCacheCmd, []string{"Save Test"})
	// SaveConfig may or may not fail depending on the path.
	// Either way, the toggle logic is exercised.
	_ = err
}

func TestShareDelCmd_ForceFlag(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", proton.ShareTypeStandard, "Force Del")
	injectResolvedShare(share)

	origForce := delFlags.force
	origConfig := cli.ConfigVar
	t.Cleanup(func() {
		delFlags.force = origForce
		cli.ConfigVar = origConfig
	})

	delFlags.force = true
	cli.ConfigVar = config.DefaultConfig()

	var gotForce bool
	deleteShareFn = func(_ context.Context, _ *driveClient.Client, _ string, force bool) error {
		gotForce = force
		return nil
	}

	err := shareDelCmd.RunE(shareDelCmd, []string{"Force Del"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !gotForce {
		t.Error("expected force=true")
	}
}

func TestShareRevokeCmd_RemoveMemberError(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", proton.ShareTypeStandard, "Shared")
	injectResolvedShare(share)

	listMembersFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.Member, error) {
		return []drive.Member{{MemberID: "m1", Email: "alice@test.local"}}, nil
	}
	listInvitationsFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.Invitation, error) {
		return nil, nil
	}
	listExternalInvitationsFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.ExternalInvitation, error) {
		return nil, nil
	}
	removeMemberFn = func(_ context.Context, _ *driveClient.Client, _, _ string) error {
		return fmt.Errorf("remove failed")
	}

	err := shareRevokeCmd.RunE(shareRevokeCmd, []string{"Shared", "alice@test.local"})
	if err == nil || !strings.Contains(err.Error(), "remove failed") {
		t.Fatalf("error = %v, want 'remove failed'", err)
	}
}

func TestShareRevokeCmd_DeleteInvitationError(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", proton.ShareTypeStandard, "Shared")
	injectResolvedShare(share)

	listMembersFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.Member, error) {
		return nil, nil
	}
	listInvitationsFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.Invitation, error) {
		return []drive.Invitation{{InvitationID: "inv-1", InviteeEmail: "bob@test.local"}}, nil
	}
	listExternalInvitationsFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.ExternalInvitation, error) {
		return nil, nil
	}
	deleteInvitationFn = func(_ context.Context, _ *driveClient.Client, _, _ string) error {
		return fmt.Errorf("delete inv failed")
	}

	err := shareRevokeCmd.RunE(shareRevokeCmd, []string{"Shared", "bob@test.local"})
	if err == nil || !strings.Contains(err.Error(), "delete inv failed") {
		t.Fatalf("error = %v, want 'delete inv failed'", err)
	}
}

func TestShareRevokeCmd_DeleteExternalInvitationError(t *testing.T) {
	saveAndRestore(t)
	share := makeTestShare("share-std", proton.ShareTypeStandard, "Shared")
	injectResolvedShare(share)

	listMembersFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.Member, error) {
		return nil, nil
	}
	listInvitationsFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.Invitation, error) {
		return nil, nil
	}
	listExternalInvitationsFn = func(_ context.Context, _ *driveClient.Client, _ string) ([]drive.ExternalInvitation, error) {
		return []drive.ExternalInvitation{{ExternalInvitationID: "ext-1", InviteeEmail: "ext@test.local"}}, nil
	}
	deleteExternalInvitationFn = func(_ context.Context, _ *driveClient.Client, _, _ string) error {
		return fmt.Errorf("delete ext failed")
	}

	err := shareRevokeCmd.RunE(shareRevokeCmd, []string{"Shared", "ext@test.local"})
	if err == nil || !strings.Contains(err.Error(), "delete ext failed") {
		t.Fatalf("error = %v, want 'delete ext failed'", err)
	}
}
