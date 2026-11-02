package server

import (
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

// Ce que l'opérateur saisit pour faire d'une machine une cible restic.
type resticTargetRequest struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	// Vide : le dossier du droit restic.target.
	Dir string `json:"dir"`
	// Absent : la cible garde son plancher (sept nuits à la première fois).
	RetentionFloor *resticFloorRequest `json:"retention_floor"`
}

// Le plancher de rétention de la cible : elle refuse toute rétention qui
// garderait moins.
type resticFloorRequest struct {
	KeepDaily   int `json:"keep_daily"`
	KeepWeekly  int `json:"keep_weekly"`
	KeepMonthly int `json:"keep_monthly"`
}

func (r *resticFloorRequest) floor() *agentwire.ResticRetentionFloor {
	if r == nil {
		return nil
	}
	return &agentwire.ResticRetentionFloor{KeepDaily: r.KeepDaily, KeepWeekly: r.KeepWeekly, KeepMonthly: r.KeepMonthly}
}

// La rétention d'une sauvegarde restic, que la cible applique après la nuit.
type resticRetentionRequest struct {
	KeepLast    int `json:"keep_last"`
	KeepDaily   int `json:"keep_daily"`
	KeepWeekly  int `json:"keep_weekly"`
	KeepMonthly int `json:"keep_monthly"`
	KeepYearly  int `json:"keep_yearly"`
}

func (r resticRetentionRequest) retention() restic.Retention {
	return restic.Retention{Last: r.KeepLast, Daily: r.KeepDaily, Weekly: r.KeepWeekly, Monthly: r.KeepMonthly, Yearly: r.KeepYearly}
}

// Une sauvegarde restic, de la machine de la route vers une cible.
type resticBackupRequest struct {
	Name  string `json:"name"`
	Scope string `json:"scope"`
	// Portée vm : le numéro de l'invité ; portée service : le service déclaré.
	VMID            int    `json:"vmid"`
	ServiceID       string `json:"service_id"`
	TargetMachineID string `json:"target_machine_id"`
	// Absente : quatorze nuits, huit semaines, trois mois.
	Retention *resticRetentionRequest `json:"retention"`
}

type resticBackupChangeRequest struct {
	Name      string                 `json:"name"`
	Retention resticRetentionRequest `json:"retention"`
}

// La sauvegarde créée : son identifiant, que la route de ses gestes nomme.
type resticBackupCreatedJSON struct {
	ID string `json:"id"`
}

// setupResticTarget fait de la machine une cible : l'ordre part, l'état de
// rest-server arrive par le signal de l'agent.
func (s *Server) setupResticTarget(w http.ResponseWriter, r *http.Request) {
	var request resticTargetRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	setup := restic.TargetSetup{Host: request.Host, Port: request.Port, Dir: request.Dir, Floor: request.RetentionFloor.floor()}
	_, err := s.restic.SetupTarget(r.Context(), r.PathValue("id"), setup)
	s.finishAPIAction(w, r, err, nil)
}

func (s *Server) removeResticTarget(w http.ResponseWriter, r *http.Request) {
	var request struct{}
	if !s.readAPI(w, r, &request) {
		return
	}
	s.finishAPIAction(w, r, s.restic.RemoveTarget(r.Context(), r.PathValue("id")), nil)
}

func (s *Server) createResticBackup(w http.ResponseWriter, r *http.Request) {
	var request resticBackupRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	retention := restic.DefaultRetention()
	if request.Retention != nil {
		retention = request.Retention.retention()
	}
	created, err := s.restic.CreateBackup(r.Context(), restic.Definition{
		Name: request.Name, SourceMachineID: r.PathValue("id"), Scope: request.Scope, VMID: request.VMID,
		ServiceID: request.ServiceID, TargetMachineID: request.TargetMachineID, Retention: retention,
	})
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusCreated, resticBackupCreatedJSON{ID: created.ID})
}

func (s *Server) updateResticBackup(w http.ResponseWriter, r *http.Request) {
	var request resticBackupChangeRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	_, err := s.restic.UpdateBackup(r.Context(), r.PathValue("id"), r.PathValue("backup"), request.Name, request.Retention.retention())
	s.finishAPIAction(w, r, err, restic.ErrBackupNotFound)
}

func (s *Server) deleteResticBackup(w http.ResponseWriter, r *http.Request) {
	err := s.restic.DeleteBackup(r.Context(), r.PathValue("id"), r.PathValue("backup"))
	s.finishAPIAction(w, r, err, restic.ErrBackupNotFound)
}

func (s *Server) runResticBackup(w http.ResponseWriter, r *http.Request) {
	var request struct{}
	if !s.readAPI(w, r, &request) {
		return
	}
	run, err := s.restic.RunNow(r.Context(), r.PathValue("id"), r.PathValue("backup"))
	if s.failed(w, r, err, restic.ErrBackupNotFound) {
		return
	}
	s.writeAPI(w, http.StatusCreated, actionRunToJSON(run))
}

// verifyResticBackup fait vérifier le dépôt de la sauvegarde par sa cible
// (restic.verify) : l'ordre part, ses comptes arrivent par le signal de la
// cible.
func (s *Server) verifyResticBackup(w http.ResponseWriter, r *http.Request) {
	var request struct{}
	if !s.readAPI(w, r, &request) {
		return
	}
	run, err := s.restic.VerifyNow(r.Context(), r.PathValue("id"), r.PathValue("backup"))
	if s.failed(w, r, err, restic.ErrBackupNotFound) {
		return
	}
	s.writeAPI(w, http.StatusCreated, actionRunToJSON(run))
}

// browseResticBackup fait lire le dépôt de la sauvegarde par sa cible : les
// instantanés (corps vide), ou le contenu d'un dossier d'un instantané. La
// requête attend la cible ; rien n'est gardé.
func (s *Server) browseResticBackup(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Snapshot string `json:"snapshot"`
		Path     string `json:"path"`
	}
	if !s.readAPI(w, r, &request) {
		return
	}
	report, err := s.restic.Browse(r.Context(), r.PathValue("id"), r.PathValue("backup"), restic.BrowseRequest{Snapshot: request.Snapshot, Path: request.Path})
	if s.failed(w, r, err, restic.ErrBackupNotFound) {
		return
	}
	s.writeAPI(w, http.StatusOK, browseReportToJSON(report))
}

// browseReportToJSON : des listes toujours présentes, jamais null.
func browseReportToJSON(report agentwire.ResticBrowseReport) map[string]any {
	snapshots, entries := report.Snapshots, report.Entries
	if snapshots == nil {
		snapshots = []agentwire.BrowseSnapshot{}
	}
	if entries == nil {
		entries = []agentwire.BrowseEntry{}
	}
	return map[string]any{"snapshots": snapshots, "entries": entries, "truncated": report.Truncated}
}

// restoreResticBackup envoie à la source l'ordre de restaurer un chemin d'un
// instantané à côté de l'original. Le résultat arrive par le signal de la
// source : l'écran suit l'ordre rendu.
func (s *Server) restoreResticBackup(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Snapshot string `json:"snapshot"`
		Path     string `json:"path"`
		Stamp    string `json:"stamp"`
	}
	if !s.readAPI(w, r, &request) {
		return
	}
	run, err := s.restic.Restore(r.Context(), r.PathValue("id"), r.PathValue("backup"), restic.RestoreRequest{Snapshot: request.Snapshot, Path: request.Path, Stamp: request.Stamp})
	if s.failed(w, r, err, restic.ErrBackupNotFound) {
		return
	}
	s.writeAPI(w, http.StatusCreated, actionRunToJSON(run))
}
