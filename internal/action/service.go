package action

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/ident"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/release"
)

const watchPeriod = 30 * time.Second

// Store est ce que le composant attend de la base ; le paquet store le
// fournit.
type Store interface {
	// InsertRun prend le rang suivant de l'agent et écrit le run, dans une
	// seule transaction ; il rend le run avec son rang. machine.ErrNoAgent
	// si l'agent n'est plus actif.
	InsertRun(ctx context.Context, run Run) (Run, error)
	// SetRunState pose un état que le serveur décide (sent, failed) ;
	// finishedAt zéro laisse le run ouvert.
	SetRunState(ctx context.Context, id string, state State, reason string, finishedAt time.Time) error
	// ApplyRunResult fait avancer un run de cette machine selon ce que son
	// agent rapporte, sortie comprise ; changed est faux pour un run inconnu,
	// d'une autre machine ou déjà fini.
	// Le run rendu porte son genre, sa machine et son nouvel état.
	ApplyRunResult(ctx context.Context, machineID string, result Result, at time.Time) (run Run, changed bool, err error)
	// ExpireRuns pose expired sur ce qui attend encore après not_after ; il
	// rend les machines touchées.
	ExpireRuns(ctx context.Context, now time.Time) ([]string, error)
	// TimeoutRuns pose failed, timeout sur ce qu'un agent a accepté avant
	// acceptedBefore sans le finir ; il rend les machines touchées.
	TimeoutRuns(ctx context.Context, acceptedBefore, now time.Time) ([]string, error)
	PurgeRuns(ctx context.Context, before time.Time) error
	// CompleteAgentUpdates pose succeeded sur les mises à jour en attente de
	// cet agent qui visaient cette version ; il rend les runs touchés.
	CompleteAgentUpdates(ctx context.Context, agentID, version string, at time.Time) ([]Run, error)
}

// Machines est ce que le composant lit des machines : l'agent courant et
// s'il tient son flux ; et ce qu'il note quand un agent a pris la clé neuve
// du serveur.
type Machines interface {
	Get(ctx context.Context, id string) (machine.Status, error)
	NoteServerKey(ctx context.Context, agentID, fingerprint string) error
}

// Commander pousse une commande à l'agent d'une machine par son flux : le
// serveur, qui tient les flux.
type Commander interface {
	Command(machineID string, command agentwire.Command) error
}

// Watcher reçoit chaque changement visible d'un run ; le direct s'y
// branche, nil est toléré.
type Watcher interface {
	ActionsChanged(machineID string)
}

// Result est ce qu'un agent rapporte d'un run, déjà jugé lisible.
type Result struct {
	RunID  string
	State  State
	Reason string
	Output []LogLine
}

type Service struct {
	store     Store
	machines  Machines
	commander Commander
	watcher   Watcher
	key       *Key
	outbox    *Outbox
	auditor   *audit.Recorder
	logger    *slog.Logger
	now       func() time.Time

	// Les versions importées et celle du serveur, que reçoivent ses agents ;
	// nil sans bibliothèque (voir SetReleases).
	releases      Releases
	serverVersion release.Version
}

func New(store Store, machines Machines, logger *slog.Logger) *Service {
	return &Service{store: store, machines: machines, logger: logger, now: time.Now}
}

// SetKey branche la clé qui signe les ordres ; sans elle, rien ne part.
func (s *Service) SetKey(key *Key) {
	s.key = key
}

// SetOutbox branche les colis que les agents téléchargent ; sans lui,
// OpenParcel ne trouve rien.
func (s *Service) SetOutbox(outbox *Outbox) {
	s.outbox = outbox
}

func (s *Service) SetCommander(commander Commander) {
	s.commander = commander
}

func (s *Service) SetWatcher(watcher Watcher) {
	s.watcher = watcher
}

func (s *Service) SetAuditor(auditor *audit.Recorder) {
	s.auditor = auditor
}

