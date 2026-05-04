package drive

import (
	"context"
)

// ListVolumes returns all volumes accessible by this session.
func (c *Client) ListVolumes(ctx context.Context) ([]Volume, error) {
	pVolumes, err := c.Session.Client.ListVolumes(ctx)
	if err != nil {
		return nil, err
	}

	volumes := make([]Volume, len(pVolumes))
	for i := range pVolumes {
		volumes[i] = Volume{ProtonVolume: pVolumes[i]}
	}

	return volumes, nil
}

// GetVolume returns the volume with the given ID.
func (c *Client) GetVolume(ctx context.Context, id string) (Volume, error) {
	pVolume, err := c.Session.Client.GetVolume(ctx, id)
	if err != nil {
		return Volume{}, err
	}

	return Volume{ProtonVolume: pVolume}, nil
}
