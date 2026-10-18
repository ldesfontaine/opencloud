package alert

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
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
	CountAlerts(ctx context.Context) (Counts, error)
	PurgeAlerts(ctx context.Context, resolvedBefore time.Time) error

	InsertChannel(ctx context.Context, channel Channel) (int64, error)
	ListChannels(ctx context.Context) ([]Channel, error)
	GetChannel(ctx context.Context, id int64) (Channel, error)
	UpdateChannel(ctx context.Context, channel Channel) error
	DeleteChannel(ctx context.Context, id int64) error

	InsertSilence(ctx context.Context, silence Silence) (int64, error)
	ListSilences(ctx context.Context) ([]Silence, error)
	DeleteSilence(ctx context.Context, id int64) error

	// InsertDelivery réserve la livraison ; ErrDeliveryExists quand la
	// même est déjà réservée.
	InsertDelivery(ctx context.Context, delivery Delivery) (int64, error)
	UpdateDelivery(ctx context.Context, delivery Delivery) error
	ListDeliveries(ctx context.Context, alertID int64) ([]Delivery, error)
	ListPendingDeliveries(ctx context.Context) ([]Delivery, error)
}

var ErrDeliveryExists = errors.New("alert: delivery already reserved")

// Watcher reçoit « les alertes ont changé » : le direct s'y branche. nil
// est toléré.
type Watcher interface {
	AlertsChanged()
}

// Maintenance dit si un objet est sous une maintenance en cours de la page
// de statut : son alerte s'ouvre alors sans partir. Le certificat suit sa
// sonde, le volume sa machine. nil est toléré.
type Maintenance interface {
	UnderMaintenance(ctx context.Context, objectKind, objectID string) (bool, error)
}

// Sender envoie une livraison réservée ; le Notifier le fait par webhook.
// nil est toléré : les alertes se voient, rien ne part.
type Sender interface {
	Enqueue(job Job)
}

// Job est tout ce qu'il faut pour livrer : la ligne réservée, l'alerte
// telle qu'elle était, le canal.
type Job struct {
	Delivery Delivery
	Alert    Alert
	Channel  Channel
	// Held : pour un résumé, combien d'envois la limite a retenus.
	Held int
}

type Engine struct {
	store       Store
	sender      Sender
	watcher     Watcher
	maintenance Maintenance
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
	// limits tient la limite de débit de chaque canal ; sous mu.
	limits map[int64]*channelLimit
}

func New(store Store, logger *slog.Logger) *Engine {
	return &Engine{store: store, logger: logger, now: time.Now, limits: map[int64]*channelLimit{}}
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

func (e *Engine) SetMaintenance(maintenance Maintenance) {
	e.maintenance = maintenance
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
		Object: fact.Object, MachineID: fact.MachineID, Details: fact.Details,
		OpenedAt: now, UpdatedAt: now, ClearSince: fact.ClearSince,
	}
	silenced, err := e.silenced(ctx, fact, now)
	if err != nil {
		// Une panne du silence ne retient jamais une alerte.
		e.fail(ctx, "check silences", err)
	}
	alert.Silenced = silenced
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
		"object_kind", string(alert.Object.Kind), "object", alert.Object.Name, "silenced", silenced)
	e.changed()
	if !silenced {
		e.deliver(ctx, alert, EventOpened)
	}
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
	moved := existing.MachineID != fact.MachineID
	if !aggravated && !renamed && !moved && existing.Kind == fact.Kind && existing.Details.Equal(fact.Details) && existing.ClearSince.Equal(fact.ClearSince) {
		return
	}
	if renamed {
		e.logger.Warn("alert object renamed", "alert_id", existing.ID, "from", existing.Object.Name, "to", fact.Object.Name)
	}
	existing.Kind = fact.Kind
	existing.Object.Name = fact.Object.Name
	// Une tâche ou une sonde éditée a pu changer de machine.
	existing.MachineID = fact.MachineID
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
		if !existing.Silenced {
			e.deliver(ctx, existing, EventAggravated)
		}
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
	if !alert.Silenced {
		e.deliver(ctx, alert, EventResolved)
	}
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

func (e *Engine) Get(ctx context.Context, id int64) (Alert, error) {
	return e.store.GetAlert(ctx, id)
}

// List rend les alertes d'un statut, les plus graves puis les plus
// récentes d'abord.
func (e *Engine) List(ctx context.Context, status Status) ([]Alert, error) {
	return e.store.ListAlerts(ctx, Filter{Status: status, Limit: MaxListed})
}

func (e *Engine) Count(ctx context.Context) (Counts, error) {
	return e.store.CountAlerts(ctx)
}

