package lumoCmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/major0/proton-cli/api/lumo"
	"github.com/major0/proton-cli/api/shortid"
	cli "github.com/major0/proton-cli/cmd"
	"github.com/spf13/cobra"
)

var spaceCmd = &cobra.Command{
	Use:   "space",
	Short: "Manage Lumo spaces",
	Run: func(cmd *cobra.Command, _ []string) {
		_ = cmd.Help()
	},
}

func init() {
	AddCommand(spaceCmd)
	spaceCmd.AddCommand(spaceListCmd)
	spaceCmd.AddCommand(spaceDeleteCmd)
	spaceCmd.AddCommand(spaceCreateCmd)
	spaceCmd.AddCommand(spaceConfigCmd)
}

// --- space list ---

var spaceShowAll bool
var spaceShowEmpty bool
var spaceShowSimple bool

var spaceListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List spaces (projects only; -A all; --simple chats; --is-empty empty)",
	RunE:    runSpaceList,
}

func init() {
	spaceListCmd.Flags().BoolVarP(&spaceShowAll, "all", "A", false, "Show all spaces including simple chat spaces")
	spaceListCmd.Flags().BoolVar(&spaceShowEmpty, "is-empty", false, "Find and verify empty spaces")
	spaceListCmd.Flags().BoolVar(&spaceShowSimple, "simple", false, "Show simple chat spaces only")
}

func runSpaceList(cmd *cobra.Command, _ []string) error {
	rc := cli.GetContext(cmd)
	ctx := cmd.Context()
	client, err := restoreClient(cmd)
	if err != nil {
		return err
	}

	spaces, err := client.ListSpaces(ctx)
	if err != nil {
		return fmt.Errorf("listing spaces: %w", err)
	}

	if spaceShowEmpty {
		return runSpaceListEmpty(ctx, client, spaces)
	}

	// Filter by type: -A shows all, --simple shows simple, default shows projects.
	if !spaceShowAll {
		if spaceShowSimple {
			spaces = filterSimpleSpaces(ctx, client, spaces)
		} else {
			spaces = filterProjectSpaces(ctx, client, spaces)
		}
	}

	// Hide deleted and empty spaces unless -A is set.
	if !spaceShowAll {
		var visible []lumo.Space
		for _, s := range spaces {
			if s.DeleteTime != "" {
				continue
			}
			if len(s.Conversations) == 0 {
				continue
			}
			visible = append(visible, s)
		}
		spaces = visible
	}

	rows := buildSpaceRows(ctx, client, spaces)

	// Apply short IDs.
	if rc.Verbose < 1 && len(rows) > 0 {
		ids := make([]string, len(rows))
		for i := range rows {
			ids[i] = rows[i].ID
		}
		short := shortid.Format(ids)
		for i := range rows {
			if s, ok := short[rows[i].ID]; ok {
				rows[i].ID = s
			}
		}
	}

	_, _ = fmt.Fprint(os.Stdout, FormatSpaceList(rows))
	return nil
}

