package web

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/zone"
)

// Les champs des deux formulaires de zone. Le jeton est un champ mot de passe,
// jamais pré-rempli, jamais renvoyé.
const (
	zoneNameFieldName  = "zone"
	zoneTokenFieldName = "token"
)

// submitZone ajoute une zone et pose son jeton. Le jeton entre ici, il ne
// ressort d'aucune page.
func (s *Server) submitZone(w http.ResponseWriter, r *http.Request, account store.Account) {
	if !s.zoneGesture(w, r, account) {
		return
	}
	name := r.PostFormValue(zoneNameFieldName)

	if err := s.zones.Add(r.Context(), name, r.PostFormValue(zoneTokenFieldName)); err != nil {
		s.showZoneOutcome(w, r, account, name, err)
		return
	}

	s.logger.Info("zone added", "zone", name, "username", account.Username)
	redirect(w, r, "/domains")
}

// submitZoneRotation remplace le jeton d'une zone dans openCloud. Les machines
// suivent en rejouant « Poser le jeton DNS » ; la vue dit lesquelles restent.
func (s *Server) submitZoneRotation(w http.ResponseWriter, r *http.Request, account store.Account) {
	if !s.zoneGesture(w, r, account) {
		return
	}
	name := r.PathValue("name")

	if err := s.zones.Rotate(r.Context(), name, r.PostFormValue(zoneTokenFieldName)); err != nil {
		s.showZoneOutcome(w, r, account, name, err)
		return
	}

	s.logger.Info("zone token rotated", "zone", name, "username", account.Username)
	redirect(w, r, "/domains")
}

func (s *Server) submitZoneRemoval(w http.ResponseWriter, r *http.Request, account store.Account) {
	if !s.zoneGesture(w, r, account) {
		return
	}
	name := r.PathValue("name")

	if err := s.zones.Remove(r.Context(), name); err != nil {
		s.showZoneOutcome(w, r, account, name, err)
		return
	}

	s.logger.Info("zone removed", "zone", name, "username", account.Username)
	redirect(w, r, "/domains")
}

// zoneGesture décode le formulaire et vérifie le jeton anti-CSRF ; il répond
// lui-même quand quelque chose manque.
func (s *Server) zoneGesture(w http.ResponseWriter, r *http.Request, account store.Account) bool {
	if !s.zonesReady() {
		http.NotFound(w, r)
		return false
	}
	if !s.readForm(w, r) {
		return false
	}
	if err := s.verifyCSRF(r); err != nil {
		s.showDomainsMessage(w, r, account, http.StatusForbidden, messageFormExpired)
		return false
	}
	return true
}

// showZoneOutcome réaffiche la vue Domaines avec ce que le geste a refusé. Le
// nom saisi revient ; le jeton, jamais.
func (s *Server) showZoneOutcome(w http.ResponseWriter, r *http.Request, account store.Account, name string, err error) {
	var refused refusal.Refusal
	if !errors.As(err, &refused) {
		s.serverError(w, "zone gesture", err)
		return
	}

	view, buildErr := s.newDomainsView(r)
	if buildErr != nil {
		s.serverError(w, "build domains view", buildErr)
		return
	}
	shown := newRefusalView(refused)
	view.ZoneName = name
	view.ZoneRefusal = &shown
	s.render(w, http.StatusUnprocessableEntity, "domains", s.newPage(r, &account, s.csrfFormToken(w, r)).withData(view))
}

// showDomainsMessage réaffiche la vue Domaines avec un message : formulaire
// périmé. Le jeton anti-CSRF est renouvelé, le formulaire reste jouable.
func (s *Server) showDomainsMessage(w http.ResponseWriter, r *http.Request, account store.Account, status int, message refusalView) {
	view, err := s.newDomainsView(r)
	if err != nil {
		s.serverError(w, "build domains view", err)
		return
	}
	s.render(w, status, "domains", s.newPage(r, &account, s.rotateCSRF(w)).withData(view).withError(message))
}

// newZoneRows dit, pour chaque zone, quelles machines portent le jeton
// courant, lesquelles portent encore l'ancien, et lesquelles ne l'ont jamais
// reçu. Une machine déclarée y figure toujours : c'est de là qu'on pose.
func newZoneRows(zones []zone.Zone, machines []store.Machine) []zoneRow {
	var rows []zoneRow
	for _, registered := range zones {
		rows = append(rows, zoneRow{
			Name:        registered.Name,
			AddedAt:     formatMoment(registered.AddedAt),
			RotatedAt:   formatMoment(registered.RotatedAt),
			Fingerprint: registered.Fingerprint,
			Machines:    newZoneMachineRows(registered, machines),
			RotatePath:  "/zones/" + url.PathEscape(registered.Name) + "/token",
			RemovePath:  "/zones/" + url.PathEscape(registered.Name) + "/remove",
		})
	}
	return rows
}

func newZoneMachineRows(registered zone.Zone, machines []store.Machine) []zoneMachineRow {
	placed := map[string]zone.Placement{}
	for _, placement := range registered.Machines {
		placed[placement.MachineID] = placement
	}

	var rows []zoneMachineRow
	for _, machine := range machines {
		row := zoneMachineRow{
			MachineID:   machine.ID,
			MachineName: machine.Name,
			PlacePath:   actionFormPath(machine.ID, catalog.KindDNSToken, catalog.DNSTokenParams(registered.Name)),
		}
		placement, carried := placed[machine.ID]
		switch {
		case !carried:
			row.State, row.Mark, row.Label = tokenAbsent, markAbsent, labelTokenAbsent
		case placement.Current:
			row.State, row.Mark, row.Label = tokenCurrent, markCurrent, labelTokenCurrent
		default:
			row.State, row.Mark, row.Label = tokenStale, markStale, labelTokenStale
		}
		if carried {
			row.PlacedAt = formatMoment(placement.PlacedAt)
			row.Fingerprint = placement.Fingerprint
		}
		rows = append(rows, row)
	}
	return rows
}
