package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Repository est le dépôt des releases, fixé dans le binaire : l'attestation
// prouve que le fichier a été construit par le workflow de ce dépôt, ce qui
// n'a de sens que si le dépôt n'est pas au choix de la configuration.
const Repository = "ldesfontaine/opencloud"

const (
	githubAPIBaseURL = "https://api.github.com"
	githubAPIVersion = "2022-11-28"

	apiTimeout      = 30 * time.Second
	downloadTimeout = 10 * time.Minute

	// Bornes de lecture : ce que l'API rend, ce qu'un binaire pèse, ce qu'un
	// SHA256SUMS ou un lot d'attestations occupe. Au-delà, ce n'est pas ça.
	maxAPIResponseBytes = 1 << 20
	maxAttestationBytes = 8 << 20
	maxBinaryBytes      = 128 << 20
	maxChecksumsBytes   = 64 << 10
)

var (
	// ErrNotFound : la release, le fichier ou l'attestation n'existe pas.
	ErrNotFound = errors.New("not found on github")
	// ErrUnauthorized : GitHub refuse — dépôt privé sans jeton, ou jeton invalide.
	ErrUnauthorized = errors.New("github refused the request: token required or invalid")
)

// Client parle à l'API GitHub : releases, fichiers, attestations. Le jeton
// n'est nécessaire que tant que le dépôt est privé.
type Client struct {
	httpClient *http.Client
	baseURL    string
	repository string
	token      string
	userAgent  string
}

func NewClient(token, userAgent string) *Client {
	return &Client{
		httpClient: &http.Client{},
		baseURL:    githubAPIBaseURL,
		repository: Repository,
		token:      token,
		userAgent:  userAgent,
	}
}

// Release est une release publiée et ses fichiers.
type Release struct {
	Version Version
	Assets  []Asset
}

// Asset est un fichier de release. URL est son adresse dans l'API : avec
// Accept: application/octet-stream, GitHub redirige vers le contenu.
type Asset struct {
	Name string
	URL  string
	Size int64
}

// Asset retrouve un fichier de la release par son nom.
func (r Release) Asset(name string) (Asset, bool) {
	for _, asset := range r.Assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return Asset{}, false
}

// Ce que l'API rend pour une release ; le reste est ignoré.
type releaseJSON struct {
	TagName    string      `json:"tag_name"`
	Draft      bool        `json:"draft"`
	Prerelease bool        `json:"prerelease"`
	Assets     []assetJSON `json:"assets"`
}

type assetJSON struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
}

// LatestRelease rend la dernière release publiée, hors brouillons et préversions.
func (c *Client) LatestRelease(ctx context.Context) (Release, error) {
	return c.fetchRelease(ctx, "/releases/latest")
}

// ReleaseByTag rend la release d'un tag, vX.Y.Z.
func (c *Client) ReleaseByTag(ctx context.Context, tag string) (Release, error) {
	return c.fetchRelease(ctx, "/releases/tags/"+url.PathEscape(tag))
}

func (c *Client) fetchRelease(ctx context.Context, path string) (Release, error) {
	body, err := c.getJSON(ctx, c.repositoryURL(path))
	if err != nil {
		return Release{}, err
	}

	var raw releaseJSON
	if err := json.Unmarshal(body, &raw); err != nil {
		return Release{}, fmt.Errorf("decode release: %w", err)
	}
	return releaseFromJSON(raw)
}

// ListReleases rend les releases publiées, sans brouillon ni préversion, et
// sans celles dont le tag n'est pas une version.
func (c *Client) ListReleases(ctx context.Context) ([]Release, error) {
	body, err := c.getJSON(ctx, c.repositoryURL("/releases?per_page=100"))
	if err != nil {
		return nil, err
	}

	var raw []releaseJSON
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode releases: %w", err)
	}

	var releases []Release
	for _, candidate := range raw {
		if candidate.Draft || candidate.Prerelease {
			continue
		}
		release, err := releaseFromJSON(candidate)
		if err != nil {
			continue
		}
		releases = append(releases, release)
	}
	return releases, nil
}

func releaseFromJSON(raw releaseJSON) (Release, error) {
	version, err := ParseVersion(raw.TagName)
	if err != nil {
		return Release{}, fmt.Errorf("release tag %q: %w", raw.TagName, err)
	}

	release := Release{Version: version}
	for _, asset := range raw.Assets {
		release.Assets = append(release.Assets, Asset(asset))
	}
	return release, nil
}

