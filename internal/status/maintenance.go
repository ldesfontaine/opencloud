package status

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
)

// maintenanceOf montre un silence annoncé comme une maintenance : planifiée
// avant sa fenêtre, en cours pendant, résolue après. Elle n'est jamais
// écrite dans incidents, et son fil est vide ; UpdatedAt change à chaque
// passage pour que la page se rafraîchisse.
func maintenanceOf(mute alert.Mute, now time.Time) Incident {
	incident := Incident{
		ID:        "mute-" + strconv.FormatInt(mute.ID, 10),
		Title:     mute.PublicTitle,
		Impact:    ImpactMaintenance,
		StartsAt:  mute.StartsAt,
		EndsAt:    mute.EndsAt,
		Targets:   []Target{{Kind: MemberKind(mute.Target.Kind), ID: mute.Target.ID}},
		Updates:   []Update{},
		CreatedAt: mute.CreatedAt,
	}
	switch {
	case now.Before(mute.StartsAt):
		incident.Status = StatusScheduled
		incident.UpdatedAt = mute.CreatedAt
	case now.Before(mute.EndsAt):
		incident.Status = StatusInProgress
		incident.UpdatedAt = mute.StartsAt
	default:
		incident.Status = StatusResolved
		incident.UpdatedAt = mute.EndsAt
		incident.ResolvedAt = mute.EndsAt
	}
	return incident
}

// announced rend les silences annoncés, planifiés, en cours ou finis depuis
// moins de quatorze jours, tels que le public les lit.
func (s *Service) announced(ctx context.Context, now time.Time) ([]Incident, error) {
	mutes, err := s.reader.ListMutes(ctx)
	if err != nil {
		return nil, err
	}
	incidents := []Incident{}
	for _, mute := range mutes {
		if !mute.Announce || !mute.EndsAt.After(now.Add(-HistorySpan)) || !isKind(MemberKind(mute.Target.Kind)) {
			continue
		}
		incidents = append(incidents, maintenanceOf(mute, now))
	}
	return incidents, nil
}

// withAnnounced joint les silences annoncés aux incidents de la base, du
// plus récent au plus ancien ; à date égale, l'incident écrit passe devant.
func (s *Service) withAnnounced(ctx context.Context, incidents []Incident, now time.Time) ([]Incident, error) {
	announced, err := s.announced(ctx, now)
	if err != nil {
		return nil, err
	}
	merged := append(slices.Clone(incidents), announced...)
	slices.SortStableFunc(merged, func(a, b Incident) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	return merged, nil
}
