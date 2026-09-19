package server

import (
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/lang"
)

// Ce que le front lit au démarrage : la version et la langue réglée.
type sessionResponse struct {
	Version   string   `json:"version"`
	Language  string   `json:"language"`
	Languages []string `json:"languages"`
}

type languageRequest struct {
	Language string `json:"language"`
}

// Les compteurs de la barre latérale et de la vue d'ensemble.
type countsResponse struct {
	Machines machineCounts `json:"machines"`
	Jobs     jobCounts     `json:"jobs"`
	Services jobCounts     `json:"services"`
}

type machineCounts struct {
	Total  int `json:"total"`
	Online int `json:"online"`
}

type jobCounts struct {
	Total     int `json:"total"`
	Attention int `json:"attention"`
}

func (s *Server) session(w http.ResponseWriter, _ *http.Request) {
	languages := make([]string, 0, len(lang.Codes()))
	for _, code := range lang.Codes() {
		languages = append(languages, string(code))
	}
	s.writeAPI(w, http.StatusOK, sessionResponse{
		Version:   s.version,
		Language:  string(s.language()),
		Languages: languages,
	})
}

// Le commutateur de langue : mémorise le choix ; le front recharge son
// catalogue sans recharger la page.
func (s *Server) setLanguage(w http.ResponseWriter, r *http.Request) {
	var request languageRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	code, ok := lang.Parse(request.Language)
	if !ok {
		s.apiRefuse(w, http.StatusBadRequest, "error.unknown_language")
		return
	}
	if err := s.saveLanguage(code); err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Le catalogue d'une langue, tel quel : les clés pointées et leurs chaînes.
// Les formats %s et %d restent au front, qui les remplit dans l'ordre.
func (s *Server) catalogAPI(w http.ResponseWriter, r *http.Request) {
	code, ok := lang.Parse(r.PathValue("code"))
	if !ok {
		s.apiNotFound(w)
		return
	}
	catalog := s.catalogs.For(code)
	strings := make(map[string]string, len(catalog.Keys()))
	for _, key := range catalog.Keys() {
		strings[key] = catalog.Get(key)
	}
	s.writeAPI(w, http.StatusOK, strings)
}

func (s *Server) counts(w http.ResponseWriter, r *http.Request) {
	total, online, err := s.machines.Count(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	jobs, attention, err := s.heartbeats.Count(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	services, failing, err := s.services.Count(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, countsResponse{
		Machines: machineCounts{Total: total, Online: online},
		Jobs:     jobCounts{Total: jobs, Attention: attention},
		Services: jobCounts{Total: services, Attention: failing},
	})
}
