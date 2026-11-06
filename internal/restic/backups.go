package restic

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/ident"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/proxmox"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Le seul rythme : chaque nuit.
const ScheduleDaily = "daily"

// Au-delà, ce n'est plus une rétention qu'on lit.
const maxKeep = 1000

// Retention est ce que l'opérateur veut garder. La cible l'applique chez
// elle après la nuit (restic.retain), sur le dépôt entier : la plus large des
// sauvegardes qui le partagent, jamais sous son plancher.
type Retention struct {
	Last    int
	Daily   int
	Weekly  int
	Monthly int
	Yearly  int
}

// LowersFrom dit si la rétention garde moins que l'ancienne, sur un seul
// des cinq comptes : c'est elle qui fait purger davantage.
func (r Retention) LowersFrom(before Retention) bool {
	return r.Last < before.Last || r.Daily < before.Daily || r.Weekly < before.Weekly ||
		r.Monthly < before.Monthly || r.Yearly < before.Yearly
}

// DefaultRetention : quatorze nuits, huit semaines, trois mois.
func DefaultRetention() Retention {
	return Retention{Daily: 14, Weekly: 8, Monthly: 3}
}

// retainParams : la rétention telle que la cible la lit.
func (r Retention) retainParams(user string) agentwire.ResticRetainParams {
	return agentwire.ResticRetainParams{
		User: user, KeepLast: r.Last, KeepDaily: r.Daily, KeepWeekly: r.Weekly, KeepMonthly: r.Monthly, KeepYearly: r.Yearly,
	}
}

// Definition est ce que l'opérateur saisit pour une sauvegarde.
type Definition struct {
	Name            string
	SourceMachineID string
	Scope           string
	// Portée vm : le numéro de l'invité, sur l'hôte Proxmox de la source.
	VMID int
	// Portée service : le service déclaré, sur la source.
	ServiceID       string
	TargetMachineID string
	Retention       Retention
}

// Backup est une sauvegarde déclarée, et ce que sa dernière exécution a dit.
type Backup struct {
	ID                string
	Name              string
	SourceMachineID   string
	SourceMachineName string
	Scope             string
	VMID              int
	GuestType         string
	ServiceID         string
	ServiceName       string
	TargetMachineID   string
	TargetMachineName string
	RepoUser          string
	Schedule          string
	Retention         Retention
	CreatedAt         time.Time
	LastAttemptAt     time.Time
	LastRunID         string
	Last              Outcome
	// La dernière exécution réussie ; zéro tant qu'aucune ne l'a été.
	LastSuccessAt time.Time
}

func (b Backup) repo() repoKey {
	return repoKey{source: b.SourceMachineID, target: b.TargetMachineID}
}

// ServiceRef est un service déclaré tel qu'une sauvegarde le vise : sa
// machine et son dossier (le projet Compose) sous la racine de l'agent.
type ServiceRef struct {
	ID          string
	Name        string
	MachineID   string
	MachineName string
	Project     string
}

