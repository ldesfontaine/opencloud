package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/backup"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
)

// Ce que la page Sauvegardes lit : la clé (jamais sa part privée, ni la
// phrase), les destinations avec le droit de leur agent, le plan et son
// dernier ordre.
type backupsJSON struct {
	Keyring      *backupKeyringJSON      `json:"keyring"`
	Destinations []backupDestinationJSON `json:"destinations"`
	Plan         *backupPlanJSON         `json:"plan"`
}

type backupKeyringJSON struct {
	Recipient   string    `json:"recipient"`
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"created_at"`
}

type backupDestinationJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MachineID   string `json:"machine_id"`
	MachineName string `json:"machine_name"`
	Path        string `json:"path"`
	// ok, missing ou unknown : l'agent peut-il écrire dans ce dossier.
	Right     string    `json:"right"`
	CreatedAt time.Time `json:"created_at"`
}

type backupPlanJSON struct {
	DestinationID string     `json:"destination_id"`
	Keep          int        `json:"keep"`
	Schedule      string     `json:"schedule"`
	LastAttemptAt *time.Time `json:"last_attempt_at"`
	// La clé du refus de la dernière tentative qui n'a pas pu partir.
	LastError string `json:"last_error"`
	// Le dernier ordre backup.receive, null s'il n'y en a pas ou plus.
	LastRun *actionRunJSON `json:"last_run"`
}

type backupKeyringRequest struct {
	Passphrase string `json:"passphrase"`
	Replace    bool   `json:"replace"`
}

type backupDestinationRequest struct {
	Name      string `json:"name"`
	MachineID string `json:"machine_id"`
	Path      string `json:"path"`
}

type backupPlanRequest struct {
	DestinationID string `json:"destination_id"`
	Keep          int    `json:"keep"`
}

func backupKeyringToJSON(keyring backup.Keyring) backupKeyringJSON {
	return backupKeyringJSON{Recipient: keyring.Recipient, Fingerprint: keyring.Fingerprint, CreatedAt: keyring.CreatedAt.UTC()}
}

func backupDestinationToJSON(view backup.DestinationView) backupDestinationJSON {
	return backupDestinationJSON{
		ID: view.ID, Name: view.Name, MachineID: view.MachineID, MachineName: view.MachineName, Path: view.Path,
		Right: string(view.Right), CreatedAt: view.CreatedAt.UTC(),
	}
}

// backupPlanToJSON pose le dernier ordre du plan ; run est nil s'il n'y en a
// pas.
func backupPlanToJSON(plan backup.Plan, run *action.Run) backupPlanJSON {
	out := backupPlanJSON{
		DestinationID: plan.DestinationID, Keep: plan.Keep, Schedule: plan.Schedule,
		LastAttemptAt: httpx.TimeOrNil(plan.LastAttemptAt), LastError: plan.LastError,
	}
	if run != nil {
		converted := actionRunToJSON(*run)
		out.LastRun = &converted
	}
	return out
}

// lastBackupRun relit le dernier ordre du plan ; la purge des runs a pu
// l'emporter, ce n'est pas une erreur.
func (s *Server) lastBackupRun(ctx context.Context, plan backup.Plan) (*action.Run, error) {
	if plan.LastRunID == "" {
		return nil, nil
	}
	run, err := s.reader.GetActionRun(ctx, plan.LastRunID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *Server) getBackups(w http.ResponseWriter, r *http.Request) {
	overview, err := s.backups.Overview(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	response := backupsJSON{Destinations: orEmpty(httpx.ConvertAll(overview.Destinations, backupDestinationToJSON))}
	if overview.Keyring != nil {
		keyring := backupKeyringToJSON(*overview.Keyring)
		response.Keyring = &keyring
	}
	if overview.Plan != nil {
		run, err := s.lastBackupRun(r.Context(), *overview.Plan)
		if s.failed(w, r, err, nil) {
			return
		}
		plan := backupPlanToJSON(*overview.Plan, run)
		response.Plan = &plan
	}
	s.writeAPI(w, http.StatusOK, response)
}

// La phrase arrive ici et nulle part ailleurs : le composant la voit le
// temps de sceller la clé, rien ne l'écrit.
func (s *Server) createBackupKeyring(w http.ResponseWriter, r *http.Request) {
	var request backupKeyringRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	keyring, err := s.backups.CreateKeyring(r.Context(), request.Passphrase, request.Replace)
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusCreated, backupKeyringToJSON(keyring))
}

func (s *Server) createBackupDestination(w http.ResponseWriter, r *http.Request) {
	var request backupDestinationRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	created, err := s.backups.CreateDestination(r.Context(), backup.DestinationDefinition(request))
	if s.failed(w, r, err, nil) {
		return
	}
	// Le droit se relit avec la liste : la création rend la destination telle
	// que la liste la montrerait.
	overview, err := s.backups.Overview(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	for _, view := range overview.Destinations {
		if view.ID == created.ID {
			s.writeAPI(w, http.StatusCreated, backupDestinationToJSON(view))
			return
		}
	}
	s.writeAPI(w, http.StatusCreated, backupDestinationToJSON(backup.DestinationView{Destination: created, Right: backup.RightUnknown}))
}

func (s *Server) deleteBackupDestination(w http.ResponseWriter, r *http.Request) {
	s.finishAPIAction(w, r, s.backups.DeleteDestination(r.Context(), r.PathValue("id")), backup.ErrNotFound)
}

func (s *Server) saveBackupPlan(w http.ResponseWriter, r *http.Request) {
	var request backupPlanRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	plan, err := s.backups.SavePlan(r.Context(), backup.PlanDefinition(request))
	if s.failed(w, r, err, nil) {
		return
	}
	run, err := s.lastBackupRun(r.Context(), plan)
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, backupPlanToJSON(plan, run))
}

func (s *Server) deleteBackupPlan(w http.ResponseWriter, r *http.Request) {
	s.finishAPIAction(w, r, s.backups.DeletePlan(r.Context()), backup.ErrNotFound)
}

// runBackup lance la sauvegarde tout de suite ; la réponse est l'ordre
// backup.receive, dont l'état avance ensuite par le direct.
func (s *Server) runBackup(w http.ResponseWriter, r *http.Request) {
	var request struct{}
	if !s.readAPI(w, r, &request) {
		return
	}
	run, err := s.backups.RunNow(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusCreated, actionRunToJSON(run))
}
