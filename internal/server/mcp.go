package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ldesfontaine/opencloud/internal/mcp"
	"github.com/ldesfontaine/opencloud/internal/ratelimit"
)

// Ce que le serveur attend du composant mcp : l'activation et les jetons
// pour l'API des réglages, l'autorisation OAuth et la vérification d'un
// Bearer pour /mcp.
type MCPService interface {
	Client(ctx context.Context) (mcp.Client, bool, error)
	Enable(ctx context.Context) (string, mcp.Client, error)
	Disable(ctx context.Context) error
	RegenerateSecret(ctx context.Context) (string, error)
	SetRedirectURIs(ctx context.Context, uris []string) (mcp.Client, error)
	CreateAPIToken(ctx context.Context, name string) (string, mcp.Token, error)
	APITokens(ctx context.Context) ([]mcp.Token, error)
	RevokeToken(ctx context.Context, id string) error
	Sessions(ctx context.Context) ([]mcp.Session, error)
	RevokeSession(ctx context.Context, familyID string) error
	RedirectAllowed(ctx context.Context, uri string) (bool, error)
	Authorize(ctx context.Context, request mcp.AuthorizeRequest) (string, error)
	Exchange(ctx context.Context, request mcp.ExchangeRequest) (mcp.Grant, error)
	Refresh(ctx context.Context, request mcp.RefreshRequest) (mcp.Grant, error)
	Verify(ctx context.Context, bearer string) (mcp.Token, error)
}

// Le serveur MCP : un agent IA lit ce qu'openCloud sait par des outils
// dont chaque lecture rend les mêmes faits JSON que l'API, et dont les
// rares écritures sont celles que l'interface a déjà, jamais une action
// sur une machine. Deux transports : HTTP streamable sur /mcp derrière un
// Bearer, et stdin/stdout par « opencloud mcp », en lecture seule.
const (
	mcpPath          = "/mcp"
	mcpServerName    = "openCloud"
	mcpSessionIdle   = 30 * time.Minute
	mcpResourcePath  = "/.well-known/oauth-protected-resource"
	mcpAuthorizePath = "/oauth/authorize"
	mcpTokenPath     = "/oauth/token" // #nosec G101 -- un chemin, pas un secret.
	// Un client MCP n'a pas d'interface devant lui : les listes sont
	// bornées ici, dans le handler.
	mcpDefaultLimit = 100
	mcpMaxLimit     = 500
	codeReadOnly    = "mcp.read_only"
	// Les comptes du catalogue, figés par un test qui énumère les outils.
	mcpReadToolCount  = 16
	mcpWriteToolCount = 9
	// Les mêmes bornes que /ping et /statut par adresse résolue ; le point
	// de jeton, où un secret se devine, est tenu plus serré.
	mcpPerSourceRate  = 10
	mcpPerSourceBurst = 20
	mcpTokenRate      = 1
	mcpTokenBurst     = 10
	mcpSweepEvery     = 5 * time.Minute
	mcpSweepIdle      = 10 * time.Minute
)

const mcpInstructions = "openCloud is a self-hosted dashboard that watches machines, their Docker services, " +
	"scheduled jobs (heartbeats), HTTP/TCP probes with their TLS certificates, a public status page and alerts. " +
	"Every read tool returns the same JSON facts as the web API: instants are ISO 8601 in UTC, durations are in " +
	"seconds or milliseconds, states are closed lists of lowercase words. Nothing here can act on a machine: " +
	"no restart, no deploy, no command. Errors come back as {\"error\":\"<code>\"} where the code is stable " +
	"(not_found, internal) or a catalog key such as probe.not_paused."

const mcpReadOnlyInstructions = " This session runs over stdio and is read-only: write tools answer " +
	"{\"error\":\"mcp.read_only\"}; use the HTTP transport to write."

type mcpLimits struct {
	bySource *ratelimit.Limiter
	byToken  *ratelimit.Limiter
	now      func() time.Time

	mu        sync.Mutex
	lastSweep time.Time
}

func newMCPLimits() *mcpLimits {
	return &mcpLimits{
		bySource: ratelimit.New(mcpPerSourceRate, mcpPerSourceBurst),
		byToken:  ratelimit.New(mcpTokenRate, mcpTokenBurst),
		now:      time.Now,
	}
}

func (l *mcpLimits) setClock(now func() time.Time) {
	l.now = now
	l.bySource.SetClock(now)
	l.byToken.SetClock(now)
}

func (l *mcpLimits) allow(source string) bool {
	l.sweepIfDue()
	return l.bySource.Allow(source)
}

func (l *mcpLimits) allowToken(source string) bool {
	l.sweepIfDue()
	return l.byToken.Allow(source)
}

func (l *mcpLimits) sweepIfDue() {
	now := l.now()
	l.mu.Lock()
	due := now.Sub(l.lastSweep) >= mcpSweepEvery
	if due {
		l.lastSweep = now
	}
	l.mu.Unlock()
	if due {
		l.bySource.Sweep(mcpSweepIdle)
		l.byToken.Sweep(mcpSweepIdle)
	}
}

