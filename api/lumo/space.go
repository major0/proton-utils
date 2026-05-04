package lumo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/major0/proton-cli/api"
)

// ListSpaces fetches all spaces from the API, paginating with
// CreateTimeUntil until no more results are returned. Deduplicates
// by ID since page boundaries can overlap when spaces share a
// CreateTime.
func (c *Client) ListSpaces(ctx context.Context) ([]Space, error) {
	var all []Space
	seen := map[string]bool{}
	var cursor string

	for {
		url := c.url("/lumo/v1/spaces")
		if cursor != "" {
			url += "?CreateTimeUntil=" + cursor
		}

		var resp ListSpacesResponse
		if err := c.Session.DoJSON(ctx, "GET", url, nil, &resp); err != nil {
			return nil, fmt.Errorf("lumo: list spaces: %w", err)
		}

		if len(resp.Spaces) == 0 {
			break
		}

		newCount := 0
		for _, s := range resp.Spaces {
			if seen[s.ID] {
				continue
			}
			seen[s.ID] = true
			all = append(all, s)
			newCount++
		}

		// No new spaces on this page — we've exhausted the results.
		if newCount == 0 {
			break
		}

		// Use the last space's CreateTime as the pagination cursor.
		last := resp.Spaces[len(resp.Spaces)-1]
		nextCursor := dateToUnix(last.CreateTime)
		if nextCursor == "" || nextCursor == cursor {
			break
		}
		cursor = nextCursor
	}

	return all, nil
}

// dateToUnix converts an ISO 8601 timestamp to a Unix timestamp string
// for the CreateTimeUntil pagination parameter.
func dateToUnix(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d", t.Unix())
}

// GetSpace fetches a single space by ID.
func (c *Client) GetSpace(ctx context.Context, spaceID string) (*Space, error) {
	var resp GetSpaceResponse
	err := c.Session.DoJSON(ctx, "GET", c.url("/lumo/v1/spaces/"+spaceID), nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("lumo: get space: %w", mapCRUDError(err))
	}
	return &resp.Space, nil
}

// CreateSpace creates a new space with an encrypted name and generated
// space key. The isProject flag is stored in the encrypted metadata.
func (c *Client) CreateSpace(ctx context.Context, name string, isProject bool) (*Space, error) {
	masterKey, err := c.GetMasterKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("lumo: create space: %w", err)
	}

	spaceKey, err := GenerateSpaceKey()
	if err != nil {
		return nil, fmt.Errorf("lumo: create space: %w", err)
	}

	wrapped, err := WrapSpaceKey(masterKey, spaceKey)
	if err != nil {
		return nil, fmt.Errorf("lumo: create space: %w", err)
	}

	spaceTag := GenerateTag()

	// Build and encrypt SpacePriv metadata.
	// Simple chat spaces use "{}" (matching the browser). Project spaces
	// include the full SpacePriv with isProject and projectName.
	var privJSON []byte
	if isProject {
		priv := SpacePriv{IsProject: &isProject, ProjectName: name}
		privJSON, err = json.Marshal(priv)
		if err != nil {
			return nil, fmt.Errorf("lumo: create space: marshal priv: %w", err)
		}
	} else {
		privJSON = []byte("{}")
	}

	dek, err := DeriveDataEncryptionKey(spaceKey)
	if err != nil {
		return nil, fmt.Errorf("lumo: create space: %w", err)
	}

	ad := SpaceAD(spaceTag)
	encrypted, err := EncryptString(string(privJSON), dek, ad)
	if err != nil {
		return nil, fmt.Errorf("lumo: create space: %w", err)
	}

	req := CreateSpaceReq{
		SpaceKey:  base64.StdEncoding.EncodeToString(wrapped),
		SpaceTag:  spaceTag,
		Encrypted: encrypted,
	}

	var resp GetSpaceResponse
	err = c.Session.DoJSON(ctx, "POST", c.url("/lumo/v1/spaces"), req, &resp)
	if err != nil {
		return nil, fmt.Errorf("lumo: create space: %w", mapCRUDError(err))
	}
	return &resp.Space, nil
}

