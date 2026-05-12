package shareCmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/major0/proton-cli/api/drive"
	"pgregory.net/rapid"
)

// genDistinctIDs generates n distinct non-empty string IDs.
func genDistinctIDs(t *rapid.T, n int, label string) []string {
	seen := make(map[string]bool, n)
	ids := make([]string, 0, n)
	for len(ids) < n {
		id := fmt.Sprintf("%s-%d-%s", label, len(ids), rapid.StringMatching(`[a-z0-9]{4,8}`).Draw(t, label))
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// TestFindRevokeTarget_UniqueMatch_Property verifies that when a search
// argument matches exactly one entity, findRevokeTarget returns it.
//
// **Property 3: Revoke Entity Lookup**
// **Validates: Requirements 4.1, 4.5**
func TestFindRevokeTarget_UniqueMatch_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		nMembers := rapid.IntRange(0, 5).Draw(t, "nMembers")
		nInvs := rapid.IntRange(0, 5).Draw(t, "nInvs")
		nExts := rapid.IntRange(0, 5).Draw(t, "nExts")
		total := nMembers + nInvs + nExts
		if total == 0 {
			return // skip empty case
		}

		// Generate distinct IDs and emails for all entities.
		allIDs := genDistinctIDs(t, total, "id")
		allEmails := genDistinctIDs(t, total, "email")

		idx := 0
		members := make([]drive.Member, nMembers)
		for i := range members {
			members[i] = drive.Member{MemberID: allIDs[idx], Email: allEmails[idx]}
			idx++
		}
		invs := make([]drive.Invitation, nInvs)
		for i := range invs {
			invs[i] = drive.Invitation{InvitationID: allIDs[idx], InviteeEmail: allEmails[idx]}
			idx++
		}
		exts := make([]drive.ExternalInvitation, nExts)
		for i := range exts {
			exts[i] = drive.ExternalInvitation{ExternalInvitationID: allIDs[idx], InviteeEmail: allEmails[idx]}
			idx++
		}

		// Pick a random entity and search by its email.
		pickIdx := rapid.IntRange(0, total-1).Draw(t, "pickIdx")
		var searchArg, wantKind, wantID string
		switch {
		case pickIdx < nMembers:
			searchArg = members[pickIdx].Email
			wantKind = "member"
			wantID = members[pickIdx].MemberID
		case pickIdx < nMembers+nInvs:
			i := pickIdx - nMembers
			searchArg = invs[i].InviteeEmail
			wantKind = "invitation"
			wantID = invs[i].InvitationID
		default:
			i := pickIdx - nMembers - nInvs
			searchArg = exts[i].InviteeEmail
			wantKind = "external-invitation"
			wantID = exts[i].ExternalInvitationID
		}

		got, err := findRevokeTarget(searchArg, members, invs, exts)
		if err != nil {
			t.Fatalf("findRevokeTarget(%q): %v", searchArg, err)
		}
		if got.kind != wantKind {
			t.Fatalf("kind = %q, want %q", got.kind, wantKind)
		}
		if got.id != wantID {
			t.Fatalf("id = %q, want %q", got.id, wantID)
		}
	})
}

// TestFindRevokeTarget_Ambiguous_Property verifies that when a search
// argument matches entities in multiple lists, an ambiguity error is returned.
func TestFindRevokeTarget_Ambiguous_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Create a member and an invitation with the same email.
		sharedEmail := fmt.Sprintf("shared-%s@test.local", rapid.StringMatching(`[a-z]{4}`).Draw(t, "email"))

		members := []drive.Member{{MemberID: "m1", Email: sharedEmail}}
		invs := []drive.Invitation{{InvitationID: "inv1", InviteeEmail: sharedEmail}}

		_, err := findRevokeTarget(sharedEmail, members, invs, nil)
		if err == nil {
			t.Fatal("expected ambiguity error, got nil")
		}
		if !strings.Contains(err.Error(), "ambiguous") {
			t.Fatalf("expected ambiguity error, got: %v", err)
		}
	})
}

