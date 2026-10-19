package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/auth"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/ratelimit"
)

// L'accès de l'opérateur à l'interface. En mode basic, HTTP Basic garde
// tout ce que l'opérateur appelle : le front, l'API, le direct, les flux
// de journaux et l'autorisation OAuth, où il consent pour un agent IA. En
// mode forward, les mêmes routes exigent l'en-tête d'identité posé par un
// mandataire de trusted_proxies.
// Restent ouverts ceux qui ont leur propre preuve ou n'en ont pas
// besoin : l'agent (sa clé), les pings (le jeton dans l'URL), la page de
// statut (publique), /mcp (son Bearer), le point de jeton OAuth (le secret
// du client) et les métadonnées well-known.

const (
	authRealmHeader  = `Basic realm="` + brand.ProductName + `", charset="UTF-8"`
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
	Verify(ctx context.Context, state auth.State, username, password string) (bool, error)
	EnableBasic(ctx context.Context, expected auth.State, username, password string) (auth.State, error)
	EnableForward(ctx context.Context, expected auth.State, header string) (auth.State, error)
	ChangePassword(ctx context.Context, expected auth.State, password string) (auth.State, error)
	Disable(ctx context.Context, expected auth.State) error
}

// codeAuthStateChanged est le refus d'une écriture autorisée sous un état
// d'authentification qui n'est plus le bon.
const codeAuthStateChanged = "auth.state_changed"

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
	return l.attempts.Allow(ratelimit.SourceKey(source))
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
			s.admitOperator(w, r, state, next)
		case auth.ModeBasic:
			if s.checkBasic(w, r, state) {
				s.admitOperator(w, r, state, next)
			}
		case auth.ModeForward:
			identity, ok := s.checkForward(w, r, state)
			if ok {
				s.admitOperator(w, r.WithContext(context.WithValue(r.Context(), forwardedIdentityKey{}, identity)), state, next)
			}
		default:
			// Un mode inconnu dans la base : fermer plutôt que deviner.
			s.logger.Error("interface authentication mode not supported", "mode", string(state.Mode))
			http.Error(w, "authentication mode not supported", http.StatusServiceUnavailable)
		}
	}
}

// admitOperator laisse passer une requête admise sous state, et pose cet
// état dans son contexte : les écritures qui accordent une autorité et les
// flux s'y comparent à l'état du moment.
//
// Sans ReadTimeout, un client peut retenir le corps d'une écriture aussi
// longtemps qu'il veut : un jugement rendu à l'arrivée des en-têtes vaudrait
// encore une fois l'authentification activée. Le corps est donc lu en entier,
// puis l'état relu ; s'il a changé entre-temps, 409. La lecture vient après
// le jugement, et non avant, pour qu'un anonyme face à une interface gardée
// reçoive son 401 sans faire tenir un Mio par connexion au serveur.
func (s *Server) admitOperator(w http.ResponseWriter, r *http.Request, state auth.State, next http.HandlerFunc) {
	if readWholeBody(w, r) {
		current, err := s.auth.State(r.Context())
		if err != nil {
			s.apiInternalError(w, r, err)
			return
		}
		if !current.Same(state) {
			s.apiRefuse(w, http.StatusConflict, codeAuthStateChanged)
			return
		}
	}
	next(w, r.WithContext(context.WithValue(r.Context(), authorizingStateKey{}, state)))
}

// readWholeBody lit le corps d'une écriture, le remet à la disposition du
// handler et dit s'il y en avait un. La borne est celle de limitBody, la plus
// large des routes : les bornes plus serrées de chaque handler (readAPI)
// jouent ensuite sur la copie. Un corps trop gros ou illisible n'est pas jugé
// ici : l'erreur est rendue au handler à la fin de ce qui a été lu, et la
// réponse reste la sienne (400 pour readAPI, 422 pour un fichier d'annuaire).
func readWholeBody(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Body == nil || r.Body == http.NoBody {
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBody))
	if err != nil {
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), failingReader{err: err}))
		return true
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	return true
}

// failingReader rend toujours la même erreur.
type failingReader struct{ err error }

func (f failingReader) Read([]byte) (int, error) {
	return 0, f.err
}

// authorizingStateKey porte, dans le contexte de la requête, l'état
// d'authentification sous lequel requireOperator l'a laissée passer.
type authorizingStateKey struct{}

// authorizingStateFrom rend cet état. Une route ouverte n'en a pas : l'état
// nul, qui n'égale aucun état stocké.
func authorizingStateFrom(ctx context.Context) (auth.State, bool) {
	state, ok := ctx.Value(authorizingStateKey{}).(auth.State)
	return state, ok
}

