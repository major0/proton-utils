package lumoCmd

import (
	"fmt"

	"github.com/major0/proton-cli/api/lumo"
	cli "github.com/major0/proton-cli/internal/cli"
)

// resolveSpace resolves a space component string to a single Space.
// Resolution order: ID prefix → exact name (case-insensitive) → substring name.
// Excludes deleted spaces. Returns error on zero or ambiguous matches.
//
// The decryptName callback decouples space name decryption from the client,
// enabling property-based testing with synthetic data. In production, the
// caller passes a closure over (ctx, client) that calls decryptSpaceName.
func resolveSpace(
	spaces []lumo.Space,
	query string,
	decryptName func(*lumo.Space) string,
) (*lumo.Space, error) {
	// Step 1: filter out deleted spaces.
	var active []lumo.Space
	for _, s := range spaces {
		if s.DeleteTime == "" {
			active = append(active, s)
		}
	}

	// Step 2: resolve via ResolveEntity (ID prefix → exact name → substring).
	ids := make([]string, len(active))
	for i := range active {
		ids[i] = active[i].ID
	}

	nameFunc := func(i int) string {
		return decryptName(&active[i])
	}

	idx, err := cli.ResolveEntity(ids, query, nameFunc)
	if err != nil {
		return nil, fmt.Errorf("space: %w", err)
	}
	return &active[idx], nil
}
