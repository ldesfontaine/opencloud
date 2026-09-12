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
// dict passe plusieurs valeurs à un gabarit nommé, qui n'en accepte qu'une.
func dict(pairs ...any) (map[string]any, error) {
	if len(pairs)%2 != 0 {
		return nil, fmt.Errorf("dict: odd number of arguments")
	}
	values := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: key %v is not a string", pairs[i])
		}
		values[key] = pairs[i+1]
	}
	return values, nil
}

func parsePages() (map[string]*template.Template, *template.Template, error) {
	base, err := template.New("").Funcs(template.FuncMap{"dict": dict}).ParseFS(templateFiles, layoutFile, partialsGlob)
	if err != nil {
		return nil, nil, err
	}
	entries, err := fs.ReadDir(templateFiles, pagesDir)
	if err != nil {
		return nil, nil, err
	}
	pages := make(map[string]*template.Template, len(entries))
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".html")
		page, err := base.Clone()
		if err != nil {
			return nil, nil, err
		}
		page, err = page.ParseFS(templateFiles, path.Join(pagesDir, entry.Name()))
		if err != nil {
			return nil, nil, fmt.Errorf("page %s: %w", name, err)
		}
		pages[name] = page
	}
	return pages, base, nil
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

// Un fragment HTMX : un seul gabarit nommé, sans la coquille.
func (s *Server) renderFragment(w http.ResponseWriter, r *http.Request, name string, data view) {
	var buffer bytes.Buffer
	if err := s.partials.ExecuteTemplate(&buffer, name, data); err != nil {
		s.internalError(w, r, fmt.Errorf("render fragment %s: %w", name, err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
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
