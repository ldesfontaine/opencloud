package server

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/ldesfontaine/opencloud/internal/mcp"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
)

// Le serveur OAuth 2.1 que les clients MCP attendent : les métadonnées de
// découverte, l'autorisation avec PKCE S256 obligatoire, le consentement de
// l'opérateur, le point de jeton avec les deux grants. Les réponses au
// client parlent le vocabulaire des RFC 6749 et 8414, en anglais. Le
// consentement est une page du front : l'opérateur, authentifié, voit le
// client, l'URI de retour et la portée, puis autorise ou refuse ; le code
// n'existe qu'après son oui. Tout ceci ferme quand MCP n'est pas activé.

type serverMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
}

type resourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
	ResourceName           string   `json:"resource_name"`
}

type oauthError struct {
	Error       string `json:"error"`
	Description string `json:"error_description,omitempty"`
}

type grantResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

func (s *Server) oauthServerMetadata(w http.ResponseWriter, r *http.Request) {
	if !s.mcpLimit.Allow(s.clientAddress(r)) {
		httpx.WriteTooMany(w)
		return
	}
	if !s.mcpEnabled(w, r) {
		return
	}
	issuer, _ := s.resolvePublicURL(r)
	s.writeAPI(w, http.StatusOK, serverMetadata{
		Issuer:                            issuer,
		AuthorizationEndpoint:             issuer + mcpAuthorizePath,
		TokenEndpoint:                     issuer + mcpTokenPath,
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code", "refresh_token"},
		TokenEndpointAuthMethodsSupported: []string{"client_secret_post", "client_secret_basic"},
		CodeChallengeMethodsSupported:     []string{mcp.ChallengeMethod},
	})
}

func (s *Server) oauthResourceMetadata(w http.ResponseWriter, r *http.Request) {
	if !s.mcpLimit.Allow(s.clientAddress(r)) {
		httpx.WriteTooMany(w)
		return
	}
	if !s.mcpEnabled(w, r) {
		return
	}
	issuer, _ := s.resolvePublicURL(r)
	s.writeAPI(w, http.StatusOK, resourceMetadata{
		Resource:               issuer + mcpPath,
		AuthorizationServers:   []string{issuer},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           mcpServerName,
	})
}

// oauthAuthorize : le redirect_uri est jugé avant tout redirect, y compris
// pour dire une erreur ; refusé, la réponse est un 400 sans redirect. Une
// demande valide attend l'opérateur : le navigateur part vers la page de
// consentement avec l'identifiant opaque de la demande, rien d'autre.
func (s *Server) oauthAuthorize(w http.ResponseWriter, r *http.Request) {
	if !s.mcpLimit.Allow(s.clientAddress(r)) {
		httpx.WriteTooMany(w)
		return
	}
	if !s.mcpEnabled(w, r) {
		return
	}
	query := r.URL.Query()
	redirectURI := query.Get("redirect_uri")
	allowed, err := s.mcp.RedirectAllowed(r.Context(), redirectURI)
	if s.failed(w, r, err, nil) {
		return
	}
	if !allowed {
		s.logger.Warn("mcp authorize refused", "reason", "redirect_uri", "redirect_uri", redirectURI)
		s.writeAPI(w, http.StatusBadRequest, oauthError{Error: "invalid_request", Description: "invalid redirect_uri"})
		return
	}
	state := query.Get("state")
	consent, err := s.mcp.RequestConsent(r.Context(), mcp.AuthorizeRequest{
		ResponseType:    query.Get("response_type"),
		ClientID:        query.Get("client_id"),
		RedirectURI:     redirectURI,
		State:           state,
		Challenge:       query.Get("code_challenge"),
		ChallengeMethod: query.Get("code_challenge_method"),
		Scope:           query.Get("scope"),
	})
	if err != nil {
		s.logger.Warn("mcp authorize refused", "reason", err)
		s.redirectWith(w, r, redirectURI, authorizeErrorParams(err, state))
		return
	}
	page := mcpConsentPage + "?" + url.Values{mcpConsentParam: {consent.ID}}.Encode()
	http.Redirect(w, r, page, http.StatusFound)
}