var (
	ErrBackupNotFound = errors.New("restic backup not found")
	// ErrBackupExists : le store le rend sur une contrainte d'unicité.
	ErrBackupExists = refusal.Conflict("restic.backup_exists", "a backup already has this name, or sends the same source to this target")

	ErrNameInvalid      = refusal.Invalid("restic.name_invalid", "the name must be 1 to 80 printable characters")
	ErrScopeInvalid     = refusal.Invalid("restic.scope_invalid", "the scope is vm or service")
	ErrRetentionInvalid = refusal.Invalid("restic.retention_invalid", "each kept count is between 0 and 1000, and at least one is not zero")
	// La cible refuserait la rétention : elle garderait moins que son plancher.
	ErrRetentionBelowFloor = refusal.Invalid("restic.retention_below_floor", "the retention keeps less than the floor of the target")
	// Baisser une rétention purge plus : l'opérateur retape le nom de la
	// sauvegarde, une hausse ne le demande pas.
	ErrRetentionConfirmName = refusal.Invalid("restic.retention_confirm_name", "lowering a retention asks for the name of the backup")
	ErrSourceUnknown        = refusal.Invalid("restic.source_unknown", "the source machine does not exist")
	ErrGuestUnknown         = refusal.Invalid("restic.guest_unknown", "the Proxmox host of the source has not reported this guest")
	ErrServiceUnknown       = refusal.Invalid("restic.service_unknown", "the service does not exist on the source machine")
	// Ni dossier Compose, ni volume nommé, ni base détectée.
	ErrServiceNothingToSave = refusal.Invalid("restic.service_nothing_to_save", "the service has no Compose folder, no named volume and no detected database")
	ErrTargetNotReady       = refusal.Conflict("restic.target_not_ready", "rest-server of this target has not reported its address and certificate yet")
	// La source n'a pas le droit : l'écran propose de la ré-enrôler avec.
	ErrSourceRightMissing     = refusal.Conflict("restic.source_right_missing", "the agent of the source may not run restic: re-enroll it with restic.produce").NeedsRight(machine.CapabilityResticProduce)
	ErrSourceRightUnavailable = refusal.Conflict("restic.source_right_unavailable", "the agent of the source holds restic.produce but cannot exercise it on this machine")
	ErrSourceVMRightMissing   = refusal.Conflict("restic.source_vm_right_missing", "a guest backup also needs backup.produce for proxmox-vzdump on the source").NeedsRight(machine.CapabilityBackupProduce)
	// Les volumes et les bases d'un service passent par restic.docker.
	ErrSourceDockerRightMissing     = refusal.Conflict("restic.source_docker_right_missing", "the volumes and databases of a service also need restic.docker on the source").NeedsRight(machine.CapabilityResticDocker)
	ErrSourceDockerRightUnavailable = refusal.Conflict("restic.source_docker_right_unavailable", "the agent of the source holds restic.docker but cannot exercise it on this machine")
	// restic manque sur la source : l'écran dit quoi installer.
	ErrResticMissing = refusal.Conflict("restic.restic_missing", "restic is not installed on the source: install it, then restart its agent")
	ErrBackupRunning = refusal.Conflict("restic.backup_running", "a backup or a retention runs on the same repository")
)

var printableName = regexp.MustCompile(`^[^\x00-\x1f\x7f]{1,80}$`)

// Validate rend le premier refus de la définition, sans base.
func (d Definition) Validate() error {
	if !printableName.MatchString(d.Name) {
		return ErrNameInvalid
	}
	switch d.Scope {
	case agentwire.ResticScopeVM:
		if d.VMID < 100 || d.ServiceID != "" {
			return ErrScopeInvalid
		}
	case agentwire.ResticScopeService:
		if d.VMID != 0 || d.ServiceID == "" {
			return ErrScopeInvalid
		}
	default:
		return ErrScopeInvalid
	}
	// Tout à zéro, la cible refuserait : une rétention garde quelque chose.
	retention := d.Retention
	if retention == (Retention{}) {
		return ErrRetentionInvalid
	}
	for _, count := range []int{retention.Last, retention.Daily, retention.Weekly, retention.Monthly, retention.Yearly} {
		if count < 0 || count > maxKeep {
			return ErrRetentionInvalid
		}
	}
	return nil
}