// runSpaceListEmpty identifies, verifies, and lists empty spaces.
func runSpaceListEmpty(ctx context.Context, client *lumo.Client, spaces []lumo.Space) error {
	total := len(spaces)

	// Phase 1: identify candidates (0 embedded conversations).
	var candidates []lumo.Space
	for _, s := range spaces {
		if len(s.Conversations) == 0 {
			candidates = append(candidates, s)
		}
	}

	_, _ = fmt.Fprintf(os.Stderr, "Scanning %d spaces, %d candidates with 0 embedded conversations...\n", total, len(candidates))

	// Phase 2: verify each candidate and filter by type.
	type emptySpace struct {
		space     lumo.Space
		spaceType string // "simple" or "project" or "unencrypted"
	}
	var verified []emptySpace

	for _, s := range candidates {
		// Verify no assets either.
		if len(s.Assets) > 0 {
			return fmt.Errorf("space %s has 0 conversations but %d assets — not empty", s.ID, len(s.Assets))
		}

		// Hide deleted spaces unless -A is set.
		if !spaceShowAll && s.DeleteTime != "" {
			continue
		}

		stype := classifySpace(ctx, client, &s)

		// Filter by type: --simple shows simple only,
		// default (no --simple) shows projects only,
		// -A relaxes the type filter to show all types.
		if !spaceShowAll {
			if spaceShowSimple && stype != "simple" {
				continue
			}
			if !spaceShowSimple && stype != "project" {
				continue
			}
		}

		verified = append(verified, emptySpace{space: s, spaceType: stype})
	}

	// Phase 3: list verified empty spaces.
	if len(verified) == 0 {
		_, _ = fmt.Fprintf(os.Stdout, "No empty spaces found (%d total).\n", total)
		return nil
	}

	// Sort newest first.
	sort.Slice(verified, func(i, j int) bool {
		return verified[i].space.CreateTime > verified[j].space.CreateTime
	})

	var b strings.Builder
	fmt.Fprintf(&b, "%-12s  %-19s  %s\n", "TYPE", "CREATED", "ID")
	for _, v := range verified {
		fmt.Fprintf(&b, "%-12s  %-19s  %s\n", v.spaceType, fmtLocalTime(v.space.CreateTime), v.space.ID)
	}
	fmt.Fprintf(&b, "\n%d empty spaces found out of %d total.\n", len(verified), total)

	// Detailed breakdown for cross-checking against the webapp.
	simpleActive, simpleDeleted := 0, 0
	projectActive, projectDeleted := 0, 0
	unknownActive, unknownDeleted := 0, 0
	totalConvs, deletedConvs := 0, 0
	simpleConvs, projectConvs, unknownConvs := 0, 0, 0
	for _, s := range spaces {
		stype := classifySpace(ctx, client, &s)
		deleted := s.DeleteTime != ""

		switch stype {
		case "simple":
			if deleted {
				simpleDeleted++
			} else {
				simpleActive++
			}
		case "project":
			if deleted {
				projectDeleted++
			} else {
				projectActive++
			}
		default:
			if deleted {
				unknownDeleted++
			} else {
				unknownActive++
			}
		}

		for _, c := range s.Conversations {
			totalConvs++
			if c.DeleteTime != "" {
				deletedConvs++
				continue
			}
			switch stype {
			case "project":
				projectConvs++
			case "simple":
				simpleConvs++
			default:
				unknownConvs++
			}
		}
	}

	totalSimple := simpleActive + simpleDeleted
	totalProject := projectActive + projectDeleted
	totalUnknown := unknownActive + unknownDeleted

	fmt.Fprintf(&b, "\nSimple spaces:  %d (active: %d, deleted: %d, conversations: %d)\n", totalSimple, simpleActive, simpleDeleted, simpleConvs)
	fmt.Fprintf(&b, "Project spaces: %d (active: %d, deleted: %d, conversations: %d)\n", totalProject, projectActive, projectDeleted, projectConvs)
	if totalUnknown > 0 {
		fmt.Fprintf(&b, "Unknown spaces: %d (active: %d, deleted: %d, conversations: %d)\n", totalUnknown, unknownActive, unknownDeleted, unknownConvs)
	}
	fmt.Fprintf(&b, "Total conversations: %d (active: %d, deleted: %d)\n", totalConvs, totalConvs-deletedConvs, deletedConvs)
	fmt.Fprintf(&b, "\nBrowser History should show: %d simple chats\n", simpleConvs)
	_, _ = fmt.Fprint(os.Stdout, b.String())
	return nil
}

// classifySpace returns "project", "simple", or "unknown" based on the
// space's encrypted metadata. Unencrypted spaces are "simple". Spaces
// that can't be decrypted are "unknown".
func classifySpace(ctx context.Context, client *lumo.Client, s *lumo.Space) string {
	if s.Encrypted == "" {
		return "simple"
	}
	priv, err := client.DecryptSpacePriv(ctx, s)
	if err != nil {
		return "unknown"
	}
	if priv.IsProject != nil && *priv.IsProject {
		return "project"
	}
	return "simple"
}

// SpaceRow is a display-only struct for the space list table.
type SpaceRow struct {
	ID         string
	CreateTime string
	ConvCount  int
	Encrypted  bool
	Deleted    bool
	Name       string // decrypted name, "(empty)", or "(encrypted)"
}

// buildSpaceRows converts API spaces into display rows, decrypting
// names where possible. The decrypted name is used only for display
// and discarded after rendering.
func buildSpaceRows(ctx context.Context, client *lumo.Client, spaces []lumo.Space) []SpaceRow {
	rows := make([]SpaceRow, len(spaces))
	for i, s := range spaces {
		name := decryptSpaceName(ctx, client, &s)
		if s.DeleteTime != "" {
			name = "(deleted)"
		}
		rows[i] = SpaceRow{
			ID:         s.ID,
			CreateTime: s.CreateTime,
			ConvCount:  len(s.Conversations),
			Encrypted:  s.Encrypted != "",
			Deleted:    s.DeleteTime != "",
			Name:       name,
		}
	}
	return rows
}

