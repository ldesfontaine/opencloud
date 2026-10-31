package server

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/backup"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
)

// Ce que la section Inventaire de la page Sauvegardes lit : ce qui existe à
// sauvegarder, où cela peut aller, et ce qui s'est passé. Lecture seule.
type backupInventoryJSON struct {
	Sources []backupSourceJSON `json:"sources"`
	Targets []backupTargetJSON `json:"targets"`
}

type backupSourceJSON struct {
	// La clé d'une source : celle que les lignes de l'historique portent.
	Key string `json:"key"`
	// database, vm, lxc ou docker_volume.
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	MachineID   string `json:"machine_id"`
	MachineName string `json:"machine_name"`
	// Le numéro de l'invité ; vide pour la base et un volume.
	ID     string   `json:"id"`
	Driver string   `json:"driver"`
	UsedBy []string `json:"used_by"`
	// Les plans d'openCloud qui la couvrent aujourd'hui.
	CoveredBy []backupCoverageJSON `json:"covered_by"`
	// Ce que les stockages de Proxmox gardent d'elle, hors des plans
	// d'openCloud ; null sinon.
	Observed *backupObservedJSON `json:"observed"`
	// La dernière exécution d'un plan d'openCloud sur la source, et la dernière
	// réussie ; null tant qu'aucune n'a eu lieu.
	LastRun *backupRunJSON `json:"last_run"`
	LastOK  *backupRunJSON `json:"last_ok"`
}

type backupCoverageJSON struct {
	Subject         string `json:"subject"`
	PlanName        string `json:"plan_name"`
	DestinationID   string `json:"destination_id"`
	DestinationName string `json:"destination_name"`
}

type backupObservedJSON struct {
	Storage    string    `json:"storage"`
	Count      int       `json:"count"`
	TotalBytes int64     `json:"total_bytes"`
	LastAt     time.Time `json:"last_at"`
	LastBytes  int64     `json:"last_bytes"`
}

type backupTargetJSON struct {
	Key         string `json:"key"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	MachineID   string `json:"machine_id"`
	MachineName string `json:"machine_name"`
	// Destination : son dossier et le droit de l'agent (ok, missing,
	// unavailable, unknown).
	Path        string `json:"path"`
	Right       string `json:"right,omitempty"`
	RightReason string `json:"right_reason,omitempty"`
	// Stockage Proxmox : son nœud, son type et sa capacité.
	Node   string   `json:"node"`
	Type   string   `json:"type"`
	Total  int64    `json:"total_bytes"`
	Used   int64    `json:"used_bytes"`
	Avail  int64    `json:"avail_bytes"`
	Active bool     `json:"active"`
	Plans  []string `json:"plans"`
}

type backupRunsJSON struct {
	Runs []backupRunJSON `json:"runs"`
}

type backupRunJSON struct {
	Plan       string    `json:"plan"`
	Source     string    `json:"source"`
	PassAt     time.Time `json:"pass_at"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	// ok, failed ou refused ; reason est un mot fermé ou la clé d'un refus.
	Result          string `json:"result"`
	Reason          string `json:"reason"`
	SizeBytes       *int64 `json:"size_bytes"`
	DestinationID   string `json:"destination_id"`
	DestinationName string `json:"destination_name"`
	ActionRunID     string `json:"action_run_id"`
}

func backupRunToJSON(record backup.RunRecord) backupRunJSON {
	return backupRunJSON{
		Plan: record.Subject, Source: record.Source, PassAt: record.PassAt.UTC(), StartedAt: record.StartedAt.UTC(),
		FinishedAt: record.FinishedAt.UTC(), Result: string(record.Result), Reason: record.Reason, SizeBytes: record.SizeBytes,
		DestinationID: record.DestinationID, DestinationName: record.DestinationName, ActionRunID: record.ActionRunID,
	}
}

func optionalBackupRun(record *backup.RunRecord) *backupRunJSON {
	if record == nil {
		return nil
	}
	converted := backupRunToJSON(*record)
	return &converted
}

func backupSourceToJSON(source backup.Source) backupSourceJSON {
	out := backupSourceJSON{
		Key: source.Key, Kind: source.Kind, Name: source.Name, MachineID: source.MachineID, MachineName: source.MachineName,
		ID: source.ID, Driver: source.Driver, UsedBy: orEmpty(source.UsedBy), LastRun: optionalBackupRun(source.LastRun),
		LastOK: optionalBackupRun(source.LastOK),
		CoveredBy: orEmpty(httpx.ConvertAll(source.CoveredBy, func(coverage backup.Coverage) backupCoverageJSON {
			return backupCoverageJSON(coverage)
		})),
	}
	if source.Observed != nil {
		out.Observed = &backupObservedJSON{
			Storage: source.Observed.Storage, Count: source.Observed.Count, TotalBytes: source.Observed.TotalBytes,
			LastAt: source.Observed.LastAt.UTC(), LastBytes: source.Observed.LastBytes,
		}
	}
	return out
}

func backupTargetToJSON(target backup.Target) backupTargetJSON {
	return backupTargetJSON{
		Key: target.Key, Kind: target.Kind, Name: target.Name, MachineID: target.MachineID, MachineName: target.MachineName,
		Path: target.Path, Right: string(target.Right), RightReason: target.RightReason, Node: target.Node, Type: target.Type,
		Total: target.Total, Used: target.Used, Avail: target.Avail, Active: target.Active, Plans: orEmpty(target.Plans),
	}
}

// getBackupInventory rend l'inventaire calculé de ce que le serveur a déjà :
// aucun appel à une machine ni à Proxmox.
func (s *Server) getBackupInventory(w http.ResponseWriter, r *http.Request) {
	inventory, err := s.backups.Inventory(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, backupInventoryJSON{
		Sources: httpx.ConvertAll(inventory.Sources, backupSourceToJSON),
		Targets: httpx.ConvertAll(inventory.Targets, backupTargetToJSON),
	})
}

// getBackupRuns rend l'historique des exécutions, les plus récentes d'abord,
// d'un plan et d'une source si on les nomme.
func (s *Server) getBackupRuns(w http.ResponseWriter, r *http.Request) {
	query, ok := backupRunQueryOf(r.URL.Query())
	if !ok {
		s.writeAPIError(w, http.StatusBadRequest, codeBadQuery)
		return
	}
	records, err := s.backups.Runs(r.Context(), query)
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, backupRunsJSON{Runs: httpx.ConvertAll(records, backupRunToJSON)})
}

func backupRunQueryOf(values url.Values) (backup.RunQuery, bool) {
	query := backup.RunQuery{Subject: values.Get("plan"), Source: values.Get("source"), Limit: backup.DefaultRunsLimit}
	if text := values.Get("limit"); text != "" {
		limit, err := strconv.Atoi(text)
		if err != nil || limit < 1 {
			return backup.RunQuery{}, false
		}
		query.Limit = min(limit, backup.MaxRunsPerPlan)
	}
	return query, true
}
