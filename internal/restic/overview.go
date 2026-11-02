package restic

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/backup"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/proxmox"
)

// Les origines d'une protection : vue chez Proxmox (une tâche vzdump qu'il
// mène), un plan par archives d'age, une sauvegarde restic d'openCloud.
const (
	OriginObserved = "observed"
	OriginArchive  = "archive"
	OriginRestic   = "restic"
)

// Les états d'une protection, tels que la carte les montre.
const (
	ProtectionOK      = "ok"
	ProtectionLate    = "late"
	ProtectionFailed  = "failed"
	ProtectionRefused = "refused"
	ProtectionRunning = "running"
	ProtectionNever   = "never"
	ProtectionUnknown = "unknown"
)

// Les sortes de cible : un rest-server d'openCloud, un stockage de Proxmox,
// une destination des plans par archives.
const (
	TargetKindRestic      = "restic"
	TargetKindStorage     = "storage"
	TargetKindDestination = "destination"
)

const (
	// Une sauvegarde de chaque nuit plus vieille que ceci est en retard.
	staleAfter = 36 * time.Hour
	// L'historique que chaque carte restic montre.
	recentRuns = 7
)

// Overview est ce que la page Sauvegardes lit d'un coup : ce qui est
// sauvegardé (et ce qui ne l'est pas), où ça va, et ce que chaque machine
// peut faire. Des faits : l'écran compose ses phrases.
type Overview struct {
	Sources  []Source
	Targets  []TargetView
	Machines []MachineFit
}

// Source est ce qui peut être sauvegardé : la base, un invité Proxmox, un
// service déclaré, un volume Docker ; avec chaque protection qui la couvre.
type Source struct {
	Key         string
	Kind        string
	Name        string
	MachineID   string
	MachineName string
	VMID        int
	ServiceID   string
	Driver      string
	UsedBy      []string
	// Vrai quand l'hôte n'a pas pu lister des archives faute de droit, ou ses
	// tâches : « rien vu » ne dit pas « rien ».
	ObservedUnknown bool
	// Portée service : ce que « le service entier » prendra ; vide sinon.
	Parts       []SourcePart
	Protections []Protection
}

// SourcePart est une partie d'un service : son dossier (Name vide), un volume
// nommé (son nom), une base (le nom de son conteneur, et son moteur).
type SourcePart struct {
	Kind   string
	Name   string
	Engine string
}

// PartResult est ce qu'une partie a donné au dernier passage restic : ok,
// failed ou skipped, une raison fermée, les octets ajoutés.
type PartResult struct {
	Kind       string
	Name       string
	State      string
	Reason     string
	BytesAdded int64
}

// Protection est une façon dont la source est sauvegardée.
type Protection struct {
	Origin string
	// L'identifiant propre à l'origine : la tâche vzdump, le sujet du plan,
	// la sauvegarde restic.
	ID   string
	Name string
	// Restic : vm ou service.
	Scope             string
	TargetMachineID   string
	TargetMachineName string
	Storage           string
	// Vue chez Proxmox : le stockage où est la plus récente archive, qui
	// peut différer de celui où la tâche écrit.
	ArchiveStorage string
	State          string
	Reason         string
	LastAt         time.Time
	// La dernière réussite, même quand le dernier essai a échoué ; zéro
	// avant la première.
	LastSuccessAt time.Time
	NextAt        time.Time
	// Le rythme : « daily », ou l'horaire de la tâche vzdump tel que Proxmox
	// l'écrit.
	Schedule   string
	SizeBytes  *int64
	AddedBytes *int64
	Copies     *int
	Retention  RetentionView
	// Restic : le chemin de la dernière exécution ; nil avant la première.
	Path *PathView
	Runs []RunRecord
	// Restic, portée service : les parties du dernier passage.
	Parts []PartResult
	// Restic : la création de la sauvegarde, d'où se mesure l'attente de sa
	// première réussite ; zéro pour les autres origines.
	CreatedAt time.Time
	// Restic : la vérification du dépôt (restic.verify) ; nil pour les autres
	// origines.
	Verify *VerifyView
}

