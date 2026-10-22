package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
)

func TestAPI_CreateJob_ReturnsItAndRefusesBadDefinitionsWithTheirKey(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	recorder := callAPI(server.Server, http.MethodPost, "/api/jobs", jobRequest{Name: "sauvegarde", MachineID: remoteID, IntervalMinutes: 60, GraceMinutes: 10})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var created jobJSON
	decodeAPI(t, recorder, &created)
	if created.Status != string(heartbeat.StatusNew) || created.IntervalSeconds != 3600 || created.GraceSeconds != 600 || created.MachineName != "vps-paris-1" {
		t.Fatalf("created %+v", created)
	}
	if got, err := server.db.GetHeartbeat(context.Background(), created.ID); err != nil || got.Name != "sauvegarde" {
		t.Fatalf("stored %+v %v", got, err)
	}

	cases := map[string]struct {
		request jobRequest
		key     string
	}{
		"empty name":      {jobRequest{Name: " ", IntervalMinutes: 60}, "job.name_invalid"},
		"short interval":  {jobRequest{Name: "x", IntervalMinutes: 0}, "job.interval_invalid"},
		"grace too long":  {jobRequest{Name: "x", IntervalMinutes: 60, GraceMinutes: 120}, "job.grace_invalid"},
		"unknown machine": {jobRequest{Name: "x", MachineID: "nope", IntervalMinutes: 60}, "job.machine_invalid"},
	}
	for name, tc := range cases {
		got := callAPI(server.Server, http.MethodPost, "/api/jobs", tc.request)
		if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != tc.key {
			t.Errorf("%s: %d %s", name, got.Code, got.Body.String())
		}
	}
}

