package action

import (
	"cmp"
	"context"
	"slices"
)

// PlanEntry est ce que le plan de mise à jour dit d'une machine.
type PlanEntry struct {
	MachineID       string
	MachineName     string
	Version         string
	Target          string
	UpToDate        bool
	RollbackCapable bool
	Online          bool
	UpdateLast      bool
	// Pourquoi rien ne peut partir pour cette machine (les raisons de la fiche) ;
	// vide quand l'ordre peut partir, ou que la machine est à jour.
	Blocked string
	// Le rang recommandé, de 1 ; 0 quand rien n'est à faire ou que rien ne peut
	// partir.
	Order int
}

// UpdatePlan lit les machines et dit, pour chacune, où en est son agent, et
// dans quel ordre les mettre à jour. Rien ne s'exécute : le serveur ne met
// jamais plusieurs agents à jour d'un coup, l'enchaînement est fait par
// l'opérateur ou par un agent IA, une machine à la fois, en s'arrêtant au
// premier échec.
func (s *Service) UpdatePlan(ctx context.Context) ([]PlanEntry, error) {
	machines, err := s.machines.List(ctx)
	if err != nil {
		return nil, err
	}
	cache := artifactCache{}
	entries := make([]PlanEntry, 0, len(machines))
	for _, target := range machines {
		offer, _, err := s.judgeMachine(target, cache)
		if err != nil {
			return nil, err
		}
		entries = append(entries, planEntryOf(target.ID, target.Name, target.UpdateLast, target.Online, target.Agent.CanRollBack(), offer))
	}
	rankPlan(entries)
	return entries, nil
}

func planEntryOf(id, name string, updateLast, online, rollbackCapable bool, offer UpdateOffer) PlanEntry {
	entry := PlanEntry{
		MachineID: id, MachineName: name, Version: offer.Current, Target: offer.Target,
		RollbackCapable: rollbackCapable, Online: online, UpdateLast: updateLast,
		UpToDate: offer.Reason == OfferSameVersion || offer.Reason == OfferNewerAgent,
	}
	if !entry.UpToDate && !offer.Available {
		entry.Blocked = offer.Reason
	}
	// Une offre disponible peut porter right_unavailable : l'agent refusera.
	if offer.Available && offer.Reason != "" {
		entry.Blocked = offer.Reason
	}
	return entry
}

// rankPlan numérote ce qui peut partir (pas à jour, pas bloqué) : d'abord les
// machines ordinaires, puis celles à mettre à jour en dernier, chaque groupe
// par nom ; les autres suivent, par nom, sans rang.
func rankPlan(entries []PlanEntry) {
	slices.SortFunc(entries, func(first, second PlanEntry) int {
		firstReady, secondReady := first.isReady(), second.isReady()
		if firstReady != secondReady {
			if firstReady {
				return -1
			}
			return 1
		}
		if first.UpdateLast != second.UpdateLast && firstReady {
			if second.UpdateLast {
				return -1
			}
			return 1
		}
		return cmp.Compare(first.MachineName, second.MachineName)
	})
	rank := 0
	for index := range entries {
		if entries[index].isReady() {
			rank++
			entries[index].Order = rank
		}
	}
}

func (e PlanEntry) isReady() bool {
	return !e.UpToDate && (e.Blocked == "" || e.Blocked == OfferRightUnavailable)
}

// TargetVersion est la version du serveur, celle que ses agents reçoivent ;
// vide quand la mise à jour des agents est coupée.
func (s *Service) TargetVersion() string {
	if s.releases == nil {
		return ""
	}
	return s.serverVersion.String()
}
