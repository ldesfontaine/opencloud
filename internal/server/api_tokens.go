package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/apitoken"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
	"github.com/ldesfontaine/opencloud/internal/verb"
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
	RecordDenied(ctx context.Context, token apitoken.Token, verbID string)
}

const (
	codeAPITokenInvalid        = "api_token.invalid"         // #nosec G101 -- une clé de catalogue, pas un secret.
	codeAPITokenRouteForbidden = "api_token.route_forbidden" // #nosec G101 -- une clé de catalogue, pas un secret.
	codeAPITokenVerbForbidden  = "api_token.verb_forbidden"  // #nosec G101 -- une clé de catalogue, pas un secret.
	codeAPITokenOnly           = "api_token.token_only"      // #nosec G101 -- une clé de catalogue, pas un secret.
	// L'en-tête où mcp-maison dit pour qui il appelle (le sub OIDC). Posé
	// par lui, indicatif : il n'ouvre aucun droit.
	onBehalfOfHeader = "X-MCP-Maison-User"
	// La borne de l'émetteur (MAX_HEADER_VALUE chez mcp-maison).
	maxOnBehalfOfRunes = 200
	// Le port d'écoute de mcp-maison, qu'openCloud suggère de lire.
	mcpDefaultPort = "3000"
	// Le port d'openCloud quand l'adresse d'écoute n'en dit pas (config.go).
	defaultListenPort = "8080"

	// Le débit des gestes d'un jeton, par jeton, distinct des échecs
	// d'authentification (authLimit, par adresse). Un agent enchaîne quelques
	// gestes puis attend le résultat : dix d'un coup, puis un toutes les
	// deux secondes. Plus vite, c'est une boucle, pas une tâche. Le 429
	// porte Retry-After.
	apiVerbRate  = 0.5
	apiVerbBurst = 10
	// Les refus de verbe qu'on journalise, par jeton : cinq d'un coup, puis
	// un par minute. Le premier refus est le signal ; mille identiques
	// noieraient l'audit. Les refus au-delà sont quand même renvoyés, seule
	// leur trace est limitée.
	apiVerbDenyRate  = 1.0 / 60
	apiVerbDenyBurst = 5
	// Un seau silencieux plus longtemps que le temps de remplir l'un ou
	// l'autre est oublié.
	apiVerbIdle = 30 * time.Minute
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
func (s *Server) admitAPIToken(w http.ResponseWriter, r *http.Request, bearer string, access tokenAccess, next http.HandlerFunc) {
	if !access.read && access.verb == "" {
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
	if access.verb != "" && !s.admitVerb(w, r, token, access.verb) {
		return
	}
	ctx := context.WithValue(r.Context(), apiTokenKey{}, token)
	ctx = audit.WithActor(ctx, audit.API(token.ID, token.Name, onBehalfOf(r)))
	next(w, r.WithContext(ctx))
}

// admitVerb juge l'action d'un verbe pour ce jeton, déjà vérifié : une
// portée read n'écrit jamais (route_forbidden) ; un verbe que le jeton ne
// porte pas est refusé et journalisé (verb_forbidden) ; un jeton qui va trop
// vite attend (429). Le débit se compte après le droit : un refus ne coûte
// rien à l'agent, il se voit à l'audit.
func (s *Server) admitVerb(w http.ResponseWriter, r *http.Request, token apitoken.Token, verbID string) bool {
	if token.Scope != apitoken.ScopeWrite {
		s.apiRefuse(w, http.StatusForbidden, codeAPITokenRouteForbidden)
		return false
	}
	if !token.CanDo(verbID) {
		// Le contexte de l'audit n'a pas encore son acteur : on le pose ici.
		ctx := audit.WithActor(r.Context(), audit.API(token.ID, token.Name, onBehalfOf(r)))
		if s.verbDenyLimit.Allow(token.ID) {
			s.apiTokens.RecordDenied(ctx, token, verbID)
		}
		s.apiRefuse(w, http.StatusForbidden, codeAPITokenVerbForbidden)
		return false
	}
	if !s.verbLimit.Allow(token.ID) {
		s.logger.Warn("api token rate limited", "token_id", token.ID, "verb", verbID)
		httpx.WriteTooMany(w)
		return false
	}
	return true
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

// --- Réponse d'un verbe ---

// verbReplyJSON est ce que mcp-maison lit après une action : le contrat est
// dans 22-fonctionnement.md. RunID est l'ordre envoyé à un agent quand le
// verbe en écrit un (agent.ping), nul quand le geste est fait sur place
// (pause, reprise) ; State « done » dans ce cas. ReverseWith est le verbe
// qui défait celui-ci.
type verbReplyJSON struct {
	Verb        string  `json:"verb"`
	RunID       *string `json:"run_id"`
	State       string  `json:"state"`
	ReverseWith string  `json:"reverse_with"`
}

// shapeVerbReply fait répondre à un jeton le contrat des verbes, et laisse
// l'opérateur à la réponse de sa route. La route est la même : le handler
// de l'opérateur s'exécute tel quel, sa réponse est lue puis reformulée,
// jamais refaite. Un échec (404, 409, 422…) passe inchangé.
func (s *Server) shapeVerbReply(verbID string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, byToken := apiTokenFrom(r.Context()); !byToken {
			next(w, r)
			return
		}
		captured := &capturedReply{header: http.Header{}}
		next(captured, r)
		if captured.status != 0 && (captured.status < 200 || captured.status > 299) {
			captured.replayTo(w)
			return
		}
		reply := verbReplyJSON{Verb: verbID, State: "done"}
		if found, ok := verb.Default.Lookup(verbID); ok {
			reply.ReverseWith = found.Reverse
		}
		var run struct {
			ID    string `json:"id"`
			State string `json:"state"`
		}
		if json.Unmarshal(captured.body.Bytes(), &run) == nil && run.ID != "" {
			reply.RunID, reply.State = &run.ID, run.State
		}
		s.writeAPI(w, http.StatusOK, reply)
	}
}

// capturedReply garde la réponse d'un handler au lieu de l'envoyer.
type capturedReply struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (c *capturedReply) Header() http.Header { return c.header }

func (c *capturedReply) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
}

func (c *capturedReply) Write(body []byte) (int, error) {
	c.WriteHeader(http.StatusOK)
	return c.body.Write(body)
}

func (c *capturedReply) replayTo(w http.ResponseWriter) {
	maps.Copy(w.Header(), c.header)
	w.WriteHeader(c.status)
	_, _ = w.Write(c.body.Bytes())
}

// --- Les verbes du jeton appelant ---

// tokenVerbJSON est un verbe tel que le client d'un jeton le lit : la
// requête à faire (méthode, chemin, paramètres de chemin, champs du corps),
// son inverse et ses textes. mcp-maison n'a ainsi aucune table de verbes
// écrite en dur.
type tokenVerbJSON struct {
	ID      string   `json:"id"`
	Method  string   `json:"method"`
	Path    string   `json:"path"`
	Params  []string `json:"params"`
	Body    []string `json:"body"`
	Reverse string   `json:"reverse"`
	Label   string   `json:"label"`
	Summary string   `json:"summary"`
}

type tokenVerbsResponse struct {
	Scope string          `json:"scope"`
	Verbs []tokenVerbJSON `json:"verbs"`
}

// listTokenVerbs rend les seuls verbes du jeton appelant, dans la langue du
// serveur. La route est ouverte à tout jeton valide, en lecture ; pour
// l'opérateur elle répond 403 : elle parle du jeton, qu'il n'a pas. Elle est
// enregistrée pour tous plutôt que cachée, un refus clair vaut mieux qu'un
// 404 qui laisse croire à une panne.
func (s *Server) listTokenVerbs(w http.ResponseWriter, r *http.Request) {
	token, byToken := apiTokenFrom(r.Context())
	if !byToken {
		s.apiRefuse(w, http.StatusForbidden, codeAPITokenOnly)
		return
	}
	code, err := s.language(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	catalog := s.catalogs.For(code)
	verbs := []tokenVerbJSON{}
	for _, offered := range verb.Default.All() {
		if !token.CanDo(offered.ID) {
			continue
		}
		method, path, _ := strings.Cut(offered.Route, " ")
		verbs = append(verbs, tokenVerbJSON{
			ID: offered.ID, Method: method, Path: path, Params: orEmpty(offered.Params), Body: orEmpty(offered.Body),
			Reverse: offered.Reverse, Label: catalog.Get(offered.Label), Summary: catalog.Get(offered.Summary),
		})
	}
	s.writeAPI(w, http.StatusOK, tokenVerbsResponse{Scope: string(token.Scope), Verbs: verbs})
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
	// Les verbes que le jeton déclenche : [] pour une lecture, ou une
	// écriture que l'opérateur n'a rien cochée.
	Verbs []string `json:"verbs"`
}

// Un verbe offert à la création, avec ses clés de catalogue et son inverse.
type verbOfferJSON struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Summary string `json:"summary"`
	Reverse string `json:"reverse"`
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
	Verbs     []verbOfferJSON       `json:"verbs"`
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
	// Les verbes cochés, pour la portée write seule.
	Verbs []string `json:"verbs,omitempty"`
}

func apiTokenToJSON(token apitoken.Token) apiTokenJSON {
	return apiTokenJSON{
		ID: token.ID, Name: token.Name, Masked: token.Masked(), Scope: string(token.Scope),
		CreatedAt: token.CreatedAt.UTC(), ExpiresAt: httpx.TimeOrNil(token.ExpiresAt), LastUsedAt: httpx.TimeOrNil(token.LastUsedAt),
		Verbs: orEmpty(token.Verbs),
	}
}

func verbOfferToJSON(offered verb.Verb) verbOfferJSON {
	return verbOfferJSON{ID: offered.ID, Label: offered.Label, Summary: offered.Summary, Reverse: offered.Reverse}
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
		Verbs:     httpx.ConvertAll(verb.Default.All(), verbOfferToJSON),
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
		Name: request.Name, Scope: apitoken.Scope(request.Scope), Days: request.ExpiresInDays, Verbs: request.Verbs,
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
