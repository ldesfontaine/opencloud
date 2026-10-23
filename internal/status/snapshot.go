package status

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/probe"
)

// Snapshot est ce que le public voit d'une page : ses composants, et les
// incidents qui touchent l'un d'eux, réduits à ceux-là. Rien d'une autre
// page n'y paraît, pas même le nom d'un composant qu'un incident touche
// aussi ailleurs. Un composant dont aucun objet ne compte et qu'aucun
// incident ne touche n'y est pas : rien à en dire.
func (s *Service) Snapshot(ctx context.Context, pageID string) (Snapshot, error) {
	page, err := s.GetPage(ctx, pageID)
	if err != nil {
		return Snapshot{}, err
	}
	view, err := s.readView(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	return view.snapshotOf(page), nil
}

// view est tout ce que les instantanés lisent, pour toutes les pages d'un
// coup : Refresh juge chaque page sans relire la base page par page.
type view struct {
	now        time.Time
	components []Component
	// Les incidents ouverts, et les résolus des quatorze derniers jours.
	incidents []Incident
	days      []probe.Day
}

func (s *Service) readView(ctx context.Context) (view, error) {
	now := s.now()
	components, err := s.store.ListComponents(ctx, "")
	if err != nil {
		return view{}, err
	}
	known, err := s.facts(ctx)
	if err != nil {
		return view{}, err
	}
	// Les incidents lus contiennent les ouverts : ce sont eux qui imposent
	// un état, les résolus ne s'appliquent plus.
	incidents, err := s.readIncidents(ctx, known, components, now.Add(-HistorySpan))
	if err != nil {
		return view{}, err
	}
	for i := range components {
		s.judge(&components[i], known, incidents)
	}
	days, err := s.reader.ListProbeDays(ctx, "", startOfDay(now.Add(-UptimeSpan)))
	if err != nil {
		return view{}, err
	}
	return view{now: now, components: components, incidents: incidents, days: days}, nil
}

func (v view) snapshotOf(page Page) Snapshot {
	snapshot := Snapshot{Page: page, GeneratedAt: v.now, Components: []PublicComponent{}, Open: []Incident{}, History: []Incident{}}
	onPage := map[string]bool{}
	states := []State{}
	for _, component := range v.components {
		if component.PageID != page.ID {
			continue
		}
		onPage[component.ID] = true
		if component.Effective == StateUnknown {
			continue
		}
		states = append(states, component.Effective)
		snapshot.Components = append(snapshot.Components, PublicComponent{
			ID:    component.ID,
			Name:  component.Name,
			State: component.Effective,
			Days:  uptimeOf(component, v.days),
		})
	}
	snapshot.Global = Worst(states)
	for _, incident := range v.incidents {
		touched, ok := restrictedTo(incident, onPage)
		if !ok {
			continue
		}
		// Les objets visés ne sortent jamais : l'instantané est ce que le
		// public lit, il n'en garde même pas la trace.
		touched.Targets = nil
		if touched.IsResolved() {
			snapshot.History = append(snapshot.History, touched)
		} else {
			snapshot.Open = append(snapshot.Open, touched)
		}
	}
	return snapshot
}

// restrictedTo rend l'incident réduit aux composants de la page, et faux
// s'il n'en touche aucun.
func restrictedTo(incident Incident, onPage map[string]bool) (Incident, bool) {
	kept := []ComponentRef{}
	for _, component := range incident.Components {
		if onPage[component.ID] {
			kept = append(kept, component)
		}
	}
	if len(kept) == 0 {
		return Incident{}, false
	}
	incident.Components = kept
	return incident, true
}

// uptimeOf additionne, jour par jour, les jours des sondes du composant ;
// rien pour un composant sans sonde.
func uptimeOf(component Component, days []probe.Day) []Day {
	probes := map[string]bool{}
	for _, member := range component.Members {
		if member.Kind == KindProbe && member.Present {
			probes[member.ID] = true
		}
	}
	if len(probes) == 0 {
		return nil
	}
	byDay := map[time.Time]*Day{}
	for _, day := range days {
		if !probes[day.ProbeID] {
			continue
		}
		key := day.Day.UTC()
		summed, ok := byDay[key]
		if !ok {
			summed = &Day{Day: key}
			byDay[key] = summed
		}
		summed.Total += day.Total
		summed.Success += day.Success
		summed.Degraded += day.Degraded
	}
	result := make([]Day, 0, len(byDay))
	for _, day := range byDay {
		result = append(result, *day)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Day.Before(result[j].Day) })
	return result
}

func startOfDay(at time.Time) time.Time {
	at = at.UTC()
	return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
}

// Refresh relit les instantanés de toutes les pages et prévient si quelque
// chose de visible a changé sur l'une d'elles. Appelé après chaque écriture
// de l'opérateur, à chaque sujet du bus interne, et toutes les trente
// secondes. Le bus public ne dit que « status » : chaque visiteur relit sa
// page, sans apprendre laquelle a changé.
func (s *Service) Refresh(ctx context.Context) {
	digest, err := s.digestOfPages(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.Error("status snapshot", "error", err)
		}
		return
	}
	s.mu.Lock()
	changed := digest != s.digest
	s.digest = digest
	s.mu.Unlock()
	if !changed {
		return
	}
	for _, watcher := range s.watchers {
		watcher.StatusChanged()
	}
}

// digestOfPages résume ce que les visiteurs voient, page par page ; une page
// créée ou supprimée change le résumé.
func (s *Service) digestOfPages(ctx context.Context) (string, error) {
	pages, err := s.ListPages(ctx)
	if err != nil {
		return "", err
	}
	view, err := s.readView(ctx)
	if err != nil {
		return "", err
	}
	digests := make([]string, 0, len(pages))
	for _, page := range pages {
		digests = append(digests, page.ID+":"+digestOf(view.snapshotOf(page)))
	}
	return strings.Join(digests, ","), nil
}

// digestOf résume ce que le visiteur voit ; l'instant de génération et les
// jours d'uptime n'en font pas partie, sinon chaque rollup réveillerait la
// page pour une barre qui n'a pas bougé à l'œil.
func digestOf(snapshot Snapshot) string {
	type component struct {
		ID    string
		Name  string
		State State
	}
	type incident struct {
		ID         string
		Status     IncidentStatus
		Title      string
		UpdatedAt  time.Time
		Components []ComponentRef
	}
	summary := struct {
		Page       Page
		Global     State
		Components []component
		Incidents  []incident
	}{Page: snapshot.Page, Global: snapshot.Global}
	for _, found := range snapshot.Components {
		summary.Components = append(summary.Components, component{found.ID, found.Name, found.State})
	}
	for _, found := range append(append([]Incident{}, snapshot.Open...), snapshot.History...) {
		summary.Incidents = append(summary.Incidents, incident{found.ID, found.Status, found.Title, found.UpdatedAt, found.Components})
	}
	encoded, _ := json.Marshal(summary)
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}
