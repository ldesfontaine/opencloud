package server

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/backup"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
)

// L'historique des exécutions des plans par archives : une ligne par cible
// d'un passage. Lecture seule, ouverte aussi à un jeton d'API.
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
