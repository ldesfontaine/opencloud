package web

import (
	"net/http"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/lang"
)

// La vue d'ensemble compte les machines ; sans machine, elle invite à en
// ajouter une.
func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	text := s.catalog()
	statuses, err := s.machines.List(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	page := s.overviewPage(text, statuses)
	data := s.newView(r, navOverview, text.Get("nav.overview"), page.Subtitle)
	data.Page = page
	s.render(w, r, http.StatusOK, "overview", data)
}

// Page d'attente d'une fonctionnalité à venir, sous l'entrée de menu donnée.
func (s *Server) soon(active, titleKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		text := s.catalog()
		data := s.newView(r, active, text.Get(titleKey), text.Get("soon.subtitle"))
		s.render(w, r, http.StatusOK, "soon", data)
	}
}

// Jauge d'exemple pour la page /systeme-visuel.
type meterSample struct {
	Percent int
	Tone    string
}

func (s *Server) visualSystem(w http.ResponseWriter, r *http.Request) {
	text := s.catalog()
	data := s.newView(r, "", text.Get("visual.title"), text.Get("visual.subtitle"))
	data.Page = map[string]meterSample{
		"cpu":  {Percent: 23},
		"disk": {Percent: 86, Tone: "warn"},
	}
	s.render(w, r, http.StatusOK, "visual-system", data)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	text := s.catalog()
	data := s.newView(r, "", text.Get("notfound.title"), text.Get("notfound.subtitle"))
	s.render(w, r, http.StatusNotFound, "not-found", data)
}

// Le commutateur de langue : mémorise le choix puis revient à la page d'où
// il a été actionné.
func (s *Server) setLanguage(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(w, r) {
		return
	}
	code, ok := lang.Parse(r.FormValue("language"))
	if !ok {
		s.refuse(w, r, http.StatusBadRequest, s.catalog().Get("error.unknown_language"))
		return
	}
	if err := s.saveLanguage(code); err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, returnPath(r.FormValue("return")), http.StatusSeeOther) // #nosec G710 -- returnPath ne laisse passer qu'un chemin local.
}

// Seul un chemin local est suivi ; tout le reste ramène à la vue d'ensemble.
func returnPath(candidate string) string {
	if strings.HasPrefix(candidate, "/") && !strings.HasPrefix(candidate, "//") {
		return candidate
	}
	return "/"
}
