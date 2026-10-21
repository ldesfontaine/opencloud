package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
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
	if err := httpx.WriteJSONNoStore(w, status, payload); err != nil {
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

// apiRefuse répond un refus que le serveur décide lui-même (un paramètre de
// requête, un en-tête, une limite du serveur), ou celui d'un composant dont
// le statut n'est ni 422 ni 409 (404 à clé propre, 403, 503). Une règle
// métier refuse par refusal.*, et la réponse passe par failed.
func (s *Server) apiRefuse(w http.ResponseWriter, status int, key string) {
	s.writeAPIError(w, status, key)
}

// apiNotFound sert aussi le 404 d'un composant : errors.Is(err, ErrNotFound).
func (s *Server) apiNotFound(w http.ResponseWriter) {
	s.writeAPIError(w, http.StatusNotFound, codeNotFound)
}

// failed répond à l'erreur d'un composant et dit si la réponse est partie :
// 404 sur notFound, la clé d'un refus en 422 ou 409, sinon 500. notFound est
// nil quand la route n'a pas de 404.
func (s *Server) failed(w http.ResponseWriter, r *http.Request, err, notFound error) bool {
	if err == nil {
		return false
	}
	if notFound != nil && errors.Is(err, notFound) {
		s.apiNotFound(w)
		return true
	}
	if key, ok := refusal.KeyOf(err); ok {
		// Une suppression retenue rend aussi son plan : ce qui la retient.
		var refused *deletion.RefusedError
		if errors.As(err, &refused) {
			s.writeAPI(w, refusalStatus(err), deletionRefusalJSON{Error: key, Plan: deletionPlanToJSON(refused.Plan)})
			return true
		}
		s.writeAPIError(w, refusalStatus(err), key)
		return true
	}
	s.apiInternalError(w, r, err)
	return true
}

// refusalStatus rend le statut HTTP d'un refus : 409 s'il vient de l'état
// présent, 422 sinon.
func refusalStatus(err error) int {
	if refusal.IsConflict(err) {
		return http.StatusConflict
	}
	return http.StatusUnprocessableEntity
}

// finishAPIAction répond à une action sans contenu : 204, ou l'échec.
func (s *Server) finishAPIAction(w http.ResponseWriter, r *http.Request, err, notFound error) {
	if s.failed(w, r, err, notFound) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