// VerifyView est la vérification du dépôt d'une sauvegarde restic : la
// dernière tentative et la dernière réussite, ce qu'elle a relu (des
// comptes), si la cible peut la mener, et quand la nuit la rejouera.
type VerifyView struct {
	State     string
	Reason    string
	At        time.Time
	SuccessAt time.Time
	Snapshots int
	Files     int
	Bytes     int64
	// Le droit restic.verify de la cible : l'écran propose de la ré-enrôler.
	Right RightFit
	// Since : d'où court l'attente d'une première réussite (la plus ancienne
	// sauvegarde du dépôt) ; sert à dire depuis quand le test manque.
	Since time.Time
	// Le dépôt n'est pas encore vérifié ou ne l'est plus assez : la nuit la
	// rejoue (si le droit le permet).
	IsStale bool
}

// RetentionView est la rétention en clair : Known faux, on ne la connaît
// pas ; Applied faux, rien ne la fait respecter. Restic : la dernière purge
// du dépôt (LastForget), nil avant la première.
type RetentionView struct {
	Known   bool
	Applied bool
	KeepAll bool
	Last    int
	Daily   int
	Weekly  int
	Monthly int
	Yearly  int
	// LastForget : la dernière rétention que la cible a appliquée au dépôt.
	LastForget *RetainOutcome
}

// PathView est le chemin d'une exécution restic, avec ce qu'il faudrait
// ouvrir pour passer en direct : SourceName doit joindre TargetHost:TargetPort.
type PathView struct {
	Path       string
	Reason     string
	SourceName string
	TargetName string
	TargetHost string
	TargetPort int
}

// TargetView est une machine, ou un stockage, qui reçoit des sauvegardes.
type TargetView struct {
	Key         string
	Kind        string
	Name        string
	MachineID   string
	MachineName string
	Online      bool
	// Restic : running, stopped, error, ou pending tant que rien n'est
	// rapporté ; stockage : active ou inactive ; destination : l'état du droit.
	State       string
	Reason      string
	Host        string
	Port        int
	Dir         string
	Version     string
	Fingerprint string
	DiskTotal   int64
	DiskFree    int64
	DiskUsed    int64
	BackupBytes int64
	ReportedAt  time.Time
	// Restic : le plancher de rétention que la cible tient ; nil tant
	// qu'elle ne l'a pas rapporté.
	RetentionFloor *agentwire.ResticRetentionFloor
	Node           string
	Type           string
	// Les clés des sources qu'elle garde.
	Holds []string
}

// MachineFit dit, pour chaque machine, si elle peut être cible ou source, et
// sinon le droit qui manque : l'écran grise et propose de ré-enrôler.
type MachineFit struct {
	MachineID   string
	MachineName string
	// Le système et l'architecture que l'agent a dits à l'enrôlement :
	// l'écran en compose la commande d'installation de restic ou rest-server.
	OS     string
	Arch   string
	Online bool
	// ResticAvailable : l'agent a restic.produce et trouve restic.
	ResticAvailable bool
	IsTarget        bool
	ProxmoxHost     bool
	// Le jeton de l'hôte ne lit pas ses tâches de sauvegarde (Sys.Audit sur
	// « / ») : la fiche dit ce qui manque, « aucune tâche » n'est pas une réponse.
	ProxmoxJobsUnknown bool
	Rights             []RightFit
}

// L'état d'une cible restic tant que son agent n'a rien rapporté.
const targetPending = "pending"

// overviewInputs est tout ce que l'aperçu lit, d'une traite.
type overviewInputs struct {
	inventory backup.Inventory
	backups   []Backup
	runs      map[string][]RunRecord
	targets   []TargetRecord
	services  []ServiceRef
	docker    map[string]ServiceDocker
	jobs      map[string][]proxmox.BackupJob
	jobsBlind map[string]bool
	links     map[repoKey]Link
	// La plus ancienne sauvegarde de chaque dépôt : d'où se mesure l'attente de
	// sa première vérification.
	oldest     map[repoKey]time.Time
	machines   []machine.Status
	machineMap map[string]machine.Status
}

