package mcpcheck

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/egress"
)

const (
	// Rien en trois secondes : la carte de Paramètres ne doit pas attendre
	// un conteneur tombé.
	requestTimeout = 3 * time.Second
	// Un /health tient en quelques centaines d'octets.
	maxBodyBytes = 64 << 10
	healthPath   = "/health"
)

// State dit ce que /health de mcp-maison a répondu ; liste fermée, traduite
// sous ai_agent.state_.
type State string

const (
	StateNotConfigured State = "not_configured" // mcp_url vide
	StateReachable     State = "reachable"
	StateGroupMissing  State = "group_missing"  // répond, sans outil opencloud_*
	StateUnreachable   State = "unreachable"    // rien en 3 s, refus de connexion
	StateInvalidAnswer State = "invalid_answer" // pas 200, pas du JSON, status != "ok"
)

func States() []State {
	return []State{StateNotConfigured, StateReachable, StateGroupMissing, StateUnreachable, StateInvalidAnswer}
}

// Health est ce que la vérification a trouvé. Version est vide quand
// mcp-maison ne la dit pas (avant v0.1.0).
type Health struct {
	State          State
	Version        string
	OpenCloudTools int
	CheckedAt      time.Time
}

// Checker lit le /health d'une adresse.
type Checker struct {
	url    string
	client *http.Client
	now    func() time.Time
}

// New prépare la vérification de l'adresse de mcp-maison ; url vide : Check
// rend not_configured sans toucher au réseau.
func New(url string, now func() time.Time) *Checker {
	transport := &http.Transport{
		// La boucle locale et le réseau privé sont l'usage voulu ; le
		// lien-local reste fermé.
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return egress.Dial(ctx, network, address, requestTimeout)
		},
		ResponseHeaderTimeout: requestTimeout,
		DisableKeepAlives:     true,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
		// Une redirection n'est pas un /health : c'est une réponse inattendue.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &Checker{url: strings.TrimRight(url, "/"), client: client, now: now}
}

// healthBody est la partie du /health de mcp-maison qu'on lit.
type healthBody struct {
	Status  string   `json:"status"`
	Version string   `json:"version"`
	Tools   []string `json:"tools"`
}

// Check interroge /health. Il ne journalise rien : l'état est la réponse.
func (c *Checker) Check(ctx context.Context) Health {
	health := Health{CheckedAt: c.now()}
	if c.url == "" {
		health.State = StateNotConfigured
		return health
	}
	body, state := c.fetch(ctx)
	health.State = state
	if state != StateReachable {
		return health
	}
	health.Version = body.Version
	health.OpenCloudTools = countOpenCloudTools(body.Tools)
	if health.OpenCloudTools == 0 {
		health.State = StateGroupMissing
	}
	return health
}

func (c *Checker) fetch(ctx context.Context) (healthBody, State) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+healthPath, nil)
	if err != nil {
		return healthBody{}, StateInvalidAnswer
	}
	response, err := c.client.Do(request)
	if err != nil {
		return healthBody{}, StateUnreachable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return healthBody{}, StateInvalidAnswer
	}
	var body healthBody
	if err := json.NewDecoder(io.LimitReader(response.Body, maxBodyBytes)).Decode(&body); err != nil || body.Status != "ok" {
		return healthBody{}, StateInvalidAnswer
	}
	return body, StateReachable
}

// countOpenCloudTools compte les outils du groupe openCloud, que mcp-maison
// préfixe du nom du produit.
func countOpenCloudTools(tools []string) int {
	prefix := brand.ID + "_"
	count := 0
	for _, tool := range tools {
		if strings.HasPrefix(tool, prefix) {
			count++
		}
	}
	return count
}
