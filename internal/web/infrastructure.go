package web

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/store"
)

func (s *Server) showInfrastructure(w http.ResponseWriter, r *http.Request, account store.Account) {
	csrfToken := s.csrfFormToken(w, r)
	view := infrastructureView{CanEnrol: s.enrolmentReady()}
	if s.actionsReady() {
		rows, err := s.machineRows(r)
		if err != nil {
			s.serverError(w, "list machines", err)
			return
		}
		view.Machines = rows
		view.Counters = newMachineCounters(rows)
		view.Subtitle = labelInfrastructureSubtitle(len(rows))
	}
	s.render(w, http.StatusOK, "infrastructure", s.newPage(r, &account, csrfToken).withData(view))
}

func (s *Server) machineRows(r *http.Request) ([]machineRow, error) {
	machines, err := s.machines.Machines(r.Context())
	if err != nil {
		return nil, err
	}

	var rows []machineRow
	for _, machine := range machines {
		status := s.enrolmentStatus(r.Context(), machine.ID)
		row := newMachineRow(machine, status)
		recent, err := s.actions.ActionsForMachine(r.Context(), machine.ID, machineRecentLimit)
		if err != nil {
			return nil, err
		}
		if len(recent) > 0 {
			last := s.newActionRow(recent[0])
			row.LastAction = &last
		}
		row.Status = newMachineStatus(status.Enrolled, s.machineHealth(r.Context(), machine.ID), recent, time.Now())
		rows = append(rows, row)
	}
	return rows, nil
}

// Les quatre compteurs de l'Infrastructure, dans l'ordre où ils se lisent :
// ce qui va, ce qui bouge, ce qui casse, ce qui attend. Une machine dont le
// dernier sondage a vieilli n'est comptée nulle part : on ne sait pas.
func newMachineCounters(rows []machineRow) []machineCounter {
	counts := map[string]int{}
	for _, row := range rows {
		switch row.Status.State {
		case statusReachable:
			counts[counterReachable]++
		case statusRunning:
			counts[counterRunning]++
		case statusSSHFailed, statusLauncherFailed:
			counts[counterFailed]++
		case statusNotEnrolled:
			counts[counterNotEnrolled]++
		}
	}
	return []machineCounter{
		{Kind: counterReachable, Count: counts[counterReachable], Label: labelCounterReachable},
		{Kind: counterRunning, Count: counts[counterRunning], Label: labelCounterRunning},
		{Kind: counterFailed, Count: counts[counterFailed], Label: labelCounterFailed},
		{Kind: counterNotEnrolled, Count: counts[counterNotEnrolled], Label: labelCounterNotEnrolled},
	}
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

	view, err := s.newMachineView(r, machine)
	if err != nil {
		s.serverError(w, "build machine view", err)
		return
	}
	s.render(w, http.StatusOK, "machine", s.newPage(r, &account, s.csrfFormToken(w, r)).withData(view))
}

// newMachineView rassemble ce que la fiche montre : le statut, l'historique,
// et les gestes d'enrôlement quand ils sont branchés.
func (s *Server) newMachineView(r *http.Request, machine store.Machine) (machineView, error) {
	history, err := s.actions.ActionsForMachine(r.Context(), machine.ID, machineHistoryLimit)
	if err != nil {
		return machineView{}, err
	}

	status := s.enrolmentStatus(r.Context(), machine.ID)
	view := machineView{
		ID:              machine.ID,
		Name:            machine.Name,
		Address:         machine.Address,
		Port:            machine.Port,
		Account:         machine.Account,
		Enrolled:        status.Enrolled,
		Enrolment:       enrolmentNotice(machine, status),
		Status:          newMachineStatus(status.Enrolled, s.machineHealth(r.Context(), machine.ID), history, time.Now()),
		CanProbe:        s.prober != nil && status.Enrolled,
		CanChangeAccess: s.enrolmentReady() && status.Enrolled,
		Available:       s.machineScopedActions(),
		History:         s.newActionRows(history),
	}

	running, err := s.newRunningAction(r.Context(), history)
	if err != nil {
		return machineView{}, err
	}
	view.Running = running

	// La machine openCloud s'amorce par enroll-local : pas de commande à
	// coller, la fiche dit déjà le geste.
	if !status.Enrolled && s.enroller != nil && machine.ID != store.LocalMachineID {
		command, err := s.enroller.Prepare(r.Context(), machine)
		if err != nil {
			return machineView{}, err
		}
		view.Command = command
	}
	return view, nil
}

// newRunningAction rend l'action qui tourne, avec ses dernières lignes, et
// nil quand rien ne tourne sur la machine.
func (s *Server) newRunningAction(ctx context.Context, history []store.Action) (*runningAction, error) {
	for _, action := range history {
		if concluded(action.State) {
			continue
		}
		lines, err := s.actions.Lines(ctx, action.ID, 0)
		if err != nil {
			return nil, err
		}
		row := s.newActionRow(action)
		return &runningAction{
			ID:         action.ID,
			Label:      row.Label,
			State:      row.State,
			StateLabel: row.StateLabel,
			CreatedAt:  row.CreatedAt,
			LaunchedAt: formatMoment(action.LaunchedAt),
			Lines:      newOutputLines(lastLines(lines, runningLinesShown)),
		}, nil
	}
	return nil, nil
}

func lastLines(lines []store.ActionLine, count int) []store.ActionLine {
	if len(lines) <= count {
		return lines
	}
	return lines[len(lines)-count:]
}

// Les actions qu'on lance depuis la fiche d'une machine : celles dont la
// portée est la machine elle-même.
func (s *Server) machineScopedActions() []availableAction {
	var available []availableAction
	for _, definition := range s.catalog.Definitions() {
		// Enrôler se joue par la commande collée sur la machine, jamais
		// depuis sa fiche : le lanceur n'y est pas encore.
		if definition.Scope != catalog.ScopeMachine || definition.Kind == catalog.KindEnroler {
			continue
		}
		available = append(available, availableAction{
			Kind:    string(definition.Kind),
			Label:   definition.Label,
			Summary: summaryOf(definition),
		})
	}
	return available
}
