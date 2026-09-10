package web

import (
	"net/http"
	"net/url"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/store"
)

// showDomains liste ce que les machines portent vraiment : la table est
// écrite quand une publication aboutit, jamais quand on la demande.
func (s *Server) showDomains(w http.ResponseWriter, r *http.Request, account store.Account) {
	if !s.domainsReady() {
		http.NotFound(w, r)
		return
	}

	published, err := s.domains.Domains(r.Context())
	if err != nil {
		s.serverError(w, "list domains", err)
		return
	}
	machines, err := s.machines.Machines(r.Context())
	if err != nil {
		s.serverError(w, "list machines", err)
		return
	}

	view := domainsView{
		Domains:  newDomainRows(published, machines),
		Subtitle: labelDomainsSubtitle(len(published)),
	}
	s.render(w, http.StatusOK, "domains", s.newPage(r, &account, s.csrfFormToken(w, r)).withData(view))
}

// showDomainMachines est le premier écran de la publication : sur quelle
// machine. Le formulaire de l'action vit déjà par machine, il n'y a rien à
// inventer de plus.
func (s *Server) showDomainMachines(w http.ResponseWriter, r *http.Request, account store.Account) {
	if !s.domainsReady() {
		http.NotFound(w, r)
		return
	}

	machines, err := s.machineRows(r)
	if err != nil {
		s.serverError(w, "list machines", err)
		return
	}

	view := domainMachinesView{Machines: machines, Kind: string(catalog.KindVhost)}
	s.render(w, http.StatusOK, "domains-new", s.newPage(r, &account, s.csrfFormToken(w, r)).withData(view))
}

func newDomainRows(published []store.Domain, machines []store.Machine) []domainRow {
	names := map[string]string{}
	for _, machine := range machines {
		names[machine.ID] = machine.Name
	}

	var rows []domainRow
	for _, domain := range published {
		// Une machine retirée laisserait son identifiant : mieux vaut le
		// montrer qu'une case vide.
		name := names[domain.MachineID]
		if name == "" {
			name = domain.MachineID
		}
		rows = append(rows, domainRow{
			Name:        domain.Name,
			MachineID:   domain.MachineID,
			MachineName: name,
			Environment: domain.Environment,
			Service:     domain.Service,
			Port:        domain.Port,
			UpdatedAt:   formatMoment(domain.UpdatedAt),
			RemovePath:  removeVhostPath(domain),
		})
	}
	return rows
}

// removeVhostPath mène au formulaire de l'action de suppression, avec le nom
// déjà posé : c'est le même écran « avant » que partout ailleurs.
func removeVhostPath(domain store.Domain) string {
	return actionFormPath(domain.MachineID, catalog.KindVhostRemove,
		catalog.VhostPublication{Domain: domain.Name}.Params())
}

// actionFormPath : l'écran « avant » d'une action sur une machine. Les valeurs
// passées remplissent le formulaire, et rien de plus — c'est le POST qui lance.
func actionFormPath(machineID string, kind catalog.Kind, values map[string]string) string {
	path := "/machines/" + url.PathEscape(machineID) + "/actions/" + url.PathEscape(string(kind))
	if len(values) == 0 {
		return path
	}

	query := url.Values{}
	for name, value := range values {
		query.Set(name, value)
	}
	return path + "?" + query.Encode()
}
