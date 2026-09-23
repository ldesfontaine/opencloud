package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/mcp"
)

// Le réglage de l'accès d'un agent IA, sous Paramètres : activer, le
// client et son secret montré une fois, les redirect_uri déclarés, les
// jetons d'API, les sessions OAuth. Rien ici ne rend un secret déjà émis.

type mcpResponse struct {
	Enabled bool `json:"enabled"`
	// Le client OAuth : son identifiant fixe, son secret masqué, et les
	// adresses où openCloud le joint.
	ClientID     string   `json:"client_id"`
	SecretMasked string   `json:"secret_masked"`
	RedirectURIs []string `json:"redirect_uris"`
	Endpoint     string   `json:"endpoint"`
	Issuer       string   `json:"issuer"`
	URLLocal     bool     `json:"url_local"`
	// La commande d'un client local, qui lit la base sans passer par HTTP.
	StdioCommand string            `json:"stdio_command"`
	Tokens       []mcpTokenJSON    `json:"tokens"`
	Sessions     []mcpSessionJSON  `json:"sessions"`
	Limits       mcpLimitsJSON     `json:"limits"`
	Lifetimes    mcpLifetimesJSON  `json:"lifetimes"`
	Tools        mcpToolCountsJSON `json:"tools"`
	EnabledAt    *time.Time        `json:"enabled_at"`
}

type mcpTokenJSON struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Masked     string     `json:"masked"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

type mcpSessionJSON struct {
	ID         string     `json:"id"`
	StartedAt  time.Time  `json:"started_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
}

type mcpLimitsJSON struct {
	Tokens       int `json:"tokens"`
	RedirectURIs int `json:"redirect_uris"`
}

// Les durées du produit, en secondes : le front les dit à l'opérateur.
type mcpLifetimesJSON struct {
	AccessSeconds  int `json:"access_seconds"`
	RefreshSeconds int `json:"refresh_seconds"`
}

type mcpToolCountsJSON struct {
	Reads  int `json:"reads"`
	Writes int `json:"writes"`
}

// Le secret ou le jeton en clair, une seule fois.
type mcpSecretResponse struct {
	Secret string `json:"secret"`
}

type mcpTokenResponse struct {
	Token string       `json:"token"`
	Item  mcpTokenJSON `json:"item"`
}

type mcpTokenRequest struct {
	Name string `json:"name"`
}

type mcpRedirectURIsRequest struct {
	RedirectURIs []string `json:"redirect_uris"`
}

func (s *Server) getMCP(w http.ResponseWriter, r *http.Request) {
	response, err := s.mcpState(r)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) mcpState(r *http.Request) (mcpResponse, error) {
	client, enabled, err := s.mcp.Client(r.Context())
	if err != nil {
		return mcpResponse{}, err
	}
	publicURL, isLocal := s.resolvePublicURL(r)
	response := mcpResponse{
		Enabled:      enabled,
		ClientID:     mcp.ClientID,
		RedirectURIs: []string{},
		Endpoint:     publicURL + mcpPath,
		Issuer:       publicURL,
		URLLocal:     isLocal,
		StdioCommand: "opencloud mcp -config " + s.configPath,
		Tokens:       []mcpTokenJSON{},
		Sessions:     []mcpSessionJSON{},
		Limits:       mcpLimitsJSON{Tokens: mcp.MaxAPITokens, RedirectURIs: mcp.MaxRedirectURIs},
		Lifetimes:    mcpLifetimesJSON{AccessSeconds: int(mcp.AccessTTL.Seconds()), RefreshSeconds: int(mcp.RefreshTTL.Seconds())},
		Tools:        mcpToolCountsJSON{Reads: mcpReadToolCount, Writes: mcpWriteToolCount},
	}
	if !enabled {
		return response, nil
	}
	response.SecretMasked = client.SecretPrefix + "…"
	response.RedirectURIs = client.RedirectURIs
	response.EnabledAt = timeOrNil(client.CreatedAt)
	tokens, err := s.mcp.APITokens(r.Context())
	if err != nil {
		return mcpResponse{}, err
	}
	for _, token := range tokens {
		response.Tokens = append(response.Tokens, mcpTokenToJSON(token))
	}
	sessions, err := s.mcp.Sessions(r.Context())
	if err != nil {
		return mcpResponse{}, err
	}
	for _, session := range sessions {
		response.Sessions = append(response.Sessions, mcpSessionJSON{
			ID: session.FamilyID, StartedAt: session.StartedAt.UTC(), LastUsedAt: timeOrNil(session.LastUsedAt), ExpiresAt: session.ExpiresAt.UTC(),
		})
	}
	return response, nil
}

