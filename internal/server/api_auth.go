package server

import (
	"net/http"
	"slices"
	"time"

	"github.com/ldesfontaine/opencloud/internal/auth"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
)

type authResponse struct {
	Mode string `json:"mode"`
	// En forward, l'identité transmise avec cette requête : rien ne la stocke.
	Username string `json:"username"`
	// Le nom de l'en-tête d'identité, en forward seulement.
	Header    string     `json:"header,omitempty"`
	EnabledAt *time.Time `json:"enabled_at"`
	// En forward, vrai quand rien ne prouve que la requête passe par le
	// mandataire : pas de secret, et la boucle locale est de confiance. Le
	// mode reste actif (le couper enfermerait l'opérateur), la page le dit.
	ForwardInsecure bool `json:"forward_insecure,omitempty"`
	// En forward, la liste blanche de forward_users ; null quand elle est vide
	// (toute identité du mandataire est admise) et dans les autres modes.
	AllowedUsers []string `json:"allowed_users"`
	// La longueur minimale, pour que le front la dise et la vérifie avant
	// d'envoyer ; le serveur reste le juge.
	PasswordMinLength int `json:"password_min_length"`
}

// Activer l'authentification révoque tous les jetons d'API : la réponse dit
// combien sont tombés, l'interface le montre.
type authEnableResponse struct {
	authResponse
	APITokensRevoked int `json:"api_tokens_revoked"`
}

// Mode vide vaut basic : le corps d'avant, {username,password}, reste bon.
// Header ne sert qu'en forward, Remote-User s'il est vide.
type authEnableRequest struct {
	Mode     string `json:"mode"`
	Username string `json:"username"`
	Password string `json:"password"`
	Header   string `json:"header"`
}

type authPasswordRequest struct {
	Password string `json:"password"`
}

// Le corps d'une action qui n'a rien à dire : {} ou rien.
type emptyRequest struct{}

// authToJSON : identity est l'identité transmise par le mandataire, lue
// seulement en forward.
func authToJSON(state auth.State, identity string) authResponse {
	response := authResponse{Mode: string(state.Mode), PasswordMinLength: auth.MinPasswordLength}
	switch state.Mode {
	case auth.ModeBasic:
		response.Username = state.Username
		response.EnabledAt = httpx.TimeOrNil(state.EnabledAt)
	case auth.ModeForward:
		response.Username = identity
		response.Header = state.Header
		response.EnabledAt = httpx.TimeOrNil(state.EnabledAt)
	}
	return response
}

func (s *Server) getAuth(w http.ResponseWriter, r *http.Request) {
	state, err := s.auth.State(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	response := s.authJSON(state, identityFrom(r.Context()))
	response.ForwardInsecure = state.Mode == auth.ModeForward && s.forwardSecretRequired()
	s.writeAPI(w, http.StatusOK, response)
}

// authJSON ajoute à authToJSON ce que seul le serveur sait : la liste blanche
// de config.toml, qui ne vaut qu'en forward.
func (s *Server) authJSON(state auth.State, identity string) authResponse {
	response := authToJSON(state, identity)
	if state.Mode == auth.ModeForward {
		response.AllowedUsers = s.allowedUsers()
	}
	return response
}

// allowedUsers rend la liste blanche triée, ou nil quand elle est vide : le
// JSON dit alors null, « toute identité du mandataire ».
func (s *Server) allowedUsers() []string {
	if len(s.forwardUsers) == 0 {
		return nil
	}
	users := make([]string, 0, len(s.forwardUsers))
	for user := range s.forwardUsers {
		users = append(users, user)
	}
	slices.Sort(users)
	return users
}

func (s *Server) enableAuth(w http.ResponseWriter, r *http.Request) {
	var request authEnableRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	// Le changement d'état et la révocation forment un seul pas pour les
	// écritures qui accordent une autorité : voir grantsAuthority.
	s.authTransition.Lock()
	defer s.authTransition.Unlock()
	state, identity, ok := s.enableMode(w, r, request)
	if !ok {
		return
	}
	// Un jeton d'API tiré sous un accès d'avant (laissé par la commande de
	// secours) ne survit pas à l'activation. Après elle, et non avant, pour
	// qu'un jeton créé entre les deux tombe aussi.
	revoked, err := s.apiTokens.RevokeAll(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, authEnableResponse{
		authResponse:     s.authJSON(state, identity),
		APITokensRevoked: revoked,
	})
}

// enableMode active le mode demandé et rend l'identité à afficher ; il
// répond lui-même quand il refuse.
func (s *Server) enableMode(w http.ResponseWriter, r *http.Request, request authEnableRequest) (auth.State, string, bool) {
	switch auth.Mode(request.Mode) {
	case "", auth.ModeBasic:
		authorizing, _ := authorizingStateFrom(r.Context())
		state, err := s.auth.EnableBasic(r.Context(), authorizing, request.Username, request.Password)
		return state, "", !s.failed(w, r, err, nil)
	case auth.ModeForward:
		return s.enableForward(w, r, request.Header)
	default:
		s.apiRefuse(w, http.StatusUnprocessableEntity, "auth.mode_invalid")
		return auth.State{}, "", false
	}
}

// enableForward refuse d'activer depuis une requête qui ne porte pas déjà
// l'identité d'un mandataire de confiance : sitôt le mode posé, tout le
// reste répond 401, et l'opérateur se serait fermé la porte.
func (s *Server) enableForward(w http.ResponseWriter, r *http.Request, header string) (auth.State, string, bool) {
	if header == "" {
		header = auth.DefaultForwardHeader
	}
	err := auth.ValidateHeader(header)
	if s.failed(w, r, err, nil) {
		return auth.State{}, "", false
	}
	// Avant l'identité : sans secret, aucun en-tête reçu de la boucle locale
	// ne prouve rien, et l'opérateur doit savoir quoi poser, pas seulement
	// que l'en-tête manque.
	if s.forwardSecretRequired() {
		s.apiRefuse(w, http.StatusUnprocessableEntity, "auth.forward_secret_required")
		return auth.State{}, "", false
	}
	identity := s.trustedIdentity(r, header)
	if identity == "" {
		s.apiRefuse(w, http.StatusUnprocessableEntity, "auth.forward_header_missing")
		return auth.State{}, "", false
	}
	// Même garde-fou que l'en-tête : une identité que la liste refuserait
	// aussitôt se fermerait la porte en activant.
	if !s.admitsForwardUser(identity) {
		s.apiRefuse(w, http.StatusUnprocessableEntity, "auth.forward_user_not_listed")
		return auth.State{}, "", false
	}
	authorizing, _ := authorizingStateFrom(r.Context())
	state, err := s.auth.EnableForward(r.Context(), authorizing, header)
	if s.failed(w, r, err, nil) {
		return auth.State{}, "", false
	}
	return state, identity, true
}

func (s *Server) changeAuthPassword(w http.ResponseWriter, r *http.Request) {
	var request authPasswordRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	authorizing, _ := authorizingStateFrom(r.Context())
	state, err := s.auth.ChangePassword(r.Context(), authorizing, request.Password)
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, authToJSON(state, ""))
}

func (s *Server) disableAuth(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength != 0 {
		var request emptyRequest
		if !s.readAPI(w, r, &request) {
			return
		}
	}
	authorizing, _ := authorizingStateFrom(r.Context())
	if s.failed(w, r, s.auth.Disable(r.Context(), authorizing), nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, authToJSON(auth.State{Mode: auth.ModeNone}, ""))
}
