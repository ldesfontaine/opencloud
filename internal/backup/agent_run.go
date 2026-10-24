package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/release"
)

// runWaitLimit borne l'attente d'un ordre : le serveur le dit fini de
// lui-même, expiré après OrderLifetime ou échoué après MaxRunDuration, mais
// une boucle de fond arrêtée ne doit pas retenir le passage pour toujours.
const runWaitLimit = action.OrderLifetime + action.MaxRunDuration + 5*time.Minute

// reasonUnknown remplace toute raison que le serveur ne reconnaît pas : ce
// qu'un agent écrit dans un ordre refusé n'entre pas dans last_error tel quel.
const reasonUnknown = "unknown"

// targetFailure dit pourquoi une cible n'a pas été reçue : la raison est un mot
// fermé (targetReason), jamais le texte d'un agent. cause garde le refus
// d'origine quand l'ordre n'est pas parti.
type targetFailure struct {
	reason string
	cause  error
}

func (f *targetFailure) Error() string {
	return "target not backed up: " + f.reason
}

func (f *targetFailure) Unwrap() error {
	return f.cause
}

// agentPass est un passage d'un plan machine : ses cibles, l'une après l'autre.
// Une cible est un colis à la fois : produit, remis à la destination, élagué,
// jeté ; la suivante ne commence qu'après.
type agentPass struct {
	service     *Service
	plan        Plan
	at          time.Time
	keyring     Keyring
	destination Destination
	targets     []string
}

// targetRun est une cible dont l'ordre backup.produce est parti : le colis
// que la machine source remplit, et l'ordre.
type targetRun struct {
	target  string
	prefix  string
	upload  *action.Upload
	produce action.Run
}

// passOutcome est ce que le passage a vu jusqu'ici : les cibles reçues, celles
// en échec, le premier souci (la clé que last_error garde) et le dernier
// ordre de réception.
type passOutcome struct {
	received int
	failed   int
	problem  string
	lastRun  action.Run
}

// startAgentPass prépare un plan machine lancé à la main, envoie l'ordre de sa
// première cible et rend cet ordre ; le reste du passage se mène en
// arrière-plan. Le verrou des passages est tenu à l'entrée : il est rendu ici
// si rien ne part, sinon à la fin du passage.
func (s *Service) startAgentPass(ctx context.Context, plan Plan) (action.Run, error) {
	at := s.now()
	pass, err := s.prepareAgentPass(ctx, plan, at)
	if err != nil {
		s.running.Unlock()
		s.noteFailure(ctx, plan.Subject, at, err)
		return action.Run{}, err
	}
	first, err := pass.begin(ctx, pass.targets[0])
	if err != nil {
		s.running.Unlock()
		s.noteFailure(ctx, plan.Subject, at, err)
		return action.Run{}, err
	}
	pass.started(ctx, first)
	// La requête finie, son contexte l'est aussi : le passage suit la vie du
	// serveur, et garde l'acteur qui l'a lancé pour l'audit.
	passCtx := audit.WithActor(s.lifetime, audit.ActorFrom(ctx))
	s.background.Add(1)
	go func() {
		defer s.background.Done()
		defer s.running.Unlock()
		pass.walk(passCtx, first)
	}()
	return first.produce, nil
}

// runAgentPass mène le passage d'un plan machine jusqu'au bout. Le verrou des
// passages est tenu par l'appelant.
func (s *Service) runAgentPass(ctx context.Context, plan Plan) (action.Run, error) {
	at := s.now()
	pass, err := s.prepareAgentPass(ctx, plan, at)
	if err != nil {
		s.noteFailure(ctx, plan.Subject, at, err)
		return action.Run{}, err
	}
	return pass.walk(ctx, nil), nil
}