// filterProjectSpaces returns only spaces that are project spaces
// (isProject=true in encrypted metadata). Simple chat spaces and
// spaces that can't be decrypted are excluded.
func filterProjectSpaces(ctx context.Context, client *lumo.Client, spaces []lumo.Space) []lumo.Space {
	var result []lumo.Space
	for i := range spaces {
		if classifySpace(ctx, client, &spaces[i]) == "project" {
			result = append(result, spaces[i])
		}
	}
	return result
}

// filterSimpleSpaces returns only spaces confirmed as simple chat spaces.
// Spaces that can't be decrypted ("unknown") are excluded.
func filterSimpleSpaces(ctx context.Context, client *lumo.Client, spaces []lumo.Space) []lumo.Space {
	var result []lumo.Space
	for i := range spaces {
		if classifySpace(ctx, client, &spaces[i]) == "simple" {
			result = append(result, spaces[i])
		}
	}
	return result
}

// decryptSpaceName resolves a display name for a space. For project
// spaces, uses the ProjectName from encrypted metadata. For simple
// spaces (no ProjectName), uses the title of the first conversation.
// Returns "(empty)" when nothing is available, "(encrypted)" on
// decryption failure.
func decryptSpaceName(ctx context.Context, client *lumo.Client, s *lumo.Space) string {
	if s.Encrypted == "" {
		return "(empty)"
	}

	priv, err := client.DecryptSpacePriv(ctx, s)
	if err != nil {
		return "(encrypted)"
	}

	if priv.ProjectName != "" {
		return priv.ProjectName
	}

	// Simple space — derive name from the first conversation's title.
	dek, err := client.DeriveSpaceDEK(ctx, s)
	if err != nil {
		return "(encrypted)"
	}
	for _, c := range s.Conversations {
		if c.Encrypted == "" || c.DeleteTime != "" {
			continue
		}
		title := decryptConversationTitle(c, dek, s.SpaceTag)
		if title != "" {
			return title
		}
	}

	return "Untitled"
}

// FormatSpaceList renders a tab-aligned table of spaces sorted by
// creation time descending (newest first).
func FormatSpaceList(rows []SpaceRow) string {
	if len(rows) == 0 {
		return "No spaces found.\n"
	}

	sorted := make([]SpaceRow, len(rows))
	copy(sorted, rows)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].CreateTime > sorted[j].CreateTime
	})

	// Compute ID column width from the longest ID in the set.
	idWidth := 2 // minimum "ID" header
	for _, r := range sorted {
		if len(r.ID) > idWidth {
			idWidth = len(r.ID)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%-*s  %-19s  %5s  %s\n", idWidth, "ID", "CREATED", "CONVS", "NAME")
	for _, r := range sorted {
		fmt.Fprintf(&b, "%-*s  %-19s  %5d  %s\n", idWidth, r.ID, fmtLocalTime(r.CreateTime), r.ConvCount, r.Name)
	}
	return b.String()
}

// --- space delete ---

var spaceForceDelete bool

var spaceDeleteCmd = &cobra.Command{
	Use:   "delete <space-id> [<space-id>...]",
	Short: "Delete spaces (must be empty unless -f)",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runSpaceDelete,
}

func init() {
	spaceDeleteCmd.Flags().BoolVarP(&spaceForceDelete, "force", "f", false, "Delete even if the space has conversations or assets")
}

func runSpaceDelete(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, err := restoreClient(cmd)
	if err != nil {
		return err
	}

	// Always load spaces for short ID resolution.
	spaces, err := client.ListSpaces(ctx)
	if err != nil {
		return fmt.Errorf("listing spaces: %w", err)
	}

	// Build ID set for short ID resolution.
	spaceIDs := make([]string, len(spaces))
	for i, s := range spaces {
		spaceIDs[i] = s.ID
	}

	for _, arg := range args {
		spaceID, resolveErr := shortid.Resolve(spaceIDs, arg)
		if resolveErr != nil {
			return fmt.Errorf("resolving space: %w", resolveErr)
		}

		if !spaceForceDelete {
			for _, s := range spaces {
				if s.ID == spaceID {
					if len(s.Conversations) > 0 {
						return fmt.Errorf("space %s has %d conversations — use -f to force delete", spaceID, len(s.Conversations))
					}
					if len(s.Assets) > 0 {
						return fmt.Errorf("space %s has %d assets — use -f to force delete", spaceID, len(s.Assets))
					}
					break
				}
			}
		}

		if err := client.DeleteSpace(ctx, spaceID); err != nil {
			return fmt.Errorf("deleting space %s: %w", spaceID, err)
		}
		_, _ = fmt.Fprintf(os.Stderr, "Space %s deleted.\n", spaceID)
	}

	return nil
}

// --- space create ---

var spaceCreateProject bool

var spaceCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new space",
	Args:  cobra.ExactArgs(1),
	RunE:  runSpaceCreate,
}

