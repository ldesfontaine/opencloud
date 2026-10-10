package server

import (
	"context"
	"errors"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/auth"
	"github.com/ldesfontaine/opencloud/internal/ratelimit"
)

// L'accès de l'opérateur à l'interface. En mode basic, HTTP Basic garde
// tout ce que l'opérateur appelle : le front, l'API, le direct, les flux
// de journaux et l'autorisation OAuth, où il consent pour un agent IA.
// Restent ouverts ceux qui ont leur propre preuve ou n'en ont pas
// besoin : l'agent (sa clé), les pings (le jeton dans l'URL), la page de
// statut (publique), /mcp (son Bearer), le point de jeton OAuth (le secret
// du client) et les métadonnées well-known.

const (
	authRealmHeader  = `Basic realm="openCloud", charset="UTF-8"`
	codeUnauthorized = "unauthorized"
	// Une vérification lente coûte ~100 ms : dix d'un coup, puis une toutes
	// les six secondes par adresse. Des identifiants justes sont retenus et
	// ne repassent plus par ici.
	authAttemptRate  = 1.0 / 6
	authAttemptBurst = 10
	authSweepEvery   = 5 * time.Minute
	authSweepIdle    = 30 * time.Minute
)

// Ce que le serveur attend du composant auth.
type AuthService interface {
	State(ctx context.Context) (auth.State, error)
	Remembered(state auth.State, username, password string) bool
	Verify(state auth.State, username, password string) bool
	EnableBasic(ctx context.Context, username, password string) (auth.State, error)
	ChangePassword(ctx context.Context, password string) (auth.State, error)
	Disable(ctx context.Context) error
}

// Les préfixes qui ne passent jamais par l'authentification de
// l'opérateur. Tout le reste, fourre-tout du front compris, y passe.
var openRoutePrefixes = []string{"/agent/", "/ping/", "/statut", "/.well-known/"}

// Les routes ouvertes exactes, à part des préfixes : /oauth/authorize,
// sa voisine, est l'étape où l'opérateur consent, elle reste gardée.
var openRoutePatterns = []string{mcpPath, mcpTokenPath}

// isOperatorRoute dit si le motif de route est réservé à l'opérateur. Le
// jugement porte sur le motif enregistré, pas sur le chemin reçu : un
// chemin détourné (/ping/../api) est d'abord nettoyé par le mux.
func isOperatorRoute(pattern string) bool {
	for _, prefix := range openRoutePrefixes {
		if strings.HasPrefix(pattern, prefix) {
			return false
		}
	}
	for _, open := range openRoutePatterns {
		if pattern == open {
			return false
		}
	}
	return true
}

type authLimits struct {
	attempts *ratelimit.Limiter
	now      func() time.Time

	mu        sync.Mutex
	lastSweep time.Time
}

func newAuthLimits() *authLimits {
	return &authLimits{attempts: ratelimit.New(authAttemptRate, authAttemptBurst), now: time.Now}
}

func (l *authLimits) setClock(now func() time.Time) {
	l.now = now
	l.attempts.SetClock(now)
}

func (l *authLimits) allowAttempt(source string) bool {
	now := l.now()
	l.mu.Lock()
	due := now.Sub(l.lastSweep) >= authSweepEvery
	if due {
		l.lastSweep = now
	}
	l.mu.Unlock()
	if due {
		l.attempts.Sweep(authSweepIdle)
	}
	return l.attempts.Allow(source)
}

// requireOperator relit le mode à chaque requête : la commande de secours
// `opencloud auth disable` écrit dans la base, et serve la suit sans
// redémarrer.
func (s *Server) requireOperator(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.isCompiledAsset(r) {
			next(w, r)
			return
		}
		state, err := s.auth.State(r.Context())
		if err != nil {
			s.apiInternalError(w, r, err)
			return
		}
		switch state.Mode {
		case auth.ModeNone:
			next(w, r)
		case auth.ModeBasic:
			if s.checkBasic(w, r, state) {
				next(w, r)
			}
		default:
			// forward n'est pas encore écrit : fermer plutôt que deviner.
			s.logger.Error("interface authentication mode not supported", "mode", string(state.Mode))
			http.Error(w, "authentication mode not supported", http.StatusServiceUnavailable)
		}
	}
}

