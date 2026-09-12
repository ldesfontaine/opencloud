package web

import (
	"fmt"
	"html/template"
	"log/slog"
	"mime"
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/lang"
)

type Server struct {
	logger     *slog.Logger
	version    string
	text       lang.Strings
	pages      map[string]*template.Template
	staticBase string
	handler    http.Handler
}

type Options struct {
	Logger  *slog.Logger
	Version string
}

func New(opts Options) (*Server, error) {
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
		text:       lang.French(),
		pages:      pages,
		staticBase: staticPrefix + build + "/",
	}
	server.handler = securityHeaders(server.mux())
	return server, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}