// Overview rend l'aperçu de la page Sauvegardes. Rien n'est demandé aux
// machines ni à Proxmox : c'est ce que le serveur a en base.
func (s *Service) Overview(ctx context.Context) (Overview, error) {
	inputs, err := s.readOverviewInputs(ctx)
	if err != nil {
		return Overview{}, err
	}
	now := s.now()
	sources := sourcesOf(inputs, now)
	return Overview{Sources: sources, Targets: targetsOf(inputs, sources), Machines: fitsOf(inputs)}, nil
}

func (s *Service) readOverviewInputs(ctx context.Context) (overviewInputs, error) {
	var inputs overviewInputs
	var err error
	if s.inventory != nil {
		if inputs.inventory, err = s.inventory.Inventory(ctx); err != nil {
			return inputs, fmt.Errorf("read backup inventory: %w", err)
		}
	}
	if inputs.backups, err = s.store.ListResticBackups(ctx); err != nil {
		return inputs, fmt.Errorf("list restic backups: %w", err)
	}
	if inputs.runs, err = s.store.ListRecentResticRuns(ctx, recentRuns); err != nil {
		return inputs, fmt.Errorf("list restic runs: %w", err)
	}
	if inputs.targets, err = s.store.ListResticTargets(ctx); err != nil {
		return inputs, fmt.Errorf("list restic targets: %w", err)
	}
	if inputs.services, err = s.store.ListResticServices(ctx); err != nil {
		return inputs, fmt.Errorf("list services: %w", err)
	}
	if inputs.docker, err = s.store.ListResticServiceDocker(ctx); err != nil {
		return inputs, fmt.Errorf("list services docker inventory: %w", err)
	}
	if inputs.jobs, err = s.store.ProxmoxBackupJobsByHost(ctx); err != nil {
		return inputs, fmt.Errorf("list proxmox backup jobs: %w", err)
	}
	if inputs.jobsBlind, err = s.store.ProxmoxJobsUnreadable(ctx); err != nil {
		return inputs, fmt.Errorf("read proxmox job access: %w", err)
	}
	links, err := s.store.ListResticLinks(ctx)
	if err != nil {
		return inputs, fmt.Errorf("list restic links: %w", err)
	}
	inputs.links = map[repoKey]Link{}
	for _, link := range links {
		inputs.links[link.repo()] = link
	}
	inputs.oldest = map[repoKey]time.Time{}
	for _, candidate := range inputs.backups {
		if seen, found := inputs.oldest[candidate.repo()]; !found || candidate.CreatedAt.Before(seen) {
			inputs.oldest[candidate.repo()] = candidate.CreatedAt
		}
	}
	if inputs.machines, err = s.machines.List(ctx); err != nil {
		return inputs, fmt.Errorf("list machines: %w", err)
	}
	inputs.machineMap = map[string]machine.Status{}
	for _, status := range inputs.machines {
		inputs.machineMap[status.ID] = status
	}
	return inputs, nil
}

