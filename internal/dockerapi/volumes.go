package dockerapi

import "context"

// Volume est un volume nommé de la machine tel que la liste le donne. Ni le
// point de montage ni la taille : on ne lit que le nom et le pilote.
type Volume struct {
	Name   string
	Driver string
}

// ListVolumes rend les volumes de la machine.
func (c *Client) ListVolumes(ctx context.Context) ([]Volume, error) {
	var listed struct {
		Volumes []Volume
	}
	if err := c.getJSON(ctx, "/volumes", &listed); err != nil {
		return nil, err
	}
	return listed.Volumes, nil
}
