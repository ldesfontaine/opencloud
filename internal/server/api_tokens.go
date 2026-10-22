package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/apitoken"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
)

// Ce que le serveur attend du composant des jetons d'API : le réglage de
// Paramètres, la révocation à la création du compte, et la vérification
// d'un Bearer.
type APITokenService interface {
	List(ctx context.Context) ([]apitoken.Token, error)
	Create(ctx context.Context, request apitoken.Request) (string, apitoken.Token, error)
	Revoke(ctx context.Context, id string) error
	RevokeAll(ctx context.Context) (int, error)
	Verify(ctx context.Context, bearer string) (apitoken.Token, error)
}

const (
	codeAPITokenInvalid        = "api_token.invalid"         // #nosec G101 -- une clé de catalogue, pas un secret.
	codeAPITokenRouteForbidden = "api_token.route_forbidden" // #nosec G101 -- une clé de catalogue, pas un secret.
	// L'en-tête où mcp-maison dit pour qui il appelle (le sub OIDC). Posé
	// par lui, indicatif : il n'ouvre aucun droit.
	onBehalfOfHeader = "X-MCP-Maison-User"
	// La borne de l'émetteur (MAX_HEADER_VALUE chez mcp-maison).
	maxOnBehalfOfRunes = 200
	// Le port d'écoute de mcp-maison, qu'openCloud suggère de lire.
	mcpDefaultPort = "3000"
	// Le port d'openCloud quand l'adresse d'écoute n'en dit pas (config.go).
	defaultListenPort = "8080"
)

// --- Admission d'un Bearer ---

// bearerOf lit le jeton d'un en-tête Authorization: Bearer, schéma insensible
// à la casse. Le booléen dit si le schéma est Bearer, jeton vide compris.
func bearerOf(r *http.Request) (string, bool) {
	scheme, token, _ := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	return strings.TrimSpace(token), true
}

type apiTokenKey struct{}

// apiTokenFrom rend le jeton qui a admis la requête : les handlers qui
// rendent des secrets s'y masquent.
func apiTokenFrom(ctx context.Context) (apitoken.Token, bool) {
	token, ok := ctx.Value(apiTokenKey{}).(apitoken.Token)
	return token, ok
}