// grantsAuthority garde une écriture qui accorde une autorité durable (un
// jeton MCP, un code OAuth, le secret du client, un jeton d'enrôlement) : si
// l'état d'authentification n'est plus celui qui a admis la requête, 409.
//
// L'activation révoque après avoir changé l'état : sans verrou, une écriture
// qui a vu l'ancien état pourrait se glisser entre les deux, par exemple en
// attendant le verrou de la base pendant que l'activation écrit, et survivre
// à la révocation. Sous authTransition, elle s'écrit soit avant l'activation,
// et la révocation l'emporte, soit après, et elle voit le nouvel état. Le
// corps est déjà lu (admitOperator) : un client lent ne tient pas le verrou.
func (s *Server) grantsAuthority(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.authTransition.RLock()
		defer s.authTransition.RUnlock()
		current, err := s.auth.State(r.Context())
		if err != nil {
			s.apiInternalError(w, r, err)
			return
		}
		authorizing, _ := authorizingStateFrom(r.Context())
		if !current.Same(authorizing) {
			s.apiRefuse(w, http.StatusConflict, codeAuthStateChanged)
			return
		}
		next(w, r)
	}
}

// authorizationLapsed dit si un flux ouvert sur une route de l'opérateur ne
// doit plus continuer : l'état d'authentification n'est plus celui qui l'a
// autorisé, ou n'a pas pu être relu. Un flux hors des routes de l'opérateur
// n'a rien à perdre.
func (s *Server) authorizationLapsed(r *http.Request) bool {
	authorizing, ok := authorizingStateFrom(r.Context())
	if !ok {
		return false
	}
	current, err := s.auth.State(r.Context())
	if err != nil {
		return true
	}
	return !current.Same(authorizing)
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
	// Même règle que serveApp : une page HTML n'est jamais un fichier libre.
	return name != "" && path.Ext(name) != ".html" && s.isAppFile(name)
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
	verified, err := s.auth.Verify(r.Context(), state, username, password)
	switch {
	case errors.Is(err, auth.ErrVerificationQueueFull):
		// Même réponse que le limiteur : l'attente de bcrypt est bornée, et
		// l'opérateur qui retombe là réessaie après Retry-After.
		s.logger.Warn("interface authentication queue full", "source", source)
		s.writeTooMany(w)
		return false
	case err != nil:
		// La requête a pris fin pendant l'attente (client parti, arrêt) :
		// ce n'est pas un mauvais mot de passe, et personne ne lira la réponse.
		http.Error(w, "request canceled", http.StatusServiceUnavailable)
		return false
	case !verified:
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
	// Le navigateur affiche ce texte quand l'opérateur annule la fenêtre :
	// c'est là qu'il faut dire comment rouvrir l'accès.
	catalog := s.catalogs.For(browserLanguage(r))
	http.Error(w, catalog.Get("auth.unauthorized"), http.StatusUnauthorized)
}

// forwardedIdentityKey porte, dans le contexte de la requête, l'identité
// que le mandataire a transmise : /api/auth la rend à la barre latérale.
type forwardedIdentityKey struct{}

func identityFrom(ctx context.Context) string {
	identity, _ := ctx.Value(forwardedIdentityKey{}).(string)
	return identity
}

// checkForward ne croit l'en-tête d'identité que d'un mandataire déclaré :
// de n'importe où ailleurs, un conteneur voisin pourrait l'écrire lui-même.
// Quand un secret est configuré, il faut en plus que la requête le porte :
// l'adresse d'un pair de boucle locale est celle de tous les processus de la
// machine, pas la preuve de passer par le mandataire.
// Il répond lui-même 401 quand il refuse.
func (s *Server) checkForward(w http.ResponseWriter, r *http.Request, state auth.State) (string, bool) {
	if !s.trustsPeer(r) {
		s.logger.Warn("interface authentication refused: peer is not a trusted proxy", "peer", peerHost(r), "path", r.URL.Path)
		s.writeForwardUnauthorized(w, r)
		return "", false
	}
	if !s.hasProxySecret(r) {
		// La valeur reçue ne se journalise pas : un secret presque juste
		// resterait lisible dans le journal.
		s.logger.Warn("interface authentication refused: proxy secret missing or wrong, the request did not go through the proxy", "header", brand.ProxySecretHeader, "peer", peerHost(r), "path", r.URL.Path)
		s.writeForwardUnauthorized(w, r)
		return "", false
	}
	if len(r.Header.Values(state.Header)) > 1 {
		s.logger.Warn("interface authentication refused: identity header sent more than once, the proxy must overwrite it", "header", state.Header, "source", s.clientAddress(r), "path", r.URL.Path)
		s.writeForwardUnauthorized(w, r)
		return "", false
	}
	identity := s.trustedIdentity(r, state.Header)
	if identity == "" {
		s.logger.Warn("interface authentication refused: identity header missing", "header", state.Header, "source", s.clientAddress(r), "path", r.URL.Path)
		s.writeForwardUnauthorized(w, r)
		return "", false
	}
	return identity, true
}

// trustedIdentity rend l'identité posée par un mandataire de confiance, ou
// rien si la requête ne vient pas de l'un d'eux, n'en prouve pas le passage
// ou ne la porte pas. Le secret a fini de servir : il est ôté de la requête
// pour qu'aucun gestionnaire ne le voie.
func (s *Server) trustedIdentity(r *http.Request, header string) string {
	if !s.trustsPeer(r) || !s.hasProxySecret(r) {
		return ""
	}
	r.Header.Del(brand.ProxySecretHeader)
	// Plusieurs valeurs : le mandataire a ajouté la sienne au lieu
	// d'écraser, et la première peut venir du client.
	values := r.Header.Values(header)
	if len(values) != 1 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

// forwardSecretRequired dit si le mode forward manque de preuve : aucun
// secret n'est configuré alors qu'un préfixe de confiance couvre la boucle
// locale. Un pair non loopback (Traefik en conteneur, en /32) reste cru sur
// son adresse, comme avant.
func (s *Server) forwardSecretRequired() bool {
	return s.forwardSecret == "" && TrustsLoopback(s.trustedProxies)
}

// hasProxySecret dit si la requête porte exactement une fois le secret du
// mandataire. Sans secret configuré, il n'y a rien à prouver. Les valeurs
// passent par une empreinte pour que la comparaison, à temps constant, ne
// dise rien de la longueur du secret.
func (s *Server) hasProxySecret(r *http.Request) bool {
	if s.forwardSecret == "" {
		return true
	}
	values := r.Header.Values(brand.ProxySecretHeader)
	if len(values) != 1 {
		return false
	}
	received := sha256.Sum256([]byte(values[0]))
	expected := sha256.Sum256([]byte(s.forwardSecret))
	return subtle.ConstantTimeCompare(received[:], expected[:]) == 1
}

// Le refus du mode forward, sans défi Basic : le navigateur n'a rien à
// demander, c'est le mandataire qui authentifie.
func (s *Server) writeForwardUnauthorized(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.writeAPIError(w, http.StatusUnauthorized, codeUnauthorized)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	catalog := s.catalogs.For(browserLanguage(r))
	http.Error(w, catalog.Get("auth.forward_unauthorized"), http.StatusUnauthorized)
}

// La langue du navigateur, faute de session : le français s'il le demande
// en premier, l'anglais sinon.
func browserLanguage(r *http.Request) lang.Code {
	preferred := strings.ToLower(strings.TrimSpace(r.Header.Get("Accept-Language")))
	if strings.HasPrefix(preferred, "fr") {
		return lang.French
	}
	return lang.English
}

// --- API : la carte « Accès à l'interface » de Paramètres ---

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
	// La longueur minimale, pour que le front la dise et la vérifie avant
	// d'envoyer ; le serveur reste le juge.
	PasswordMinLength int `json:"password_min_length"`
}

// Activer l'authentification révoque tous les jetons MCP : la réponse dit
// combien sont tombés, l'interface le montre.
type authEnableResponse struct {
	authResponse
	MCPRevoked mcpRevokedJSON `json:"mcp_revoked"`
}

type mcpRevokedJSON struct {
	APITokens int `json:"api_tokens"`
	Sessions  int `json:"sessions"`
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
		response.EnabledAt = timeOrNil(state.EnabledAt)
	case auth.ModeForward:
		response.Username = identity
		response.Header = state.Header
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
	response := authToJSON(state, identityFrom(r.Context()))
	response.ForwardInsecure = state.Mode == auth.ModeForward && s.forwardSecretRequired()
	s.writeAPI(w, http.StatusOK, response)
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
	// Tant que l'interface était ouverte, n'importe qui pouvait tirer un
	// jeton MCP : aucun ne survit à l'activation. Après elle, et non avant,
	// pour qu'un rafraîchissement OAuth glissé entre les deux tombe aussi.
	revoked, err := s.mcp.RevokeAll(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, authEnableResponse{
		authResponse: authToJSON(state, identity),
		MCPRevoked:   mcpRevokedJSON{APITokens: revoked.APITokens, Sessions: revoked.Sessions},
	})
}

// enableMode active le mode demandé et rend l'identité à afficher ; il
// répond lui-même quand il refuse.
func (s *Server) enableMode(w http.ResponseWriter, r *http.Request, request authEnableRequest) (auth.State, string, bool) {
	switch auth.Mode(request.Mode) {
	case "", auth.ModeBasic:
		authorizing, _ := authorizingStateFrom(r.Context())
		state, err := s.auth.EnableBasic(r.Context(), authorizing, request.Username, request.Password)
		return state, "", !s.refuseAuth(w, r, err)
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
	if err := auth.ValidateHeader(header); err != nil {
		s.refuseAuth(w, r, err)
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
	authorizing, _ := authorizingStateFrom(r.Context())
	state, err := s.auth.EnableForward(r.Context(), authorizing, header)
	if s.refuseAuth(w, r, err) {
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
	if s.refuseAuth(w, r, err) {
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
	if s.refuseAuth(w, r, s.auth.Disable(r.Context(), authorizing)) {
		return
	}
	s.writeAPI(w, http.StatusOK, authToJSON(auth.State{Mode: auth.ModeNone}, ""))
}

// refuseAuth répond à une erreur du composant : le refus par sa clé,
// sinon 500. Dit si la réponse est partie.
func (s *Server) refuseAuth(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return false
	}
	if s.refused(w, err) {
		return true
	}
	s.apiInternalError(w, r, err)
	return true
}