func mcpTokenToJSON(token mcp.Token) mcpTokenJSON {
	return mcpTokenJSON{ID: token.ID, Name: token.Name, Masked: token.Masked(), CreatedAt: token.CreatedAt.UTC(), LastUsedAt: timeOrNil(token.LastUsedAt)}
}

// enableMCP tire le secret et le montre une seule fois.
func (s *Server) enableMCP(w http.ResponseWriter, r *http.Request) {
	secret, _, err := s.mcp.Enable(r.Context())
	if errors.Is(err, mcp.ErrAlreadyEnabled) {
		s.apiRefuse(w, http.StatusConflict, "mcp.already_enabled")
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusCreated, mcpSecretResponse{Secret: secret})
}

func (s *Server) disableMCP(w http.ResponseWriter, r *http.Request) {
	s.finishAPIAction(w, r, s.mcp.Disable(r.Context()), mcp.ErrNotFound)
}

func (s *Server) regenerateMCPSecret(w http.ResponseWriter, r *http.Request) {
	secret, err := s.mcp.RegenerateSecret(r.Context())
	if errors.Is(err, mcp.ErrNotFound) {
		s.apiRefuse(w, http.StatusConflict, "mcp.disabled")
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, mcpSecretResponse{Secret: secret})
}

func (s *Server) setMCPRedirectURIs(w http.ResponseWriter, r *http.Request) {
	var request mcpRedirectURIsRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	_, err := s.mcp.SetRedirectURIs(r.Context(), request.RedirectURIs)
	switch {
	case errors.Is(err, mcp.ErrRedirectURIInvalid):
		s.apiRefuse(w, http.StatusUnprocessableEntity, "mcp.redirect_uri_invalid")
		return
	case errors.Is(err, mcp.ErrTooManyRedirectURIs):
		s.apiRefuse(w, http.StatusUnprocessableEntity, "mcp.too_many_redirect_uris")
		return
	case errors.Is(err, mcp.ErrNotFound):
		s.apiRefuse(w, http.StatusConflict, "mcp.disabled")
		return
	case err != nil:
		s.apiInternalError(w, r, err)
		return
	}
	s.getMCP(w, r)
}

// createMCPToken tire un jeton d'API et le montre une seule fois.
func (s *Server) createMCPToken(w http.ResponseWriter, r *http.Request) {
	var request mcpTokenRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	cleartext, token, err := s.mcp.CreateAPIToken(r.Context(), request.Name)
	switch {
	case errors.Is(err, mcp.ErrDisabled):
		s.apiRefuse(w, http.StatusConflict, "mcp.disabled")
		return
	case errors.Is(err, mcp.ErrNameInvalid):
		s.apiRefuse(w, http.StatusUnprocessableEntity, "mcp.name_invalid")
		return
	case errors.Is(err, mcp.ErrTooManyTokens):
		s.apiRefuse(w, http.StatusUnprocessableEntity, "mcp.too_many_tokens")
		return
	case err != nil:
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusCreated, mcpTokenResponse{Token: cleartext, Item: mcpTokenToJSON(token)})
}

func (s *Server) revokeMCPToken(w http.ResponseWriter, r *http.Request) {
	s.finishAPIAction(w, r, s.mcp.RevokeToken(r.Context(), r.PathValue("id")), mcp.ErrNotFound)
}

func (s *Server) revokeMCPSession(w http.ResponseWriter, r *http.Request) {
	s.finishAPIAction(w, r, s.mcp.RevokeSession(r.Context(), r.PathValue("id")), mcp.ErrNotFound)
}
