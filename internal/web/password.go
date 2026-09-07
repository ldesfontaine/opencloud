package web

import (
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/store"
)

func (s *Server) showPasswordChange(w http.ResponseWriter, r *http.Request, account store.Account) {
	csrfToken := s.csrfFormToken(w, r)
	s.render(w, http.StatusOK, "password", s.newPage(r, &account, csrfToken).withData(messageDefaultPassword))
}

func (s *Server) submitPasswordChange(w http.ResponseWriter, r *http.Request, account store.Account) {
	if !s.readForm(w, r) {
		return
	}
	if err := s.verifyCSRF(r); err != nil {
		s.render(w, http.StatusForbidden, "password", s.newPage(r, &account, s.rotateCSRF(w)).withData(messageDefaultPassword).withError(messageFormExpired))
		return
	}
	csrfToken := s.csrfFormToken(w, r)

	currentPassword := r.PostFormValue("current_password")
	newPassword := r.PostFormValue("new_password")
	confirmPassword := r.PostFormValue("confirm_password")
	if newPassword != confirmPassword {
		s.render(w, http.StatusUnprocessableEntity, "password", s.newPage(r, &account, csrfToken).withData(messageDefaultPassword).withError(messageConfirmationDiffers))
		return
	}

	session, err := s.auth.ChangePassword(r.Context(), account.ID, currentPassword, newPassword)
	if message, refused := messageForPasswordRefusal(err); refused {
		s.render(w, http.StatusUnprocessableEntity, "password", s.newPage(r, &account, csrfToken).withData(messageDefaultPassword).withError(message))
		return
	}
	if err != nil {
		s.serverError(w, "change password", err)
		return
	}

	// ChangePassword a fermé toutes les sessions : on pose la neuve.
	setSessionCookie(w, session.Token, session.ExpiresAt)
	redirect(w, r, "/")
}