// UpdateSpace updates a space's encrypted metadata.
func (c *Client) UpdateSpace(ctx context.Context, spaceID string, req UpdateSpaceReq) error {
	err := c.Session.DoJSON(ctx, "PUT", c.url("/lumo/v1/spaces/"+spaceID), req, nil)
	if err != nil {
		return fmt.Errorf("lumo: update space: %w", mapCRUDError(err))
	}
	return nil
}

// DecryptSpacePriv decrypts a space's encrypted metadata.
func (c *Client) DecryptSpacePriv(ctx context.Context, space *Space) (*SpacePriv, error) {
	if space.Encrypted == "" {
		return &SpacePriv{}, nil
	}

	dek, err := c.deriveSpaceDEK(ctx, space)
	if err != nil {
		return nil, fmt.Errorf("lumo: decrypt space priv: %w", err)
	}

	ad := SpaceAD(space.SpaceTag)
	privJSON, err := DecryptString(space.Encrypted, dek, ad)
	if err != nil {
		return nil, fmt.Errorf("lumo: decrypt space priv: %w", err)
	}

	var priv SpacePriv
	if err := json.Unmarshal([]byte(privJSON), &priv); err != nil {
		return nil, fmt.Errorf("lumo: decrypt space priv: unmarshal: %w", err)
	}
	return &priv, nil
}

// EncryptSpacePriv encrypts SpacePriv metadata for a space.
func (c *Client) EncryptSpacePriv(ctx context.Context, space *Space, priv *SpacePriv) (string, error) {
	dek, err := c.deriveSpaceDEK(ctx, space)
	if err != nil {
		return "", fmt.Errorf("lumo: encrypt space priv: %w", err)
	}

	privJSON, err := json.Marshal(priv)
	if err != nil {
		return "", fmt.Errorf("lumo: encrypt space priv: marshal: %w", err)
	}

	ad := SpaceAD(space.SpaceTag)
	encrypted, err := EncryptString(string(privJSON), dek, ad)
	if err != nil {
		return "", fmt.Errorf("lumo: encrypt space priv: %w", err)
	}
	return encrypted, nil
}

// DeleteSpace deletes a space by ID.
func (c *Client) DeleteSpace(ctx context.Context, spaceID string) error {
	err := c.Session.DoJSON(ctx, "DELETE", c.url("/lumo/v1/spaces/"+spaceID), nil, nil)
	if err != nil {
		return fmt.Errorf("lumo: delete space: %w", mapCRUDError(err))
	}
	return nil
}

// GetDefaultSpace returns the first simple space (isProject != true).
// If none exists, it creates one automatically.
func (c *Client) GetDefaultSpace(ctx context.Context) (*Space, error) {
	spaces, err := c.ListSpaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("lumo: get default space: %w", err)
	}

	for i := range spaces {
		s := &spaces[i]
		if s.Encrypted == "" {
			// No metadata — treat as simple space.
			return s, nil
		}

		isProject, err := c.isProjectSpace(ctx, s)
		if err != nil {
			continue // skip spaces we can't decrypt
		}
		if !isProject {
			return s, nil
		}
	}

	// No simple space found — create one.
	space, err := c.CreateSpace(ctx, "", false)
	if err != nil {
		return nil, fmt.Errorf("lumo: get default space: %w", err)
	}
	return space, nil
}

// isProjectSpace decrypts a space's metadata and returns whether it's
// a project space. The decrypted content is discarded after inspection.
func (c *Client) isProjectSpace(ctx context.Context, s *Space) (bool, error) {
	priv, err := c.DecryptSpacePriv(ctx, s)
	if err != nil {
		return false, err
	}
	return priv.IsProject != nil && *priv.IsProject, nil
}

// mapCRUDError maps API errors to CRUD-specific sentinels.
func mapCRUDError(err error) error {
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		return err
	}
	if apiErr.Status == 422 && apiErr.Code == 2501 {
		return ErrNotFound
	}
	if apiErr.Status == 409 {
		return ErrConflict
	}
	return err
}
