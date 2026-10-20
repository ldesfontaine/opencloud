package status

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/ldesfontaine/opencloud/internal/probe"
)

// Snapshot est ce que le public voit. Un composant dont aucun objet ne
// compte et qu'aucun incident ne touche n'y est pas : rien à en dire.
func (s *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	now := s.now()
	page, err := s.Page(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	components, err := s.Components(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	incidents, err := s.store.ListIncidents(ctx, now.Add(-HistorySpan))
	if err != nil {
		return Snapshot{}, err
	}
	days, err := s.store.ListProbeDays(ctx, "", startOfDay(now.Add(-UptimeSpan)))
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{Page: page, GeneratedAt: now, Components: []PublicComponent{}, Open: []Incident{}, History: []Incident{}}
	states := []State{}
	for _, component := range components {
		if component.Effective == StateUnknown {
			continue
		}
		states = append(states, component.Effective)
		snapshot.Components = append(snapshot.Components, PublicComponent{
			ID:    component.ID,
			Name:  component.Name,
			State: component.Effective,
			Days:  uptimeOf(component, days),
		})
	}
	snapshot.Global = Worst(states)
	for _, incident := range incidents {
		if incident.IsResolved() {
			snapshot.History = append(snapshot.History, incident)
		} else {
			snapshot.Open = append(snapshot.Open, incident)
		}
	}
	return snapshot, nil
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

// Refresh relit l'instantané et prévient si quelque chose de visible a
// changé. Appelé après chaque écriture de l'opérateur, à chaque sujet du
// bus interne, et toutes les trente secondes.
func (s *Service) Refresh(ctx context.Context) {
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.Error("status snapshot", "error", err)
		}
		return
	}
	digest := digestOf(snapshot)
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