// CreateBackup déclare une sauvegarde : il vérifie la cible, la source et
// ce qu'elle sauvegarde, puis lie la source à la cible. Le lien (son dépôt)
// a deux mots de passe tirés par le serveur : restic.provision les donne à la
// source avec l'adresse et le certificat de la cible, restic.user.add donne
// le mot de passe REST à la cible.
func (s *Service) CreateBackup(ctx context.Context, definition Definition) (Backup, error) {
	if err := definition.Validate(); err != nil {
		return Backup{}, err
	}
	target, err := s.readyTarget(ctx, definition.TargetMachineID)
	if err != nil {
		return Backup{}, err
	}
	if err := judgeFloor(target, definition.Retention); err != nil {
		return Backup{}, err
	}
	created, err := s.resolveSource(ctx, definition)
	if err != nil {
		return Backup{}, err
	}
	created.ID, err = ident.New()
	if err != nil {
		return Backup{}, fmt.Errorf("new backup id: %w", err)
	}
	created.TargetMachineID, created.TargetMachineName = target.MachineID, target.MachineName
	created.CreatedAt = s.now()
	if err := s.store.InsertResticBackup(ctx, created); err != nil {
		return Backup{}, err
	}
	if err := s.linkSource(ctx, created, target); err != nil {
		if deleteErr := s.store.DeleteResticBackup(ctx, created.ID); deleteErr != nil {
			s.logger.Error("delete unlinked restic backup", "backup_id", created.ID, "error", deleteErr)
		}
		return Backup{}, err
	}
	s.logger.Info("restic backup created", "backup_id", created.ID, "source", created.SourceMachineID, "target", created.TargetMachineID, "scope", created.Scope)
	s.auditor.Record(ctx, backupEvent(audit.ResticBackupCreate, created))
	s.changed()
	return s.store.GetResticBackup(ctx, created.ID)
}

// linkSource ouvre (crée, ou rouvre s'il avait été retiré) le lien de la
// sauvegarde et l'envoie aux deux machines ; s'il n'a pas pu partir, il
// revient à son état d'avant.
func (s *Service) linkSource(ctx context.Context, created Backup, target TargetRecord) error {
	opened, err := s.openLink(ctx, created.SourceMachineID, target.MachineID)
	if err != nil {
		return err
	}
	if err := s.provisionSource(ctx, opened.Link, target); err != nil {
		s.undoOpen(ctx, opened)
		return err
	}
	if err := s.openUser(ctx, opened.Link); err != nil {
		s.undoOpen(ctx, opened)
		return err
	}
	return nil
}

// judgeFloor refuse une rétention que la cible refuserait, d'après le
// plancher qu'elle a rapporté. Inconnu, la cible jugera seule.
func judgeFloor(target TargetRecord, retention Retention) error {
	floor := target.Reported.RetentionFloor
	if floor == nil || floor.Admits(retention.retainParams("")) {
		return nil
	}
	return ErrRetentionBelowFloor
}

// readyTarget rend la cible si son rest-server a rapporté de quoi s'y lier.
func (s *Service) readyTarget(ctx context.Context, machineID string) (TargetRecord, error) {
	target, err := s.store.GetResticTarget(ctx, machineID)
	if errors.Is(err, ErrTargetNotFound) {
		return TargetRecord{}, ErrTargetUnknown
	}
	if err != nil {
		return TargetRecord{}, fmt.Errorf("get restic target: %w", err)
	}
	if target.Reported.State == agentwire.ResticTargetError && target.Reported.Reason == agentwire.ResticErrorBinaryMissing {
		return TargetRecord{}, ErrRestServerMissing
	}
	if !target.IsReady() {
		return TargetRecord{}, ErrTargetNotReady
	}
	return target, nil
}

