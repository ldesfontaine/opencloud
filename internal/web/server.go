// Package web sert l'interface : gabarits HTML, fragments HTMX, SSE.
// Les handlers sont minces ; web ne lance rien, il dépose dans runner.
package web

import (
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/auth"
	assets "github.com/ldesfontaine/opencloud/web"
)

type Server struct {
	auth      *auth.Service
	logger    *slog.Logger
	version   string
	templates pageTemplates
	static    http.Handler
	csrf      csrfSigner
}

func New(authService *auth.Service, version string, logger *slog.Logger) (*Server, error) {
	templates, err := parsePageTemplates(assets.Templates)
	if err != nil {
		return nil, err
	}

	staticFiles, err := fs.Sub(assets.Static, "static")
	if err != nil {
		return nil, fmt.Errorf("locate static files: %w", err)
	}

	csrf, err := newCSRFSigner()
	if err != nil {
		return nil, err
	}

	return &Server{
		auth:      authService,
		logger:    logger,
		version:   version,
		templates: templates,
		static:    http.StripPrefix("/static/", http.FileServerFS(staticFiles)),
		csrf:      csrf,
	}, nil
}

// Une route : méthode et motif au format de http.ServeMux, et son handler.
type route struct {
	pattern string
	handler http.Handler
}

// La table des routes, la seule. Routes la liste, Handler la sert.
func (s *Server) routes() []route {
	return []route{
		{"GET /{$}", s.requireAccount(s.showInfrastructure)},
		{"GET /healthz", http.HandlerFunc(s.showHealth)},
		{"GET /login", http.HandlerFunc(s.showLogin)},
		{"POST /login", http.HandlerFunc(s.submitLogin)},
		{"POST /logout", http.HandlerFunc(s.submitLogout)},
		{"GET /password", s.requireAccount(s.showPasswordChange)},
		{"POST /password", s.requireAccount(s.submitPasswordChange)},
		{"GET /static/", s.static},
	}
}

// Routes liste les motifs servis. Figée par un test : toute route ajoutée ou
// retirée se voit.
func (s *Server) Routes() []string {
	var patterns []string
	for _, current := range s.routes() {
		patterns = append(patterns, current.pattern)
	}
	return patterns
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, current := range s.routes() {
		mux.Handle(current.pattern, current.handler)
	}
	// Seconde ligne de défense anti-CSRF, par les en-têtes Sec-Fetch-Site et
	// Origin du navigateur (OWASP : « Fetch Metadata »). Le jeton signé reste
	// la première.
	crossOrigin := http.NewCrossOriginProtection()
	return secureHeaders(limitRequestBody(crossOrigin.Handler(mux)))
}

// secureHeaders : aucun script ni style externe, jamais dans un cadre, pas de
// cache des pages — elles sont toutes privées.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers := w.Header()
		headers.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; form-action 'self'")
		headers.Set("X-Content-Type-Options", "nosniff")
		headers.Set("X-Frame-Options", "DENY")
		headers.Set("Referrer-Policy", "same-origin")
		headers.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
