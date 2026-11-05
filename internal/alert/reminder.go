package alert

import (
	"context"
	"time"
)

// Le délai du rappel, en heures, tant que l'opérateur n'en a pas réglé un
// autre, et sa borne haute. Zéro coupe le rappel.
const (
	DefaultReminderHours = 12
	MaxReminderHours     = 168
)

func (e *Engine) reminderAfter() time.Duration {
	if e.reminderDelay == nil {
		return DefaultReminderHours * time.Hour
	}
	return e.reminderDelay()
}

// remindDangers renvoie une fois, comme « toujours en cours », chaque alerte
// de danger ouverte que personne n'a lue ni fait taire, passé le délai
// depuis sa dernière annonce. Appelée sous e.mu par le balayage.
func (e *Engine) remindDangers(ctx context.Context, now time.Time) {
	delay := e.reminderAfter()
	if delay <= 0 {
		return
	}
	open, err := e.store.ListAlerts(ctx, Filter{Status: StatusOpen})
	if err != nil {
		e.fail(ctx, "list open alerts", err)
		return
	}
	mutes := e.activeMutes(ctx, now)
	for _, alert := range open {
		if !isRemindable(alert, mutes, now, delay) {
			continue
		}
		alert.RemindedAt = now
		if err := e.store.UpdateAlert(ctx, alert); err != nil {
			e.fail(ctx, "remind alert", err)
			continue
		}
		e.logger.Warn("alert reminded", "alert_id", alert.ID, "kind", string(alert.Kind), "object", alert.Object.Name)
		e.changed()
		e.deliver(ctx, alert, EventReminded)
	}
}

// isRemindable : ouverte en danger, ni lue, ni tue, jamais rappelée, et sa
// dernière annonce date d'au moins le délai. Une alerte sans date d'annonce
// (écrite avant le rappel) part de son ouverture.
func isRemindable(alert Alert, mutes []Mute, now time.Time, delay time.Duration) bool {
	if !alert.IsOpen() || alert.Severity != SeverityDanger || alert.IsAcknowledged() || !alert.RemindedAt.IsZero() {
		return false
	}
	announced := alert.NotifiedAt
	if announced.IsZero() {
		announced = alert.OpenedAt
	}
	if now.Sub(announced) < delay {
		return false
	}
	return !mutedBy(mutes, alert, now)
}