// resolveSource vérifie la source et ses droits, puis ce qu'elle sauvegarde :
// un invité que son hôte Proxmox a relevé, ou un service déclaré sur elle
// qui a quelque chose à sauvegarder. Il rend la sauvegarde à écrire, sans
// identifiant ni cible.
func (s *Service) resolveSource(ctx context.Context, definition Definition) (Backup, error) {
	source, err := s.machines.Get(ctx, definition.SourceMachineID)
	if errors.Is(err, machine.ErrNotFound) {
		return Backup{}, ErrSourceUnknown
	}
	if err != nil {
		return Backup{}, fmt.Errorf("get source machine: %w", err)
	}
	if err := judgeSourceRights(source, definition.Scope); err != nil {
		return Backup{}, err
	}
	user, err := agentwire.ResticUserFor(source.ID)
	if err != nil {
		return Backup{}, ErrSourceUnknown
	}
	resolved := Backup{
		Name: definition.Name, SourceMachineID: source.ID, SourceMachineName: source.Name, Scope: definition.Scope,
		RepoUser: user, Schedule: ScheduleDaily, Retention: definition.Retention,
	}
	if definition.Scope == agentwire.ResticScopeVM {
		guest, err := s.store.ResticGuest(ctx, source.ID, definition.VMID)
		if errors.Is(err, proxmox.ErrNotFound) {
			return Backup{}, ErrGuestUnknown
		}
		if err != nil {
			return Backup{}, fmt.Errorf("get proxmox guest: %w", err)
		}
		resolved.VMID, resolved.GuestType = guest.VMID, guest.Kind
		return resolved, nil
	}
	service, err := s.store.ResticService(ctx, definition.ServiceID)
	if errors.Is(err, ErrServiceNotFound) || (err == nil && service.MachineID != source.ID) {
		return Backup{}, ErrServiceUnknown
	}
	if err != nil {
		return Backup{}, fmt.Errorf("get declared service: %w", err)
	}
	if _, err := s.judgedContent(ctx, service, source); err != nil {
		return Backup{}, err
	}
	resolved.ServiceID, resolved.ServiceName = service.ID, service.Name
	return resolved, nil
}

// judgedContent rend le contenu du service s'il y a quelque chose à
// sauvegarder et que la source a le droit de le prendre.
func (s *Service) judgedContent(ctx context.Context, service ServiceRef, source machine.Status) (ServiceContent, error) {
	content, err := s.serviceContent(ctx, service)
	if err != nil {
		return ServiceContent{}, err
	}
	if content.IsEmpty() {
		return ServiceContent{}, ErrServiceNothingToSave
	}
	if err := judgeDockerRight(source, content); err != nil {
		return ServiceContent{}, err
	}
	return content, nil
}

// ErrServiceNotFound : la base n'a pas ce service déclaré.
var ErrServiceNotFound = errors.New("declared service not found")

// isServiceFolder : ce que l'agent accepte comme dossier de service.
func isServiceFolder(project string) bool {
	params := agentwire.ResticBackupParams{Target: "check", Scope: agentwire.ResticScopeService, Service: project}
	return params.Validate() == nil
}

// judgeSourceRights : restic.produce, et pour un invité backup.produce qui
// couvre vzdump. Tant que l'agent n'a rien rapporté, il jugera lui-même.
func judgeSourceRights(source machine.Status, scope string) error {
	produce := source.Agent.Fit(machine.CapabilityResticProduce, "")
	switch {
	case produce.State == machine.RightMissing:
		return ErrSourceRightMissing
	case produce.State == machine.RightUnavailable && produce.Reason == agentwire.ReasonResticMissing:
		return ErrResticMissing
	case produce.State == machine.RightUnavailable:
		return ErrSourceRightUnavailable
	}
	if scope == agentwire.ResticScopeVM && source.Agent.Fit(machine.CapabilityBackupProduce, machine.ProducerProxmoxVzdump).State == machine.RightMissing {
		return ErrSourceVMRightMissing
	}
	return nil
}

// judgeDockerRight : un volume ou une base demande restic.docker. Tant que
// l'agent n'a rien rapporté, il jugera lui-même.
func judgeDockerRight(source machine.Status, content ServiceContent) error {
	if !content.NeedsDocker() {
		return nil
	}
	switch source.Agent.Fit(machine.CapabilityResticDocker, "").State {
	case machine.RightMissing:
		return ErrSourceDockerRightMissing
	case machine.RightUnavailable:
		return ErrSourceDockerRightUnavailable
	}
	return nil
}

