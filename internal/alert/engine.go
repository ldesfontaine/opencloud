package alert

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
)

const (
	sweepPeriod = 30 * time.Second
	purgePeriod = 24 * time.Hour
)

// Ce que le moteur attend de la base ; le paquet store le fournit.
type Store interface {
	InsertAlert(ctx context.Context, alert Alert) (int64, error)
	GetAlert(ctx context.Context, id int64) (Alert, error)
	// GetOpenAlert rend l'alerte ouverte de cette clé, ErrNotFound sinon.
	GetOpenAlert(ctx context.Context, kind Kind, object ObjectKind, objectID string) (Alert, error)
	ListAlerts(ctx context.Context, filter Filter) ([]Alert, error)
	// ListOrphanAlerts rend les alertes ouvertes dont l'objet n'existe plus.
	ListOrphanAlerts(ctx context.Context) ([]Alert, error)
	// UpdateAlert réécrit ce qui bouge : type, gravité, détails, nom, machine, dates.
	UpdateAlert(ctx context.Context, alert Alert) error
	// AcknowledgeAlerts acquitte d'un seul tenant celles des alertes nommées
	// qui sont ouvertes et pas encore acquittées ; rend combien.
	AcknowledgeAlerts(ctx context.Context, ids []int64, at time.Time) (int, error)
	PurgeAlerts(ctx context.Context, resolvedBefore time.Time) error

	InsertChannel(ctx context.Context, channel Channel) (int64, error)
	ListChannels(ctx context.Context) ([]Channel, error)
	GetChannel(ctx context.Context, id int64) (Channel, error)
	UpdateChannel(ctx context.Context, channel Channel) error
	DeleteChannel(ctx context.Context, id int64) error

	InsertMute(ctx context.Context, mute Mute) (int64, error)
	// MuteTargetName rend le nom de la cible d'une fenêtre, ErrNotFound
	// quand elle n'existe pas.
	MuteTargetName(ctx context.Context, target Target) (string, error)
	// ListActiveMutes rend les fenêtres ouvertes à cet instant.
	ListActiveMutes(ctx context.Context, at time.Time) ([]Mute, error)
	DeleteMute(ctx context.Context, id int64) error
	PurgeMutes(ctx context.Context, endedBefore time.Time) error

	// InsertDelivery réserve la livraison ; ErrDeliveryExists quand la
	// même est déjà réservée.
	InsertDelivery(ctx context.Context, delivery Delivery) (int64, error)
	UpdateDelivery(ctx context.Context, delivery Delivery) error
	ListDeliveries(ctx context.Context, alertID int64) ([]Delivery, error)
	ListPendingDeliveries(ctx context.Context) ([]Delivery, error)
	// ListDueDeliveries rend les livraisons en échec dont le prochain essai
	// est échu à cet instant.
	ListDueDeliveries(ctx context.Context, at time.Time) ([]Delivery, error)

	// Les livraisons du journal d'audit : réservées avec l'événement (voir
	// audit.Store), jouées ici comme celles des alertes.
	ListAuditDeliveries(ctx context.Context, auditID int64) ([]Delivery, error)
	ListPendingAuditDeliveries(ctx context.Context) ([]Delivery, error)
	ListDueAuditDeliveries(ctx context.Context, at time.Time) ([]Delivery, error)
	UpdateAuditDelivery(ctx context.Context, delivery Delivery) error
	GetAuditEvent(ctx context.Context, id int64) (audit.Event, error)
}

var ErrDeliveryExists = errors.New("alert: delivery already reserved")

// Watcher reçoit « les alertes ont changé » : le direct s'y branche. nil
// est toléré.
type Watcher interface {
	AlertsChanged()
}

// Sender envoie une livraison réservée ; le Notifier le fait par webhook.
// nil est toléré : les alertes se voient, rien ne part.
type Sender interface {
	Enqueue(job Job)
}

