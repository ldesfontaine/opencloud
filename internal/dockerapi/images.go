package dockerapi

import (
	"context"
	"net/url"
	"strings"
)

// Image est une image inspectée ; seuls les champs lus sont déclarés.
// RepoDigests note, par dépôt, l'empreinte de ce que la machine a tiré :
// « alpine@sha256:… ». Vide pour une image construite sur place.
type Image struct {
	ID          string `json:"Id"`
	RepoTags    []string
	RepoDigests []string
}

// PulledDigest rend l'empreinte tirée pour ce dépôt, tel que Docker le
// nomme sans le registre par défaut ; vide si aucune.
func (i Image) PulledDigest(repository string) string {
	for _, entry := range i.RepoDigests {
		name, digest, ok := strings.Cut(entry, "@")
		if ok && name == repository {
			return digest
		}
	}
	return ""
}

// InspectImage lit une image par son nom ou son identifiant.
func (c *Client) InspectImage(ctx context.Context, reference string) (Image, error) {
	var image Image
	if err := c.getJSON(ctx, "/images/"+url.PathEscape(reference)+"/json", &image); err != nil {
		return Image{}, err
	}
	return image, nil
}
