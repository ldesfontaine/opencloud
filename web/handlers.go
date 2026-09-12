package web

import "net/http"

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	data := s.newView(navOverview, s.text.NavOverview, s.text.OverviewSubtitle)
	s.render(w, r, http.StatusOK, "overview", data)
}

// Page d'attente d'une fonctionnalité à venir, sous l'entrée de menu donnée.
func (s *Server) soon(active, title string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := s.newView(active, title, s.text.SoonSubtitle)
		s.render(w, r, http.StatusOK, "soon", data)
	}
}

// Jauge d'exemple pour la page /systeme-visuel.
type meterSample struct {
	Percent int
	Tone    string
}

func (s *Server) visualSystem(w http.ResponseWriter, r *http.Request) {
	data := s.newView("", s.text.VisualSystemTitle, s.text.VisualSystemSubtitle)
	data.Page = map[string]meterSample{
		"cpu":  {Percent: 23},
		"disk": {Percent: 86, Tone: "warn"},
	}
	s.render(w, r, http.StatusOK, "visual-system", data)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	data := s.newView("", s.text.NotFoundTitle, s.text.NotFoundSubtitle)
	s.render(w, r, http.StatusNotFound, "not-found", data)
}