type Engine struct {
	store   Store
	sender  Sender
	watcher Watcher
	auditor *audit.Recorder
	// disks rend les seuils du disque réglés ; nil : ceux par défaut.
	disks  func() DiskThresholds
	logger *slog.Logger
	now    func() time.Time
	// mu sérialise lire-puis-écrire : deux sources ne se voient jamais un
	// état périmé de la même clé.
	mu sync.Mutex
	// creatingChannel tient compter-puis-insérer d'un seul tenant : deux
	// créations simultanées ne passent pas toutes deux sur la dernière place.
	creatingChannel sync.Mutex
	// limits tient la limite de débit de chaque canal, shares la part de
	// chaque machine sur chaque canal ; sous mu.
	limits map[int64]*channelLimit
	shares map[shareKey]*sendWindow
	// Au démarrage, Requeue et ForwardAuditEvent peuvent voir la même
	// livraison du journal en attente : celle d'un événement écrit pendant
	// que Requeue liste. auditClaimed note qui l'a prise, l'autre la saute ;
	// une fois Requeue passé, il ne garde que les livraisons du démarrage.
	// Sous auditMu, jamais sous mu.
	auditMu       sync.Mutex
	auditRequeued bool
	auditClaimed  map[int64]bool
}

func New(store Store, logger *slog.Logger) *Engine {
	return &Engine{store: store, logger: logger, now: time.Now, limits: map[int64]*channelLimit{}, shares: map[shareKey]*sendWindow{}}
}

func (e *Engine) SetClock(now func() time.Time) {
	e.now = now
}

func (e *Engine) SetSender(sender Sender) {
	e.sender = sender
}

func (e *Engine) SetWatcher(watcher Watcher) {
	e.watcher = watcher
}

// SetDiskThresholds branche la lecture des seuils réglés ; elle est
// appelée à chaque lecture de ressources.
func (e *Engine) SetDiskThresholds(disks func() DiskThresholds) {
	e.disks = disks
}

func (e *Engine) diskThresholds() DiskThresholds {
	if e.disks == nil {
		return DefaultDiskThresholds()
	}
	return e.disks()
}

func (e *Engine) changed() {
	if e.watcher != nil {
		e.watcher.AlertsChanged()
	}
}