// TestFindRevokeTarget_NoMatch verifies that a non-matching argument
// returns a "no matching" error.
func TestFindRevokeTarget_NoMatch(t *testing.T) {
	members := []drive.Member{{MemberID: "m1", Email: "alice@test.local"}}
	_, err := findRevokeTarget("nobody@test.local", members, nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no matching") {
		t.Fatalf("expected 'no matching' error, got: %v", err)
	}
}

// TestFindRevokeTarget_ByID verifies lookup by ID (not email).
func TestFindRevokeTarget_ByID(t *testing.T) {
	members := []drive.Member{{MemberID: "m1", Email: "alice@test.local"}}
	got, err := findRevokeTarget("m1", members, nil, nil)
	if err != nil {
		t.Fatalf("findRevokeTarget by ID: %v", err)
	}
	if got.kind != "member" || got.id != "m1" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

// TestFindRevokeTarget_EdgeCases covers additional edge cases for findRevokeTarget.
func TestFindRevokeTarget_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		arg     string
		members []drive.Member
		invs    []drive.Invitation
		exts    []drive.ExternalInvitation
		wantID  string
		wantErr string
	}{
		{
			"empty lists",
			"nobody@test.local",
			nil, nil, nil,
			"",
			"no matching",
		},
		{
			"match by invitation ID",
			"inv-42",
			nil,
			[]drive.Invitation{{InvitationID: "inv-42", InviteeEmail: "bob@test.local"}},
			nil,
			"inv-42",
			"",
		},
		{
			"match by external invitation ID",
			"ext-99",
			nil, nil,
			[]drive.ExternalInvitation{{ExternalInvitationID: "ext-99", InviteeEmail: "ext@test.local"}},
			"ext-99",
			"",
		},
		{
			"ambiguous: same email in member and external",
			"shared@test.local",
			[]drive.Member{{MemberID: "m1", Email: "shared@test.local"}},
			nil,
			[]drive.ExternalInvitation{{ExternalInvitationID: "ext1", InviteeEmail: "shared@test.local"}},
			"",
			"ambiguous",
		},
		{
			"ambiguous: same email in all three lists",
			"all@test.local",
			[]drive.Member{{MemberID: "m1", Email: "all@test.local"}},
			[]drive.Invitation{{InvitationID: "inv1", InviteeEmail: "all@test.local"}},
			[]drive.ExternalInvitation{{ExternalInvitationID: "ext1", InviteeEmail: "all@test.local"}},
			"",
			"ambiguous",
		},
		{
			"member ID matches but email does not",
			"m-special",
			[]drive.Member{{MemberID: "m-special", Email: "other@test.local"}},
			nil, nil,
			"m-special",
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := findRevokeTarget(tt.arg, tt.members, tt.invs, tt.exts)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.id != tt.wantID {
				t.Errorf("id = %q, want %q", got.id, tt.wantID)
			}
		})
	}
}

// TestShareRevokeCmd_RestoreError verifies that runShareRevoke returns
// an error when session restore fails.
func TestShareRevokeCmd_RestoreError(t *testing.T) {
	saveAndRestore(t)
	injectSessionError(fmt.Errorf("not logged in"))

	err := shareRevokeCmd.RunE(shareRevokeCmd, []string{"myshare", "user@test.local"})
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("error = %v, want 'not logged in'", err)
	}
}

// TestShareRevokeCmd_ArgsValidation verifies cobra argument validation.
func TestShareRevokeCmd_ArgsValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"no args", []string{}, true},
		{"one arg", []string{"share"}, true},
		{"two args valid", []string{"share", "user"}, false},
		{"three args", []string{"share", "user", "extra"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := shareRevokeCmd.Args(shareRevokeCmd, tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("Args(%v) error = %v, wantErr %v", tt.args, err, tt.wantErr)
			}
		})
	}
}
