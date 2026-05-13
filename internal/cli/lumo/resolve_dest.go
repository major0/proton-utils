package lumoCmd

import (
	"context"
	"fmt"

	"github.com/major0/proton-utils/api/lumo"
)

// ResolvedDestination holds the resolved destination for a copy operation.
type ResolvedDestination struct {
	Space *lumo.Space
	DEK   []byte
	Title string
	IsNew bool // true if a new space was created
}

// resolveDestTitle derives the destination conversation title.
// If path is empty, returns srcTitle + " (copy)"; otherwise returns path verbatim.
func resolveDestTitle(path, srcTitle string) string {
	if path == "" {
		return srcTitle + " (copy)"
	}
	return path
}

// resolveDestination resolves a parsed destination URI to a space, DEK, and title.
// If Space component is empty, creates a new simple space.
// If Path component is empty, uses srcTitle + " (copy)".
//
// This is the integration point that wires the callback-based resolveSpace
// to the real client. It calls client.CreateSpace, client.DeriveSpaceDEK,
// and passes a decryptName closure (over ctx/client) to resolveSpace.
func resolveDestination(
	ctx context.Context,
	client *lumo.Client,
	spaces []lumo.Space,
	dest LumoURI,
	srcTitle string,
) (*ResolvedDestination, error) {
	title := resolveDestTitle(dest.Path, srcTitle)

	if dest.Space == "" {
		// Create a new simple space.
		space, err := client.CreateSpace(ctx, "", false)
		if err != nil {
			return nil, fmt.Errorf("create destination space: %w", err)
		}

		dek, err := client.DeriveSpaceDEK(ctx, space)
		if err != nil {
			return nil, fmt.Errorf("derive destination DEK: %w", err)
		}

		return &ResolvedDestination{
			Space: space,
			DEK:   dek,
			Title: title,
			IsNew: true,
		}, nil
	}

	// Resolve existing space.
	decryptName := func(s *lumo.Space) string {
		return decryptSpaceName(ctx, client, s)
	}

	space, err := resolveSpace(spaces, dest.Space, decryptName)
	if err != nil {
		return nil, fmt.Errorf("resolve destination space: %w", err)
	}

	dek, err := client.DeriveSpaceDEK(ctx, space)
	if err != nil {
		return nil, fmt.Errorf("derive destination DEK: %w", err)
	}

	return &ResolvedDestination{
		Space: space,
		DEK:   dek,
		Title: title,
		IsNew: false,
	}, nil
}