// sourcesOf : celles de l'inventaire (base, invités, volumes), puis les
// services déclarés qui ont de quoi être sauvegardés (ou une sauvegarde),
// puis les sauvegardes restic d'une source que la base ne montre plus.
// Chaque sauvegarde restic se pose sur sa source.
func sourcesOf(inputs overviewInputs, now time.Time) []Source {
	sources := []Source{}
	used := map[string]bool{}
	for _, item := range inputs.inventory.Sources {
		source := inventorySource(item, inputs, now)
		source.Protections = append(source.Protections, resticProtections(inputs, now, used, func(candidate Backup) bool {
			return candidate.Scope == agentwire.ResticScopeVM && backup.GuestSourceKey(candidate.SourceMachineID, candidate.VMID) == item.Key
		})...)
		sources = append(sources, source)
	}
	for _, service := range inputs.services {
		content, _ := deriveContent(service, inputs.docker[service.ID])
		isBackedUp := slices.ContainsFunc(inputs.backups, func(candidate Backup) bool { return candidate.ServiceID == service.ID })
		if content.IsEmpty() && !isBackedUp {
			continue
		}
		source := Source{
			Key: "service:" + service.ID, Kind: agentwire.ResticScopeService, Name: service.Name,
			MachineID: service.MachineID, MachineName: service.MachineName, ServiceID: service.ID, UsedBy: []string{},
			Parts: sourcePartsOf(content),
		}
		source.Protections = resticProtections(inputs, now, used, func(candidate Backup) bool { return candidate.ServiceID == service.ID })
		sources = append(sources, source)
	}
	for _, orphan := range inputs.backups {
		if used[orphan.ID] {
			continue
		}
		source := Source{
			Key: backup.GuestSourceKey(orphan.SourceMachineID, orphan.VMID), Kind: backup.KindVM, Name: orphan.Name,
			MachineID: orphan.SourceMachineID, MachineName: orphan.SourceMachineName, VMID: orphan.VMID, UsedBy: []string{},
			Parts: []SourcePart{},
		}
		switch {
		case orphan.Scope == agentwire.ResticScopeService:
			source.Key, source.Kind, source.ServiceID, source.VMID = "service:"+orphan.ServiceID, agentwire.ResticScopeService, orphan.ServiceID, 0
		case orphan.GuestType == proxmox.KindLXC:
			source.Kind = backup.KindLXC
		}
		source.Protections = resticProtections(inputs, now, used, func(candidate Backup) bool { return candidate.ID == orphan.ID })
		sources = append(sources, source)
	}
	return sources
}

// sourcePartsOf : le dossier d'abord, puis les volumes, puis les bases.
func sourcePartsOf(content ServiceContent) []SourcePart {
	parts := []SourcePart{}
	if content.Folder != "" {
		parts = append(parts, SourcePart{Kind: agentwire.ResticPartFolder})
	}
	for _, volume := range content.Volumes {
		parts = append(parts, SourcePart{Kind: agentwire.ResticPartVolume, Name: volume})
	}
	for _, database := range content.Databases {
		parts = append(parts, SourcePart{Kind: agentwire.ResticPartDatabase, Name: database.Container, Engine: database.Engine})
	}
	return parts
}

// inventorySource reprend une source de l'inventaire avec ce qui la couvre
// déjà : les plans par archives et la tâche vzdump de son hôte.
func inventorySource(item backup.Source, inputs overviewInputs, now time.Time) Source {
	source := Source{
		Key: item.Key, Kind: item.Kind, Name: item.Name, MachineID: item.MachineID, MachineName: item.MachineName,
		Driver: item.Driver, UsedBy: orEmpty(item.UsedBy), ObservedUnknown: item.ObservedUnknown, Parts: []SourcePart{},
		Protections: []Protection{},
	}
	if vmid, err := strconv.Atoi(item.ID); err == nil {
		source.VMID = vmid
	}
	for _, coverage := range item.CoveredBy {
		source.Protections = append(source.Protections, archiveProtection(coverage, item, now))
	}
	if item.Kind == backup.KindVM || item.Kind == backup.KindLXC {
		if observed, ok := observedProtection(item, source.VMID, inputs, now); ok {
			source.Protections = append(source.Protections, observed)
		}
		source.ObservedUnknown = item.ObservedUnknown || inputs.jobsBlind[item.MachineID]
	}
	return source
}

// archiveProtection : un plan par archives d'age. Son état vient de la
// dernière ligne de l'historique de la source, si elle est de ce plan.
func archiveProtection(coverage backup.Coverage, item backup.Source, now time.Time) Protection {
	protection := Protection{
		Origin: OriginArchive, ID: coverage.Subject, Name: coverage.PlanName, TargetMachineID: coverage.DestinationMachineID,
		TargetMachineName: coverage.DestinationMachineName, Storage: coverage.DestinationName, State: ProtectionNever,
		NextAt: coverage.NextAt, Schedule: ScheduleDaily, Runs: []RunRecord{}, Parts: []PartResult{},
		Retention: RetentionView{Known: true, Applied: true, Last: coverage.Keep},
	}
	if item.LastRun != nil && item.LastRun.Subject == coverage.Subject {
		protection.LastAt = item.LastRun.FinishedAt
		protection.Reason = item.LastRun.Reason
		protection.State = archiveState(item.LastRun.Result, item.LastRun.FinishedAt, now)
	}
	if item.LastOK != nil && item.LastOK.Subject == coverage.Subject {
		protection.SizeBytes = item.LastOK.SizeBytes
		protection.LastSuccessAt = item.LastOK.FinishedAt
	}
	return protection
}

