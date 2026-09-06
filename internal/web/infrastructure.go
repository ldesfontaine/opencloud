package web

import (
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/store"
)

func (s *Server) showInfrastructure(w http.ResponseWriter, r *http.Request, account store.Account) {
	csrfToken := s.csrfFormToken(w, r)
	s.render(w, http.StatusOK, "infrastructure", s.newPage(&account, csrfToken))
}