// prepareAgentPass juge, avant tout ordre, ce que le passage demande : la clé
// et la destination (comme pour la base), l'agent de la destination, la
// source, puis les cibles. Un refus ici n'a rien lancé ni rien produit.
func (s *Service) prepareAgentPass(ctx context.Context, plan Plan, at time.Time) (*agentPass, error) {
	keyring, destination, destinationMachine, err := s.prepareDelivery(ctx, plan)
	if err != nil {
		return nil, err
	}
	if err := checkDestinationAgent(destinationMachine); err != nil {
		return nil, err
	}
	source, err := s.machines.Get(ctx, plan.SourceMachineID)
	if errors.Is(err, machine.ErrNotFound) {
		return nil, ErrSourceMachineUnknown
	}
	if err != nil {
		return nil, fmt.Errorf("get source machine: %w", err)
	}
	if err := judgeSource(source, plan.Producer); err != nil {
		return nil, err
	}
	targets, err := s.resolveTargets(ctx, plan)
	if err != nil {
		return nil, err
	}
	return &agentPass{service: s, plan: plan, at: at, keyring: keyring, destination: destination, targets: targets}, nil
}

// judgeSource : la machine source a un agent en ligne dont le droit couvre le
// producteur. Tant que l'agent n'a rien rapporté, c'est lui qui jugera.
func judgeSource(source machine.Status, producer string) error {
	if !source.Agent.IsActive() {
		return ErrSourceRightMissing
	}
	if !source.Online {
		return ErrSourceOffline
	}
	if !source.Agent.ReportedAt.IsZero() && !machine.CoversProducer(source.Agent.Capabilities, producer) {
		return ErrSourceRightMissing
	}
	return nil
}

// checkDestinationAgent refuse une destination dont l'agent ne lit pas le
// préfixe de backup.prune : il élaguerait la base avec le nombre à garder d'un
// plan machine. Une version illisible est jugée trop ancienne.
func checkDestinationAgent(destination machine.Status) error {
	minimum, err := release.ParseVersion(agentwire.PrefixSinceVersion)
	if err != nil {
		return fmt.Errorf("parse the first version that prunes by prefix: %w", err)
	}
	version, err := release.ParseVersion(destination.Agent.Version)
	if err != nil || version.Compare(minimum) < 0 {
		return ErrDestinationAgentOutdated
	}
	return nil
}

// resolveTargets dit ce que le passage sauvegarde : les numéros du sélecteur,
// ou, pour « all », les invités que l'hôte Proxmox de la source a relevés.
func (s *Service) resolveTargets(ctx context.Context, plan Plan) ([]string, error) {
	if plan.Selector != SelectorAll {
		return listedTargets(plan.Selector), nil
	}
	if s.guests == nil {
		return nil, ErrSelectorNeedsList
	}
	ids, err := s.guests.ListProxmoxGuestIDs(ctx, plan.SourceMachineID)
	if err != nil {
		return nil, fmt.Errorf("list guests of the source: %w", err)
	}
	if len(ids) == 0 {
		return nil, ErrSelectorNeedsList
	}
	targets := make([]string, 0, len(ids))
	for _, id := range ids {
		targets = append(targets, strconv.Itoa(id))
	}
	return targets, nil
}

// listedTargets lit « 5010,5012 » : chaque numéro une fois, dans l'ordre donné,
// sans zéro de tête (007 et 7 sont la même machine virtuelle).
func listedTargets(selector string) []string {
	var targets []string
	for _, item := range strings.Split(selector, ",") {
		number, err := strconv.Atoi(item)
		if err != nil {
			continue
		}
		target := strconv.Itoa(number)
		if !slices.Contains(targets, target) {
			targets = append(targets, target)
		}
	}
	return targets
}

