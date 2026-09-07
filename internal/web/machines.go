package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/store"
)

// showMachineForm : déclarer une machine, premier temps de l'enrôlement.
func (s *Server) showMachineForm(w http.ResponseWriter, r *http.Request, account store.Account) {
	if !s.enrolmentReady() {
		http.NotFound(w, r)
		return
	}

	view := machineFormView{Values: map[string]string{portFieldName: strconv.Itoa(defaultSSHPort)}}
	s.render(w, http.StatusOK, "machine-new", s.newPage(r, &account, s.csrfFormToken(w, r)).withData(view))
}

// submitMachine déclare la machine puis engendre sa clé et la commande à
// coller. La fiche montre ensuite la commande et attend l'empreinte.
func (s *Server) submitMachine(w http.ResponseWriter, r *http.Request, account store.Account) {
	if !s.enrolmentReady() {
		http.NotFound(w, r)
		return
	}
	if !s.readForm(w, r) {
		return
	}
	if err := s.verifyCSRF(r); err != nil {
		page := s.newPage(r, &account, s.rotateCSRF(w)).withData(submittedMachineForm(r, nil)).withError(messageFormExpired)
		s.render(w, http.StatusForbidden, "machine-new", page)
		return
	}

	machine, refused := declaredMachine(r)
	if refused == nil {
		taken, err := s.identifierTaken(r.Context(), machine.ID)
		if err != nil {
			s.serverError(w, "read machine", err)
			return
		}
		if taken {
			alreadyTaken := refusalIdentifierTaken(machine.ID)
			refused = &alreadyTaken
		}
	}
	if refused != nil {
		page := s.newPage(r, &account, s.csrfFormToken(w, r)).withData(submittedMachineForm(r, refused))
		s.render(w, http.StatusUnprocessableEntity, "machine-new", page)
		return
	}

	if err := s.declaration.Insert(r.Context(), machine); err != nil {
		s.serverError(w, "insert machine", err)
		return
	}
	if _, err := s.enroller.Prepare(r.Context(), machine); err != nil {
		s.serverError(w, "prepare enrolment", err)
		return
	}

	s.logger.Info("machine declared", "machine", machine.ID, "username", account.Username)
	redirect(w, r, "/machines/"+machine.ID)
}

// submitFingerprint est le second temps : openCloud relève la clé d'hôte et
// refuse si elle ne correspond pas à ce que la commande a affiché.
func (s *Server) submitFingerprint(w http.ResponseWriter, r *http.Request, account store.Account) {
	if s.enroller == nil {
		http.NotFound(w, r)
		return
	}
	machine, ok := s.machineGesture(w, r)
	if !ok {
		return
	}
	if err := s.verifyCSRF(r); err != nil {
		s.showMachineMessage(w, r, account, machine, http.StatusForbidden, messageFormExpired)
		return
	}

	fingerprint := strings.TrimSpace(r.PostFormValue(fingerprintFieldName))
	if !validFingerprint(fingerprint) {
		s.showMachineMessage(w, r, account, machine, http.StatusBadRequest, messageFingerprintMalformed)
		return
	}

	err := s.enroller.Confirm(r.Context(), machine, fingerprint)
	var refused refusal.Refusal
	if errors.As(err, &refused) {
		s.showMachineRefusal(w, r, account, machine, newRefusalView(refused))
		return
	}
	if err != nil {
		s.serverError(w, "confirm host fingerprint", err)
		return
	}

	s.logger.Info("machine enrolled", "machine", machine.ID, "username", account.Username)
	redirect(w, r, "/machines/"+machine.ID)
}

// submitProbe joue « Tester l'accès » tout de suite : le bouton de la fiche,
// à côté du sondage périodique qui tourne en silence.
func (s *Server) submitProbe(w http.ResponseWriter, r *http.Request, account store.Account) {
	if s.prober == nil {
		http.NotFound(w, r)
		return
	}
	machine, ok := s.machineGesture(w, r)
	if !ok {
		return
	}
	if err := s.verifyCSRF(r); err != nil {
		s.showMachineMessage(w, r, account, machine, http.StatusForbidden, messageFormExpired)
		return
	}

	err := s.prober.Now(r.Context(), machine.ID)
	var refused refusal.Refusal
	if errors.As(err, &refused) {
		s.showMachineRefusal(w, r, account, machine, newRefusalView(refused))
		return
	}
	if err != nil {
		s.serverError(w, "probe machine", err)
		return
	}

	s.logger.Info("machine probed", "machine", machine.ID, "username", account.Username)
	redirect(w, r, "/machines/"+machine.ID)
}

