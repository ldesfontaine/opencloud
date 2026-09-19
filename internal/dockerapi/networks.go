package dockerapi

import "context"

// Network est un réseau de la machine tel que la liste le donne. La liste
// ne nomme pas ses membres : l'appartenance se lit sur chaque conteneur,
// arrêtés compris, ce que l'inspect d'un réseau ne dirait pas.
type Network struct {
	ID       string `json:"Id"`
	Name     string
	Driver   string
	Scope    string
	Internal bool
	Labels   map[string]string
}

func (c *Client) ListNetworks(ctx context.Context) ([]Network, error) {
	var list []Network
	if err := c.getJSON(ctx, "/networks", &list); err != nil {
		return nil, err
	}
	return list, nil
}
