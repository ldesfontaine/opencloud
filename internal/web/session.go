package web

import (
	"errors"
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/auth"
	"github.com/ldesfontaine/opencloud/internal/store"
)

type accountHandler func(w http.ResponseWriter, r *http.Request, account store.Account)

// requireAccount charge le compte de la session, renvoie vers la connexion
// s'il n'y en a pas, et vers le changement de mot de passe tant qu'il est dû.
func (s *Server) requireAccount(next accountHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, found := sessionToken(r)
		if !found {
			redirect(w, r, "/login")
			return
		}

		account, err := s.auth.Authenticate(r.Context(), token)
		if errors.Is(err, auth.ErrSessionExpired) {
			clearSessionCookie(w)
			redirect(w, r, "/login")
			return
		}
		if err != nil {
			s.serverError(w, "authenticate session", err)
			return
		}

		if account.MustChangePassword && r.URL.Path != "/password" {
			redirect(w, r, "/password")
			return
		}
		next(w, r, account)
	}
}