// UpdateBackup change le nom et la rétention ; la source, la cible
// et ce qui est sauvegardé ne changent pas (un autre dépôt, une autre
// sauvegarde).
func (s *Service) UpdateBackup(ctx context.Context, sourceID, id, name string, retention Retention, confirmName string) (Backup, error) {
	current, err := s.ownedBackup(ctx, sourceID, id)
	if err != nil {
		return Backup{}, err
	}
	definition := Definition{Name: name, Scope: current.Scope, VMID: current.VMID, ServiceID: current.ServiceID, Retention: retention}
	if err := definition.Validate(); err != nil {
		return Backup{}, err
	}
	target, err := s.store.GetResticTarget(ctx, current.TargetMachineID)
	if err != nil {
		return Backup{}, fmt.Errorf("get restic target: %w", err)
	}
	if err := judgeFloor(target, retention); err != nil {
		return Backup{}, err
	}
	lowered := retention.LowersFrom(current.Retention)
	if lowered && confirmName != current.Name {
		return Backup{}, ErrRetentionConfirmName
	}
	before := current.Retention
	current.Name, current.Retention = name, retention
	if err := s.store.UpdateResticBackup(ctx, current); err != nil {
		return Backup{}, err
	}
	s.auditor.Record(ctx, backupEvent(audit.ResticBackupUpdate, current))
	if lowered {
		s.auditor.Record(ctx, retentionLoweredEvent(current, before))
	}
	s.changed()
	return s.store.GetResticBackup(ctx, id)
}

// DeleteBackup retire la sauvegarde. Le dépôt reste sur la cible : en ajout
// seul, rien ne s'efface d'ici. La dernière sauvegarde de la source vers la
// cible partie, la cible ferme le dépôt (restic.user.remove) et les mots de
// passe du lien s'effacent de la base.
func (s *Service) DeleteBackup(ctx context.Context, sourceID, id string) error {
	current, err := s.ownedBackup(ctx, sourceID, id)
	if err != nil {
		return err
	}
	if err := s.store.DeleteResticBackup(ctx, id); err != nil {
		return err
	}
	s.logger.Info("restic backup deleted", "backup_id", id)
	s.auditor.Record(ctx, backupEvent(audit.ResticBackupDelete, current))
	s.closeLinkIfUnused(ctx, current)
	s.changed()
	return nil
}

// ownedBackup rend la sauvegarde si elle part bien de cette source : la
// route nomme la machine, la portée d'un jeton se juge sur elle.
func (s *Service) ownedBackup(ctx context.Context, sourceID, id string) (Backup, error) {
	current, err := s.store.GetResticBackup(ctx, id)
	if err != nil {
		return Backup{}, err
	}
	if current.SourceMachineID != sourceID {
		return Backup{}, ErrBackupNotFound
	}
	return current, nil
}

// retentionLoweredEvent : la ligne d'audit d'une rétention baissée, avec les
// cinq comptes d'avant et d'après.
func retentionLoweredEvent(backup Backup, before Retention) audit.Event {
	event := backupEvent(audit.ResticBackupRetentionLower, backup)
	event.Details["before"] = retentionSummary(before)
	event.Details["after"] = retentionSummary(backup.Retention)
	return event
}

func retentionSummary(retention Retention) string {
	return fmt.Sprintf("last=%d daily=%d weekly=%d monthly=%d yearly=%d",
		retention.Last, retention.Daily, retention.Weekly, retention.Monthly, retention.Yearly)
}

func backupEvent(verb audit.Action, backup Backup) audit.Event {
	details := audit.Details{
		"source_machine_id": backup.SourceMachineID, "target_machine_id": backup.TargetMachineID, "scope": backup.Scope,
	}
	if backup.Scope == agentwire.ResticScopeVM {
		details["vmid"] = strconv.Itoa(backup.VMID)
	} else {
		details["service_id"] = backup.ServiceID
	}
	return audit.Event{Action: verb, ObjectID: backup.ID, ObjectName: backup.Name, Details: details}
}