func archiveState(result backup.Result, finishedAt, now time.Time) string {
	switch result {
	case backup.ResultFailed:
		return ProtectionFailed
	case backup.ResultRefused:
		return ProtectionRefused
	}
	return freshness(finishedAt, now)
}

// freshness : réussie, à jour ou en retard selon son âge.
func freshness(at, now time.Time) string {
	if now.Sub(at) > staleAfter {
		return ProtectionLate
	}
	return ProtectionOK
}

// observedProtection : ce que Proxmox fait de lui-même pour cet invité, la
// tâche vzdump qui le couvre (prochaine exécution, stockage, rétention) et
// les archives que les stockages montrent. Rien des deux : pas de carte.
func observedProtection(item backup.Source, vmid int, inputs overviewInputs, now time.Time) (Protection, bool) {
	guest := proxmox.Guest{VMID: vmid, Node: item.Node}
	job, hasJob := proxmox.JobFor(inputs.jobs[item.MachineID], guest)
	if !hasJob && item.Observed == nil {
		return Protection{}, false
	}
	protection := Protection{
		Origin: OriginObserved, TargetMachineID: item.MachineID, TargetMachineName: item.MachineName,
		State: ProtectionNever, Runs: []RunRecord{}, Parts: []PartResult{},
	}
	if hasJob {
		protection.ID, protection.Name, protection.Storage = job.ID, job.ID, job.Storage
		protection.NextAt, protection.Schedule = job.NextRun, job.Schedule
		protection.Retention = retentionOfJob(job.Retention)
	}
	if archives := item.Observed; archives != nil {
		size, copies := archives.LastBytes, archives.Count
		protection.SizeBytes, protection.Copies = &size, &copies
		// Une archive vue est une réussite : Proxmox ne garde pas les ratées.
		protection.LastAt, protection.LastSuccessAt = archives.LastAt, archives.LastAt
		protection.State = freshness(archives.LastAt, now)
		protection.ArchiveStorage = archives.Storage
		if protection.Storage == "" {
			protection.Storage = archives.Storage
		}
	} else if item.ObservedUnknown {
		protection.State = ProtectionUnknown
	}
	return protection, true
}

func retentionOfJob(retention proxmox.Retention) RetentionView {
	return RetentionView{
		Known: retention.Known, Applied: retention.Known, KeepAll: retention.KeepAll, Last: retention.Last,
		Daily: retention.Daily, Weekly: retention.Weekly, Monthly: retention.Monthly, Yearly: retention.Yearly,
	}
}

// resticProtections rend les sauvegardes restic que match retient, notées
// prises.
func resticProtections(inputs overviewInputs, now time.Time, used map[string]bool, match func(Backup) bool) []Protection {
	protections := []Protection{}
	for _, candidate := range inputs.backups {
		if used[candidate.ID] || !match(candidate) {
			continue
		}
		used[candidate.ID] = true
		link, hasLink := inputs.links[candidate.repo()]
		protection := resticProtection(candidate, inputs.runs[candidate.ID], now)
		if hasLink && link.Retain.State != "" {
			forget := link.Retain
			protection.Retention.LastForget = &forget
		}
		if hasLink {
			protection.Verify = verifyViewOf(link, inputs.machineMap[candidate.TargetMachineID], inputs.oldest[candidate.repo()], now)
		}
		protections = append(protections, protection)
	}
	return protections
}

