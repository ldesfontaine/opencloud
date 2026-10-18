package server

import (
	"errors"
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// checkUpdates demande à une machine de vérifier ses images maintenant ;
// hors ligne, elle ne peut pas, et l'opérateur le lit.
func (s *Server) checkUpdates(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.machineExists(w, r, id) {
		return
	}
	err := s.updates.CheckNow(r.Context(), id)
	if errors.Is(err, update.ErrMachineOffline) {
		s.apiRefuse(w, http.StatusServiceUnavailable, "update.machine_offline")
		return
	}
	s.finishAPIAction(w, r, err, service.ErrNotFound)
}
