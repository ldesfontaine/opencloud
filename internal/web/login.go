package web

import (
	"errors"
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/auth"
)

func (s *Server) showLogin(w http.ResponseWriter, r *http.Request) {
	csrfToken := s.csrfFormToken(w, r)
	s.render(w, http.StatusOK, "login", s.newPage(nil, csrfToken))
}

func (s *Server) submitLogin(w http.ResponseWriter, r *http.Request) {
	if err := s.verifyCSRF(r); err != nil {
		s.render(w, http.StatusForbidden, "login", s.newPage(nil, s.rotateCSRF(w)).withError(messageFormExpired))
		return
	}

	username := r.PostFormValue("username")
	password := r.PostFormValue("password")
	session, err := s.auth.Login(r.Context(), username, password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		s.logger.Warn("login refused", "username", username)
		s.render(w, http.StatusUnauthorized, "login", s.newPage(nil, s.csrfFormToken(w, r)).withError(messageInvalidLogin))
		return
	}
	if errors.Is(err, auth.ErrTooManyAttempts) {
		s.logger.Warn("login throttled", "username", username)
		s.render(w, http.StatusTooManyRequests, "login", s.newPage(nil, s.csrfFormToken(w, r)).withError(messageTooManyAttempts))
		return
	}
	if err != nil {
		s.serverError(w, "login", err)
		return
	}

	s.logger.Info("login", "username", username)
	setSessionCookie(w, session.Token, session.ExpiresAt)
	s.rotateCSRF(w)
	redirect(w, r, "/")
}

// submitLogout ne passe pas par requireAccount : une session déjà périmée
// doit pouvoir se déconnecter proprement.
func (s *Server) submitLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.verifyCSRF(r); err != nil {
		http.Error(w, messageFormExpired, http.StatusForbidden)
		return
	}

	if token, found := sessionToken(r); found {
		if err := s.auth.Logout(r.Context(), token); err != nil {
			s.serverError(w, "logout", err)
			return
		}
	}
	clearSessionCookie(w)
	s.rotateCSRF(w)
	redirect(w, r, "/login")
}
