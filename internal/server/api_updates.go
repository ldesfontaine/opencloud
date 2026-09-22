package server

import (
	"errors"
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/internal/update"
)

type updatePolicyRequest struct {
	Policy string `json:"policy"`
}

// setUpdatePolicy règle ce que l'opérateur veut des mises à jour d'un
// service : les suivre, les taire, ne jamais interroger le registre.
func (s *Server) setUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	var request updatePolicyRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	err := s.updates.SetPolicy(r.Context(), r.PathValue("id"), service.UpdatePolicy(request.Policy))
	if errors.Is(err, update.ErrBadPolicy) {
		s.apiRefuse(w, http.StatusBadRequest, "update.policy_invalid")
		return
	}
	s.finishAPIAction(w, r, err, service.ErrNotFound)
}

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