func authorizeErrorParams(err error, state string) url.Values {
	params := url.Values{"state": {state}}
	switch {
	case errors.Is(err, mcp.ErrResponseTypeInvalid):
		params.Set("error", "unsupported_response_type")
	case errors.Is(err, mcp.ErrClientUnknown):
		params.Set("error", "unauthorized_client")
	case errors.Is(err, mcp.ErrChallengeInvalid):
		params.Set("error", "invalid_request")
		params.Set("error_description", "code_challenge with method S256 is required")
	default:
		params.Set("error", "server_error")
	}
	return params
}

// redirectWith renvoie vers l'URI déjà jugée, ses paramètres ajoutés aux
// siens.
func (s *Server) redirectWith(w http.ResponseWriter, r *http.Request, redirectURI string, params url.Values) {
	target, err := redirectTarget(redirectURI, params)
	if err != nil {
		s.writeAPI(w, http.StatusBadRequest, oauthError{Error: "invalid_request", Description: "invalid redirect_uri"})
		return
	}
	http.Redirect(w, r, target, http.StatusFound) // #nosec G710 -- redirect_uri jugé par RedirectAllowed avant tout redirect.
}

// redirectTarget ajoute les paramètres à ceux de l'URI ; un state vide ne
// s'écrit pas.
func redirectTarget(redirectURI string, params url.Values) (string, error) {
	target, err := url.Parse(redirectURI)
	if err != nil {
		return "", err
	}
	query := target.Query()
	for key, values := range params {
		if len(values) > 0 && values[0] != "" {
			query.Set(key, values[0])
		}
	}
	target.RawQuery = query.Encode()
	return target.String(), nil
}

// --- Le consentement, sous /api : la page du front le lit, puis répond ---

// La demande telle que la page la montre : le client, l'URI de retour
// exacte, ce que l'autorisation donne et combien de temps. WriteAvailable
// est faux tant que /mcp est en lecture seule : la page ne propose pas
// l'écriture.
type mcpConsentJSON struct {
	ID             string    `json:"id"`
	ClientID       string    `json:"client_id"`
	RedirectURI    string    `json:"redirect_uri"`
	WriteRequested bool      `json:"write_requested"`
	WriteAvailable bool      `json:"write_available"`
	ReadTools      int       `json:"read_tools"`
	AccessSeconds  int       `json:"access_seconds"`
	RefreshSeconds int       `json:"refresh_seconds"`
	ExpiresAt      time.Time `json:"expires_at"`
}

// La décision de l'opérateur, en plus de l'identifiant porté par le
// chemin : l'écriture cochée ou non.
type mcpConsentDecision struct {
	Write bool `json:"write"`
}

// Où le navigateur part ensuite : l'URI de retour du client, avec le code
// ou l'erreur. Une réponse JSON, pas un 302 : fetch suivrait le redirect
// lui-même, hors de la page.
type mcpConsentAnswer struct {
	Redirect string `json:"redirect"`
}

func (s *Server) getMCPConsent(w http.ResponseWriter, r *http.Request) {
	consent, err := s.mcp.PendingConsent(r.Context(), r.PathValue("id"))
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, mcpConsentJSON{
		ID: consent.ID, ClientID: consent.ClientID, RedirectURI: consent.RedirectURI,
		WriteRequested: consent.WriteRequested, WriteAvailable: !mcpHTTPReadOnly,
		ReadTools: mcpReadToolCount, AccessSeconds: int(mcp.AccessTTL.Seconds()), RefreshSeconds: int(mcp.RefreshTTL.Seconds()),
		ExpiresAt: consent.ExpiresAt.UTC(),
	})
}

// allowMCPConsent pose le code : c'est une autorité durable, gardée par
// grantsAuthority. L'écriture cochée ne compte pas tant que /mcp est en
// lecture seule : un jeton write qui dort se réveillerait avec les
// écritures, sans que l'opérateur l'ait vu proposer.
func (s *Server) allowMCPConsent(w http.ResponseWriter, r *http.Request) {
	var decision mcpConsentDecision
	if !s.readAPI(w, r, &decision) {
		return
	}
	allowWrite := decision.Write && !mcpHTTPReadOnly
	consent, code, err := s.mcp.ApproveConsent(r.Context(), r.PathValue("id"), allowWrite)
	if s.failed(w, r, err, nil) {
		return
	}
	s.answerConsent(w, r, consent, url.Values{"code": {code}, "state": {consent.State}})
}

