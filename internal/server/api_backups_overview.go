package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/anchor"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/restic"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
)

// L'aperçu de la page Sauvegardes : un modèle de lecture pour ses cartes.
// Ce qui est sauvegardé (et comment : vu chez Proxmox, plan par archives,
// restic), où ça va, et ce que chaque machine peut faire. Des faits : instants
// ISO, octets, mots fermés ; l'écran compose « pour passer en direct : … ».
type backupOverviewJSON struct {
	Sources  []overviewSourceJSON  `json:"sources"`
	Targets  []overviewTargetJSON  `json:"targets"`
	Machines []overviewMachineJSON `json:"machines"`
}

type overviewSourceJSON struct {
	Key string `json:"key"`
	// database, vm, lxc, service ou docker_volume.
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	MachineID   string `json:"machine_id"`
	MachineName string `json:"machine_name"`
	// Le numéro d'un invité ; null pour les autres.
	VMID      *int     `json:"vmid"`
	ServiceID *string  `json:"service_id"`
	Driver    string   `json:"driver"`
	UsedBy    []string `json:"used_by"`
	// Vrai quand l'hôte n'a pas pu lire des archives ou ses tâches faute de
	// droit : l'écran dit « inconnu », jamais « aucune ».
	ObservedUnknown bool `json:"observed_unknown"`
	// Portée service : ce que « le service entier » prendra ; [] sinon.
	Parts       []overviewSourcePartJSON `json:"parts"`
	Protections []overviewProtectionJSON `json:"protections"`
}

// Une partie d'un service : folder (name vide), volume (son nom) ou database
// (le nom du conteneur, et son moteur : postgres, mariadb, mysql, mongo).
type overviewSourcePartJSON struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Engine string `json:"engine"`
}

// Ce qu'une partie a donné : ok, failed ou skipped ; reason est un mot fermé.
type overviewPartResultJSON struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	State      string `json:"state"`
	Reason     string `json:"reason"`
	BytesAdded int64  `json:"bytes_added"`
}