// Open ouvre l'alerte de ce fait, ou met à jour celle qui est déjà ouverte
// sur la même clé : aggravée si la gravité monte, sinon ses détails, sans
// prévenir personne. Un fait sans objet est refusé et journalisé : c'est
// presque toujours une source qui oublie l'identifiant, et deux objets se
// partageraient une alerte.
func (e *Engine) Open(ctx context.Context, fact Fact) {
	if err := fact.Validate(); err != nil {
		e.logger.Error("alert fact refused", "kind", string(fact.Kind), "object_kind", string(fact.Object.Kind), "object_id", fact.Object.ID, "error", err)
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	existing, err := e.store.GetOpenAlert(ctx, fact.Kind, fact.Object.Kind, fact.Object.ID)
	switch {
	case errors.Is(err, ErrNotFound):
		e.open(ctx, fact)
	case err != nil:
		e.fail(ctx, "read open alert", err)
	default:
		e.update(ctx, existing, fact)
	}
}

func (e *Engine) open(ctx context.Context, fact Fact) {
	now := e.now()
	alert := Alert{
		Kind: fact.Kind, Severity: fact.Severity, Status: StatusOpen,
		Object: fact.Object, MachineID: fact.MachineID, ServiceID: fact.ServiceID, Details: fact.Details,
		OpenedAt: now, UpdatedAt: now, ClearSince: fact.ClearSince,
	}
	id, err := e.store.InsertAlert(ctx, alert)
	if err != nil {
		e.fail(ctx, "insert alert", err)
		return
	}
	// Relue pour porter ce que la base seule sait : le nom de sa machine.
	alert, err = e.store.GetAlert(ctx, id)
	if err != nil {
		e.fail(ctx, "read alert", err)
		return
	}
	e.logger.Warn("alert opened", "alert_id", id, "kind", string(alert.Kind), "severity", string(alert.Severity),
		"object_kind", string(alert.Object.Kind), "object", alert.Object.Name)
	e.changed()
	e.deliver(ctx, alert, EventOpened)
}

// update ne baisse jamais la gravité, sauf celle d'un plantage, qui suit
// l'état : arrêté, relancé, en boucle. Une alerte s'aggrave quand sa
// gravité monte ; un plantage, seulement quand il devient une boucle,
// sinon chaque arrêt d'une boucle naissante préviendrait. Au plus une
// aggravation par canal, l'index des livraisons y veille.
func (e *Engine) update(ctx context.Context, existing Alert, fact Fact) {
	aggravated := rank(fact.Severity) > rank(existing.Severity)
	if isCrash(fact.Kind) {
		aggravated = isLoop(fact.Kind, fact.Details) && !existing.IsLoop()
	}
	renamed := existing.Object.Name != fact.Object.Name
	// Une tâche ou une sonde éditée a pu changer de machine ; un conteneur,
	// être déclaré ou rattaché à un autre service.
	moved := existing.MachineID != fact.MachineID || existing.ServiceID != fact.ServiceID
	if !aggravated && !renamed && !moved && existing.Kind == fact.Kind && existing.Details.Equal(fact.Details) && existing.ClearSince.Equal(fact.ClearSince) {
		return
	}
	if renamed {
		e.logger.Warn("alert object renamed", "alert_id", existing.ID, "from", existing.Object.Name, "to", fact.Object.Name)
	}
	existing.Kind = fact.Kind
	existing.Object.Name = fact.Object.Name
	existing.MachineID = fact.MachineID
	existing.ServiceID = fact.ServiceID
	existing.Details = fact.Details
	existing.ClearSince = fact.ClearSince
	existing.UpdatedAt = e.now()
	if isCrash(fact.Kind) || rank(fact.Severity) > rank(existing.Severity) {
		existing.Severity = fact.Severity
	}
	if aggravated {
		// Ce que l'opérateur avait acquitté n'est plus la même chose.
		existing.AcknowledgedAt = time.Time{}
	}
	if err := e.store.UpdateAlert(ctx, existing); err != nil {
		e.fail(ctx, "update alert", err)
		return
	}
	e.changed()
	if aggravated {
		e.logger.Warn("alert aggravated", "alert_id", existing.ID, "kind", string(existing.Kind), "object", existing.Object.Name)
		e.deliver(ctx, existing, EventAggravated)
	}
}

// Resolve ferme l'alerte ouverte de cette clé, s'il y en a une, et le dit
// aux canaux qui veulent l'entendre.
func (e *Engine) Resolve(ctx context.Context, kind Kind, object Object) {
	e.mu.Lock()
	defer e.mu.Unlock()
	existing, err := e.store.GetOpenAlert(ctx, kind, object.Kind, object.ID)
	if errors.Is(err, ErrNotFound) {
		return
	}
	if err != nil {
		e.fail(ctx, "read open alert", err)
		return
	}
	e.resolve(ctx, existing)
}

func (e *Engine) resolve(ctx context.Context, alert Alert) {
	now := e.now()
	alert.Status = StatusResolved
	alert.ResolvedAt = now
	alert.UpdatedAt = now
	if err := e.store.UpdateAlert(ctx, alert); err != nil {
		e.fail(ctx, "resolve alert", err)
		return
	}
	e.logger.Info("alert resolved", "alert_id", alert.ID, "kind", string(alert.Kind), "object", alert.Object.Name)
	e.changed()
	e.deliver(ctx, alert, EventResolved)
}

// Gone résout tout ce qui est ouvert sur un objet qui n'existe plus, ou
// qui ne compte plus : supprimé, archivé, en pause. Une machine emporte ce
// qui vit sur elle ; retirée de la base, ses clés étrangères sont déjà à
// NULL, et c'est le balayage des orphelines qui les trouve.
func (e *Engine) Gone(ctx context.Context, object Object) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resolveAll(ctx, Filter{Status: StatusOpen, ObjectKind: object.Kind, ObjectID: object.ID})
	if object.Kind != ObjectMachine {
		return
	}
	e.resolveAll(ctx, Filter{Status: StatusOpen, MachineID: object.ID})
	e.resolveOrphans(ctx)
}

// removed : l'objet est retiré de la base. Ce qui était ouvert sur lui se
// résout ; ses fenêtres pour taire sont parties avec lui, par la clé
// étrangère, et la page des silences se relit.
func (e *Engine) removed(ctx context.Context, object Object) {
	e.Gone(ctx, object)
	e.changed()
}

func (e *Engine) resolveOrphans(ctx context.Context) {
	orphans, err := e.store.ListOrphanAlerts(ctx)
	if err != nil {
		e.fail(ctx, "list orphan alerts", err)
		return
	}
	for _, alert := range orphans {
		e.resolve(ctx, alert)
	}
}

func (e *Engine) resolveAll(ctx context.Context, filter Filter) {
	open, err := e.store.ListAlerts(ctx, filter)
	if err != nil {
		e.fail(ctx, "list open alerts", err)
		return
	}
	for _, alert := range open {
		e.resolve(ctx, alert)
	}
}

