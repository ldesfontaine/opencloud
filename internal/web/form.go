package web

import (
	"errors"
	"net/http"
)

// Un formulaire d'openCloud tient en quelques centaines d'octets ; au-delà,
// ce n'est pas un formulaire. Sans cette borne, ParseForm lirait 10 Mio.
const maxFormBytes = 16 << 10

func limitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
		next.ServeHTTP(w, r)
	})
}

// readForm décode le formulaire et répond lui-même s'il est trop gros ou
// illisible ; l'appelant s'arrête alors. PostFormValue tairait l'erreur.
func (s *Server) readForm(w http.ResponseWriter, r *http.Request) bool {
	err := r.ParseForm()
	if err == nil {
		return true
	}

	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(w, messageFormTooLarge, http.StatusRequestEntityTooLarge)
		return false
	}
	http.Error(w, messageFormUnreadable, http.StatusBadRequest)
	return false
}
