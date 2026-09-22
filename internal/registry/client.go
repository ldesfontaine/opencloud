package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/egress"
)

var (
	// ErrUnauthorized : le registre refuse, anonyme ou avec le trousseau.
	// Ce n'est pas une erreur du produit : l'image est privée, on passe.
	ErrUnauthorized = errors.New("registry refused access")
	// ErrNotFound : le dépôt ou le tag n'existe pas sur ce registre.
	ErrNotFound = errors.New("repository or tag not found")
	// ErrUnreachable : pas de réponse, ou une réponse qui n'est pas celle
	// d'un registre v2.
	ErrUnreachable = errors.New("registry unreachable")
)

const (
	RequestTimeout = 15 * time.Second
	// Une page de tags, la plus grande que le Hub accepte ; au-delà de
	// dix pages, un dépôt est assez gros pour qu'on s'arrête là.
	tagsPerPage = 1000
	maxTagPages = 10
	maxBody     = 4 << 20
	// Un jeton sans durée annoncée vaut ce que la norme dit : 60 s.
	defaultTokenLife = 60 * time.Second
	userAgent        = "openCloud-agent"
	// Ce qu'un tag peut désigner : un index multi-architecture ou un
	// manifeste seul, dans les deux écritures. L'empreinte rendue est
	// celle de ce que le tag pointe, la même que Docker note en tirant.
	acceptManifests = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, " +
		"application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"
)

type Client struct {
	http     *http.Client
	keychain Keychain

	mu     sync.Mutex
	tokens map[string]bearerToken
}

type bearerToken struct {
	value   string
	expires time.Time
}

// New fait un client qui sort par la garde egress et ne suit aucune
// redirection : un registre v2 répond en direct ou pas du tout.
func New(keychain Keychain) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return egress.Dial(ctx, network, address, RequestTimeout)
		},
		TLSHandshakeTimeout:   RequestTimeout,
		ResponseHeaderTimeout: RequestTimeout,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   RequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	if keychain == nil {
		keychain = Static{}
	}
	return &Client{http: client, keychain: keychain, tokens: make(map[string]bearerToken)}
}

// Tags liste les tags d'un dépôt, page après page, dans l'ordre où le
// registre les donne.
func (c *Client) Tags(ctx context.Context, ref Reference) ([]string, error) {
	next := fmt.Sprintf("/v2/%s/tags/list?n=%d", ref.Repository, tagsPerPage)
	var tags []string
	for page := 0; page < maxTagPages && next != ""; page++ {
		response, err := c.do(ctx, ref, http.MethodGet, next, "")
		if err != nil {
			return nil, err
		}
		var body struct {
			Tags []string `json:"tags"`
		}
		err = json.NewDecoder(io.LimitReader(response.Body, maxBody)).Decode(&body)
		_ = response.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("%w: decode tags: %w", ErrUnreachable, err)
		}
		tags = append(tags, body.Tags...)
		next = nextPage(response.Header.Get("Link"))
	}
	return tags, nil
}

// nextPage lit « </v2/…?last=x&n=1000>; rel="next" » ; vide sans suite.
func nextPage(link string) string {
	for _, part := range strings.Split(link, ",") {
		target, attributes, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.Contains(attributes, `rel="next"`) {
			continue
		}
		return strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(target), "<"), ">")
	}
	return ""
}