// denyMCPConsent : RFC 6749 §4.1.2.1, access_denied avec le state du client.
func (s *Server) denyMCPConsent(w http.ResponseWriter, r *http.Request) {
	consent, err := s.mcp.DenyConsent(r.Context(), r.PathValue("id"))
	if s.failed(w, r, err, nil) {
		return
	}
	s.answerConsent(w, r, consent, url.Values{"error": {"access_denied"}, "state": {consent.State}})
}

func (s *Server) answerConsent(w http.ResponseWriter, r *http.Request, consent mcp.Consent, params url.Values) {
	target, err := redirectTarget(consent.RedirectURI, params)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, mcpConsentAnswer{Redirect: target})
}

// oauthToken : les identifiants du client viennent du corps ou de
// l'en-tête Basic, les deux façons que les clients pratiquent.
func (s *Server) oauthToken(w http.ResponseWriter, r *http.Request) {
	if !s.mcpTokenLimit.Allow(s.clientAddress(r)) {
		httpx.WriteTooMany(w)
		return
	}
	if !s.mcpEnabled(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.writeAPI(w, http.StatusBadRequest, oauthError{Error: "invalid_request", Description: "malformed form"})
		return
	}
	clientID, clientSecret := r.PostFormValue("client_id"), r.PostFormValue("client_secret")
	if user, password, ok := r.BasicAuth(); ok {
		clientID, clientSecret = user, password
	}
	// Un code ou un rafraîchissement consommé avant la révocation de
	// l'activation ne doit pas en ressortir des jetons écrits après elle :
	// voir grantsAuthority. Le formulaire est déjà lu, un client lent ne
	// tient pas le verrou.
	s.authTransition.RLock()
	defer s.authTransition.RUnlock()
	var grant mcp.Grant
	var err error
	switch r.PostFormValue("grant_type") {
	case "authorization_code":
		grant, err = s.mcp.Exchange(r.Context(), mcp.ExchangeRequest{
			ClientID: clientID, ClientSecret: clientSecret, Code: r.PostFormValue("code"),
			RedirectURI: r.PostFormValue("redirect_uri"), Verifier: r.PostFormValue("code_verifier"),
		})
	case "refresh_token":
		grant, err = s.mcp.Refresh(r.Context(), mcp.RefreshRequest{ClientID: clientID, ClientSecret: clientSecret, RefreshToken: r.PostFormValue("refresh_token")})
	default:
		s.writeAPI(w, http.StatusBadRequest, oauthError{Error: "unsupported_grant_type"})
		return
	}
	if err != nil {
		s.writeTokenError(w, r, err)
		return
	}
	w.Header().Set("Pragma", "no-cache")
	s.writeAPI(w, http.StatusOK, grantResponse{AccessToken: grant.AccessToken, TokenType: "Bearer", ExpiresIn: grant.ExpiresIn, RefreshToken: grant.RefreshToken})
}

func (s *Server) writeTokenError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, mcp.ErrSecretInvalid):
		s.logger.Warn("mcp token refused", "reason", "client")
		s.writeAPI(w, http.StatusUnauthorized, oauthError{Error: "invalid_client"})
	case errors.Is(err, mcp.ErrCodeInvalid), errors.Is(err, mcp.ErrVerifierInvalid), errors.Is(err, mcp.ErrTokenInvalid), errors.Is(err, mcp.ErrTokenReplayed):
		s.logger.Warn("mcp token refused", "reason", err)
		s.writeAPI(w, http.StatusBadRequest, oauthError{Error: "invalid_grant"})
	default:
		s.logger.Error("mcp token failed", "error", err)
		s.writeAPI(w, http.StatusInternalServerError, oauthError{Error: "server_error"})
	}
}
