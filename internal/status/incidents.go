package status

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/ident"
)

// OpenIncident écrit l'incident, ses cibles et la première entrée de son
// fil. Une maintenance dont la fenêtre est déjà ouverte commence tout
// de suite.
func (s *Service) OpenIncident(ctx context.Context, definition IncidentDefinition) (Incident, error) {
	now := s.now()
	if err := definition.Validate(now); err != nil {
		return Incident{}, err
	}
	if err := s.checkTargets(ctx, definition.Targets); err != nil {
		return Incident{}, err
	}
	id, err := ident.New()
	if err != nil {
		return Incident{}, fmt.Errorf("generate id: %w", err)
	}
	status := definition.Status
	if definition.Impact == ImpactMaintenance && !definition.StartsAt.After(now) {
		status = StatusInProgress
	}
	incident := Incident{
		ID:        id,
		Title:     strings.TrimSpace(definition.Title),
		Impact:    definition.Impact,
		Status:    status,
		StartsAt:  definition.StartsAt,
		EndsAt:    definition.EndsAt,
		Targets:   targetsOf(definition.Targets),
		CreatedAt: now,
		UpdatedAt: now,
		Updates:   []Update{{Status: status, Message: strings.TrimSpace(definition.Message), CreatedAt: now}},
	}
	if err := s.store.InsertIncident(ctx, incident); err != nil {
		return Incident{}, err
	}
	s.logger.Info("incident opened", "incident_id", id, "impact", incident.Impact, "title", incident.Title)
	s.auditor.Record(ctx, incidentEvent(audit.IncidentOpen, incident))
	s.Refresh(ctx)
	return s.GetIncident(ctx, id)
}

// AddUpdate ajoute une entrée au fil et fait passer l'incident à ce
// statut ; « résolu » le clôt et note l'instant.
func (s *Service) AddUpdate(ctx context.Context, id string, status IncidentStatus, message string) (Incident, error) {
	incident, err := s.store.GetIncident(ctx, id)
	if err != nil {
		return Incident{}, err
	}
	if incident.IsResolved() {
		return Incident{}, ErrResolved
	}
	if !allowedStatus(incident.Impact, status) || status == StatusScheduled {
		return Incident{}, ErrStatusInvalid
	}
	if !validMessage(message) {
		return Incident{}, ErrMessageInvalid
	}
	// Ni message ni changement de statut : l'entrée ne dirait rien au
	// public.
	if status == incident.Status && strings.TrimSpace(message) == "" {
		return Incident{}, ErrUpdateEmpty
	}
	if err := s.transition(ctx, incident, status, strings.TrimSpace(message)); err != nil {
		return Incident{}, err
	}
	s.Refresh(ctx)
	return s.GetIncident(ctx, id)
}

func (s *Service) transition(ctx context.Context, incident Incident, status IncidentStatus, message string) error {
	now := s.now()
	incident.Status = status
	incident.UpdatedAt = now
	if status == StatusResolved {
		incident.ResolvedAt = now
	}
	if err := s.store.Transition(ctx, incident, Update{Status: status, Message: message, CreatedAt: now}); err != nil {
		return err
	}
	s.logger.Info("incident updated", "incident_id", incident.ID, "status", status)
	s.auditor.Record(ctx, postEvent(incident, message))
	return nil
}

// ChangeIncident corrige le titre, les cibles et la fenêtre ; le fil reste
// tel quel.
func (s *Service) ChangeIncident(ctx context.Context, id string, change IncidentChange) (Incident, error) {
	incident, err := s.store.GetIncident(ctx, id)
	if err != nil {
		return Incident{}, err
	}
	if !validText(change.Title, MaxTitleLength) {
		return Incident{}, ErrTitleInvalid
	}
	if err := validateTargets(change.Targets); err != nil {
		return Incident{}, err
	}
	if err := validateWindow(incident.Impact, change.StartsAt, change.EndsAt, s.now()); err != nil {
		return Incident{}, err
	}
	if err := s.checkTargets(ctx, change.Targets); err != nil {
		return Incident{}, err
	}
	incident.Title = strings.TrimSpace(change.Title)
	incident.StartsAt = change.StartsAt
	incident.EndsAt = change.EndsAt
	incident.UpdatedAt = s.now()
	incident.Targets = targetsOf(change.Targets)
	if err := s.store.UpdateIncident(ctx, incident); err != nil {
		return Incident{}, err
	}
	s.auditor.Record(ctx, incidentEvent(audit.IncidentUpdate, incident))
	s.Refresh(ctx)
	return s.GetIncident(ctx, id)
}

