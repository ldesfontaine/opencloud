package web

import (
	"errors"
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Les onglets d'une machine : segment d'URL → clé de libellé. Seul le
// résumé a un contenu ; les autres attendent leur fonctionnalité.
var machineTabs = []tabDef{
	{"", "tab.summary"},
	{"services", "tab.services"},
	{"domaines", "tab.domains"},
	{"reseau", "tab.network"},
	{"sauvegardes", "tab.backups"},
	{"journaux", "tab.logs"},
	{"reglages", "tab.settings"},
}

type tabDef struct {
	Slug string
	Key  string
}

func (s *Server) machinesPage(w http.ResponseWriter, r *http.Request) {
	text := s.catalog()
	page, err := s.machinesPageData(r)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	data := s.newView(r, navMachines, text.Get("nav.machines"), countSubtitle(text, page.Total, page.Online))
	data.Page = page
	s.render(w, r, http.StatusOK, "machines", data)
}

// Le tableau seul, rafraîchi par HTMX toutes les dix secondes.
func (s *Server) machinesTable(w http.ResponseWriter, r *http.Request) {
	page, err := s.machinesPageData(r)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	data := s.newView(r, navMachines, "", "")
	data.Page = page
	s.renderFragment(w, r, "machines-table", data)
}

func (s *Server) newMachinePage(w http.ResponseWriter, r *http.Request) {
	s.renderNewMachine(w, r, http.StatusOK, newMachineForm{})
}

// L'opérateur nomme la machine ; le jeton naît et s'affiche une seule fois,
// avec la commande à coller sur la machine.
func (s *Server) createMachineToken(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(w, r) {
		return
	}
	name := r.FormValue("name")
	cleartext, token, err := s.machines.CreateToken(r.Context(), name)
	if errors.Is(err, machine.ErrNameInvalid) {
		s.renderNewMachine(w, r, http.StatusUnprocessableEntity, newMachineForm{Name: name, ErrorKey: "machine.name_invalid"})
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.renderToken(w, r, token, cleartext)
}

func (s *Server) renderNewMachine(w http.ResponseWriter, r *http.Request, status int, form newMachineForm) {
	text := s.catalog()
	data := s.newView(r, navMachines, text.Get("machine.new_title"), text.Get("machine.new_subtitle"))
	data.Page = form
	s.render(w, r, status, "machine-new", data)
}

func (s *Server) renderToken(w http.ResponseWriter, r *http.Request, token machine.Token, cleartext string) {
	text := s.catalog()
	url, isLocal := s.resolvePublicURL(r)
	data := s.newView(r, navMachines, text.Get("machine.token_title"), text.Format("machine.token_subtitle", token.Name))
	data.Page = tokenPage{
		Name:      token.Name,
		Token:     cleartext,
		Command:   installCommand(url, cleartext),
		ExpiresIn: formatDuration(text, machine.TokenLifetime),
		URLLocal:  isLocal,
	}
	s.render(w, r, http.StatusOK, "machine-token", data)
}

func installCommand(publicURL, token string) string {
	return "sudo opencloud agent -server " + publicURL + " -token " + token
}

func (s *Server) cancelMachineToken(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(w, r) {
		return
	}
	err := s.machines.CancelToken(r.Context(), r.PathValue("id"))
	if err != nil && !errors.Is(err, machine.ErrTokenNotFound) {
		s.internalError(w, r, err)
		return
	}
	s.redirect(w, r, "/machines")
}

// La page d'une machine : l'en-tête de la direction artistique, les onglets,
// le résumé ; les autres onglets sont vides jusqu'à leur fonctionnalité.
func (s *Server) machinePage(w http.ResponseWriter, r *http.Request) {
	text := s.catalog()
	tab, ok := findTab(r.PathValue("tab"))
	if !ok {
		s.notFound(w, r)
		return
	}
	head, err := s.machineHeadData(r, r.PathValue("id"))
	if errors.Is(err, machine.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	data := s.newView(r, navMachines, head.Current.Name, "")
	data.Page = machinePage{
		Head:    head,
		Tab:     tab.Slug,
		Tabs:    s.tabLinks(text, head.Current.ID, tab.Slug),
		Summary: s.summaryRows(text, head.Current),
	}
	s.render(w, r, http.StatusOK, "machine", data)
}

// L'en-tête seul, rafraîchi par HTMX : pastille et « vu il y a ».
func (s *Server) machineHead(w http.ResponseWriter, r *http.Request) {
	head, err := s.machineHeadData(r, r.PathValue("id"))
	if errors.Is(err, machine.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	data := s.newView(r, navMachines, "", "")
	data.Page = machinePage{Head: head}
	s.renderFragment(w, r, "machine-head", data)
}

func (s *Server) removeMachine(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(w, r) {
		return
	}
	err := s.machines.Remove(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, machine.ErrLocalMachine):
		s.refuse(w, r, http.StatusForbidden, s.catalog().Get("machine.local_kept"))
	case errors.Is(err, machine.ErrNotFound):
		s.notFound(w, r)
	case err != nil:
		s.internalError(w, r, err)
	default:
		s.redirect(w, r, "/machines")
	}
}

// Ré-enrôler : un nouveau jeton pour la même machine, qui garde son id.
func (s *Server) reenrollMachine(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(w, r) {
		return
	}
	cleartext, token, err := s.machines.CreateReenrollToken(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, machine.ErrLocalMachine):
		s.refuse(w, r, http.StatusForbidden, s.catalog().Get("machine.local_kept"))
	case errors.Is(err, machine.ErrNotFound):
		s.notFound(w, r)
	case err != nil:
		s.internalError(w, r, err)
	default:
		s.renderToken(w, r, token, cleartext)
	}
}

func findTab(slug string) (tabDef, bool) {
	for _, tab := range machineTabs {
		if tab.Slug == slug {
			return tab, true
		}
	}
	return tabDef{}, false
}