// verifyViewOf : la vérification est celle du dépôt, que partagent toutes les
// sauvegardes d'une source vers une cible. Avant la première réussite,
// l'attente se mesure depuis la plus ancienne d'entre elles (linkSince).
func verifyViewOf(link Link, target machine.Status, linkSince, now time.Time) *VerifyView {
	verify := link.Verify
	baseline := verify.SuccessAt
	if baseline.IsZero() {
		baseline = linkSince
	}
	return &VerifyView{
		State: verify.State, Reason: verify.Reason, At: verify.At, SuccessAt: verify.SuccessAt,
		Snapshots: verify.Snapshots, Files: verify.Files, Bytes: verify.Bytes,
		Right:   RightOf(target, machine.CapabilityResticVerify),
		Since:   linkSince,
		IsStale: now.Sub(baseline) > VerifyStaleAfter,
	}
}

func resticProtection(current Backup, runs []RunRecord, now time.Time) Protection {
	retention := current.Retention
	protection := Protection{
		Origin: OriginRestic, ID: current.ID, Name: current.Name, Scope: current.Scope,
		TargetMachineID: current.TargetMachineID, TargetMachineName: current.TargetMachineName,
		State: resticState(current, now), Reason: current.Last.Reason, LastAt: current.Last.FinishedAt,
		LastSuccessAt: current.LastSuccessAt, NextAt: nextRun(now, current.LastAttemptAt), Schedule: current.Schedule,
		CreatedAt: current.CreatedAt,
		Runs:      orEmpty(runs), Parts: PartResults(current.Last.Report.Parts),
		Retention: RetentionView{
			Known: true, Applied: true, KeepAll: retention == Retention{}, Last: retention.Last, Daily: retention.Daily,
			Weekly: retention.Weekly, Monthly: retention.Monthly, Yearly: retention.Yearly,
		},
	}
	if current.Last.Report.Path != "" {
		added := current.Last.Report.BytesAdded
		protection.AddedBytes = &added
		report := current.Last.Report
		protection.Path = &PathView{
			Path: report.Path, Reason: report.Reason, SourceName: current.SourceMachineName, TargetName: current.TargetMachineName,
			TargetHost: report.TargetHost, TargetPort: report.TargetPort,
		}
	}
	return protection
}

// PartResults lit les parties d'un rapport d'agent déjà rejugé.
func PartResults(reports []agentwire.ResticPartReport) []PartResult {
	results := []PartResult{}
	for _, report := range reports {
		results = append(results, PartResult{
			Kind: report.Kind, Name: report.Name, State: report.State, Reason: report.Reason, BytesAdded: report.BytesAdded,
		})
	}
	return results
}

// resticState : l'ordre en cours, son échec ou son refus, sinon l'âge de la
// dernière réussite.
func resticState(current Backup, now time.Time) string {
	switch current.Last.State {
	case StateSent, StateRunning:
		return ProtectionRunning
	case StateFailed:
		return ProtectionFailed
	case StateRefused:
		return ProtectionRefused
	}
	if current.LastSuccessAt.IsZero() {
		return ProtectionNever
	}
	return freshness(current.LastSuccessAt, now)
}

// targetsOf : les cibles restic, puis les stockages de Proxmox et les
// destinations des plans, tels que l'inventaire les voit.
func targetsOf(inputs overviewInputs, sources []Source) []TargetView {
	targets := []TargetView{}
	for _, record := range inputs.targets {
		view := TargetView{
			Key: TargetKindRestic + ":" + record.MachineID, Kind: TargetKindRestic, Name: record.MachineName,
			MachineID: record.MachineID, MachineName: record.MachineName, Online: inputs.machineMap[record.MachineID].Online,
			State: record.Reported.State, Reason: record.Reported.Reason, Host: record.Requested.Host, Port: record.Requested.Port,
			Dir: record.Requested.Dir, Version: record.Reported.Version, Fingerprint: record.Reported.Fingerprint,
			DiskTotal: record.Reported.DiskTotal, DiskFree: record.Reported.DiskFree,
			DiskUsed: max(record.Reported.DiskTotal-record.Reported.DiskFree, 0), BackupBytes: record.Reported.RepoBytes,
			ReportedAt: record.ReportedAt, RetentionFloor: record.Reported.RetentionFloor,
			Holds: holdsOf(sources, func(p Protection) bool {
				return p.Origin == OriginRestic && p.TargetMachineID == record.MachineID
			}),
		}
		if view.State == "" {
			view.State, view.Reason = pendingState(inputs.machineMap[record.MachineID])
		}
		// Ce que rest-server annonce prime sur la demande, une fois rapporté.
		if record.Reported.Host != "" {
			view.Host, view.Port = record.Reported.Host, record.Reported.Port
		}
		targets = append(targets, view)
	}
	for _, item := range inputs.inventory.Targets {
		targets = append(targets, inventoryTarget(item, inputs, sources))
	}
	return targets
}