func (e *Engine) Deliveries(ctx context.Context, alertID int64) ([]Delivery, error) {
	return e.store.ListDeliveries(ctx, alertID)
}

// silenced dit si le fait tombe sous un silence actif ou sous une
// maintenance en cours ; un volume suit sa machine.
func (e *Engine) silenced(ctx context.Context, fact Fact, now time.Time) (bool, error) {
	silences, err := e.store.ListSilences(ctx)
	if err != nil {
		return false, err
	}
	for _, silence := range silences {
		if silence.Matches(fact, now) {
			return true, nil
		}
	}
	if e.maintenance == nil {
		return false, nil
	}
	object := fact.Object
	if object.Kind == ObjectVolume {
		object = Object{Kind: ObjectMachine, ID: fact.MachineID}
	}
	return e.maintenance.UnderMaintenance(ctx, string(object.Kind), object.ID)
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
		allowed := limit.allow(now)
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
			limit.held++
			e.logger.Warn("alert delivery held by the channel rate limit", "alert_id", alert.ID, "channel_id", channel.ID, "event", string(event))
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

func (e *Engine) limitOf(channelID int64) *channelLimit {
	limit, found := e.limits[channelID]
	if !found {
		limit = &channelLimit{}
		e.limits[channelID] = limit
	}
	return limit
}

// flushSummary envoie le résumé de ce que la limite a retenu, dès que la
// fenêtre du canal a de la place : un message, quel que soit le nombre.
// Il n'est la livraison d'aucune alerte, rien n'en reste en base.
func (e *Engine) flushSummary(limit *channelLimit, channel Channel, now time.Time) {
	if limit.held == 0 || !limit.allow(now) {
		return
	}
	held := limit.held
	limit.held = 0
	e.logger.Info("alert deliveries summarized", "channel_id", channel.ID, "held", held)
	e.sender.Enqueue(Job{Delivery: Delivery{ChannelID: channel.ID, Event: EventSummary, Status: DeliveryPending, CreatedAt: now, UpdatedAt: now}, Channel: channel, Held: held})
}

// flushSummaries passe sur chaque canal qui a retenu des envois ; le
// balayage l'appelle, une tempête finie se résume sans attendre une
// nouvelle alerte.
func (e *Engine) flushSummaries(ctx context.Context) {
	if e.sender == nil {
		return
	}
	pending := false
	for _, limit := range e.limits {
		pending = pending || limit.held > 0
	}
	if !pending {
		return
	}
	channels, err := e.store.ListChannels(ctx)
	if err != nil {
		e.fail(ctx, "list channels", err)
		return
	}
	now := e.now()
	for _, channel := range channels {
		if limit, found := e.limits[channel.ID]; found && channel.Enabled {
			e.flushSummary(limit, channel, now)
		}
	}
}

// channelLimit compte les envois d'un canal sur une fenêtre glissante.
type channelLimit struct {
	sent []time.Time
	held int
}

// allow prend une place dans la fenêtre s'il en reste une.
func (l *channelLimit) allow(now time.Time) bool {
	kept := l.sent[:0]
	for _, at := range l.sent {
		if at.After(now.Add(-ChannelRateWindow)) {
			kept = append(kept, at)
		}
	}
	l.sent = kept
	if len(l.sent) >= ChannelRateLimit {
		return false
	}
	l.sent = append(l.sent, now)
	return true
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

// Sweep fait ce que le temps seul change : les alertes d'un objet disparu
// se résolvent ; celles d'un service redevenu bon aussi, passé leur délai
// de calme : RestartWindow après un redémarrage, ServiceClearDelay pour un
// arrêt ou une santé qui échouait. Puis les résumés des envois retenus.
func (e *Engine) Sweep(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	e.resolveOrphans(ctx)
	e.resolveAll(ctx, Filter{Status: StatusOpen, Kind: KindServiceRestart, ClearBefore: now.Add(-RestartWindow)})
	e.resolveAll(ctx, Filter{Status: StatusOpen, Kind: KindServiceDown, ClearBefore: now.Add(-ServiceClearDelay)})
	e.resolveAll(ctx, Filter{Status: StatusOpen, Kind: KindServiceUnhealthy, ClearBefore: now.Add(-ServiceClearDelay)})
	e.flushSummaries(ctx)
}

func (e *Engine) Purge(ctx context.Context) error {
	return e.store.PurgeAlerts(ctx, e.now().Add(-ResolvedRetention))
}

// Watch est la boucle de fond : le rattrapage des livraisons au départ, le
// balayage toutes les 30 s, la purge au départ puis une fois par jour.
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
