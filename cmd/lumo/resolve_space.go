package lumoCmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/major0/proton-cli/api/lumo"
)

// spaceNameMatch pairs a space with its decrypted name for disambiguation.
type spaceNameMatch struct {
	space *lumo.Space
	name  string
}

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

	// Step 2: try ID prefix resolution.
	ids := make([]string, len(active))
	for i := range active {
		ids[i] = active[i].ID
	}

	resolved, err := resolveShortID(ids, query)
	if err == nil {
		for i := range active {
			if active[i].ID == resolved {
				return &active[i], nil
			}
		}
	}

	// If ambiguous ID match, return that error directly.
	var ambErr *shortIDAmbiguousError
	if errors.As(err, &ambErr) {
		return nil, fmt.Errorf("space %s", ambErr.Error())
	}

	// Step 3: try case-insensitive exact name match.
	lowerQuery := strings.ToLower(query)
	var exactMatches []spaceNameMatch
	var substringMatches []spaceNameMatch

	for i := range active {
		name := decryptName(&active[i])
		if name == "" {
			continue
		}
		lowerName := strings.ToLower(name)
		if lowerName == lowerQuery {
			exactMatches = append(exactMatches, spaceNameMatch{space: &active[i], name: name})
		} else if strings.Contains(lowerName, lowerQuery) {
			substringMatches = append(substringMatches, spaceNameMatch{space: &active[i], name: name})
		}
	}

	// Exact match wins over substring.
	if len(exactMatches) == 1 {
		return exactMatches[0].space, nil
	}
	if len(exactMatches) > 1 {
		return nil, spaceAmbiguousError(query, exactMatches)
	}

	// Step 4: try case-insensitive substring match.
	if len(substringMatches) == 1 {
		return substringMatches[0].space, nil
	}
	if len(substringMatches) > 1 {
		return nil, spaceAmbiguousError(query, substringMatches)
	}

	return nil, fmt.Errorf("no space matching %q", query)
}

// spaceAmbiguousError formats an error listing ambiguous space matches.
func spaceAmbiguousError(query string, matches []spaceNameMatch) error {
	var b strings.Builder
	fmt.Fprintf(&b, "multiple spaces match %q:", query)
	for _, m := range matches {
		fmt.Fprintf(&b, "\n  %s  %s", m.space.ID, m.name)
	}
	return fmt.Errorf("%s", b.String())
}