func inventoryTarget(item backup.Target, inputs overviewInputs, sources []Source) TargetView {
	view := TargetView{
		Key: item.Key, Kind: item.Kind, Name: item.Name, MachineID: item.MachineID, MachineName: item.MachineName,
		Online: inputs.machineMap[item.MachineID].Online, Dir: item.Path, Node: item.Node, Type: item.Type,
		DiskTotal: item.Total, DiskFree: item.Avail, DiskUsed: item.Used,
	}
	if item.Kind == backup.TargetStorage {
		view.Kind, view.State = TargetKindStorage, "inactive"
		if item.Active {
			view.State = "active"
		}
		view.Holds = holdsOf(sources, func(p Protection) bool {
			return p.Origin == OriginObserved && p.TargetMachineID == item.MachineID && (p.Storage == item.Name || p.ArchiveStorage == item.Name)
		})
		return view
	}
	view.Kind, view.State, view.Reason = TargetKindDestination, string(item.Right), item.RightReason
	view.Holds = holdsOf(sources, func(p Protection) bool {
		return p.Origin == OriginArchive && slices.Contains(item.Plans, p.ID)
	})
	return view
}

func holdsOf(sources []Source, match func(Protection) bool) []string {
	holds := []string{}
	for _, source := range sources {
		if slices.ContainsFunc(source.Protections, match) {
			holds = append(holds, source.Key)
		}
	}
	return holds
}

// pendingState : rien de rapporté par rest-server, mais l'agent dit déjà
// qu'il ne le trouve pas : l'écran dit quoi installer.
func pendingState(target machine.Status) (string, string) {
	right := RightOf(target, machine.CapabilityResticTarget)
	if right.State == RightUnavailable && right.Reason == agentwire.ReasonRestServerMissing {
		return agentwire.ResticTargetError, agentwire.ResticErrorBinaryMissing
	}
	return targetPending, ""
}

// fitsOf : pour chaque machine, les droits de restic (dont restic.docker,
// pour les volumes et les bases d'un service) et backup.produce tels que son
// agent les tient.
func fitsOf(inputs overviewInputs) []MachineFit {
	isTarget := map[string]bool{}
	for _, record := range inputs.targets {
		isTarget[record.MachineID] = true
	}
	fits := []MachineFit{}
	for _, status := range inputs.machines {
		jobsBlind, isProxmoxHost := inputs.jobsBlind[status.ID]
		produce := RightOf(status, machine.CapabilityResticProduce)
		fits = append(fits, MachineFit{
			MachineID: status.ID, MachineName: status.Name, OS: status.OS, Arch: status.Arch, Online: status.Online,
			ResticAvailable: produce.State == RightOK, IsTarget: isTarget[status.ID],
			ProxmoxHost: isProxmoxHost, ProxmoxJobsUnknown: jobsBlind,
			Rights: []RightFit{
				RightOf(status, machine.CapabilityResticTarget),
				produce,
				RightOf(status, machine.CapabilityResticDocker),
				RightOf(status, machine.CapabilityResticVerify),
				RightOf(status, machine.CapabilityBackupProduce),
			},
		})
	}
	return fits
}

func orEmpty[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}
