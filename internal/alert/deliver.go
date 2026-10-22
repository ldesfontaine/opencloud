package alert

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
)

// Job est tout ce qu'il faut pour livrer : la ligne réservée, l'alerte
// telle qu'elle était, le canal.
type Job struct {
	Delivery Delivery
	Alert    Alert
	Channel  Channel
	// Held : pour un résumé, ce que la limite a retenu.
	Held Held
	// Audit : pour une livraison du journal, l'événement.
	Audit audit.Event
}

// deliver réserve une livraison par canal qui veut cet événement, puis la
// confie au notifieur. Réserver d'abord : une coupure entre les deux laisse
// une ligne en attente que le démarrage rejoue, jamais un doublon. Une
// alerte qu'une fenêtre tait au moment de l'envoi est notée « tue » pour
// chaque canal, sans rien prendre à sa limite de débit.
func (e *Engine) deliver(ctx context.Context, alert Alert, event Event) {
	if e.sender == nil {
		return
	}
	channels, err := e.store.ListChannels(ctx)
	if err != nil {
		e.fail(ctx, "list channels", err)
		return
	}
	told := e.toldChannels(ctx, alert, event)
	now := e.now()
	muted := mutedBy(e.activeMutes(ctx, now), alert, now)
	if muted {
		e.logger.Info("alert delivery muted", "alert_id", alert.ID, "kind", string(alert.Kind), "event", string(event))
	}
	for _, channel := range channels {
		if !channel.Wants(alert, event) && !(told[channel.ID] && channel.Enabled && channel.NotifyResolve) {
			continue
		}
		if muted {
			e.reserveMuted(ctx, Delivery{AlertID: alert.ID, ChannelID: channel.ID, Event: event, Status: DeliveryMuted, CreatedAt: now, UpdatedAt: now})
			continue
		}
		limit := e.limitOf(channel.ID)
		e.flushSummary(limit, channel, now)
		delivery := Delivery{AlertID: alert.ID, ChannelID: channel.ID, Event: event, Status: DeliveryPending, CreatedAt: now, UpdatedAt: now}
		allowed := e.allowSend(limit, channel.ID, alert.MachineID, now)
		if !allowed {
			delivery.Status = DeliveryHeld
		}
		id, err := e.store.InsertDelivery(ctx, delivery)
		if errors.Is(err, ErrDeliveryExists) {
			continue
		}
		if err != nil {
			e.fail(ctx, "reserve delivery", err)
			continue
		}
		if !allowed {
			limit.held.note(alert)
			e.logger.Warn("alert delivery held by the channel rate limit", "alert_id", alert.ID, "channel_id", channel.ID, "machine_id", alert.MachineID, "event", string(event))
			continue
		}
		delivery.ID = id
		e.sender.Enqueue(Job{Delivery: delivery, Alert: alert, Channel: channel})
	}
}

// reserveMuted note la livraison qu'une fenêtre a tue : la fiche de
// l'alerte dit pourquoi rien n'est parti.
func (e *Engine) reserveMuted(ctx context.Context, delivery Delivery) {
	if _, err := e.store.InsertDelivery(ctx, delivery); err != nil && !errors.Is(err, ErrDeliveryExists) {
		e.fail(ctx, "reserve muted delivery", err)
	}
}

// toldChannels rend, pour une résolution, les canaux à qui l'alerte a été
// annoncée : ils apprennent sa fin même si sa gravité est redescendue sous
// leur seuil, un plantage relancé n'étant plus qu'une attention. Une
// annonce retenue ou tue n'a rien dit.
func (e *Engine) toldChannels(ctx context.Context, alert Alert, event Event) map[int64]bool {
	told := map[int64]bool{}
	if event != EventResolved {
		return told
	}
	deliveries, err := e.store.ListDeliveries(ctx, alert.ID)
	if err != nil {
		e.fail(ctx, "list deliveries", err)
		return told
	}
	for _, delivery := range deliveries {
		if delivery.Status != DeliveryHeld && delivery.Status != DeliveryMuted {
			told[delivery.ChannelID] = true
		}
	}
	return told
}

// Requeue rejoue au démarrage les livraisons restées en attente, avec
// l'alerte et le canal tels qu'ils sont maintenant.
func (e *Engine) Requeue(ctx context.Context) error {
	if e.sender == nil {
		return nil
	}
	pending, err := e.store.ListPendingDeliveries(ctx)
	if err != nil {
		return err
	}
	now := e.now()
	mutes := e.activeMutes(ctx, now)
	for _, delivery := range pending {
		if err := e.replay(ctx, delivery, mutes, now); err != nil {
			return err
		}
	}
	if len(pending) > 0 {
		e.logger.Info("alert deliveries requeued", "count", len(pending))
	}
	return e.requeueAudit(ctx)
}

// retryDue rejoue les livraisons en échec dont l'essai suivant est échu :
// un canal en panne une heure reçoit encore l'alerte quand il revient.
func (e *Engine) retryDue(ctx context.Context, now time.Time) {
	if e.sender == nil {
		return
	}
	e.retryDueAudit(ctx, now)
	due, err := e.store.ListDueDeliveries(ctx, now)
	if err != nil {
		e.fail(ctx, "list due deliveries", err)
		return
	}
	if len(due) == 0 {
		return
	}
	mutes := e.activeMutes(ctx, now)
	for _, delivery := range due {
		if err := e.replay(ctx, delivery, mutes, now); err != nil {
			e.fail(ctx, "retry delivery", err)
		}
	}
	e.logger.Info("alert deliveries retried", "count", len(due))
}

// replay renvoie une livraison réservée. Une fenêtre qui tait l'alerte
// maintenant la note « tue » ; une ouverture ou une aggravation encore en
// échec quand l'alerte est résolue est abandonnée : elle arriverait après
// la résolution. Sinon la ligne repasse en attente avant de partir, et le
// balayage suivant ne la reprend pas.
func (e *Engine) replay(ctx context.Context, delivery Delivery, mutes []Mute, now time.Time) error {
	alert, err := e.store.GetAlert(ctx, delivery.AlertID)
	if err != nil {
		return fmt.Errorf("replay delivery %d: %w", delivery.ID, err)
	}
	channel, err := e.store.GetChannel(ctx, delivery.ChannelID)
	if err != nil {
		return fmt.Errorf("replay delivery %d: %w", delivery.ID, err)
	}
	delivery.NextAttemptAt = time.Time{}
	delivery.UpdatedAt = now
	switch {
	case mutedBy(mutes, alert, now):
		delivery.Status = DeliveryMuted
		return e.store.UpdateDelivery(ctx, delivery)
	case delivery.Status == DeliveryFailed && !alert.IsOpen() && delivery.Event != EventResolved:
		return e.store.UpdateDelivery(ctx, delivery)
	}
	delivery.Status = DeliveryPending
	if err := e.store.UpdateDelivery(ctx, delivery); err != nil {
		return fmt.Errorf("replay delivery %d: %w", delivery.ID, err)
	}
	e.sender.Enqueue(Job{Delivery: delivery, Alert: alert, Channel: channel})
	return nil
}
