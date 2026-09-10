package web

import (
	"errors"
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/store"
)

// Le champ que coche l'opérateur quand l'action est irréversible ou coupe un
// service en marche.
const confirmFieldName = "confirm"

// showActionForm est l'écran « avant » : les quatre attributs, ce qui va être
// posé, les paramètres, et la confirmation quand elle est due.
func (s *Server) showActionForm(w http.ResponseWriter, r *http.Request, account store.Account) {
	machine, definition, ok := s.actionTarget(w, r)
	if !ok {
		return
	}

	// Un lien peut porter les valeurs — « Supprimer » depuis la vue Domaines,
	// ou le formulaire qui se rafraîchit pendant la saisie. Elles remplissent
	// l'écran, elles ne lancent rien : c'est le POST qui lance.
	view := s.newActionFormView(r, machine, definition, queriedValues(r, definition))
	s.render(w, http.StatusOK, "action-form", s.newPage(r, &account, s.csrfFormToken(w, r)).withData(view))
}

func (s *Server) submitAction(w http.ResponseWriter, r *http.Request, account store.Account) {
	machine, definition, ok := s.actionTarget(w, r)
	if !ok {
		return
	}
	if !s.readForm(w, r) {
		return
	}
	if err := s.verifyCSRF(r); err != nil {
		view := s.newActionFormView(r, machine, definition, map[string]string{})
		page := s.newPage(r, &account, s.rotateCSRF(w)).withData(view).withError(messageFormExpired)
		s.render(w, http.StatusForbidden, "action-form", page)
		return
	}

	values := submittedValues(r, definition)
	csrfToken := s.csrfFormToken(w, r)

	if definition.NeedsConfirmation() && r.PostFormValue(confirmFieldName) == "" {
		view := s.newActionFormView(r, machine, definition, values)
		page := s.newPage(r, &account, csrfToken).withData(view).withError(messageConfirmationRequired)
		s.render(w, http.StatusBadRequest, "action-form", page)
		return
	}

	action, err := s.actions.Enqueue(r.Context(), machine.ID, definition.Kind, values)
	var refused refusal.Refusal
	if errors.As(err, &refused) {
		view := s.newActionFormView(r, machine, definition, values)
		view.Refusal = &refusalView{Cause: refused.Cause, Remedy: refused.Remedy}
		page := s.newPage(r, &account, csrfToken).withData(view)
		s.render(w, http.StatusUnprocessableEntity, "action-form", page)
		return
	}
	if err != nil {
		s.serverError(w, "enqueue action", err)
		return
	}

	s.logger.Info("action enqueued", "action_id", action.ID, "machine", machine.ID,
		"kind", action.Kind, "username", account.Username)
	redirect(w, r, "/actions/"+action.ID)
}

// actionTarget lit la machine et la définition de l'URL, et répond lui-même
// quand l'une des deux manque.
func (s *Server) actionTarget(w http.ResponseWriter, r *http.Request) (store.Machine, catalog.Definition, bool) {
	if !s.actionsReady() {
		http.NotFound(w, r)
		return store.Machine{}, catalog.Definition{}, false
	}

	machine, err := s.machines.Machine(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, messageMachineUnknown, http.StatusNotFound)
		return store.Machine{}, catalog.Definition{}, false
	}
	if err != nil {
		s.serverError(w, "read machine", err)
		return store.Machine{}, catalog.Definition{}, false
	}

	definition, found := s.catalog.Lookup(catalog.Kind(r.PathValue("kind")))
	if !found {
		http.Error(w, messageActionUnknown, http.StatusNotFound)
		return store.Machine{}, catalog.Definition{}, false
	}
	return machine, definition, true
}

func submittedValues(r *http.Request, definition catalog.Definition) map[string]string {
	values := map[string]string{}
	for _, param := range definition.Params {
		values[param.Name] = r.PostFormValue(param.Name)
	}
	return values
}

