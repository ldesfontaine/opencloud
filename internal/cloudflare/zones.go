package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// Zone : ce qu'openCloud lit d'une zone. Cloudflare en rend davantage ; rien
// d'autre ne sert ici.
type Zone struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// ZoneByName trouve une zone par son nom exact.
//
// GET /zones?name=exemple.com rend, d'après la documentation
// (https://developers.cloudflare.com/api/resources/zones/methods/list/) :
//
//	{"success": true, "errors": [], "messages": [],
//	 "result": [{"id": "023e105f…", "name": "example.com", "status": "active"}],
//	 "result_info": {"count": 1, …}}
//
// Une liste vide veut dire « pas cette zone sur ce compte », et c'est un
// résultat, pas une panne.
func (c *Client) ZoneByName(ctx context.Context, token, name string) (Zone, error) {
	query := url.Values{}
	query.Set("name", name)

	result, err := c.call(ctx, token, http.MethodGet, "/zones", query, nil)
	if err != nil {
		var apiError *APIError
		if errors.As(err, &apiError) && apiError.Unauthorized() {
			return Zone{}, ErrInvalidToken
		}
		return Zone{}, err
	}

	var zones []Zone
	if err := json.Unmarshal(result, &zones); err != nil {
		return Zone{}, ErrUnreadableReply
	}
	for _, zone := range zones {
		// Le filtre de Cloudflare est un préfixe sur certaines versions : on
		// n'accepte que le nom exact.
		if zone.Name == name {
			return zone, nil
		}
	}
	return Zone{}, fmt.Errorf("%w: %s", ErrZoneNotFound, name)
}