// begin ouvre le colis de la cible, y dépose la clé scellée, puis envoie
// backup.produce à la machine source. Rien ne reste si l'ordre ne part pas.
func (p *agentPass) begin(ctx context.Context, target string) (*targetRun, error) {
	s := p.service
	upload, err := s.parcels.CreateForUpload(p.destination.MachineID, p.plan.SourceMachineID)
	if err != nil {
		return nil, fmt.Errorf("create upload parcel: %w", err)
	}
	// La clé scellée voyage avec chaque sauvegarde, comme pour la base.
	if err := upload.Stage(agentwire.BackupKeyFileName(p.keyring.Fingerprint), p.keyring.IdentitySealed); err != nil {
		upload.Discard()
		return nil, fmt.Errorf("stage sealed key: %w", err)
	}
	prefix := p.plan.TargetPrefix(target)
	params, err := json.Marshal(agentwire.BackupProduceParams{
		Producer: p.plan.Producer, Target: target, Prefix: prefix, Recipient: p.keyring.Recipient, Parcel: upload.ID(),
	})
	if err != nil {
		upload.Discard()
		return nil, fmt.Errorf("encode produce params: %w", err)
	}
	produce, err := s.sender.Send(ctx, p.plan.SourceMachineID, agentwire.KindBackupProduce, params)
	if err != nil {
		upload.Discard()
		return nil, sendFailure(err, machine.CapabilityBackupProduce)
	}
	if produce.State.IsFinished() {
		// L'ordre n'est pas parti : personne ne viendra remplir le colis.
		upload.Discard()
		return nil, &targetFailure{reason: runReason(produce), cause: action.ErrMachineOffline}
	}
	return &targetRun{target: target, prefix: prefix, upload: upload, produce: produce}, nil
}

// sendFailure dit, pour une cible, pourquoi un ordre n'a pas pu partir. Le
// refus d'origine reste dans la chaîne : le plan le note, l'appelant le lit.
func sendFailure(err error, capability string) error {
	switch {
	case errors.Is(err, action.ErrMachineOffline):
		return &targetFailure{reason: action.ReasonUndelivered, cause: err}
	case errors.Is(err, action.ErrRightMissing):
		return &targetFailure{reason: agentwire.RightMissing(capability), cause: err}
	}
	return err
}

// started note le début du passage : sa tentative (qui date la nuit), l'ordre
// en cours, et l'audit du lancement.
func (p *agentPass) started(ctx context.Context, first *targetRun) {
	s := p.service
	if err := s.store.NoteBackupAttempt(ctx, p.plan.Subject, p.at, "", first.produce.ID); err != nil {
		s.logger.Error("note backup attempt", "subject", p.plan.Subject, "error", err)
	}
	s.passes.set(p.plan.Subject, 0, 0)
	s.logger.Info("backup pass started", "plan_id", p.plan.ID, "subject", p.plan.Subject, "run_id", first.produce.ID,
		"destination_id", p.plan.DestinationID, "targets", len(p.targets))
	s.auditor.Record(ctx, audit.Event{
		Action: audit.BackupPlanRun, ObjectID: p.plan.ID, ObjectName: p.plan.Subject,
		Details: audit.Details{"run_id": first.produce.ID, "destination_id": p.plan.DestinationID, "targets": strconv.Itoa(len(p.targets))},
	})
	s.changed()
}

// walk mène les cibles l'une après l'autre, jusqu'à la dernière ; une cible en
// échec n'arrête pas les suivantes. first est la première cible si son ordre
// est déjà parti. Il rend le dernier ordre de réception.
func (p *agentPass) walk(ctx context.Context, first *targetRun) action.Run {
	var outcome passOutcome
	for index, target := range p.targets {
		current := first
		if index > 0 || current == nil {
			var err error
			current, err = p.begin(ctx, target)
			if err != nil {
				p.settle(ctx, &outcome, target, action.Run{}, err, nil)
				continue
			}
			if index == 0 {
				p.started(ctx, current)
			}
		}
		received, err := p.finish(ctx, current)
		var pruneErr error
		if err == nil {
			pruneErr = p.prune(ctx, current)
		}
		if ctx.Err() != nil {
			// Le serveur s'arrête : ce passage-là n'a pas de fin à noter.
			p.service.logger.Warn("backup pass interrupted", "subject", p.plan.Subject, "target", target)
			return outcome.lastRun
		}
		p.settle(ctx, &outcome, target, received, err, pruneErr)
	}
	p.service.logger.Info("backup pass done", "plan_id", p.plan.ID, "subject", p.plan.Subject, "received", outcome.received, "failed", outcome.failed)
	return outcome.lastRun
}

