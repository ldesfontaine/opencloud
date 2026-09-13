package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

const (
	jobRunsShown  = 20
	jobPingsShown = 20
)

// Une tâche telle que le front la lit : états, durées en secondes,
// instants ; les libellés et les temps relatifs se font dans le navigateur.
type jobJSON struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	MachineID       string     `json:"machine_id"`
	MachineName     string     `json:"machine_name"`
	Status          string     `json:"status"`
	IntervalSeconds int        `json:"interval_seconds"`
	GraceSeconds    int        `json:"grace_seconds"`
	LastPingAt      *time.Time `json:"last_ping_at"`
	NextDeadlineAt  *time.Time `json:"next_deadline_at"`
	RunStartedAt    *time.Time `json:"run_started_at"`
	LastExitCode    *int       `json:"last_exit_code"`
	LastDurationMs  *int64     `json:"last_duration_ms"`
	CreatedAt       time.Time  `json:"created_at"`
}

type runJSON struct {
	ID          int64      `json:"id"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
	DurationMs  *int64     `json:"duration_ms"`
	ExitCode    *int       `json:"exit_code"`
	Outcome     string     `json:"outcome"`
	Payload     string     `json:"payload"`
}

type pingJSON struct {
	ID         int64     `json:"id"`
	Kind       string    `json:"kind"`
	ExitCode   *int      `json:"exit_code"`
	Source     string    `json:"source"`
	Method     string    `json:"method"`
	ReceivedAt time.Time `json:"received_at"`
}

type jobsResponse struct {
	Jobs []jobJSON `json:"jobs"`
}

type jobRequest struct {
	Name            string `json:"name"`
	MachineID       string `json:"machine_id"`
	IntervalMinutes int    `json:"interval_minutes"`
	GraceMinutes    int    `json:"grace_minutes"`
}

// Un extrait à coller : la clé nomme sa forme, le front met le libellé.
type snippetJSON struct {
	Key  string `json:"key"`
	Code string `json:"code"`
}

type jobResponse struct {
	Job      jobJSON       `json:"job"`
	PingURL  string        `json:"ping_url"`
	URLLocal bool          `json:"url_local"`
	Snippets []snippetJSON `json:"snippets"`
	Runs     []runJSON     `json:"runs"`
	Pings    []pingJSON    `json:"pings"`
}

func jobToJSON(found heartbeat.Heartbeat) jobJSON {
	return jobJSON{
		ID:              found.ID,
		Name:            found.Name,
		MachineID:       found.MachineID,
		MachineName:     found.MachineName,
		Status:          string(found.Status),
		IntervalSeconds: int(found.Interval.Seconds()),
		GraceSeconds:    int(found.Grace.Seconds()),
		LastPingAt:      timeOrNil(found.LastPingAt),
		NextDeadlineAt:  timeOrNil(found.NextDeadlineAt),
		RunStartedAt:    timeOrNil(found.RunStartedAt),
		LastExitCode:    found.LastExitCode,
		LastDurationMs:  durationMs(found.LastDuration),
		CreatedAt:       found.CreatedAt.UTC(),
	}
}

func durationMs(d *time.Duration) *int64 {
	if d == nil {
		return nil
	}
	ms := d.Milliseconds()
	return &ms
}

func runToJSON(run heartbeat.Run) runJSON {
	return runJSON{
		ID:          run.ID,
		StartedAt:   timeOrNil(run.StartedAt),
		CompletedAt: timeOrNil(run.CompletedAt),
		DurationMs:  durationMs(run.Duration),
		ExitCode:    run.ExitCode,
		Outcome:     string(run.Outcome),
		Payload:     run.Payload,
	}
}

func pingToJSON(ping heartbeat.Ping) pingJSON {
	return pingJSON{
		ID:         ping.ID,
		Kind:       string(ping.Kind),
		ExitCode:   ping.ExitCode,
		Source:     ping.Source,
		Method:     ping.Method,
		ReceivedAt: ping.ReceivedAt.UTC(),
	}
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	heartbeats, err := s.heartbeats.List(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	response := jobsResponse{Jobs: make([]jobJSON, 0, len(heartbeats))}
	for _, found := range heartbeats {
		response.Jobs = append(response.Jobs, jobToJSON(found))
	}
	s.writeAPI(w, http.StatusOK, response)
}

// L'opérateur nomme la tâche et dit tous les combien elle doit pinger ; les
// minutes du formulaire deviennent des durées, et le service les valide.
func (s *Server) createJob(w http.ResponseWriter, r *http.Request) {
	var request jobRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	if request.MachineID != "" {
		_, err := s.machines.Get(r.Context(), request.MachineID)
		if errors.Is(err, machine.ErrNotFound) {
			s.apiRefuse(w, http.StatusUnprocessableEntity, "job.machine_invalid")
			return
		}
		if err != nil {
			s.apiInternalError(w, r, err)
			return
		}
	}
	created, err := s.heartbeats.Create(r.Context(), heartbeat.Definition{
		Name:      request.Name,
		MachineID: request.MachineID,
		Interval:  time.Duration(request.IntervalMinutes) * time.Minute,
		Grace:     time.Duration(request.GraceMinutes) * time.Minute,
	})
	if key, refused := jobRefusalKey(err); refused {
		s.apiRefuse(w, http.StatusUnprocessableEntity, key)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusCreated, jobToJSON(created))
}

func jobRefusalKey(err error) (string, bool) {
	switch {
	case errors.Is(err, heartbeat.ErrNameInvalid):
		return "job.name_invalid", true
	case errors.Is(err, heartbeat.ErrIntervalInvalid):
		return "job.interval_invalid", true
	case errors.Is(err, heartbeat.ErrGraceInvalid):
		return "job.grace_invalid", true
	}
	return "", false
}

// La page d'une tâche : son état, l'URL de ping et les extraits à coller,
// puis les dernières exécutions et les derniers pings.
func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	found, err := s.heartbeats.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, heartbeat.ErrNotFound) {
		s.apiNotFound(w)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	runs, err := s.heartbeats.Runs(r.Context(), found.ID, jobRunsShown)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	pings, err := s.heartbeats.Pings(r.Context(), found.ID, jobPingsShown)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	publicURL, isLocal := s.resolvePublicURL(r)
	pingURL := publicURL + "/ping/" + found.Token
	response := jobResponse{
		Job:      jobToJSON(found),
		PingURL:  pingURL,
		URLLocal: isLocal,
		Snippets: snippets(pingURL),
		Runs:     make([]runJSON, 0, len(runs)),
		Pings:    make([]pingJSON, 0, len(pings)),
	}
	for _, run := range runs {
		response.Runs = append(response.Runs, runToJSON(run))
	}
	for _, ping := range pings {
		response.Pings = append(response.Pings, pingToJSON(ping))
	}
	s.writeAPI(w, http.StatusOK, response)
}

// Les extraits à coller ; l'URL n'est construite qu'ici, par le serveur.
func snippets(pingURL string) []snippetJSON {
	curl := "curl -fsS -m 10 --retry 3 -o /dev/null "
	return []snippetJSON{
		{Key: "curl", Code: curl + pingURL},
		{Key: "cron", Code: "0 2 * * * /usr/local/bin/sauvegarde.sh && " + curl + pingURL},
		{Key: "script", Code: "#!/bin/bash\n" +
			curl + pingURL + "/start\n" +
			"/usr/local/bin/sauvegarde.sh\n" +
			"code=$?\n" +
			curl + pingURL + "/\"$code\"\n" +
			"exit \"$code\""},
		{Key: "docker", Code: "HEALTHCHECK --interval=5m CMD " + curl + pingURL + " || exit 1"},
	}
}

func (s *Server) deleteJob(w http.ResponseWriter, r *http.Request) {
	s.finishAPIAction(w, r, s.heartbeats.Delete(r.Context(), r.PathValue("id")), heartbeat.ErrNotFound)
}

func (s *Server) pauseJob(w http.ResponseWriter, r *http.Request) {
	s.finishAPIAction(w, r, s.heartbeats.Pause(r.Context(), r.PathValue("id")), heartbeat.ErrNotFound)
}

func (s *Server) resumeJob(w http.ResponseWriter, r *http.Request) {
	err := s.heartbeats.Resume(r.Context(), r.PathValue("id"))
	if errors.Is(err, heartbeat.ErrNotPaused) {
		s.apiRefuse(w, http.StatusConflict, "job.not_paused")
		return
	}
	s.finishAPIAction(w, r, err, heartbeat.ErrNotFound)
}