func (s *Service) DeleteIncident(ctx context.Context, id string) error {
	// Relu avant : le journal garde le titre de ce qui part.
	found, err := s.store.GetIncident(ctx, id)
	if err != nil {
		return err
	}
	if err := s.store.DeleteIncident(ctx, id); err != nil {
		return err
	}
	s.logger.Info("incident deleted", "incident_id", id)
	s.auditor.Record(ctx, incidentEvent(audit.IncidentDelete, found))
	s.Refresh(ctx)
	return nil
}

// Incidents rend ce que la base garde, du plus récent au plus ancien ; avec
// une page, ceux qui touchent l'un de ses composants, tels quels : un
// incident n'appartient pas à une page, il vise des objets. Les silences
// annoncés n'y entrent pas : ils ne s'écrivent pas.
func (s *Service) Incidents(ctx context.Context, pageID string) ([]Incident, error) {
	if pageID != "" {
		if _, err := s.store.GetPage(ctx, pageID); err != nil {
			return nil, err
		}
	}
	components, err := s.store.ListComponents(ctx, "")
	if err != nil {
		return nil, err
	}
	incidents, err := s.store.ListIncidents(ctx, time.Time{})
	if err != nil {
		return nil, err
	}
	known, err := s.facts(ctx)
	if err != nil {
		return nil, err
	}
	known.describe(incidents, components)
	if pageID == "" {
		return incidents, nil
	}
	onPage := make(map[string]bool, len(components))
	for _, component := range components {
		if component.PageID == pageID {
			onPage[component.ID] = true
		}
	}
	kept := []Incident{}
	for _, incident := range incidents {
		if _, touches := restrictedTo(incident, onPage); touches {
			kept = append(kept, incident)
		}
	}
	return kept, nil
}

// GetIncident rend l'incident avec le nom de ses cibles et les composants
// qu'elles touchent.
func (s *Service) GetIncident(ctx context.Context, id string) (Incident, error) {
	incident, err := s.store.GetIncident(ctx, id)
	if err != nil {
		return Incident{}, err
	}
	components, err := s.store.ListComponents(ctx, "")
	if err != nil {
		return Incident{}, err
	}
	known, err := s.facts(ctx)
	if err != nil {
		return Incident{}, err
	}
	incidents := []Incident{incident}
	known.describe(incidents, components)
	return incidents[0], nil
}

func (s *Service) CountOpenIncidents(ctx context.Context) (int, error) {
	return s.store.CountOpenIncidents(ctx)
}

// checkTargets refuse un objet que l'annuaire ne connaît pas.
func (s *Service) checkTargets(ctx context.Context, targets []MemberRef) error {
	known, err := s.facts(ctx)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if !known.exists(target.Kind, target.ID) {
			return ErrTargetInvalid
		}
	}
	return nil
}

// ApplyWindows ouvre les maintenances dont l'heure est venue et résout
// celles dont la fenêtre est passée : le fil public, rien d'autre. Une
// maintenance ne tait pas les alertes ; c'est une fenêtre de mutes qui le
// fait.
func (s *Service) ApplyWindows(ctx context.Context) {
	now := s.now()
	incidents, err := s.store.ListIncidents(ctx, now)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.Error("list incidents", "error", err)
		}
		return
	}
	for _, incident := range incidents {
		if !incident.IsMaintenance() || incident.IsResolved() {
			continue
		}
		switch {
		case incident.Status == StatusScheduled && !incident.StartsAt.After(now):
			err = s.transition(ctx, incident, StatusInProgress, "")
		case incident.Status == StatusInProgress && !incident.EndsAt.After(now):
			err = s.transition(ctx, incident, StatusResolved, "")
		}
		if err != nil && ctx.Err() == nil {
			s.logger.Error("apply maintenance window", "incident_id", incident.ID, "error", err)
		}
	}
}

// Purge efface les incidents résolus depuis plus d'un an, fil et cibles
// avec eux.
func (s *Service) Purge(ctx context.Context) error {
	return s.store.PurgeIncidents(ctx, s.now().Add(-IncidentRetention))
}
