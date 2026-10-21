package server

import (
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/auth"
)

// withOperatorActor pose l'opérateur dans le contexte, une fois la requête
// admise par requireOperator : chaque geste qu'elle appelle le journalise
// sous son identité, sans que les signatures aient à la porter.
func (s *Server) withOperatorActor(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		next(w, r.WithContext(audit.WithActor(r.Context(), audit.Operator(operatorName(r)))))
	}
}

// operatorName est l'identité que l'authentification a vue : l'identifiant
// Basic, ou l'en-tête du mandataire. À la première configuration, personne :
// un en-tête Basic qu'aucun mode ne vérifie ne nomme pas l'opérateur. Ce nom
// est indicatif, borné et sans caractère de contrôle.
func operatorName(r *http.Request) string {
	state, _ := authorizingStateFrom(r.Context())
	name := ""
	switch state.Mode {
	case auth.ModeBasic:
		name, _, _ = r.BasicAuth()
	case auth.ModeForward:
		name = identityFrom(r.Context())
	}
	return printable(name, maxActorNameRunes)
}
