package server

import (
	"errors"
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// checkUpdates demande à une machine de vérifier ses images maintenant ;
// hors ligne, elle ne peut pas, et l'opérateur le lit.
func (s *Server) checkUpdates(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.machineExists(w, r, id) {
		return
	}
	err := s.updates.CheckNow(r.Context(), id)
	if errors.Is(err, machine.ErrBusy) {
		s.apiRefuse(w, http.StatusServiceUnavailable, codeMachineBusy)
		return
	}
	s.finishAPIAction(w, r, err, container.ErrNotFound)
}
