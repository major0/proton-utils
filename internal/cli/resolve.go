package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/major0/proton-cli/internal/cli/shortid"
)

// ResolveEntity resolves a query string to a unique entity index.
// Resolution order: ID prefix → exact name (case-insensitive) → substring name.
//
// Parameters:
//   - ids: slice of entity IDs (same order as the entity collection)
//   - query: user-provided search string
//   - name: callback returning the display name for entity at index i
//
// Returns the index of the matched entity, or an error.
func ResolveEntity(ids []string, query string, name func(int) string) (int, error) {
	// Step 1: ID prefix resolution.
	resolved, err := shortid.Resolve(ids, query)
	if err == nil {
		for i, id := range ids {
			if id == resolved {
				return i, nil
			}
		}
	}
	// Ambiguous ID → return directly, no name fallback.
	var ambErr *shortid.AmbiguousError
	if errors.As(err, &ambErr) {
		return -1, ambErr
	}

	// Step 2: case-insensitive exact name match.
	lowerQuery := strings.ToLower(query)
	var exactMatches []int
	var substringMatches []int

	for i := range ids {
		n := name(i)
		if n == "" {
			continue
		}
		lower := strings.ToLower(n)
		if lower == lowerQuery {
			exactMatches = append(exactMatches, i)
		} else if strings.Contains(lower, lowerQuery) {
			substringMatches = append(substringMatches, i)
		}
	}

	if len(exactMatches) == 1 {
		return exactMatches[0], nil
	}
	if len(exactMatches) > 1 {
		return -1, ambiguousNameError(query, exactMatches, name)
	}

	// Step 3: substring match.
	if len(substringMatches) == 1 {
		return substringMatches[0], nil
	}
	if len(substringMatches) > 1 {
		return -1, ambiguousNameError(query, substringMatches, name)
	}

	return -1, fmt.Errorf("no match for %q", query)
}

func ambiguousNameError(query string, indices []int, name func(int) string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "multiple matches for %q:", query)
	for _, i := range indices {
		fmt.Fprintf(&b, "\n  %s", name(i))
	}
	return fmt.Errorf("%s", b.String())
}
