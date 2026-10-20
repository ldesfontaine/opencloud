package alert

import (
	"context"
	"errors"
	"fmt"
)

// Job est tout ce qu'il faut pour livrer : la ligne réservée, l'alerte
// telle qu'elle était, le canal.
type Job struct {
	Delivery Delivery
	Alert    Alert
	Channel  Channel
	// Held : pour un résumé, ce que la limite a retenu.
	Held Held
}

// deliver réserve une livraison par canal qui veut cet événement, puis la
// confie au notifieur. Réserver d'abord : une coupure entre les deux laisse
// une ligne en attente que le démarrage rejoue, jamais un doublon.
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
	for _, channel := range channels {
		if !channel.Wants(alert, event) && !(told[channel.ID] && channel.Enabled && channel.NotifyResolve) {
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

// toldChannels rend, pour une résolution, les canaux à qui l'alerte a été
// annoncée : ils apprennent sa fin même si sa gravité est redescendue sous
// leur seuil, un plantage relancé n'étant plus qu'une attention.
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
		if delivery.Status != DeliveryHeld {
			told[delivery.ChannelID] = true
		}
	}
	return told
}

// Requeue rejoue au démarrage les livraisons restées en attente : une
// fois chacune, avec l'alerte et le canal tels qu'ils sont maintenant.
func (e *Engine) Requeue(ctx context.Context) error {
	if e.sender == nil {
		return nil
	}
	pending, err := e.store.ListPendingDeliveries(ctx)
	if err != nil {
		return err
	}
	for _, delivery := range pending {
		alert, err := e.store.GetAlert(ctx, delivery.AlertID)
		if err != nil {
			return fmt.Errorf("requeue delivery %d: %w", delivery.ID, err)
		}
		channel, err := e.store.GetChannel(ctx, delivery.ChannelID)
		if err != nil {
			return fmt.Errorf("requeue delivery %d: %w", delivery.ID, err)
		}
		e.sender.Enqueue(Job{Delivery: delivery, Alert: alert, Channel: channel})
	}
	if len(pending) > 0 {
		e.logger.Info("alert deliveries requeued", "count", len(pending))
	}
	return nil
}
