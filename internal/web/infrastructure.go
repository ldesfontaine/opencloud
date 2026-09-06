package web

import (
	"errors"
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/store"
)

func (s *Server) showInfrastructure(w http.ResponseWriter, r *http.Request, account store.Account) {
	csrfToken := s.csrfFormToken(w, r)
	view := infrastructureView{}
	if s.actionsReady() {
		rows, err := s.machineRows(r)
		if err != nil {
			s.serverError(w, "list machines", err)
			return
		}
		view.Machines = rows
	}
	s.render(w, http.StatusOK, "infrastructure", s.newPage(&account, csrfToken).withData(view))
}

func (s *Server) machineRows(r *http.Request) ([]machineRow, error) {
	machines, err := s.machines.Machines(r.Context())
	if err != nil {
		return nil, err
	}

	var rows []machineRow
	for _, machine := range machines {
		row := newMachineRow(machine, s.enrolmentStatus(r.Context(), machine.ID))
		recent, err := s.actions.ActionsForMachine(r.Context(), machine.ID, 1)
		if err != nil {
			return nil, err
		}
		if len(recent) > 0 {
			last := s.newActionRow(recent[0])
			row.LastAction = &last
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (s *Server) showMachine(w http.ResponseWriter, r *http.Request, account store.Account) {
	if !s.actionsReady() {
		http.NotFound(w, r)
		return
	}

	machine, err := s.machines.Machine(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, messageMachineUnknown, http.StatusNotFound)
		return
	}
	if err != nil {
		s.serverError(w, "read machine", err)
		return
	}

	history, err := s.actions.ActionsForMachine(r.Context(), machine.ID, machineHistoryLimit)
	if err != nil {
		s.serverError(w, "list machine actions", err)
		return
	}

	status := s.enrolmentStatus(r.Context(), machine.ID)
	view := machineView{
		ID:             machine.ID,
		Name:           machine.Name,
		Address:        machine.Address,
		Port:           machine.Port,
		Account:        machine.Account,
		Enrolled:       status.Enrolled,
		EnrolmentLabel: enrolmentLabel(machine, status),
		Available:      s.machineScopedActions(),
		History:        s.newActionRows(history),
	}
	s.render(w, http.StatusOK, "machine", s.newPage(&account, s.csrfFormToken(w, r)).withData(view))
}

// Les actions qu'on lance depuis la fiche d'une machine : celles dont la
// portée est la machine elle-même.
func (s *Server) machineScopedActions() []availableAction {
	var available []availableAction
	for _, definition := range s.catalog.Definitions() {
		if definition.Scope != catalog.ScopeMachine {
			continue
		}
		available = append(available, availableAction{
			Kind:    string(definition.Kind),
			Label:   definition.Label,
			Summary: definition.Summary,
		})
	}
	return available
}
