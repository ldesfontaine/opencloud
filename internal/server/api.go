package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/refusal"
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
	codeHost     = "bad_host"
)

func (s *Server) readAPI(w http.ResponseWriter, r *http.Request, into any) bool {
	if !readBounded(w, r, into, maxAPIBody) {
		s.writeAPIError(w, http.StatusBadRequest, codeBadJSON)
		return false
	}
	return true
}

// readBounded lit le corps JSON, d'au plus limit octets, sans champ inconnu.
func readBounded(w http.ResponseWriter, r *http.Request, into any, limit int64) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	return decoder.Decode(into) == nil
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

// refused répond au refus d'un domaine par sa clé, en 422 ou en 409, et dit
// si la réponse est partie. Une erreur qui n'est pas un refus reste à
// l'appelant.
func (s *Server) refused(w http.ResponseWriter, err error) bool {
	key, ok := refusal.KeyOf(err)
	if !ok {
		return false
	}
	status := http.StatusUnprocessableEntity
	if refusal.IsConflict(err) {
		status = http.StatusConflict
	}
	s.apiRefuse(w, status, key)
	return true
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

// requireSameOrigin remplace le jeton anti-CSRF des formulaires : toute
// écriture porte application/json, corps vide compris, et un navigateur ne
// peut pas l'envoyer à un autre site sans une pré-requête CORS que rien
// n'accepte. Un Sec-Fetch-Site présent dit en plus d'où vient la requête.
// Les deux garde-fous se cumulent ; les GET ne changent rien et passent.
func (s *Server) requireSameOrigin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				s.writeAPIError(w, http.StatusForbidden, codeOrigin)
				return
			}
			if !isJSON(r.Header.Get("Content-Type")) {
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

// Une liste nulle s'écrit [] : le front n'a pas à distinguer null de vide.
func orEmpty[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}

// convertAll convertit chaque élément ; le résultat n'est jamais nul, il
// s'écrit [] quand il n'y a rien.
func convertAll[T, J any](items []T, convert func(T) J) []J {
	converted := make([]J, 0, len(items))
	for _, item := range items {
		converted = append(converted, convert(item))
	}
	return converted
}