// DownloadAsset lit le fichier en entier, borné, et rend son contenu avec sa
// somme SHA-256.
func (c *Client) DownloadAsset(ctx context.Context, asset Asset, maxBytes int64) ([]byte, [sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	if asset.Size > maxBytes {
		return nil, digest, fmt.Errorf("%s weighs %d bytes, more than the %d allowed", asset.Name, asset.Size, maxBytes)
	}

	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	response, err := c.get(ctx, asset.URL, "application/octet-stream")
	if err != nil {
		return nil, digest, fmt.Errorf("download %s: %w", asset.Name, err)
	}
	defer response.Body.Close()

	content, err := readBounded(response.Body, maxBytes)
	if err != nil {
		return nil, digest, fmt.Errorf("download %s: %w", asset.Name, err)
	}
	return content, sha256.Sum256(content), nil
}

// Ce que l'API rend pour les attestations d'une somme. Le bundle est en ligne,
// ou à télécharger à part quand il est gros.
type attestationsJSON struct {
	Attestations []struct {
		Bundle    json.RawMessage `json:"bundle"`
		BundleURL string          `json:"bundle_url"`
	} `json:"attestations"`
}

// FetchAttestations rend les bundles Sigstore que GitHub garde pour cette
// somme. Aucun n'est une réponse possible : c'est à l'appelant de refuser.
func (c *Client) FetchAttestations(ctx context.Context, digest [sha256.Size]byte) ([][]byte, error) {
	subject := "sha256:" + hex.EncodeToString(digest[:])
	body, err := c.getJSONBounded(ctx, c.repositoryURL("/attestations/"+subject+"?per_page=10"), maxAttestationBytes)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fetch attestations: %w", err)
	}

	var raw attestationsJSON
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode attestations: %w", err)
	}

	var bundles [][]byte
	for _, attestation := range raw.Attestations {
		if len(attestation.Bundle) > 0 && string(attestation.Bundle) != "null" {
			bundles = append(bundles, attestation.Bundle)
			continue
		}
		if attestation.BundleURL == "" {
			continue
		}
		bundle, err := c.fetchDetachedBundle(ctx, attestation.BundleURL)
		if err != nil {
			return nil, err
		}
		bundles = append(bundles, bundle)
	}
	return bundles, nil
}

// fetchDetachedBundle lit un bundle depuis son adresse signée, sans jeton :
// l'hébergeur refuserait deux authentifications.
func (c *Client) fetchDetachedBundle(ctx context.Context, rawURL string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch bundle: %w", err)
	}
	request.Header.Set("User-Agent", c.userAgent)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch bundle: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch bundle: %s", response.Status)
	}
	return readBounded(response.Body, maxAttestationBytes)
}

func (c *Client) repositoryURL(path string) string {
	return c.baseURL + "/repos/" + c.repository + path
}

// getJSON fait un GET sur l'API, borné en temps et en taille.
func (c *Client) getJSON(ctx context.Context, rawURL string) ([]byte, error) {
	return c.getJSONBounded(ctx, rawURL, maxAPIResponseBytes)
}

func (c *Client) getJSONBounded(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	response, err := c.get(ctx, rawURL, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	return readBounded(response.Body, maxBytes)
}

// get envoie la requête avec les en-têtes que GitHub attend. Sur une
// redirection vers un autre domaine (le stockage des fichiers), le client
// HTTP de Go retire lui-même l'en-tête Authorization.
func (c *Client) get(ctx context.Context, rawURL, accept string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Accept", accept)
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	request.Header.Set("User-Agent", c.userAgent)
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call github: %w", err)
	}
	switch response.StatusCode {
	case http.StatusOK:
		return response, nil
	case http.StatusNotFound:
		_ = response.Body.Close()
		return nil, ErrNotFound
	case http.StatusUnauthorized, http.StatusForbidden:
		_ = response.Body.Close()
		return nil, ErrUnauthorized
	default:
		_ = response.Body.Close()
		return nil, fmt.Errorf("github answered %s", response.Status)
	}
}

func readBounded(reader io.Reader, maxBytes int64) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if int64(len(content)) > maxBytes {
		return nil, fmt.Errorf("response larger than the %d bytes allowed", maxBytes)
	}
	return content, nil
}