// List rend les alertes d'un statut, les plus graves puis les plus
// récentes d'abord, chacune avec ce qui la tait en ce moment.
func (e *Engine) List(ctx context.Context, status Status) ([]Alert, error) {
	alerts, err := e.store.ListAlerts(ctx, Filter{Status: status, Limit: MaxListed})
	if err != nil {
		return nil, err
	}
	now := e.now()
	mutes, err := e.store.ListActiveMutes(ctx, now)
	if err != nil {
		return nil, err
	}
	for index := range alerts {
		alerts[index].Muted = mutedBy(mutes, alerts[index], now)
	}
	return alerts, nil
}

// Get rend une alerte avec ce qui la tait en ce moment.
func (e *Engine) Get(ctx context.Context, id int64) (Alert, error) {
	found, err := e.store.GetAlert(ctx, id)
	if err != nil {
		return Alert{}, err
	}
	now := e.now()
	mutes, err := e.store.ListActiveMutes(ctx, now)
	if err != nil {
		return Alert{}, err
	}
	found.Muted = mutedBy(mutes, found, now)
	return found, nil
}

// activeMutes rend les fenêtres ouvertes. Une panne de lecture ne retient
// jamais une alerte : sans fenêtre lue, tout part.
func (e *Engine) activeMutes(ctx context.Context, now time.Time) []Mute {
	mutes, err := e.store.ListActiveMutes(ctx, now)
	if err != nil {
		e.fail(ctx, "list active mutes", err)
		return nil
	}
	return mutes
}

// Sweep fait ce que le temps seul change : les alertes d'un objet disparu
// se résolvent ; celles d'un service redevenu bon aussi, passé leur délai
// de calme : RestartWindow après un redémarrage, ServiceClearDelay pour un
// arrêt ou une santé qui échouait. Puis les livraisons en échec dont
// l'essai suivant est échu, et les résumés des envois retenus.
func (e *Engine) Sweep(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	e.resolveOrphans(ctx)
	e.resolveAll(ctx, Filter{Status: StatusOpen, Kind: KindServiceRestart, ClearBefore: now.Add(-RestartWindow)})
	e.resolveAll(ctx, Filter{Status: StatusOpen, Kind: KindServiceDown, ClearBefore: now.Add(-ServiceClearDelay)})
	e.resolveAll(ctx, Filter{Status: StatusOpen, Kind: KindServiceUnhealthy, ClearBefore: now.Add(-ServiceClearDelay)})
	e.retryDue(ctx, now)
	e.flushSummaries(ctx)
	e.forgetIdleShares(now)
}

// Purge efface les alertes résolues et les fenêtres finies au-delà de leur
// rétention.
func (e *Engine) Purge(ctx context.Context) error {
	now := e.now()
	if err := e.store.PurgeAlerts(ctx, now.Add(-ResolvedRetention)); err != nil {
		return err
	}
	if err := e.store.PurgeMutes(ctx, now.Add(-MuteRetention)); err != nil {
		return err
	}
	// Le journal d'audit n'a pas de boucle à lui : il se purge ici, au même
	// rythme, à l'âge d'audit.Retention.
	return e.auditor.Purge(ctx)
}

// Watch est la boucle de fond : le rattrapage des livraisons au départ, le
// balayage toutes les 30 s (essais suivants compris), la purge au départ
// puis une fois par jour.
func (e *Engine) Watch(ctx context.Context) {
	if err := e.Requeue(ctx); err != nil {
		e.logger.Error("requeue alert deliveries", "error", err)
	}
	if err := e.Purge(ctx); err != nil {
		e.logger.Warn("purge alerts", "error", err)
	}
	e.Sweep(ctx)
	sweep := time.NewTicker(sweepPeriod)
	defer sweep.Stop()
	purge := time.NewTicker(purgePeriod)
	defer purge.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sweep.C:
			e.Sweep(ctx)
		case <-purge.C:
			if err := e.Purge(ctx); err != nil {
				e.logger.Warn("purge alerts", "error", err)
			}
		}
	}
}

func (e *Engine) fail(ctx context.Context, what string, err error) {
	if ctx.Err() == nil {
		e.logger.Error(what, "error", err)
	}
}