// queriedValues lit dans l'URL ce que l'action déclare, et rien d'autre : un
// paramètre inconnu n'entre pas dans l'écran.
func queriedValues(r *http.Request, definition catalog.Definition) map[string]string {
	query := r.URL.Query()
	values := map[string]string{}
	for _, param := range definition.Params {
		values[param.Name] = query.Get(param.Name)
	}
	return values
}

// newActionFormView tente la préparation pour montrer les fichiers qui vont
// être posés. Une préparation qui refuse ne se voit pas ici : sur GET les
// paramètres ne sont pas encore saisis, et sur POST le refus est affiché à
// part.
func (s *Server) newActionFormView(r *http.Request, machine store.Machine, definition catalog.Definition, values map[string]string) actionFormView {
	view := actionFormView{
		Machine:           newMachineRow(machine, s.enrolmentStatus(r.Context(), machine.ID)),
		Kind:              string(definition.Kind),
		Label:             definition.Label,
		Summary:           summaryOf(definition),
		Attributes:        attributesOf(definition),
		Params:            definition.Params,
		Values:            values,
		NeedsConfirmation: definition.NeedsConfirmation(),
	}
	if description, found := describeAction(definition.Kind); found {
		view.ItemsTitle = description.ItemsTitle
		view.Items = description.Items
	}
	if prepared, err := s.catalog.Prepare(definition.Kind, values); err == nil {
		view.FilesKnown = true
		view.Files = describeFiles(prepared.Files)
	}
	return view
}

func (s *Server) showAction(w http.ResponseWriter, r *http.Request, account store.Account) {
	if !s.actionsReady() {
		http.NotFound(w, r)
		return
	}

	action, err := s.actions.Action(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, messageActionUnknown, http.StatusNotFound)
		return
	}
	if err != nil {
		s.serverError(w, "read action", err)
		return
	}

	machine, err := s.machines.Machine(r.Context(), action.MachineID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.serverError(w, "read machine of action", err)
		return
	}

	lines, err := s.actions.Lines(r.Context(), action.ID, 0)
	if err != nil {
		s.serverError(w, "read action lines", err)
		return
	}

	view := s.newActionView(r, action, machine, lines)
	s.render(w, http.StatusOK, "action", s.newPage(r, &account, s.csrfFormToken(w, r)).withData(view))
}

func (s *Server) newActionView(r *http.Request, action store.Action, machine store.Machine, lines []store.ActionLine) actionView {
	view := actionView{
		ID:         action.ID,
		Machine:    newMachineRow(machine, s.enrolmentStatus(r.Context(), machine.ID)),
		Label:      action.Kind,
		Kind:       action.Kind,
		State:      string(action.State),
		StateLabel: actionStateLabel(action.State),
		Result:     action.Result,
		Note:       newActionNote(action.Note),
		CreatedAt:  formatMoment(action.CreatedAt),
		LaunchedAt: formatMoment(action.LaunchedAt),
		FinishedAt: formatMoment(action.FinishedAt),
		Lines:      newOutputLines(lines),
		Running:    !concluded(action.State),
	}
	if definition, found := s.catalog.Lookup(catalog.Kind(action.Kind)); found {
		view.Label = definition.Label
		view.Attributes = attributesOf(definition)
	}
	// Le rapport se construit à la fin : une action qui tourne garde son
	// journal en direct.
	if !view.Running {
		view.Report = newReport(catalog.Kind(action.Kind), lines)
	}
	if action.ExitCode != nil {
		view.ExitCode = exitCodeLabel(*action.ExitCode)
	}

	// Le direct reprend après la dernière ligne déjà affichée.
	lastSeq := int64(0)
	if len(lines) > 0 {
		lastSeq = lines[len(lines)-1].Seq
	}
	view.StreamPath = streamPath(action.ID, lastSeq)
	return view
}

func concluded(state store.ActionState) bool {
	return state != store.StatePrepared && state != store.StateRunning
}