func (s *Service) SetClock(now func() time.Time) {
	s.now = now
}

func (s *Service) changed(machineID string) {
	if s.watcher != nil {
		s.watcher.ActionsChanged(machineID)
	}
}

// ServerKey rend la clé publique et son empreinte, ce que l'opérateur
// compare à ce que chaque agent a épinglé.
func (s *Service) ServerKey() (public []byte, fingerprint string, err error) {
	if s.key == nil {
		return nil, "", ErrNoServerKey
	}
	return s.key.PublicKey(), s.key.Fingerprint(), nil
}

// PreviousServerKey rend l'empreinte de la clé précédente, vide hors
// rotation.
func (s *Service) PreviousServerKey() string {
	if s.key == nil {
		return ""
	}
	return s.key.PreviousFingerprint()
}

// Rekey livre la clé courante à l'agent d'une machine resté sur la
// précédente, pendant une rotation : un ordre agent.server_key, signé par
// la précédente, qu'il vérifie avec ce qu'il a épinglé. Sans rotation, ou
// pour un agent déjà à jour, rien ne part. serve l'appelle à l'ouverture de
// chaque flux.
func (s *Service) Rekey(ctx context.Context, machineID string) error {
	if s.key == nil || s.key.PreviousFingerprint() == "" {
		return nil
	}
	target, err := s.machines.Get(ctx, machineID)
	if err != nil {
		return err
	}
	if target.Agent.ServerKeyFingerprint != s.key.PreviousFingerprint() {
		return nil
	}
	params, err := json.Marshal(agentwire.ServerKeyParams{PublicKey: base64.StdEncoding.EncodeToString(s.key.PublicKey())})
	if err != nil {
		return err
	}
	_, err = s.Send(ctx, machineID, agentwire.KindServerKey, params)
	return err
}

// Ping envoie l'ordre agent.ping : il ne change rien sur la machine et
// prouve le chemin d'un ordre de bout en bout.
func (s *Service) Ping(ctx context.Context, machineID string) (Run, error) {
	return s.Send(ctx, machineID, agentwire.KindAgentPing, nil)
}

// Send écrit le run, le journalise, le signe, le marque envoyé, puis le
// pousse par le flux de la machine : une trace existe avant que l'ordre ne
// parte. Un ordre qui ne part pas finit failed, undelivered. C'est le geste
// commun des ordres ; l'API n'en offre que Ping.
//
// Un ordre dont le serveur sait déjà que le droit manque (l'agent a rapporté
// ses droits, celui-là n'y est pas) est refusé tout de suite, sans run : il
// serait refusé par l'agent. Tant que l'agent n'a rien rapporté, c'est lui
// qui juge.
func (s *Service) Send(ctx context.Context, machineID string, kind agentwire.Kind, params json.RawMessage) (Run, error) {
	return s.send(ctx, ServiceOrder{MachineID: machineID, Kind: kind, Params: params})
}

// SendWithSecret fait comme Send et signe en plus un secret avec l'ordre : il
// part à l'agent, qui le passe à l'exécuteur du genre, et c'est tout. Il
// n'entre ni dans le run, ni dans le journal d'audit, ni dans un journal
// slog. label dit, dans l'audit, qu'un secret est parti (« env: sent »).
func (s *Service) SendWithSecret(ctx context.Context, machineID string, kind agentwire.Kind, params json.RawMessage, secret json.RawMessage, label string) (Run, error) {
	return s.send(ctx, ServiceOrder{MachineID: machineID, Kind: kind, Params: params, Secret: secret, SecretLabel: label})
}

// ServiceOrder est un ordre à envoyer : la machine, le genre, ses
// paramètres, et ce qui s'y ajoute selon le geste. ServiceID rattache le run
// au service déclaré qu'il vise (le déploiement) ; Secret et SecretLabel
// sont ceux de SendWithSecret.
type ServiceOrder struct {
	MachineID   string
	ServiceID   string
	Kind        agentwire.Kind
	Params      json.RawMessage
	Secret      json.RawMessage
	SecretLabel string
}

