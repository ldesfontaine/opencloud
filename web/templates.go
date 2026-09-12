package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed templates
var templateFiles embed.FS

const (
	layoutFile   = "templates/layout.html"
	partialsGlob = "templates/partials/*.html"
	pagesDir     = "templates/pages"
)

// Chaque page est le layout cloné plus son propre fichier : elle redéfinit
// « content » et, si besoin, « actions ».
func parsePages() (map[string]*template.Template, error) {
	base, err := template.ParseFS(templateFiles, layoutFile, partialsGlob)
	if err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(templateFiles, pagesDir)
	if err != nil {
		return nil, err
	}
	pages := make(map[string]*template.Template, len(entries))
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".html")
		page, err := base.Clone()
		if err != nil {
			return nil, err
		}
		page, err = page.ParseFS(templateFiles, path.Join(pagesDir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("page %s: %w", name, err)
		}
		pages[name] = page
	}
	return pages, nil
}

// Rend dans un tampon d'abord : une erreur de gabarit donne un vrai 500,
// pas une page à moitié écrite.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, page string, data view) {
	tmpl, ok := s.pages[page]
	if !ok {
		s.internalError(w, r, fmt.Errorf("unknown page %q", page))
		return
	}
	var buffer bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buffer, "layout", data); err != nil {
		s.internalError(w, r, fmt.Errorf("render %s: %w", page, err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buffer.WriteTo(w); err != nil {
		s.logger.Warn("write response", "path", r.URL.Path, "error", err)
	}
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.Error("internal error", "path", r.URL.Path, "error", err)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	if _, err := fmt.Fprintln(w, s.catalog().Get("error.internal")); err != nil {
		s.logger.Warn("write error response", "path", r.URL.Path, "error", err)
	}
}

// Un refus dit sa cause en texte, avec le code HTTP qui va avec.
func (s *Server) refuse(w http.ResponseWriter, r *http.Request, status int, cause string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	if _, err := fmt.Fprintln(w, cause); err != nil {
		s.logger.Warn("write refusal", "path", r.URL.Path, "error", err)
	}
}