func TestAPI_JobActions_PauseResumeDelete(t *testing.T) {
	server := newTestServer(t)
	job := server.createJob(t, "job")
	if got := callAPI(server.Server, http.MethodPost, "/api/jobs/"+job.ID+"/actions/resume", nil); got.Code != http.StatusConflict || errorCode(t, got) != "job.not_paused" {
		t.Fatalf("resume while running: %d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/jobs/"+job.ID+"/actions/pause", nil); got.Code != http.StatusNoContent {
		t.Fatalf("pause: %d %s", got.Code, got.Body.String())
	}
	if found, _ := server.db.GetHeartbeat(context.Background(), job.ID); !found.IsPaused() {
		t.Fatalf("not paused: %+v", found)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/jobs/"+job.ID+"/actions/resume", nil); got.Code != http.StatusNoContent {
		t.Fatalf("resume: %d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodDelete, "/api/jobs/"+job.ID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", got.Code)
	}
	for _, path := range []string{"/api/jobs/" + job.ID, "/api/jobs/" + job.ID + "/actions/pause"} {
		method := http.MethodGet
		if path != "/api/jobs/"+job.ID {
			method = http.MethodPost
		}
		if got := callAPI(server.Server, method, path, nil); got.Code != http.StatusNotFound {
			t.Errorf("%s after delete: %d", path, got.Code)
		}
	}
	if got := ping(server.Server, http.MethodGet, "/ping/"+job.Token, "", ""); got.Code != http.StatusNotFound {
		t.Fatalf("ping after delete: %d", got.Code)
	}
}

func TestAPI_GetJob_CarriesThePingURLAndTheSnippets(t *testing.T) {
	server := newTestServer(t)
	job := server.createJob(t, "job")
	var response jobResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/jobs/"+job.ID, nil), &response)
	if response.PingURL != "http://example.com/ping/"+job.Token || response.URLLocal || len(response.Snippets) != 4 {
		t.Fatalf("response %+v", response)
	}
	server.publicURL = "https://cloud.exemple.fr/"
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/jobs/"+job.ID, nil), &response)
	if response.PingURL != "https://cloud.exemple.fr/ping/"+job.Token || response.URLLocal {
		t.Fatalf("with public_url: %+v", response)
	}
}

// Un retard s'écrit dans l'historique : l'API rend l'exécution « missed »
// telle quelle, sans début, sans durée, sans code, close à l'échéance.
func TestAPI_GetJob_ShowsAMissedRunAfterALateness(t *testing.T) {
	server := newTestServer(t)
	job := server.createJob(t, "job")
	server.heartbeats.SetClock(func() time.Time { return testNow.Add(time.Hour) })
	if err := server.heartbeats.CheckDeadlines(context.Background()); err != nil {
		t.Fatal(err)
	}
	var response jobResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/jobs/"+job.ID, nil), &response)
	if len(response.Runs) != 1 {
		t.Fatalf("runs %+v", response.Runs)
	}
	run := response.Runs[0]
	if run.Outcome != "missed" || run.StartedAt != nil || run.DurationMs != nil || run.ExitCode != nil ||
		run.CompletedAt == nil || !run.CompletedAt.Equal(job.NextDeadlineAt) {
		t.Fatalf("run %+v, deadline %v", run, job.NextDeadlineAt)
	}
}

func TestAPI_UpdateJob_KeepsItsTokenAndRefusesWithTheKey(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	job := server.createJob(t, "job")

	recorder := callAPI(server.Server, http.MethodPut, "/api/jobs/"+job.ID, jobRequest{Name: "sauvegarde", MachineID: remoteID, IntervalMinutes: 60, GraceMinutes: 10})
	if recorder.Code != http.StatusOK {
		t.Fatalf("update: %d %s", recorder.Code, recorder.Body.String())
	}
	var updated jobJSON
	decodeAPI(t, recorder, &updated)
	if updated.ID != job.ID || updated.Name != "sauvegarde" || updated.IntervalSeconds != 3600 || updated.GraceSeconds != 600 || updated.MachineName != "vps-paris-1" {
		t.Fatalf("updated %+v", updated)
	}
	if got, _ := server.db.GetHeartbeat(context.Background(), job.ID); got.Token != job.Token {
		t.Fatal("an edit must not change the token")
	}
	if got := ping(server.Server, http.MethodGet, "/ping/"+job.Token, "", ""); got.Code != http.StatusOK {
		t.Fatalf("ping after edit: %d", got.Code)
	}

	// La machine est facultative : l'omettre détache la tâche.
	recorder = callAPI(server.Server, http.MethodPut, "/api/jobs/"+job.ID, jobRequest{Name: "sauvegarde", IntervalMinutes: 60})
	decodeAPI(t, recorder, &updated)
	if updated.MachineID != "" {
		t.Fatalf("machine kept: %+v", updated)
	}

	cases := map[string]struct {
		request jobRequest
		key     string
	}{
		"empty name":      {jobRequest{Name: " ", IntervalMinutes: 60}, "job.name_invalid"},
		"short interval":  {jobRequest{Name: "x", IntervalMinutes: 0}, "job.interval_invalid"},
		"grace too long":  {jobRequest{Name: "x", IntervalMinutes: 60, GraceMinutes: 120}, "job.grace_invalid"},
		"unknown machine": {jobRequest{Name: "x", MachineID: "nope", IntervalMinutes: 60}, "job.machine_invalid"},
	}
	for name, tc := range cases {
		got := callAPI(server.Server, http.MethodPut, "/api/jobs/"+job.ID, tc.request)
		if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != tc.key {
			t.Errorf("%s: %d %s", name, got.Code, got.Body.String())
		}
	}
	if got := callAPI(server.Server, http.MethodPut, "/api/jobs/inconnue", jobRequest{Name: "x", IntervalMinutes: 60}); got.Code != http.StatusNotFound {
		t.Fatalf("unknown job: %d %s", got.Code, got.Body.String())
	}
	// Une tâche inconnue l'emporte sur une machine inconnue.
	if got := callAPI(server.Server, http.MethodPut, "/api/jobs/inconnue", jobRequest{Name: "x", MachineID: "nope", IntervalMinutes: 60}); got.Code != http.StatusNotFound {
		t.Fatalf("unknown job and machine: %d %s", got.Code, got.Body.String())
	}
}

func TestAPI_RegenerateToken_ChangesThePingURL(t *testing.T) {
	server := newTestServer(t)
	job := server.createJob(t, "job")

	if got := callAPI(server.Server, http.MethodPost, "/api/jobs/"+job.ID+"/actions/regenerate-token", nil); got.Code != http.StatusNoContent {
		t.Fatalf("regenerate: %d %s", got.Code, got.Body.String())
	}
	var response jobResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/jobs/"+job.ID, nil), &response)
	if response.PingURL == "http://example.com/ping/"+job.Token || response.Job.ID != job.ID {
		t.Fatalf("the ping URL must change: %+v", response)
	}
	if got := ping(server.Server, http.MethodGet, "/ping/"+job.Token, "", ""); got.Code != http.StatusNotFound {
		t.Fatalf("old URL: %d", got.Code)
	}
	newPath := strings.TrimPrefix(response.PingURL, "http://example.com")
	if got := ping(server.Server, http.MethodGet, newPath, "", ""); got.Code != http.StatusOK {
		t.Fatalf("new URL: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/jobs/inconnue/actions/regenerate-token", nil); got.Code != http.StatusNotFound {
		t.Fatalf("unknown job: %d", got.Code)
	}
}