// Digest rend l'empreinte de ce qu'un tag pointe, par un HEAD : le Hub
// ne compte pas un HEAD dans son quota de pulls. Un registre qui ne rend
// pas l'en-tête se lit en GET et l'empreinte se calcule sur le corps.
func (c *Client) Digest(ctx context.Context, ref Reference) (string, error) {
	path := "/v2/" + ref.Repository + "/manifests/" + ref.Tag
	response, err := c.do(ctx, ref, http.MethodHead, path, acceptManifests)
	if err != nil {
		return "", err
	}
	_ = response.Body.Close()
	if digest := response.Header.Get("Docker-Content-Digest"); digest != "" {
		return digest, nil
	}
	response, err = c.do(ctx, ref, http.MethodGet, path, acceptManifests)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody))
	if err != nil {
		return "", fmt.Errorf("%w: read manifest: %w", ErrUnreachable, err)
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// do joue la requête, s'authentifie si le registre le demande, et
// traduit la réponse en erreur sentinelle. Un 2xx rend la réponse
// ouverte : c'est l'appelant qui la ferme.
func (c *Client) do(ctx context.Context, ref Reference, method, path, accept string) (*http.Response, error) {
	response, err := c.send(ctx, ref, method, path, accept, c.cachedToken(ref))
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusUnauthorized {
		challenge := response.Header.Get("Www-Authenticate")
		_ = response.Body.Close()
		authorization, err := c.authorize(ctx, ref, challenge)
		if err != nil {
			return nil, err
		}
		response, err = c.send(ctx, ref, method, path, accept, authorization)
		if err != nil {
			return nil, err
		}
	}
	switch {
	case response.StatusCode/100 == 2:
		return response, nil
	case response.StatusCode == http.StatusUnauthorized, response.StatusCode == http.StatusForbidden:
		_ = response.Body.Close()
		return nil, ErrUnauthorized
	case response.StatusCode == http.StatusNotFound:
		_ = response.Body.Close()
		return nil, ErrNotFound
	default:
		_ = response.Body.Close()
		return nil, fmt.Errorf("%w: %s answered %d", ErrUnreachable, ref.Registry, response.StatusCode)
	}
}

func (c *Client) send(ctx context.Context, ref Reference, method, path, accept, authorization string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, scheme(ref.Registry)+ref.Registry+path, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	request.Header.Set("User-Agent", userAgent)
	if accept != "" {
		request.Header.Set("Accept", accept)
	}
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	return response, nil
}

// authorize répond au défi du registre : un jeton Bearer tiré du realm
// annoncé, anonyme ou signé du trousseau, ou un Basic direct.
func (c *Client) authorize(ctx context.Context, ref Reference, challenge string) (string, error) {
	scheme, parameters, _ := strings.Cut(challenge, " ")
	credentials, hasCredentials := c.keychain.Lookup(ref.Registry)
	switch strings.ToLower(scheme) {
	case "bearer":
		token, err := c.fetchToken(ctx, parseChallenge(parameters), credentials, hasCredentials)
		if err != nil {
			return "", err
		}
		c.remember(ref, token)
		return "Bearer " + token.value, nil
	case "basic":
		if !hasCredentials {
			return "", ErrUnauthorized
		}
		return basicAuthorization(credentials), nil
	default:
		return "", ErrUnauthorized
	}
}

// parseChallenge lit realm="…",service="…",scope="…".
func parseChallenge(parameters string) map[string]string {
	values := make(map[string]string)
	for _, pair := range strings.Split(parameters, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if ok {
			values[strings.ToLower(key)] = strings.Trim(value, `"`)
		}
	}
	return values
}

func (c *Client) fetchToken(ctx context.Context, challenge map[string]string, credentials Credentials, signed bool) (bearerToken, error) {
	realm, err := url.Parse(challenge["realm"])
	if err != nil || realm.Scheme != "https" && !isLoopback(realm.Host) {
		return bearerToken{}, ErrUnauthorized
	}
	query := realm.Query()
	if service := challenge["service"]; service != "" {
		query.Set("service", service)
	}
	if scope := challenge["scope"]; scope != "" {
		query.Set("scope", scope)
	}
	realm.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return bearerToken{}, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	request.Header.Set("User-Agent", userAgent)
	if signed {
		request.SetBasicAuth(credentials.Username, credentials.Password)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return bearerToken{}, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return bearerToken{}, ErrUnauthorized
	}
	if response.StatusCode/100 != 2 {
		return bearerToken{}, fmt.Errorf("%w: token endpoint answered %d", ErrUnreachable, response.StatusCode)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxBody)).Decode(&body); err != nil {
		return bearerToken{}, fmt.Errorf("%w: decode token: %w", ErrUnreachable, err)
	}
	value := body.Token
	if value == "" {
		value = body.AccessToken
	}
	if value == "" {
		return bearerToken{}, ErrUnauthorized
	}
	life := defaultTokenLife
	if body.ExpiresIn > 0 {
		life = time.Duration(body.ExpiresIn) * time.Second
	}
	return bearerToken{value: value, expires: time.Now().Add(life)}, nil
}

// Le jeton vaut pour un dépôt : le tags/list puis le HEAD du même dépôt
// le partagent, sans redemander.
func (c *Client) remember(ref Reference, token bearerToken) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokens[ref.Registry+"/"+ref.Repository] = token
}

func (c *Client) cachedToken(ref Reference) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	token, ok := c.tokens[ref.Registry+"/"+ref.Repository]
	if !ok || time.Now().After(token.expires.Add(-5*time.Second)) {
		return ""
	}
	return "Bearer " + token.value
}

func basicAuthorization(credentials Credentials) string {
	request := &http.Request{Header: http.Header{}}
	request.SetBasicAuth(credentials.Username, credentials.Password)
	return request.Header.Get("Authorization")
}

// scheme : TLS partout, sauf vers la boucle locale, où Docker lui-même
// parle en clair ; c'est aussi là que les tests jouent un registre.
func scheme(registry string) string {
	if isLoopback(registry) {
		return "http://"
	}
	return "https://"
}

func isLoopback(host string) bool {
	name, _, err := net.SplitHostPort(host)
	if err != nil {
		name = host
	}
	ip := net.ParseIP(name)
	return name == "localhost" || (ip != nil && ip.IsLoopback())
}