// SendForService fait comme SendWithSecret pour un ordre qui vise un service
// déclaré : le run garde le service, l'audit aussi.
func (s *Service) SendForService(ctx context.Context, order ServiceOrder) (Run, error) {
	return s.send(ctx, order)
}

func (s *Service) send(ctx context.Context, order ServiceOrder) (Run, error) {
	machineID, kind, params, secret := order.MachineID, order.Kind, order.Params, order.Secret
	if s.key == nil {
		return Run{}, ErrNoServerKey
	}
	right, ok := RightOf(kind)
	if !ok || !kind.IsOrder() {
		return Run{}, fmt.Errorf("%q: %w", kind, ErrUnknownKind)
	}
	if len(secret) > agentwire.MaxSecretBytes {
		return Run{}, ErrSecretTooLarge
	}
	target, err := s.machines.Get(ctx, machineID)
	if err != nil {
		return Run{}, err
	}
	if !target.Agent.IsActive() {
		return Run{}, machine.ErrNoAgent
	}
	if !target.Agent.ReportedAt.IsZero() && !machine.HasCapability(target.Agent.Capabilities, right) {
		return Run{}, ErrRightMissing
	}
	if !target.Online {
		return Run{}, ErrMachineOffline
	}
	run, err := s.newRun(ctx, target, kind, params)
	if err != nil {
		return Run{}, err
	}
	run.ServiceID = order.ServiceID
	run, err = s.store.InsertRun(ctx, run)
	if err != nil {
		return Run{}, fmt.Errorf("insert run: %w", err)
	}
	s.auditor.Record(ctx, orderEvent(run, order.SecretLabel))
	run, err = s.deliver(ctx, run, target.Agent.ServerKeyFingerprint, secret)
	s.changed(machineID)
	return run, err
}

func (s *Service) newRun(ctx context.Context, target machine.Status, kind agentwire.Kind, params json.RawMessage) (Run, error) {
	id, err := ident.New()
	if err != nil {
		return Run{}, fmt.Errorf("generate run id: %w", err)
	}
	now := s.now()
	return Run{
		ID: id, Kind: kind, Params: params,
		MachineID: target.ID, MachineName: target.Name, AgentID: target.Agent.ID,
		State: StateQueued, NotAfter: now.Add(OrderLifetime),
		RequestedBy: requesterOf(audit.ActorFrom(ctx)), RequestedAt: now,
	}, nil
}

// deliver signe le run écrit avec la clé que l'agent a épinglée, le marque
// envoyé, puis le pousse. Marqué avant de partir : la réponse de l'agent
// trouve toujours un run envoyé.
func (s *Service) deliver(ctx context.Context, run Run, pinned string, secret json.RawMessage) (Run, error) {
	order := run.order()
	order.Secret = secret
	signed, err := s.key.Sign(order, pinned)
	if err != nil {
		return s.failUndelivered(ctx, run, err)
	}
	if err := s.store.SetRunState(ctx, run.ID, StateSent, "", time.Time{}); err != nil {
		return run, fmt.Errorf("mark run sent: %w", err)
	}
	run.State = StateSent
	if s.commander == nil {
		return s.failUndelivered(ctx, run, ErrMachineOffline)
	}
	if err := s.commander.Command(run.MachineID, agentwire.OrderCommand(run.Kind, signed)); err != nil {
		return s.failUndelivered(ctx, run, err)
	}
	s.logger.Info("order sent", "run_id", run.ID, "kind", string(run.Kind), "machine_id", run.MachineID, "seq", run.Seq)
	return run, nil
}