// isCompiledAsset dit si la requête lit un fichier du front compilé, hors
// index.html : la page de statut publique charge ses scripts, ses styles et
// ses polices par le même fourre-tout que l'interface, et un visiteur sans
// identifiants verrait une page vide. Ces fichiers sont le même code pour
// tous, embarqué dans le binaire : les données restent derrière /api.
func (s *Server) isCompiledAsset(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == legacyFavicon {
		name = brandFavicon
	}
	return name != "" && name != indexFile && s.isAppFile(name)
}

// checkBasic répond lui-même 401 ou 429 quand il refuse.
func (s *Server) checkBasic(w http.ResponseWriter, r *http.Request, state auth.State) bool {
	username, password, presented := r.BasicAuth()
	if !presented {
		s.writeUnauthorized(w, r)
		return false
	}
	if s.auth.Remembered(state, username, password) {
		return true
	}
	source := s.clientAddress(r)
	if !s.authLimits.allowAttempt(source) {
		s.logger.Warn("interface authentication rate limited", "source", source)
		s.writeTooMany(w)
		return false
	}
	if !s.auth.Verify(state, username, password) {
		s.logger.Warn("interface authentication failed", "source", source, "path", r.URL.Path)
		s.writeUnauthorized(w, r)
		return false
	}
	return true
}

// Le défi Basic : le navigateur demande les identifiants, puis les
// renvoie seul à chaque requête de la même origine, EventSource compris.
func (s *Server) writeUnauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", authRealmHeader)
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.writeAPIError(w, http.StatusUnauthorized, codeUnauthorized)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, "authentication required", http.StatusUnauthorized)
}

// --- API : la carte « Accès à l'interface » de Paramètres ---

type authResponse struct {
	Mode      string     `json:"mode"`
	Username  string     `json:"username"`
	EnabledAt *time.Time `json:"enabled_at"`
	// La longueur minimale, pour que le front la dise et la vérifie avant
	// d'envoyer ; le serveur reste le juge.
	PasswordMinLength int `json:"password_min_length"`
}

type authEnableRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authPasswordRequest struct {
	Password string `json:"password"`
}

// Le corps d'une action qui n'a rien à dire : {} ou rien.
type emptyRequest struct{}

func authToJSON(state auth.State) authResponse {
	response := authResponse{Mode: string(state.Mode), PasswordMinLength: auth.MinPasswordLength}
	if state.Mode == auth.ModeBasic {
		response.Username = state.Username
		response.EnabledAt = timeOrNil(state.EnabledAt)
	}
	return response
}

func (s *Server) getAuth(w http.ResponseWriter, r *http.Request) {
	state, err := s.auth.State(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, authToJSON(state))
}

func (s *Server) enableAuth(w http.ResponseWriter, r *http.Request) {
	var request authEnableRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	state, err := s.auth.EnableBasic(r.Context(), request.Username, request.Password)
	if s.refuseAuth(w, r, err) {
		return
	}
	s.writeAPI(w, http.StatusOK, authToJSON(state))
}

func (s *Server) changeAuthPassword(w http.ResponseWriter, r *http.Request) {
	var request authPasswordRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	state, err := s.auth.ChangePassword(r.Context(), request.Password)
	if s.refuseAuth(w, r, err) {
		return
	}
	s.writeAPI(w, http.StatusOK, authToJSON(state))
}

func (s *Server) disableAuth(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength != 0 {
		var request emptyRequest
		if !s.readAPI(w, r, &request) {
			return
		}
	}
	if s.refuseAuth(w, r, s.auth.Disable(r.Context())) {
		return
	}
	s.writeAPI(w, http.StatusOK, authToJSON(auth.State{Mode: auth.ModeNone}))
}

// refuseAuth traduit une erreur du composant en refus du catalogue, et
// dit si la réponse est partie.
func (s *Server) refuseAuth(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, auth.ErrUsernameInvalid):
		s.apiRefuse(w, http.StatusUnprocessableEntity, "auth.username_invalid")
	case errors.Is(err, auth.ErrPasswordTooShort):
		s.apiRefuse(w, http.StatusUnprocessableEntity, "auth.password_too_short")
	case errors.Is(err, auth.ErrPasswordTooLong):
		s.apiRefuse(w, http.StatusUnprocessableEntity, "auth.password_too_long")
	case errors.Is(err, auth.ErrAlreadyEnabled):
		s.apiRefuse(w, http.StatusConflict, "auth.already_enabled")
	case errors.Is(err, auth.ErrDisabled):
		s.apiRefuse(w, http.StatusConflict, "auth.disabled")
	default:
		s.apiInternalError(w, r, err)
	}
	return true
}