// finish attend l'archive de la cible, la remet à la destination, attend qu'elle
// l'ait écrite, puis jette le colis : un seul vit à la fois sur la machine
// openCloud, quelle que soit la taille des machines virtuelles. Il rend l'ordre
// de réception.
func (p *agentPass) finish(ctx context.Context, current *targetRun) (action.Run, error) {
	defer current.upload.Discard()
	s := p.service
	produced, err := s.waitFor(ctx, current.produce.ID)
	if err != nil {
		return action.Run{}, err
	}
	if produced.State != action.StateSucceeded {
		return action.Run{}, &targetFailure{reason: runReason(produced)}
	}
	files := current.upload.Handover()
	if !hasArchive(files, current.prefix) {
		// L'agent a dit avoir envoyé, le colis n'a rien de sa cible.
		return action.Run{}, &targetFailure{reason: agentwire.ReasonUploadFailed}
	}
	received, err := s.sendReceive(ctx, p.destination, current.upload.ID(), files)
	if err != nil {
		return action.Run{}, sendFailure(err, machine.CapabilityBackupReceive)
	}
	if received.State.IsFinished() {
		return received, &targetFailure{reason: runReason(received), cause: action.ErrMachineOffline}
	}
	finished, err := s.waitFor(ctx, received.ID)
	if err != nil {
		return received, err
	}
	if finished.State != action.StateSucceeded {
		return finished, &targetFailure{reason: runReason(finished)}
	}
	return finished, nil
}

// hasArchive dit si le colis porte l'archive de ce préfixe (la clé scellée,
// qui y est toujours, ne compte pas).
func hasArchive(files []agentwire.ParcelFile, prefix string) bool {
	return slices.ContainsFunc(files, func(file agentwire.ParcelFile) bool {
		return agentwire.IsBackupFileOf(file.Name, prefix)
	})
}

// prune élague les sauvegardes de la cible sur la destination, et attend : un
// élagage qui échoue laisse la destination grossir, l'opérateur doit le voir.
// L'agent de la destination est relu à l'instant : il a pu être remplacé
// depuis le début du passage.
func (p *agentPass) prune(ctx context.Context, current *targetRun) error {
	s := p.service
	destinationMachine, err := s.machines.Get(ctx, p.destination.MachineID)
	if err != nil {
		return fmt.Errorf("get destination machine: %w", err)
	}
	if err := checkDestinationAgent(destinationMachine); err != nil {
		return err
	}
	pruned, err := s.requestPrune(ctx, p.destination, current.prefix, p.plan.Keep)
	if err != nil {
		return sendFailure(err, machine.CapabilityBackupReceive)
	}
	finished, err := s.waitFor(ctx, pruned.ID)
	if err != nil {
		return err
	}
	if finished.State != action.StateSucceeded {
		return &targetFailure{reason: runReason(finished)}
	}
	return nil
}

// waitFor attend la fin d'un ordre, au plus runWaitLimit. Un ordre que le
// serveur n'a pas fini à temps est un ordre sans réponse (timeout).
func (s *Service) waitFor(ctx context.Context, runID string) (action.Run, error) {
	waitCtx, cancel := context.WithTimeout(ctx, runWaitLimit)
	defer cancel()
	run, err := s.sender.Wait(waitCtx, runID)
	if err != nil && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		return action.Run{}, &targetFailure{reason: action.ReasonTimeout}
	}
	return run, err
}

