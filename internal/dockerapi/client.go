package dockerapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

const (
	DefaultSocket = "/var/run/docker.sock"
	// L'API de Docker 20.10, celle du docker.io de Debian 12 : tout ce qu'on
	// lit existe depuis là, dont la mesure en un coup. Un démon plus récent
	// la sert toujours ; un plus vieux est refusé au ping.
	APIVersion     = "1.41"
	requestTimeout = 15 * time.Second
	// La liste d'une machine chargée, avec ses labels : bien au-dessus de
	// ce qu'un démon rend, bien en dessous de ce qui épuiserait l'agent.
	maxBody = 8 << 20
	// Le corps d'une erreur du démon ne sert qu'au journal.
	maxErrorBody = 4 << 10
	// L'hôte de l'URL ne sert à rien sur une socket ; il faut en donner un.
	baseURL = "http://docker/v" + APIVersion
)

var (
	ErrNoSocket      = errors.New("docker socket not found")
	ErrSocketDenied  = errors.New("docker socket permission denied")
	ErrEngineDown    = errors.New("docker engine not answering")
	ErrNotFound      = errors.New("container not found")
	ErrVersionTooOld = errors.New("docker engine api too old")
)

// Engine est ce que le ping dit du démon.
type Engine struct {
	Version    string
	APIVersion string
}

type Client struct {
	http   *http.Client
	socket string
}

func New(socketPath string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socketPath)
		},
		// Le flux d'événements et les journaux suivis durent : pas de délai
		// de réponse ici, le contexte de l'appelant borne chaque appel.
		DisableCompression: true,
	}
	return &Client{http: &http.Client{Transport: transport}, socket: socketPath}
}

// Ping dit si le démon répond et refuse une API plus vieille que la nôtre.
func (c *Client) Ping(ctx context.Context) (Engine, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	response, err := c.get(ctx, "/_ping")
	if err != nil {
		return Engine{}, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxErrorBody))
	engine := Engine{
		Version:    versionOf(response.Header.Get("Server")),
		APIVersion: response.Header.Get("Api-Version"),
	}
	if olderThan(engine.APIVersion, APIVersion) {
		return engine, fmt.Errorf("%w: %s", ErrVersionTooOld, engine.APIVersion)
	}
	return engine, nil
}

// versionOf lit « 29.8.0 » dans « Docker/29.8.0 (linux) ».
func versionOf(server string) string {
	_, version, ok := strings.Cut(server, "/")
	if !ok {
		return server
	}
	version, _, _ = strings.Cut(version, " ")
	return version
}

// olderThan compare deux versions d'API « majeur.mineur ».
func olderThan(version, reference string) bool {
	var major, minor, refMajor, refMinor int
	if _, err := fmt.Sscanf(version, "%d.%d", &major, &minor); err != nil {
		return true
	}
	if _, err := fmt.Sscanf(reference, "%d.%d", &refMajor, &refMinor); err != nil {
		return true
	}
	return major < refMajor || (major == refMajor && minor < refMinor)
}

// get fait une requête et traduit ce qui peut mal tourner en erreur
// sentinelle ; une réponse hors 2xx est une erreur, son corps au journal.
func (c *Client) get(ctx context.Context, path string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, classifyDialError(err)
	}
	if response.StatusCode == http.StatusNotFound {
		_ = response.Body.Close()
		return nil, ErrNotFound
	}
	if response.StatusCode/100 != 2 {
		defer response.Body.Close()
		var body struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, maxErrorBody)).Decode(&body)
		return nil, fmt.Errorf("docker answered %d: %s", response.StatusCode, body.Message)
	}
	return response, nil
}

// classifyDialError distingue « pas de Docker », « pas le droit » et « Docker
// ne répond pas » : trois causes, trois remèdes pour l'opérateur.
func classifyDialError(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOENT):
		return fmt.Errorf("%w: %w", ErrNoSocket, err)
	case errors.Is(err, fs.ErrPermission), errors.Is(err, syscall.EACCES):
		return fmt.Errorf("%w: %w", ErrSocketDenied, err)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return fmt.Errorf("%w: %w", ErrEngineDown, err)
	}
}

func (c *Client) getJSON(ctx context.Context, path string, into any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	response, err := c.get(ctx, path)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if err := json.NewDecoder(io.LimitReader(response.Body, maxBody)).Decode(into); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}