// submitAccess change l'adresse, le port ou l'empreinte : une action nommée,
// jamais un effet de bord d'autre chose.
func (s *Server) submitAccess(w http.ResponseWriter, r *http.Request, account store.Account) {
	if !s.enrolmentReady() {
		http.NotFound(w, r)
		return
	}
	machine, ok := s.machineGesture(w, r)
	if !ok {
		return
	}
	if err := s.verifyCSRF(r); err != nil {
		s.showMachineMessage(w, r, account, machine, http.StatusForbidden, messageFormExpired)
		return
	}

	address, port, refused := declaredAccess(r)
	if refused != nil {
		s.showMachineRefusal(w, r, account, machine, *refused)
		return
	}
	fingerprint := strings.TrimSpace(r.PostFormValue(fingerprintFieldName))
	if !validFingerprint(fingerprint) {
		s.showMachineMessage(w, r, account, machine, http.StatusBadRequest, messageFingerprintMalformed)
		return
	}

	if err := s.declaration.UpdateAccess(r.Context(), machine.ID, address, port); err != nil {
		s.serverError(w, "update machine access", err)
		return
	}
	// L'adresse déclarée est écrite d'abord : c'est elle que l'enrôlement
	// joint pour relever la clé d'hôte. Un refus laisse la machine sur sa
	// nouvelle adresse et sur son ancienne clé, et le dit.
	machine.Address = address
	machine.Port = port

	err := s.enroller.UpdateAccess(r.Context(), machine, fingerprint)
	var refusedByEnrolment refusal.Refusal
	if errors.As(err, &refusedByEnrolment) {
		s.showMachineRefusal(w, r, account, machine, newRefusalView(refusedByEnrolment))
		return
	}
	if err != nil {
		s.serverError(w, "update enrolment access", err)
		return
	}

	s.logger.Info("machine access changed", "machine", machine.ID,
		"address", address, "port", port, "username", account.Username)
	redirect(w, r, "/machines/"+machine.ID)
}

// machineGesture lit la machine d'un geste posté depuis sa fiche et décode le
// formulaire ; il répond lui-même quand l'un des deux manque. La fiche est
// réaffichée sur refus, elle demande donc tout ce qu'une fiche demande.
func (s *Server) machineGesture(w http.ResponseWriter, r *http.Request) (store.Machine, bool) {
	if !s.actionsReady() {
		http.NotFound(w, r)
		return store.Machine{}, false
	}

	machine, err := s.machines.Machine(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, messageMachineUnknown, http.StatusNotFound)
		return store.Machine{}, false
	}
	if err != nil {
		s.serverError(w, "read machine", err)
		return store.Machine{}, false
	}
	if !s.readForm(w, r) {
		return store.Machine{}, false
	}
	return machine, true
}

// L'identifiant dit où vont la clé et le known_hosts de la machine : deux
// machines ne peuvent pas le partager.
func (s *Server) identifierTaken(ctx context.Context, id string) (bool, error) {
	_, err := s.machines.Machine(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// showMachineRefusal réaffiche la fiche avec la cause du refus et le geste qui
// le lève.
func (s *Server) showMachineRefusal(w http.ResponseWriter, r *http.Request, account store.Account, machine store.Machine, refused refusalView) {
	view, err := s.newMachineView(r, machine)
	if err != nil {
		s.serverError(w, "build machine view", err)
		return
	}
	view.Refusal = &refused
	s.render(w, http.StatusUnprocessableEntity, "machine", s.newPage(r, &account, s.csrfFormToken(w, r)).withData(view))
}

// showMachineMessage réaffiche la fiche avec un message : formulaire périmé,
// empreinte hors forme. Le jeton est renouvelé, le formulaire reste jouable.
func (s *Server) showMachineMessage(w http.ResponseWriter, r *http.Request, account store.Account, machine store.Machine, status int, message refusalView) {
	view, err := s.newMachineView(r, machine)
	if err != nil {
		s.serverError(w, "build machine view", err)
		return
	}
	s.render(w, status, "machine", s.newPage(r, &account, s.rotateCSRF(w)).withData(view).withError(message))
}

// submittedMachineForm rend le formulaire tel qu'il a été saisi : un refus ne
// fait pas retaper l'adresse.
func submittedMachineForm(r *http.Request, refused *refusalView) machineFormView {
	values := map[string]string{}
	for _, field := range []string{nameFieldName, addressFieldName, portFieldName} {
		values[field] = r.PostFormValue(field)
	}
	return machineFormView{Values: values, Refusal: refused}
}