func (s *Service) failUndelivered(ctx context.Context, run Run, cause error) (Run, error) {
	if err := s.undelivered(ctx, run, cause); err != nil {
		return run, err
	}
	run.State, run.Reason, run.FinishedAt = StateFailed, ReasonUndelivered, s.now()
	return run, nil
}

func (s *Service) undelivered(ctx context.Context, run Run, cause error) error {
	s.logger.Warn("order undelivered", "run_id", run.ID, "machine_id", run.MachineID, "error", cause)
	if err := s.store.SetRunState(ctx, run.ID, StateFailed, ReasonUndelivered, s.now()); err != nil {
		return fmt.Errorf("mark run undelivered: %w", err)
	}
	return nil
}

// requesterOf recopie l'acteur : son genre, et son nom s'il en a un.
func requesterOf(actor audit.Actor) string {
	if actor.Name == "" {
		return string(actor.Kind)
	}
	return string(actor.Kind) + ":" + actor.Name
}

// orderEvent dit qu'un ordre est écrit et part. Un secret signé avec lui se
// dit par son nom (« env: sent »), jamais par sa valeur.
func orderEvent(run Run, secretLabel string) audit.Event {
	details := audit.Details{"kind": string(run.Kind), "machine_id": run.MachineID, "agent_id": run.AgentID}
	if run.ServiceID != "" {
		details["service_id"] = run.ServiceID
	}
	if secretLabel != "" {
		details[secretLabel] = "sent"
	}
	return audit.Event{Action: audit.ActionOrder, ObjectID: run.ID, ObjectName: run.MachineName, Details: details}
}

// refusedEvent dit qu'un agent a refusé un ordre : un droit qui manque, ou
// une signature, un rang, une échéance qui ne tiennent pas. L'acteur est
// l'agent, que le signal porte.
func refusedEvent(run Run) audit.Event {
	return audit.Event{
		Action: audit.ActionRefused, ObjectID: run.ID, ObjectName: run.MachineName,
		Details: audit.Details{"kind": string(run.Kind), "machine_id": run.MachineID, "agent_id": run.AgentID, "reason": run.Reason},
	}
}

// Watch est la boucle de fond : ce qui attend après not_after expire, un
// ordre accepté depuis plus de MaxRunDuration échoue, les colis échus
// partent, ce qui a passé Retention part.
func (s *Service) Watch(ctx context.Context) {
	ticker := time.NewTicker(watchPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tidy(ctx)
		}
	}
}

func (s *Service) tidy(ctx context.Context) {
	if err := s.Expire(ctx); err != nil && ctx.Err() == nil {
		s.logger.Error("expire runs", "error", err)
	}
	if err := s.Timeout(ctx); err != nil && ctx.Err() == nil {
		s.logger.Error("time out runs", "error", err)
	}
	s.outbox.Sweep()
	if err := s.store.PurgeRuns(ctx, s.now().Add(-Retention)); err != nil && ctx.Err() == nil {
		s.logger.Error("purge runs", "error", err)
	}
}

// Expire pose expired sur les runs qui attendent encore leur agent après
// not_after. Un run accepté n'expire pas : l'agent y travaille (Timeout le
// borne).
func (s *Service) Expire(ctx context.Context) error {
	machines, err := s.store.ExpireRuns(ctx, s.now())
	if err != nil {
		return err
	}
	for _, machineID := range machines {
		s.changed(machineID)
	}
	return nil
}

// Timeout pose failed, timeout sur les ordres que l'agent a acceptés il y a
// plus de MaxRunDuration sans les finir : un ordre long ne reste pas
// « accepté » pour toujours. Il ne rend pas la main à l'agent, qui a pu
// mourir en route.
func (s *Service) Timeout(ctx context.Context) error {
	now := s.now()
	machines, err := s.store.TimeoutRuns(ctx, now.Add(-MaxRunDuration), now)
	if err != nil {
		return err
	}
	for _, machineID := range machines {
		s.changed(machineID)
	}
	return nil
}