// newMCPServer enregistre les outils. En lecture seule, les écritures
// restent listées et répondent une clé stable : le modèle sait pourquoi.
func (s *Server) newMCPServer(readOnly bool) *gomcp.Server {
	instructions := mcpInstructions
	if readOnly {
		instructions += mcpReadOnlyInstructions
	}
	server := gomcp.NewServer(&gomcp.Implementation{Name: mcpServerName, Version: s.version}, &gomcp.ServerOptions{Instructions: instructions})
	s.registerReadTools(server)
	s.registerWriteTools(server, readOnly)
	return server
}

// newMCPHandler monte le transport HTTP streamable. La protection contre
// le DNS rebinding du SDK est levée : derrière Traefik, la requête arrive
// sur la boucle locale avec l'hôte public, et c'est le Bearer qui garde.
func (s *Server) newMCPHandler() http.Handler {
	return gomcp.NewStreamableHTTPHandler(func(*http.Request) *gomcp.Server { return s.mcpServer }, &gomcp.StreamableHTTPOptions{
		DisableLocalhostProtection: true,
		SessionTimeout:             mcpSessionIdle,
	})
}

// serveMCP : limité par adresse, fermé quand MCP n'est pas activé, puis le
// Bearer du SDK, qui répond 401 avec l'adresse des métadonnées pour que le
// client découvre OAuth.
func (s *Server) serveMCP(w http.ResponseWriter, r *http.Request) {
	if !s.mcpLimits.allow(s.clientAddress(r)) {
		s.writeTooMany(w)
		return
	}
	if !s.mcpEnabled(w, r) {
		return
	}
	publicURL, _ := s.resolvePublicURL(r)
	require := auth.RequireBearerToken(s.verifyBearer, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL:    publicURL + mcpResourcePath,
		AllowMissingExpiration: true,
	})
	require(s.mcpHandler).ServeHTTP(w, r)
}

func (s *Server) verifyBearer(ctx context.Context, bearer string, _ *http.Request) (*auth.TokenInfo, error) {
	token, err := s.mcp.Verify(ctx, bearer)
	if errors.Is(err, mcp.ErrTokenInvalid) || errors.Is(err, mcp.ErrDisabled) {
		return nil, auth.ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	// L'échéance a été jugée par Verify, sur l'horloge du serveur ; le SDK
	// n'a pas à la rejuger sur la sienne, d'où AllowMissingExpiration.
	return &auth.TokenInfo{Extra: map[string]any{"token_id": token.ID, "kind": string(token.Kind)}}, nil
}

// mcpEnabled répond 404 quand MCP n'est pas activé : rien à découvrir.
func (s *Server) mcpEnabled(w http.ResponseWriter, r *http.Request) bool {
	_, enabled, err := s.mcp.Client(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return false
	}
	if !enabled {
		s.apiNotFound(w)
	}
	return enabled
}

// RunMCPStdio parle MCP sur stdin/stdout jusqu'à ce que le client
// raccroche, en lecture seule : ce processus ne tient ni le bus du direct
// ni les flux des agents, une écriture d'ici ne serait pas relayée.
func (s *Server) RunMCPStdio(ctx context.Context) error {
	return s.newMCPServer(true).Run(ctx, &gomcp.StdioTransport{})
}

// --- Les résultats d'outil ---

// toolJSON rend des faits en JSON, comme l'API.
func toolJSON(payload any) (*gomcp.CallToolResult, any, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	return &gomcp.CallToolResult{Content: []gomcp.Content{&gomcp.TextContent{Text: string(encoded)}}}, nil, nil
}

// toolRefuse rend un refus sous la forme de l'API : un seul mot, clé de
// catalogue ou code court, que le modèle peut lire et corriger.
func toolRefuse(code string) (*gomcp.CallToolResult, any, error) {
	encoded, _ := json.Marshal(apiError{Error: code})
	return &gomcp.CallToolResult{Content: []gomcp.Content{&gomcp.TextContent{Text: string(encoded)}}, IsError: true}, nil, nil
}

// toolInternal journalise et rend « internal » : le détail reste au journal.
func (s *Server) toolInternal(tool string, err error) (*gomcp.CallToolResult, any, error) {
	s.logger.Error("mcp tool failed", "tool", tool, "error", err)
	return toolRefuse(codeInternal)
}

// pageOf borne une page demandée par un client sans interface.
func pageOf(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = mcpDefaultLimit
	}
	limit = min(limit, mcpMaxLimit)
	return limit, max(offset, 0)
}

func sliceOf[T any](items []T, limit, offset int) []T {
	if offset >= len(items) {
		return []T{}
	}
	return items[offset:min(offset+limit, len(items))]
}

func readOnlyHints() *gomcp.ToolAnnotations {
	return &gomcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPointer(false)}
}

// writeHints dit honnêtement ce qu'une écriture fait : aucune n'est
// destructive, certaines se rejouent sans effet. Le SDK tient une
// écriture pour destructive tant qu'on ne dit pas le contraire.
func writeHints(idempotent bool) *gomcp.ToolAnnotations {
	return &gomcp.ToolAnnotations{DestructiveHint: boolPointer(false), IdempotentHint: idempotent, OpenWorldHint: boolPointer(false)}
}

func boolPointer(value bool) *bool {
	return &value
}
