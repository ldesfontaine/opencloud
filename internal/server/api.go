package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// Ce que le navigateur peut envoyer d'un coup : un formulaire, jamais plus.
const maxAPIBody = 64 << 10

// Une réponse d'erreur de l'API : un seul mot. Quand l'opérateur doit le
// lire, c'est une clé du catalogue (machine.name_invalid) et le front la
// traduit ; sinon un code court (not_found, bad_json, internal).
type apiError struct {
	Error string `json:"error"`
}

const (
	codeNotFound = "not_found"
	codeBadJSON  = "bad_json"
	codeInternal = "internal"
	codeOrigin   = "bad_origin"
)

func (s *Server) readAPI(w http.ResponseWriter, r *http.Request, into any) bool {
	body := http.MaxBytesReader(w, r.Body, maxAPIBody)
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, codeBadJSON)
		return false
	}
	return true
}

func (s *Server) writeAPI(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		s.logger.Warn("write json", "error", err)
	}
}

func (s *Server) writeAPIError(w http.ResponseWriter, status int, code string) {
	s.writeAPI(w, status, apiError{Error: code})
}

// Une erreur qu'on n'avait pas prévue : journal complet, réponse muette.
func (s *Server) apiInternalError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.Error("internal error", "path", r.URL.Path, "error", err)
	s.writeAPIError(w, http.StatusInternalServerError, codeInternal)
}

// Un refus : la clé du catalogue que le front affichera dans sa langue.
func (s *Server) apiRefuse(w http.ResponseWriter, status int, key string) {
	s.writeAPIError(w, status, key)
}

// apiNotFound sert aussi le 404 d'un composant : errors.Is(err, ErrNotFound).
func (s *Server) apiNotFound(w http.ResponseWriter) {
	s.writeAPIError(w, http.StatusNotFound, codeNotFound)
}

// finishAPIAction répond à une action sans contenu : 204, 404, ou 500.
func (s *Server) finishAPIAction(w http.ResponseWriter, r *http.Request, err error, notFound error) {
	switch {
	case errors.Is(err, notFound):
		s.apiNotFound(w)
	case err != nil:
		s.apiInternalError(w, r, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// requireSameOrigin remplace le jeton anti-CSRF des formulaires : un POST
// venu d'un autre site porte Sec-Fetch-Site: cross-site, et il ne peut pas
// envoyer application/json sans une pré-requête CORS que rien n'accepte.
// Les deux garde-fous se cumulent ; les GET ne changent rien et passent.
func (s *Server) requireSameOrigin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				s.writeAPIError(w, http.StatusForbidden, codeOrigin)
				return
			}
			if r.ContentLength != 0 && !isJSON(r.Header.Get("Content-Type")) {
				s.writeAPIError(w, http.StatusForbidden, codeOrigin)
				return
			}
		}
		next(w, r)
	}
}

func isJSON(contentType string) bool {
	mediaType, _, _ := strings.Cut(contentType, ";")
	return strings.TrimSpace(mediaType) == "application/json"
}