// admitAPIToken juge un Bearer : une route que le jeton n'ouvre pas, 403 ; un
// jeton inconnu, révoqué ou échu, 401 sous le limiteur des échecs Basic.
// Admis, la requête porte le jeton et l'acteur api:<id> ; aucune route
// admise n'accorde d'autorité ni ne tient un flux, d'où pas d'état
// d'authentification dans le contexte.
func (s *Server) admitAPIToken(w http.ResponseWriter, r *http.Request, bearer string, tokenRoute bool, next http.HandlerFunc) {
	if !tokenRoute {
		s.apiRefuse(w, http.StatusForbidden, codeAPITokenRouteForbidden)
		return
	}
	token, err := s.apiTokens.Verify(r.Context(), bearer)
	if errors.Is(err, apitoken.ErrInvalid) {
		source := s.clientAddress(r)
		if !s.authLimit.Allow(source) {
			s.logger.Warn("api token rate limited", "source", source)
			httpx.WriteTooMany(w)
			return
		}
		// Jamais le jeton présenté : un presque juste resterait lisible.
		s.logger.Warn("api token refused", "source", source, "path", r.URL.Path)
		w.Header().Set("WWW-Authenticate", "Bearer")
		s.writeAPIError(w, http.StatusUnauthorized, codeAPITokenInvalid)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	ctx := context.WithValue(r.Context(), apiTokenKey{}, token)
	ctx = audit.WithActor(ctx, audit.API(token.ID, onBehalfOf(r)))
	next(w, r.WithContext(ctx))
}

// onBehalfOf est la valeur de X-MCP-Maison-User : exactement une, sans
// caractère de contrôle, bornée. Lue seulement sur une requête admise par
// jeton ; sur Basic ou forward, ignorée.
func onBehalfOf(r *http.Request) string {
	values := r.Header.Values(onBehalfOfHeader)
	if len(values) != 1 {
		return ""
	}
	return printable(values[0], maxOnBehalfOfRunes)
}

// --- Masquage ---

// redactedProbe est la seule règle « un jeton masque la cible » : la liste
// et la fiche d'une sonde passent par elle.
func redactedProbe(converted probeJSON) probeJSON {
	converted.Target = probe.RedactTarget(converted.Target)
	return converted
}

// originOf réduit une URL à son schéma et son hôte : le chemin d'un
// webhook Discord est son jeton.
func originOf(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

// --- Le réglage, sous Paramètres ---

type apiTokenJSON struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Masked     string     `json:"masked"`
	Scope      string     `json:"scope"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

// Les durées offertes, en jours, 0 pour sans échéance.
type apiTokenLifetimesJSON struct {
	Days        []int `json:"days"`
	DefaultDays int   `json:"default_days"`
}

// env est ce que mcp-maison lit dans son .env, tiré de l'adresse d'écoute :
// il joint openCloud en direct, jamais par Traefik ni par le SSO.
type apiTokenEnvJSON struct {
	MCPHost      string `json:"mcp_host"`
	OpenCloudURL string `json:"opencloud_url"`
}

type apiTokensResponse struct {
	Tokens    []apiTokenJSON        `json:"tokens"`
	Limit     int                   `json:"limit"`
	Lifetimes apiTokenLifetimesJSON `json:"lifetimes"`
	Env       apiTokenEnvJSON       `json:"env"`
}

// Le jeton en clair, une seule fois, avec sa ligne.
type apiTokenCreated struct {
	Token string       `json:"token"`
	Item  apiTokenJSON `json:"item"`
}

// Une portée absente vaut read ; une durée absente, 90 jours ; 0 veut dire
// sans échéance.
type apiTokenRequest struct {
	Name          string `json:"name"`
	Scope         string `json:"scope,omitempty"`
	ExpiresInDays *int   `json:"expires_in_days,omitempty"`
}

func apiTokenToJSON(token apitoken.Token) apiTokenJSON {
	return apiTokenJSON{
		ID: token.ID, Name: token.Name, Masked: token.Masked(), Scope: string(token.Scope),
		CreatedAt: token.CreatedAt.UTC(), ExpiresAt: httpx.TimeOrNil(token.ExpiresAt), LastUsedAt: httpx.TimeOrNil(token.LastUsedAt),
	}
}

func (s *Server) listAPITokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.apiTokens.List(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	host, openCloudURL := mcpEnvOf(s.listen)
	s.writeAPI(w, http.StatusOK, apiTokensResponse{
		Tokens:    httpx.ConvertAll(tokens, apiTokenToJSON),
		Limit:     apitoken.MaxTokens,
		Lifetimes: apiTokenLifetimesJSON{Days: apitoken.Lifetimes(), DefaultDays: apitoken.DefaultDays},
		Env:       apiTokenEnvJSON{MCPHost: host, OpenCloudURL: openCloudURL},
	})
}

// createAPIToken tire un jeton et le montre une seule fois.
func (s *Server) createAPIToken(w http.ResponseWriter, r *http.Request) {
	var request apiTokenRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	cleartext, token, err := s.apiTokens.Create(r.Context(), apitoken.Request{
		Name: request.Name, Scope: apitoken.Scope(request.Scope), Days: request.ExpiresInDays,
	})
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusCreated, apiTokenCreated{Token: cleartext, Item: apiTokenToJSON(token)})
}

func (s *Server) revokeAPIToken(w http.ResponseWriter, r *http.Request) {
	s.finishAPIAction(w, r, s.apiTokens.Revoke(r.Context(), r.PathValue("id")), apitoken.ErrNotFound)
}

// mcpEnvOf tire de l'adresse d'écoute l'hôte où mcp-maison écoute (MCP_HOST)
// et l'adresse où il joint openCloud (OPENCLOUD_URL). Un hôte joker ou vide
// vaut la boucle locale.
func mcpEnvOf(listen string) (host, openCloudURL string) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		host, port = "", ""
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	if port == "" {
		port = defaultListenPort
	}
	return host, "http://" + net.JoinHostPort(host, port)
}
