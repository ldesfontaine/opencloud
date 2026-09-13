package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
)

func TestCreateJob_RedirectsToItsPage(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	cookie := csrfCookieFrom(t, server.Server)
	form := url.Values{"csrf": {cookie.Value}, "name": {"sauvegarde nextcloud"}, "machine_id": {remoteID}, "interval_minutes": {"1440"}, "grace_minutes": {"60"}}
	recorder := postForm(server.Server, cookie, "/taches/nouvelle", form, false)
	if recorder.Code != http.StatusSeeOther || !strings.HasPrefix(recorder.Header().Get("Location"), "/taches/") {
		t.Fatalf("status %d location %q", recorder.Code, recorder.Header().Get("Location"))
	}
	jobs, _ := server.heartbeats.List(context.Background())
	if len(jobs) != 1 || jobs[0].MachineName != "vps-paris-1" || jobs[0].Interval.Hours() != 24 || jobs[0].Status != heartbeat.StatusNew {
		t.Fatalf("jobs %+v", jobs)
	}
	page := httptest.NewRecorder()
	server.ServeHTTP(page, httptest.NewRequest(http.MethodGet, recorder.Header().Get("Location"), nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "http://example.com/ping/"+jobs[0].Token) {
		t.Fatalf("job page %d lacks the ping URL", page.Code)
	}
}

func TestCreateJob_BadInput_ReturnsTheFormWithAnError(t *testing.T) {
	server := newTestServer(t)
	cookie := csrfCookieFrom(t, server.Server)
	cases := map[string]url.Values{
		"name":     {"name": {" "}, "interval_minutes": {"5"}, "grace_minutes": {"1"}},
		"interval": {"name": {"x"}, "interval_minutes": {"0"}, "grace_minutes": {"1"}},
		"grace":    {"name": {"x"}, "interval_minutes": {"5"}, "grace_minutes": {"6"}},
		"machine":  {"name": {"x"}, "interval_minutes": {"5"}, "grace_minutes": {"1"}, "machine_id": {"nobody"}},
	}
	for name, form := range cases {
		form.Set("csrf", cookie.Value)
		recorder := postForm(server.Server, cookie, "/taches/nouvelle", form, false)
		if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "pill-danger") {
			t.Errorf("%s: status %d", name, recorder.Code)
		}
	}
}

func TestJobActions_RequireCSRF(t *testing.T) {
	server := newTestServer(t)
	job := server.createJob(t, "job")
	for _, path := range []string{"/taches/nouvelle", "/taches/" + job.ID + "/actions/pause", "/taches/" + job.ID + "/actions/reprendre", "/taches/" + job.ID + "/actions/supprimer"} {
		if got := postForm(server.Server, nil, path, url.Values{"name": {"x"}}, false); got.Code != http.StatusForbidden {
			t.Errorf("%s without csrf: %d", path, got.Code)
		}
	}
}

func TestPauseResumeDelete_FollowTheJob(t *testing.T) {
	server := newTestServer(t)
	job := server.createJob(t, "job")
	cookie := csrfCookieFrom(t, server.Server)
	form := url.Values{"csrf": {cookie.Value}}

	if got := postForm(server.Server, cookie, "/taches/"+job.ID+"/actions/reprendre", form, false); got.Code != http.StatusConflict {
		t.Fatalf("resume of a running job: %d", got.Code)
	}
	if got := postForm(server.Server, cookie, "/taches/"+job.ID+"/actions/pause", form, false); got.Code != http.StatusSeeOther {
		t.Fatalf("pause: %d", got.Code)
	}
	if found, _ := server.heartbeats.Get(context.Background(), job.ID); !found.IsPaused() {
		t.Fatal("not paused")
	}
	if got := postForm(server.Server, cookie, "/taches/"+job.ID+"/actions/reprendre", form, false); got.Code != http.StatusSeeOther {
		t.Fatalf("resume: %d", got.Code)
	}
	htmx := postForm(server.Server, cookie, "/taches/"+job.ID+"/actions/supprimer", form, true)
	if htmx.Code != http.StatusOK || htmx.Header().Get("HX-Redirect") != "/taches" {
		t.Fatalf("htmx delete: %d %v", htmx.Code, htmx.Header())
	}
	if _, err := server.heartbeats.Get(context.Background(), job.ID); !errors.Is(err, heartbeat.ErrNotFound) {
		t.Fatalf("still there: %v", err)
	}
	if got := postForm(server.Server, cookie, "/taches/"+job.ID+"/actions/pause", form, false); got.Code != http.StatusNotFound {
		t.Fatalf("pause of a deleted job: %d", got.Code)
	}
}

func TestJobPage_UnknownJob_Is404AndFragmentsRenderWithoutTheShell(t *testing.T) {
	server := newTestServer(t)
	job := server.createJob(t, "job")
	unknown := httptest.NewRecorder()
	server.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/taches/nobody", nil))
	if unknown.Code != http.StatusNotFound {
		t.Errorf("unknown job: %d", unknown.Code)
	}
	for _, path := range []string{"/taches/tableau", "/taches/" + job.ID + "/etat"} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		body := recorder.Body.String()
		if recorder.Code != http.StatusOK || strings.Contains(body, "<html") || !strings.Contains(body, "pill-neutral") {
			t.Errorf("%s: %d\n%s", path, recorder.Code, body)
		}
	}
}

func TestNavigationAndOverview_CountJobsNeedingAttention(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedJobs(t)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	body := recorder.Body.String()
	if !strings.Contains(body, `<span class="count hot">1</span>`) || !strings.Contains(body, "1 à traiter") {
		t.Fatalf("overview does not count the failed job:\n%s", body)
	}
}
