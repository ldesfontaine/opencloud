package container

import (
	"context"
)

// Rematch refait le rapprochement des conteneurs vivants d'une machine
// avec l'annuaire tel qu'il est maintenant, et n'écrit que ce qui change.
// L'annuaire l'appelle après chaque écriture, une fois sa transaction
// validée : jamais pendant, sinon il attendrait le verrou de la machine
// que le rapport en cours tient en lisant l'annuaire.
func (t *Tracker) Rematch(ctx context.Context, machineID string) error {
	if t.units == nil {
		return nil
	}
	count, err := t.rematch(ctx, machineID)
	if err != nil {
		return err
	}
	if count > 0 {
		t.logger.Info("containers rematched", "machine", machineID, "count", count)
		if t.watcher != nil {
			t.watcher.ContainersChanged(machineID)
		}
	}
	return nil
}

func (t *Tracker) rematch(ctx context.Context, machineID string) (int, error) {
	lock := t.machineLock(machineID)
	lock.Lock()
	defer lock.Unlock()
	stored, err := t.store.ListContainers(ctx, machineID)
	if err != nil {
		return 0, err
	}
	living := make([]Container, 0, len(stored))
	for _, found := range stored {
		if !found.IsArchived() {
			living = append(living, found)
		}
	}
	if len(living) == 0 {
		return 0, nil
	}
	matches, err := t.units.MatchUnits(ctx, machineID, living)
	if err != nil {
		return 0, err
	}
	var assignments []UnitAssignment
	for _, found := range living {
		match := matches[found.ID]
		if match.UnitID != found.UnitID || match.Level != found.MatchLevel {
			assignments = append(assignments, UnitAssignment{ContainerID: found.ID, UnitID: match.UnitID, Level: match.Level})
		}
	}
	if len(assignments) == 0 {
		return 0, nil
	}
	if err := t.store.SetContainerUnits(ctx, assignments); err != nil {
		return 0, err
	}
	return len(assignments), nil
}

// RematchAll rattache d'un coup les conteneurs de chaque machine : au
// démarrage, pour ce que l'annuaire a reçu avant que le rapprochement
// existe, sans attendre l'inventaire suivant de chaque agent.
func (t *Tracker) RematchAll(ctx context.Context) {
	stored, err := t.store.ListContainers(ctx, "")
	if err != nil {
		t.logger.Warn("rematch containers", "error", err)
		return
	}
	done := map[string]bool{}
	for _, found := range stored {
		if done[found.MachineID] {
			continue
		}
		done[found.MachineID] = true
		if err := t.Rematch(ctx, found.MachineID); err != nil {
			t.logger.Warn("rematch containers", "machine", found.MachineID, "error", err)
		}
	}
}
