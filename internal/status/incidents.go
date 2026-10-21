package status

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/ident"
)

// UnderMaintenance dit si un objet est rattaché à un composant sous un
// incident de maintenance qui s'applique : le moteur des alertes ouvre
// alors l'alerte sans la livrer. Un objet rattaché nulle part ne l'est
// jamais.
//
// Le genre « service » d'une alerte désigne un conteneur observé, alors que
// celui d'un membre désigne un service de l'annuaire : le conteneur est
// retrouvé par son unité. Un conteneur non déclaré n'est dans aucun composant.
func (s *Service) UnderMaintenance(ctx context.Context, objectKind, objectID string) (bool, error) {
	ref := MemberRef{Kind: MemberKind(objectKind), ID: objectID}
	if !isKind(ref.Kind) || ref.ID == "" {
		return false, nil
	}
	if ref.Kind == KindService {
		declaredID, err := s.declaredServiceOf(ctx, ref.ID)
		if err != nil || declaredID == "" {
			return false, err
		}
		ref.ID = declaredID
	}
	components, err := s.store.ListComponents(ctx)
	if err != nil {
		return false, err
	}
	var touched []string
	for _, component := range components {
		for _, member := range component.Members {
			if member.Kind == ref.Kind && member.ID == ref.ID {
				touched = append(touched, component.ID)
			}
		}
	}
	if len(touched) == 0 {
		return false, nil
	}
	incidents, err := s.store.ListIncidents(ctx, s.now())
	if err != nil {
		return false, err
	}
	for _, incident := range incidents {
		if !incident.IsMaintenance() || !incident.Applies() {
			continue
		}
		for _, component := range incident.Components {
			for _, id := range touched {
				if component.ID == id {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

// declaredServiceOf rend le service de l'annuaire qui porte l'unité d'un
// conteneur ; vide si le conteneur n'existe plus ou n'est pas déclaré.
func (s *Service) declaredServiceOf(ctx context.Context, containerID string) (string, error) {
	item, err := s.reader.GetContainer(ctx, containerID)
	if errors.Is(err, container.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	// La fiche porte déjà le service de son unité : pas besoin de relire
	// l'annuaire à chaque alerte.
	return item.DeclaredServiceID, nil
}

// OpenIncident écrit l'incident, ses composants et la première entrée de
// son fil. Une maintenance dont la fenêtre est déjà ouverte commence tout
// de suite.
func (s *Service) OpenIncident(ctx context.Context, definition IncidentDefinition) (Incident, error) {
	now := s.now()
	if err := definition.Validate(now); err != nil {
		return Incident{}, err
	}
	if err := s.checkComponents(ctx, definition.ComponentIDs); err != nil {
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
		CreatedAt: now,
		UpdatedAt: now,
		Updates:   []Update{{Status: status, Message: strings.TrimSpace(definition.Message), CreatedAt: now}},
	}
	for _, componentID := range definition.ComponentIDs {
		incident.Components = append(incident.Components, ComponentRef{ID: componentID})
	}
	if err := s.store.InsertIncident(ctx, incident); err != nil {
		return Incident{}, err
	}
	s.logger.Info("incident opened", "incident_id", id, "impact", incident.Impact, "title", incident.Title)
	s.auditor.Record(ctx, incidentEvent(audit.IncidentOpen, incident))
	s.Refresh(ctx)
	return s.store.GetIncident(ctx, id)
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
	return s.store.GetIncident(ctx, id)
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

// ChangeIncident corrige le titre, les composants et la fenêtre ; le fil
// reste tel quel.
func (s *Service) ChangeIncident(ctx context.Context, id string, change IncidentChange) (Incident, error) {
	incident, err := s.store.GetIncident(ctx, id)
	if err != nil {
		return Incident{}, err
	}
	if !validText(change.Title, MaxTitleLength) {
		return Incident{}, ErrTitleInvalid
	}
	if len(change.ComponentIDs) == 0 || len(change.ComponentIDs) > MaxComponents {
		return Incident{}, ErrComponentInvalid
	}
	if err := validateWindow(incident.Impact, change.StartsAt, change.EndsAt, s.now()); err != nil {
		return Incident{}, err
	}
	if err := s.checkComponents(ctx, change.ComponentIDs); err != nil {
		return Incident{}, err
	}
	incident.Title = strings.TrimSpace(change.Title)
	incident.StartsAt = change.StartsAt
	incident.EndsAt = change.EndsAt
	incident.UpdatedAt = s.now()
	incident.Components = incident.Components[:0]
	for _, componentID := range change.ComponentIDs {
		incident.Components = append(incident.Components, ComponentRef{ID: componentID})
	}
	if err := s.store.UpdateIncident(ctx, incident); err != nil {
		return Incident{}, err
	}
	s.auditor.Record(ctx, incidentEvent(audit.IncidentUpdate, incident))
	s.Refresh(ctx)
	return s.store.GetIncident(ctx, id)
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

// Incidents rend tout ce que la base garde, du plus récent au plus ancien.
func (s *Service) Incidents(ctx context.Context) ([]Incident, error) {
	return s.store.ListIncidents(ctx, time.Time{})
}

func (s *Service) GetIncident(ctx context.Context, id string) (Incident, error) {
	return s.store.GetIncident(ctx, id)
}

func (s *Service) CountOpenIncidents(ctx context.Context) (int, error) {
	return s.store.CountOpenIncidents(ctx)
}

// checkComponents refuse un identifiant qui n'est pas un composant, ou
// donné deux fois.
func (s *Service) checkComponents(ctx context.Context, ids []string) error {
	components, err := s.store.ListComponents(ctx)
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(components))
	for _, component := range components {
		known[component.ID] = true
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !known[id] || seen[id] {
			return ErrComponentInvalid
		}
		seen[id] = true
	}
	return nil
}

// ApplyWindows ouvre les maintenances dont l'heure est venue et résout
// celles dont la fenêtre est passée.
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

// Purge efface les incidents résolus depuis plus d'un an, fil et
// rattachements avec eux.
func (s *Service) Purge(ctx context.Context) error {
	return s.store.PurgeIncidents(ctx, s.now().Add(-IncidentRetention))
}
