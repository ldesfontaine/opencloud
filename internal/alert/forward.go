package alert

import (
	"context"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
)

var _ audit.Forwarder = (*Engine)(nil)

// ForwardAuditEvent envoie l'événement aux canaux dont la livraison a été
// réservée avec lui ; sans notifieur (une commande hors ligne), elles attendent le
// prochain démarrage. Il ne prend ni e.mu ni creatingChannel : les gestes du
// moteur journalisent en les tenant, et Enqueue ne bloque jamais. auditMu,
// lui, ne couvre que des lectures du store.
func (e *Engine) ForwardAuditEvent(ctx context.Context, event audit.Event) {
	if e.sender == nil {
		return
	}
	e.auditMu.Lock()
	defer e.auditMu.Unlock()
	deliveries, err := e.store.ListAuditDeliveries(ctx, event.ID)
	if err != nil {
		e.fail(ctx, "list audit deliveries", err)
		return
	}
	for _, delivery := range deliveries {
		if delivery.Status != DeliveryPending || e.auditClaimed[delivery.ID] {
			continue
		}
		channel, err := e.store.GetChannel(ctx, delivery.ChannelID)
		if err != nil {
			e.fail(ctx, "read audit delivery channel", err)
			continue
		}
		if !e.auditRequeued {
			e.claimAudit(delivery.ID)
		}
		e.sender.Enqueue(Job{Delivery: delivery, Audit: event, Channel: channel})
	}
}

// claimAudit note, avant que Requeue passe, une livraison déjà confiée au
// notifieur : Requeue ne la confiera pas une seconde fois.
func (e *Engine) claimAudit(id int64) {
	if e.auditClaimed == nil {
		e.auditClaimed = map[int64]bool{}
	}
	e.auditClaimed[id] = true
}

// requeueAudit rejoue au démarrage les livraisons du journal restées en
// attente : celles qu'une coupure a laissées, et celles qu'une commande sans
// notifieur (secrets rotate) a réservées. Celles que ForwardAuditEvent
// a déjà confiées depuis le démarrage sont sautées ; les siennes restent
// notées, pour qu'il les saute à son tour.
func (e *Engine) requeueAudit(ctx context.Context) error {
	pending, err := e.takePendingAudit(ctx)
	if err != nil {
		return err
	}
	now := e.now()
	for _, delivery := range pending {
		e.replayAudit(ctx, delivery, now)
	}
	if len(pending) > 0 {
		e.logger.Info("audit deliveries requeued", "count", len(pending))
	}
	return nil
}

func (e *Engine) takePendingAudit(ctx context.Context) ([]Delivery, error) {
	e.auditMu.Lock()
	defer e.auditMu.Unlock()
	pending, err := e.store.ListPendingAuditDeliveries(ctx)
	taken := map[int64]bool{}
	var mine []Delivery
	for _, delivery := range pending {
		if !e.auditClaimed[delivery.ID] {
			taken[delivery.ID] = true
			mine = append(mine, delivery)
		}
	}
	e.auditClaimed = taken
	e.auditRequeued = true
	return mine, err
}

// retryDueAudit rejoue les livraisons du journal en échec dont l'essai
// suivant est échu. Ni silence ni « alerte résolue » : le journal part entier.
func (e *Engine) retryDueAudit(ctx context.Context, now time.Time) {
	due, err := e.store.ListDueAuditDeliveries(ctx, now)
	if err != nil {
		e.fail(ctx, "list due audit deliveries", err)
		return
	}
	for _, delivery := range due {
		e.replayAudit(ctx, delivery, now)
	}
	if len(due) > 0 {
		e.logger.Info("audit deliveries retried", "count", len(due))
	}
}

// replayAudit repasse la livraison en attente avant de l'envoyer, pour que le
// balayage suivant ne la reprenne pas. Une erreur se journalise : une seule
// livraison illisible ne retient pas les autres.
func (e *Engine) replayAudit(ctx context.Context, delivery Delivery, now time.Time) {
	if err := e.tryReplayAudit(ctx, delivery, now); err != nil {
		e.fail(ctx, "replay audit delivery", err)
	}
}

func (e *Engine) tryReplayAudit(ctx context.Context, delivery Delivery, now time.Time) error {
	event, err := e.store.GetAuditEvent(ctx, delivery.AuditID)
	if err != nil {
		return fmt.Errorf("replay audit delivery %d: %w", delivery.ID, err)
	}
	channel, err := e.store.GetChannel(ctx, delivery.ChannelID)
	if err != nil {
		return fmt.Errorf("replay audit delivery %d: %w", delivery.ID, err)
	}
	delivery.Status = DeliveryPending
	delivery.NextAttemptAt = time.Time{}
	delivery.UpdatedAt = now
	if err := e.store.UpdateAuditDelivery(ctx, delivery); err != nil {
		return fmt.Errorf("replay audit delivery %d: %w", delivery.ID, err)
	}
	e.sender.Enqueue(Job{Delivery: delivery, Audit: event, Channel: channel})
	return nil
}
