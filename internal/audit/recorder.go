package audit

import (
	"context"
	"log/slog"
	"maps"
	"time"
)

const (
	// Retention est l'âge au-delà duquel un événement part, à la purge. Un
	// an : de quoi reconstituer une saison d'exploitation, sans faire du
	// journal la plus grosse table de la base.
	Retention = 365 * 24 * time.Hour
	// Le geste est fait : l'écriture de sa ligne ne dépend plus du client,
	// qui peut raccrocher, mais reste bornée.
	recordTimeout = 5 * time.Second
)

// Store est ce que le journal attend de la base ; le paquet store le
// fournit. Rien n'y modifie une ligne : on ajoute, on purge par l'âge.
type Store interface {
	InsertAuditEvent(ctx context.Context, event Event) error
	PurgeAuditEvents(ctx context.Context, before time.Time) (int64, error)
}

// Recorder écrit le journal. Un *Recorder nil ne fait rien : un composant
// sans journal branché (un test, une commande) continue sans lui.
type Recorder struct {
	store  Store
	logger *slog.Logger
	now    func() time.Time
}

func New(store Store, logger *slog.Logger) *Recorder {
	return &Recorder{store: store, logger: logger, now: time.Now}
}

func (r *Recorder) SetClock(now func() time.Time) {
	r.now = now
}

// Record écrit l'événement d'un geste qui vient de réussir, avec l'acteur
// et le passage d'apply du contexte. Un échec d'écriture ne défait pas le
// geste : il est journalisé en erreur, et le geste reste fait.
func (r *Recorder) Record(ctx context.Context, event Event) {
	if r == nil {
		return
	}
	event.At = r.now()
	event.Actor = ActorFrom(ctx)
	event.ApplyID = applyFrom(ctx)
	event.ObjectKind = event.Action.ObjectKind()
	event.Details = r.cleanDetails(event)
	if event.Actor.Kind == ActorUnknown {
		r.logger.Warn("audit event without actor", "action", string(event.Action), "object_id", event.ObjectID)
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	if err := r.store.InsertAuditEvent(writeCtx, event); err != nil {
		r.logger.Error("record audit event", "action", string(event.Action), "object_id", event.ObjectID, "error", err)
	}
}

// cleanDetails retire un détail vide, qui ne dit rien, et un détail dont le
// nom dit un secret. Le second est un filet : un composant qui en passe un
// est un défaut, dit en erreur.
func (r *Recorder) cleanDetails(event Event) Details {
	kept := maps.Clone(event.Details)
	for name, value := range kept {
		if value == "" {
			delete(kept, name)
			continue
		}
		if isForbiddenDetail(name) {
			r.logger.Error("audit detail dropped: its name says a secret", "action", string(event.Action), "detail", name)
			delete(kept, name)
		}
	}
	return kept
}

// Purge retire ce qui a dépassé Retention.
func (r *Recorder) Purge(ctx context.Context) error {
	if r == nil {
		return nil
	}
	_, err := r.store.PurgeAuditEvents(ctx, r.now().Add(-Retention))
	return err
}
