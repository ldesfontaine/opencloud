package web

import (
	"fmt"
	"html/template"
	"log/slog"
	"mime"
	"net/http"
	"sync"

	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/settings"
)

// Ce que le serveur attend du composant qui mémorise les réglages.
type SettingsStore interface {
	Load() (settings.Settings, error)
	Save(settings.Settings) error
}

type Server struct {
	logger     *slog.Logger
	version    string
	catalogs   lang.Catalogs
	settings   SettingsStore
	pages      map[string]*template.Template
	staticBase string
	handler    http.Handler

	// Les réglages courants, relus à chaque page, réécrits à chaque changement.
	mu      sync.RWMutex
	current settings.Settings
}

type Options struct {
	Logger   *slog.Logger
	Version  string
	Settings SettingsStore
}

func New(opts Options) (*Server, error) {
	catalogs, err := lang.Load()
	if err != nil {
		return nil, fmt.Errorf("load languages: %w", err)
	}
	current, err := opts.Settings.Load()
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	pages, err := parsePages()
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	build, err := staticBuild()
	if err != nil {
		return nil, fmt.Errorf("fingerprint static files: %w", err)
	}
	// La table MIME de Go ignore woff2 ; sans cela le navigateur reçoit un
	// octet-stream et certains refusent la police.
	if err := mime.AddExtensionType(".woff2", "font/woff2"); err != nil {
		return nil, fmt.Errorf("register woff2 type: %w", err)
	}
	server := &Server{
		logger:     opts.Logger,
		version:    opts.Version,
		catalogs:   catalogs,
		settings:   opts.Settings,
		pages:      pages,
		staticBase: staticPrefix + build + "/",
		current:    current,
	}
	server.handler = securityHeaders(server.csrfCookie(server.mux()))
	return server, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// La langue de l'interface : celle du réglage, sinon le français.
func (s *Server) language() lang.Code {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.current.Language == "" {
		return lang.Default
	}
	return s.current.Language
}

func (s *Server) catalog() lang.Catalog {
	return s.catalogs.For(s.language())
}

func (s *Server) saveLanguage(code lang.Code) error {
	s.mu.Lock()
	s.current.Language = code
	snapshot := s.current
	s.mu.Unlock()
	return s.settings.Save(snapshot)
}
