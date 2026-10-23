package alert

import (
	"context"
)

// Acknowledge note que l'opérateur a vu : l'alerte reste ouverte et sort
// du compteur rouge. Une aggravation la ré-arme.
func (e *Engine) Acknowledge(ctx context.Context, id int64) (Alert, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	alert, err := e.store.GetAlert(ctx, id)
	if err != nil {
		return Alert{}, err
	}
	if !alert.IsOpen() {
		return Alert{}, ErrAlreadyClosed
	}
	if alert.IsAcknowledged() {
		return alert, nil
	}
	alert.AcknowledgedAt = e.now()
	if err := e.store.UpdateAlert(ctx, alert); err != nil {
		return Alert{}, err
	}
	e.logger.Info("alert acknowledged", "alert_id", id)
	e.auditor.Record(ctx, acknowledgeEvent(alert))
	e.changed()
	return alert, nil
}

// AcknowledgeMany acquitte une sélection en une écriture. Une alerte
// résolue, déjà acquittée ou disparue entre l'affichage et le clic est
// passée sans refus : l'opérateur voulait qu'elle ne compte plus, c'est le
// cas. Rend le nombre d'alertes réellement acquittées.
func (e *Engine) AcknowledgeMany(ctx context.Context, ids []int64) (int, error) {
	unique := uniqueIDs(ids)
	if len(unique) == 0 {
		return 0, ErrSelectionEmpty
	}
	if len(unique) > MaxAcknowledgedAtOnce {
		return 0, ErrSelectionTooLarge
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	acknowledged, err := e.store.AcknowledgeAlerts(ctx, unique, e.now())
	if err != nil {
		return 0, err
	}
	e.logger.Info("alerts acknowledged", "requested", len(unique), "acknowledged", acknowledged)
	e.auditor.Record(ctx, acknowledgeManyEvent(unique, acknowledged))
	if acknowledged > 0 {
		e.changed()
	}
	return acknowledged, nil
}

func uniqueIDs(ids []int64) []int64 {
	seen := make(map[int64]bool, len(ids))
	unique := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	return unique
}
