package server

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/ldesfontaine/opencloud/internal/mcp"
)

// Le serveur OAuth 2.1 que les clients MCP attendent : les métadonnées de
// découverte, l'autorisation avec PKCE S256 obligatoire, le point de
// jeton avec les deux grants. Les réponses parlent le vocabulaire des RFC
// 6749 et 8414, en anglais : elles sont lues par un client, jamais par
// l'opérateur. Aucune page de consentement : le secret du client, vérifié
// à l'échange, est la seule preuve. Tout ceci ferme quand MCP n'est pas activé.

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
	if !s.mcpLimits.allow(s.clientAddress(r)) {
		s.writeTooMany(w)
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
	if !s.mcpLimits.allow(s.clientAddress(r)) {
		s.writeTooMany(w)
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
// pour dire une erreur ; refusé, la réponse est un 400 sans redirect.
func (s *Server) oauthAuthorize(w http.ResponseWriter, r *http.Request) {
	if !s.mcpLimits.allow(s.clientAddress(r)) {
		s.writeTooMany(w)
		return
	}
	if !s.mcpEnabled(w, r) {
		return
	}
	query := r.URL.Query()
	redirectURI := query.Get("redirect_uri")
	allowed, err := s.mcp.RedirectAllowed(r.Context(), redirectURI)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	if !allowed {
		s.logger.Warn("mcp authorize refused", "reason", "redirect_uri", "redirect_uri", redirectURI)
		s.writeAPI(w, http.StatusBadRequest, oauthError{Error: "invalid_request", Description: "invalid redirect_uri"})
		return
	}
	code, err := s.mcp.Authorize(r.Context(), mcp.AuthorizeRequest{
		ResponseType:    query.Get("response_type"),
		ClientID:        query.Get("client_id"),
		RedirectURI:     redirectURI,
		Challenge:       query.Get("code_challenge"),
		ChallengeMethod: query.Get("code_challenge_method"),
	})
	state := query.Get("state")
	if err != nil {
		s.logger.Warn("mcp authorize refused", "reason", err)
		s.redirectWith(w, r, redirectURI, authorizeErrorParams(err, state))
		return
	}
	s.redirectWith(w, r, redirectURI, url.Values{"code": {code}, "state": {state}})
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
// siens ; un state vide ne s'écrit pas.
func (s *Server) redirectWith(w http.ResponseWriter, r *http.Request, redirectURI string, params url.Values) {
	target, err := url.Parse(redirectURI)
	if err != nil {
		s.writeAPI(w, http.StatusBadRequest, oauthError{Error: "invalid_request", Description: "invalid redirect_uri"})
		return
	}
	query := target.Query()
	for key, values := range params {
		if values[0] != "" {
			query.Set(key, values[0])
		}
	}
	target.RawQuery = query.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound) // #nosec G710 -- redirect_uri jugé par RedirectAllowed avant tout redirect.
}

// oauthToken : les identifiants du client viennent du corps ou de
// l'en-tête Basic, les deux façons que les clients pratiquent.
func (s *Server) oauthToken(w http.ResponseWriter, r *http.Request) {
	if !s.mcpLimits.allowToken(s.clientAddress(r)) {
		s.writeTooMany(w)
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