type overviewProtectionJSON struct {
	// observed, archive ou restic.
	Origin string `json:"origin"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	// restic : vm ou service ; vide sinon.
	Scope             string `json:"scope"`
	TargetMachineID   string `json:"target_machine_id"`
	TargetMachineName string `json:"target_machine_name"`
	Storage           string `json:"storage"`
	// Vue chez Proxmox : le stockage de la plus récente archive.
	ArchiveStorage string `json:"archive_storage"`
	// ok, late, failed, refused, running, never ou unknown ; reason est un mot
	// fermé ou la clé d'un refus.
	State  string     `json:"state"`
	Reason string     `json:"reason"`
	LastAt *time.Time `json:"last_at"`
	// La dernière réussite, même quand le dernier essai a échoué ; null
	// avant la première.
	LastSuccessAt *time.Time            `json:"last_success_at"`
	NextAt        *time.Time            `json:"next_at"`
	Schedule      string                `json:"schedule"`
	SizeBytes     *int64                `json:"size_bytes"`
	AddedBytes    *int64                `json:"added_bytes"`
	Copies        *int                  `json:"copies"`
	Retention     overviewRetentionJSON `json:"retention"`
	// restic : le chemin de la dernière exécution ; null avant la première.
	Path *overviewPathJSON `json:"path"`
	Runs []resticRunJSON   `json:"runs"`
	// restic, portée service : les parties du dernier passage ; [] sinon.
	Parts []overviewPartResultJSON `json:"parts"`
	// restic : la vérification du dépôt par sa cible ; null pour les autres.
	Verify *overviewVerifyJSON `json:"verify"`
	// Quand reason est un refus d'ancre de confiance locale : la commande à lancer
	// en root sur la machine source, et si elle pose la valeur de l'ordre (faux :
	// « trust list »). Vide sinon.
	FixCommand string `json:"fix_command,omitempty"`
	FixExact   bool   `json:"fix_exact,omitempty"`
}

// La vérification du dépôt d'une sauvegarde restic : state est sent,
// succeeded, failed ou refused, vide si jamais tentée ; reason un mot fermé ou
// la clé d'un refus. snapshots, files et bytes sont des comptes : jamais un
// nom ni un contenu. right dit si la cible peut vérifier (ok, missing,
// unavailable, unknown) ; stale, que le dernier test réussi est trop ancien.
type overviewVerifyJSON struct {
	State       string     `json:"state"`
	Reason      string     `json:"reason"`
	At          *time.Time `json:"at"`
	SuccessAt   *time.Time `json:"success_at"`
	Snapshots   int        `json:"snapshots"`
	Files       int        `json:"files"`
	Bytes       int64      `json:"bytes"`
	Right       string     `json:"right"`
	RightReason string     `json:"right_reason"`
	Stale       bool       `json:"stale"`
}

// La rétention en clair : known faux, inconnue ; applied faux, rien ne la
// fait respecter. Restic : last_forget, la dernière purge du dépôt par la
// cible, null avant la première.
type overviewRetentionJSON struct {
	Known       bool            `json:"known"`
	Applied     bool            `json:"applied"`
	KeepAll     bool            `json:"keep_all"`
	KeepLast    int             `json:"keep_last"`
	KeepDaily   int             `json:"keep_daily"`
	KeepWeekly  int             `json:"keep_weekly"`
	KeepMonthly int             `json:"keep_monthly"`
	KeepYearly  int             `json:"keep_yearly"`
	LastForget  *lastForgetJSON `json:"last_forget"`
}

// La dernière rétention d'un dépôt : sent, succeeded, failed ou refused ;
// reason est un mot fermé (retention_below_floor…) ou la clé d'un refus.
type lastForgetJSON struct {
	At     *time.Time `json:"at"`
	State  string     `json:"state"`
	Reason string     `json:"reason"`
}

// Le plancher de rétention que la cible tient.
type retentionFloorJSON struct {
	KeepDaily   int `json:"keep_daily"`
	KeepWeekly  int `json:"keep_weekly"`
	KeepMonthly int `json:"keep_monthly"`
}

// Le chemin d'une exécution restic : direct ou relay, sa raison, et pour
// « ce qu'il faut ouvrir » : source_name doit joindre target_host:target_port.
type overviewPathJSON struct {
	Path       string `json:"path"`
	Reason     string `json:"reason"`
	SourceName string `json:"source_name"`
	TargetName string `json:"target_name"`
	TargetHost string `json:"target_host"`
	TargetPort int    `json:"target_port"`
}

type resticRunJSON struct {
	RunID           string    `json:"run_id"`
	FinishedAt      time.Time `json:"finished_at"`
	State           string    `json:"state"`
	Reason          string    `json:"reason"`
	Path            string    `json:"path"`
	PathReason      string    `json:"path_reason"`
	BytesAdded      int64     `json:"bytes_added"`
	DurationSeconds int64     `json:"duration_seconds"`
	// Portée service : ce que chaque partie a donné à ce passage ; [] sinon.
	Parts []overviewPartResultJSON `json:"parts"`
}

type overviewTargetJSON struct {
	Key string `json:"key"`
	// restic, storage ou destination.
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	MachineID   string `json:"machine_id"`
	MachineName string `json:"machine_name"`
	Online      bool   `json:"online"`
	// restic : running, stopped, error (reason : binary_missing quand
	// rest-server manque), pending ; storage : active, inactive ;
	// destination : ok, missing, unavailable, unknown (le droit de l'agent).
	State       string     `json:"state"`
	Reason      string     `json:"reason"`
	Host        string     `json:"host"`
	Port        int        `json:"port"`
	Dir         string     `json:"dir"`
	Version     string     `json:"version"`
	Fingerprint string     `json:"fingerprint"`
	DiskTotal   int64      `json:"disk_total_bytes"`
	DiskFree    int64      `json:"disk_free_bytes"`
	DiskUsed    int64      `json:"disk_used_bytes"`
	BackupBytes int64      `json:"backup_bytes"`
	ReportedAt  *time.Time `json:"reported_at"`
	// Restic : null tant que la cible ne l'a pas rapporté.
	RetentionFloor *retentionFloorJSON `json:"retention_floor"`
	Node           string              `json:"node"`
	Type           string              `json:"type"`
	Holds          []string            `json:"holds"`
}

type overviewMachineJSON struct {
	MachineID   string `json:"machine_id"`
	MachineName string `json:"machine_name"`
	// Le système et l'architecture enrôlés : l'écran compose la commande
	// d'installation de restic ou de rest-server.
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Online bool   `json:"online"`
	// restic.produce coché et restic trouvé ; sinon la raison est dans rights.
	ResticAvailable bool `json:"restic_available"`
	IsTarget        bool `json:"is_target"`
	ProxmoxHost     bool `json:"proxmox_host"`
	// Le jeton de l'hôte ne lit pas /cluster/backup : il lui faut Sys.Audit sur « / ».
	ProxmoxJobsUnknown bool                `json:"proxmox_jobs_unknown"`
	Rights             []overviewRightJSON `json:"rights"`
}

// Un droit sur l'agent : ok, missing, unavailable (raison) ou unknown ; path
// est le dossier de restic.target.
type overviewRightJSON struct {
	Right  string `json:"right"`
	State  string `json:"state"`
	Reason string `json:"reason"`
	Path   string `json:"path"`
}

// getBackupOverview rend l'aperçu calculé de ce que le serveur a déjà :
// aucun appel à une machine ni à Proxmox.
func (s *Server) getBackupOverview(w http.ResponseWriter, r *http.Request) {
	overview, err := s.restic.Overview(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	recipient := s.keyringRecipient(r.Context())
	s.writeAPI(w, http.StatusOK, backupOverviewJSON{
		Sources:  httpx.ConvertAll(overview.Sources, func(source restic.Source) overviewSourceJSON { return overviewSourceToJSON(source, recipient) }),
		Targets:  httpx.ConvertAll(overview.Targets, overviewTargetToJSON),
		Machines: httpx.ConvertAll(overview.Machines, overviewMachineToJSON),
	})
}

// keyringRecipient rend la clé publique du trousseau, vide sans trousseau : la
// commande qui pose l'ancre d'une sauvegarde de VM la porte.
func (s *Server) keyringRecipient(ctx context.Context) string {
	overview, err := s.backups.Overview(ctx)
	if err != nil || overview.Keyring == nil {
		return ""
	}
	return overview.Keyring.Recipient
}

func overviewSourceToJSON(source restic.Source, recipient string) overviewSourceJSON {
	out := overviewSourceJSON{
		Key: source.Key, Kind: source.Kind, Name: source.Name, MachineID: source.MachineID, MachineName: source.MachineName,
		ServiceID: stringOrNil(source.ServiceID), Driver: source.Driver, UsedBy: orEmpty(source.UsedBy),
		ObservedUnknown: source.ObservedUnknown, Protections: httpx.ConvertAll(source.Protections, func(protection restic.Protection) overviewProtectionJSON {
			return overviewProtectionToJSON(protection, source, recipient)
		}),
		Parts: httpx.ConvertAll(source.Parts, func(part restic.SourcePart) overviewSourcePartJSON {
			return overviewSourcePartJSON{Kind: part.Kind, Name: part.Name, Engine: part.Engine}
		}),
	}
	if source.VMID != 0 {
		vmid := source.VMID
		out.VMID = &vmid
	}
	return out
}

// protectionFix rend la commande qui lève le refus d'ancre qu'une protection
// porte comme raison. Le plan par archives a refusé un ordre backup.produce dont
// les paramètres se refont : la VM de la source, la clé du trousseau. Les autres
// origines (restic) n'ont pas la valeur : « trust list ».
func protectionFix(protection restic.Protection, source restic.Source, recipient string) (command string, exact bool) {
	name, ok := agentwire.AnchorOfReason(protection.Reason)
	if !ok {
		return "", false
	}
	if protection.Origin != restic.OriginArchive || source.VMID == 0 {
		return anchor.FixFor(name, "", nil)
	}
	params, err := json.Marshal(agentwire.BackupProduceParams{
		Producer: machine.ProducerProxmoxVzdump, Target: strconv.Itoa(source.VMID), Recipient: recipient,
	})
	if err != nil {
		return anchor.FixFor(name, "", nil)
	}
	return anchor.FixFor(name, string(agentwire.KindBackupProduce), params)
}

func overviewProtectionToJSON(protection restic.Protection, source restic.Source, recipient string) overviewProtectionJSON {
	fix, exact := protectionFix(protection, source, recipient)
	retention := protection.Retention
	out := overviewProtectionJSON{
		FixCommand: fix, FixExact: exact,
		Origin: protection.Origin, ID: protection.ID, Name: protection.Name, Scope: protection.Scope,
		TargetMachineID: protection.TargetMachineID, TargetMachineName: protection.TargetMachineName, Storage: protection.Storage,
		ArchiveStorage: protection.ArchiveStorage, State: protection.State, Reason: protection.Reason, LastAt: httpx.TimeOrNil(protection.LastAt),
		LastSuccessAt: httpx.TimeOrNil(protection.LastSuccessAt), NextAt: httpx.TimeOrNil(protection.NextAt),
		Schedule: protection.Schedule, SizeBytes: protection.SizeBytes,
		AddedBytes: protection.AddedBytes, Copies: protection.Copies,
		Retention: overviewRetentionJSON{
			Known: retention.Known, Applied: retention.Applied, KeepAll: retention.KeepAll, KeepLast: retention.Last,
			KeepDaily: retention.Daily, KeepWeekly: retention.Weekly, KeepMonthly: retention.Monthly, KeepYearly: retention.Yearly,
		},
		Runs:  httpx.ConvertAll(protection.Runs, resticRunToJSON),
		Parts: httpx.ConvertAll(protection.Parts, partResultToJSON),
	}
	if forget := retention.LastForget; forget != nil {
		out.Retention.LastForget = &lastForgetJSON{At: httpx.TimeOrNil(forget.At), State: forget.State, Reason: forget.Reason}
	}
	if verify := protection.Verify; verify != nil {
		out.Verify = &overviewVerifyJSON{
			State: verify.State, Reason: verify.Reason, At: httpx.TimeOrNil(verify.At), SuccessAt: httpx.TimeOrNil(verify.SuccessAt),
			Snapshots: verify.Snapshots, Files: verify.Files, Bytes: verify.Bytes,
			Right: string(verify.Right.State), RightReason: verify.Right.Reason, Stale: verify.IsStale,
		}
	}
	if path := protection.Path; path != nil {
		out.Path = &overviewPathJSON{
			Path: path.Path, Reason: path.Reason, SourceName: path.SourceName, TargetName: path.TargetName,
			TargetHost: path.TargetHost, TargetPort: path.TargetPort,
		}
	}
	return out
}

func resticRunToJSON(run restic.RunRecord) resticRunJSON {
	return resticRunJSON{
		RunID: run.RunID, FinishedAt: run.FinishedAt.UTC(), State: run.State, Reason: run.Reason, Path: run.Path,
		PathReason: run.PathReason, BytesAdded: run.BytesAdded, DurationSeconds: run.DurationSeconds,
		Parts: httpx.ConvertAll(run.Parts, partResultToJSON),
	}
}

func partResultToJSON(part restic.PartResult) overviewPartResultJSON {
	return overviewPartResultJSON{Kind: part.Kind, Name: part.Name, State: part.State, Reason: part.Reason, BytesAdded: part.BytesAdded}
}

func overviewTargetToJSON(target restic.TargetView) overviewTargetJSON {
	out := overviewTargetJSON{
		Key: target.Key, Kind: target.Kind, Name: target.Name, MachineID: target.MachineID, MachineName: target.MachineName,
		Online: target.Online, State: target.State, Reason: target.Reason, Host: target.Host, Port: target.Port, Dir: target.Dir,
		Version: target.Version, Fingerprint: target.Fingerprint, DiskTotal: target.DiskTotal, DiskFree: target.DiskFree,
		DiskUsed: target.DiskUsed, BackupBytes: target.BackupBytes,
		ReportedAt: httpx.TimeOrNil(target.ReportedAt), Node: target.Node, Type: target.Type, Holds: orEmpty(target.Holds),
	}
	if floor := target.RetentionFloor; floor != nil {
		out.RetentionFloor = &retentionFloorJSON{KeepDaily: floor.KeepDaily, KeepWeekly: floor.KeepWeekly, KeepMonthly: floor.KeepMonthly}
	}
	return out
}

func overviewMachineToJSON(fit restic.MachineFit) overviewMachineJSON {
	return overviewMachineJSON{
		MachineID: fit.MachineID, MachineName: fit.MachineName, OS: fit.OS, Arch: fit.Arch, Online: fit.Online,
		ResticAvailable: fit.ResticAvailable, IsTarget: fit.IsTarget,
		ProxmoxHost: fit.ProxmoxHost, ProxmoxJobsUnknown: fit.ProxmoxJobsUnknown,
		Rights: httpx.ConvertAll(fit.Rights, func(right restic.RightFit) overviewRightJSON {
			return overviewRightJSON{Right: right.Right, State: string(right.State), Reason: right.Reason, Path: right.Path}
		}),
	}
}