func init() {
	spaceCreateCmd.Flags().BoolVar(&spaceCreateProject, "project", false, "Create a project space")
}

func runSpaceCreate(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, err := restoreClient(cmd)
	if err != nil {
		return err
	}

	name := args[0]
	space, err := client.CreateSpace(ctx, name, spaceCreateProject)
	if err != nil {
		return fmt.Errorf("creating space: %w", err)
	}

	_, _ = fmt.Fprintln(os.Stdout, space.ID)
	return nil
}

// --- space config ---

var (
	spaceConfigName         string
	spaceConfigInstructions string
	spaceConfigIcon         string
)

var spaceConfigCmd = &cobra.Command{
	Use:   "config <space-id>",
	Short: "View or update space configuration",
	Args:  cobra.ExactArgs(1),
	RunE:  runSpaceConfig,
}

func init() {
	spaceConfigCmd.Flags().StringVar(&spaceConfigName, "name", "", "Set the space name")
	spaceConfigCmd.Flags().StringVar(&spaceConfigInstructions, "instructions", "", "Set project instructions")
	spaceConfigCmd.Flags().StringVar(&spaceConfigIcon, "icon", "", "Set the space icon")
}

func runSpaceConfig(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, err := restoreClient(cmd)
	if err != nil {
		return err
	}

	spaceID := args[0]

	// Find the space via ListSpaces (GetSpace is unreliable).
	spaces, err := client.ListSpaces(ctx)
	if err != nil {
		return fmt.Errorf("listing spaces: %w", err)
	}

	// Resolve short ID.
	spaceIDs := make([]string, len(spaces))
	for i, s := range spaces {
		spaceIDs[i] = s.ID
	}
	spaceID, err = shortid.Resolve(spaceIDs, spaceID)
	if err != nil {
		return fmt.Errorf("resolving space: %w", err)
	}

	var space *lumo.Space
	for i := range spaces {
		if spaces[i].ID == spaceID {
			space = &spaces[i]
			break
		}
	}
	if space == nil {
		return fmt.Errorf("space %s not found", spaceID)
	}

	priv, err := client.DecryptSpacePriv(ctx, space)
	if err != nil {
		return fmt.Errorf("decrypting space config: %w", err)
	}

	// No flags set — view mode.
	hasUpdate := cmd.Flags().Changed("name") || cmd.Flags().Changed("instructions") || cmd.Flags().Changed("icon")
	if !hasUpdate {
		return printSpaceConfig(priv)
	}

	// Update mode — modify the specified fields.
	if cmd.Flags().Changed("name") {
		priv.ProjectName = spaceConfigName
	}
	if cmd.Flags().Changed("instructions") {
		priv.ProjectInstructions = spaceConfigInstructions
	}
	if cmd.Flags().Changed("icon") {
		priv.ProjectIcon = spaceConfigIcon
	}

	encrypted, err := client.EncryptSpacePriv(ctx, space, priv)
	if err != nil {
		return fmt.Errorf("encrypting space config: %w", err)
	}

	if err := client.UpdateSpace(ctx, spaceID, lumo.UpdateSpaceReq{Encrypted: encrypted}); err != nil {
		return fmt.Errorf("updating space: %w", err)
	}

	_, _ = fmt.Fprintf(os.Stderr, "Space %s updated.\n", spaceID)
	return nil
}

// printSpaceConfig prints the decrypted SpacePriv fields.
func printSpaceConfig(priv *lumo.SpacePriv) error {
	var b strings.Builder
	if priv.IsProject != nil && *priv.IsProject {
		fmt.Fprintf(&b, "Type:         project\n")
	} else {
		fmt.Fprintf(&b, "Type:         simple\n")
	}
	if priv.ProjectName != "" {
		fmt.Fprintf(&b, "Name:         %s\n", priv.ProjectName)
	}
	if priv.ProjectInstructions != "" {
		fmt.Fprintf(&b, "Instructions: %s\n", priv.ProjectInstructions)
	}
	if priv.ProjectIcon != "" {
		fmt.Fprintf(&b, "Icon:         %s\n", priv.ProjectIcon)
	}
	if priv.LinkedDriveFolder != nil {
		fmt.Fprintf(&b, "Drive Folder: %s (%s)\n", priv.LinkedDriveFolder.FolderName, priv.LinkedDriveFolder.FolderPath)
	}
	_, _ = fmt.Fprint(os.Stdout, b.String())
	return nil
}