// settle note la fin d'une cible : le compte, la clé du premier souci du
// passage, le dernier ordre. receiveErr dit que la cible n'est pas reçue ;
// pruneErr, qu'elle l'est mais que son élagage n'a pas eu lieu.
func (p *agentPass) settle(ctx context.Context, outcome *passOutcome, target string, received action.Run, receiveErr, pruneErr error) {
	s := p.service
	if received.ID != "" {
		outcome.lastRun = received
	}
	switch {
	case receiveErr != nil:
		outcome.failed++
		outcome.keepProblem(TargetFailedKey + ":" + target + ":" + failureReason(receiveErr))
		s.logger.Warn("backup target failed", "subject", p.plan.Subject, "target", target, "error", receiveErr)
	case pruneErr != nil:
		outcome.received++
		outcome.keepProblem(pruneProblem(target, pruneErr))
		s.logger.Warn("backup target received, not pruned", "subject", p.plan.Subject, "target", target, "error", pruneErr)
	default:
		outcome.received++
		s.logger.Info("backup target received", "subject", p.plan.Subject, "target", target, "run_id", received.ID)
	}
	s.passes.set(p.plan.Subject, outcome.received, outcome.failed)
	if err := s.store.NoteBackupAttempt(ctx, p.plan.Subject, p.at, outcome.problem, outcome.lastRun.ID); err != nil {
		s.logger.Error("note backup attempt", "subject", p.plan.Subject, "error", err)
	}
	s.changed()
}

// keepProblem garde le premier souci du passage : c'est celui qui explique les
// suivants ; les comptes disent combien il y en a eu.
func (o *passOutcome) keepProblem(key string) {
	if o.problem == "" {
		o.problem = key
	}
}

// pruneProblem : la clé d'un élagage qui n'a pas eu lieu. Un agent trop ancien
// a son refus propre ; le reste dit la cible et la raison.
func pruneProblem(target string, err error) string {
	if key, isRefusal := refusal.KeyOf(err); isRefusal && errors.Is(err, ErrDestinationAgentOutdated) {
		return key
	}
	return PruneFailedKey + ":" + target + ":" + failureReason(err)
}

// failureReason rend le mot fermé qui dit pourquoi, jamais le texte d'une erreur.
func failureReason(err error) string {
	var failure *targetFailure
	if errors.As(err, &failure) {
		return failure.reason
	}
	return reasonUnknown
}

// runReason rend la raison d'un ordre fini sans réussir : un ordre expiré se
// dit expiré, une raison de la liste fermée passe, tout autre texte devient
// « unknown ». Ce qu'un agent rapporte est borné par le protocole, pas jugé :
// c'est ici qu'il l'est, avant d'atteindre last_error.
func runReason(run action.Run) string {
	if run.State == action.StateExpired {
		return agentwire.ReasonExpired
	}
	if run.Reason == action.ReasonUndelivered || run.Reason == action.ReasonTimeout || isAgentReason(run.Reason) {
		return run.Reason
	}
	return reasonUnknown
}

// isAgentReason : une raison que l'agent sait dire, avec le droit nommé pour
// « right_missing:<droit> ».
func isAgentReason(reason string) bool {
	if capability, found := strings.CutPrefix(reason, agentwire.ReasonRightMissing+":"); found {
		return slices.Contains(machine.CapabilityNames(), capability)
	}
	return slices.Contains(agentwire.RefusalReasons(), reason)
}

// passSummaries garde, en mémoire, les comptes du dernier passage de chaque
// plan machine. Une colonne de plus aurait gardé ces deux nombres après un
// redémarrage ; ils ne servent qu'à lire le passage qui vient de finir.
type passSummaries struct {
	mu        sync.Mutex
	bySubject map[string]passSummary
}

type passSummary struct {
	received int
	failed   int
}

func (p *passSummaries) set(subject string, received, failed int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.bySubject == nil {
		p.bySubject = map[string]passSummary{}
	}
	p.bySubject[subject] = passSummary{received: received, failed: failed}
}

func (p *passSummaries) forget(subject string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.bySubject, subject)
}

// applyTo pose sur le plan les comptes de son dernier passage, s'il en a un
// depuis le démarrage.
func (p *passSummaries) applyTo(plan Plan) Plan {
	p.mu.Lock()
	defer p.mu.Unlock()
	summary := p.bySubject[plan.Subject]
	plan.LastTargetsOK, plan.LastTargetsFailed = summary.received, summary.failed
	return plan
}
