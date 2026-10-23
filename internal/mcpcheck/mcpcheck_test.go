package mcpcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

var checkedAt = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)

func now() time.Time { return checkedAt }

// serve répond à /health avec le corps et le statut donnés.
func serve(t *testing.T, status int, contentType, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func toolNames(names ...string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = `"` + name + `"`
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

func TestCheck_NotConfiguredTouchesNoNetwork(t *testing.T) {
	health := New("", now).Check(context.Background())
	if health.State != StateNotConfigured || !health.CheckedAt.Equal(checkedAt) {
		t.Fatalf("health %+v", health)
	}
}

func TestCheck_ReachableReportsVersionAndOpenCloudTools(t *testing.T) {
	tools := toolNames(brand.ID+"_list_machines", brand.ID+"_get_job", brand.ID+"_list_alerts", "gmail_search")
	server := serve(t, http.StatusOK, "application/json", `{"status":"ok","version":"0.1.0","tools":`+tools+`}`)
	// Une barre finale dans mcp_url ne double pas le chemin.
	health := New(server.URL+"/", now).Check(context.Background())
	if health.State != StateReachable || health.Version != "0.1.0" || health.OpenCloudTools != 3 {
		t.Fatalf("health %+v", health)
	}
}

func TestCheck_AnOlderMCPMaisonHasNoVersion(t *testing.T) {
	server := serve(t, http.StatusOK, "application/json", `{"status":"ok","tools":`+toolNames(brand.ID+"_get_job")+`}`)
	health := New(server.URL, now).Check(context.Background())
	if health.State != StateReachable || health.Version != "" {
		t.Fatalf("health %+v", health)
	}
}

func TestCheck_AnswersWithoutTheOpenCloudGroup(t *testing.T) {
	server := serve(t, http.StatusOK, "application/json", `{"status":"ok","version":"0.1.0","tools":`+toolNames("gmail_search", "calendar_list")+`}`)
	if health := New(server.URL, now).Check(context.Background()); health.State != StateGroupMissing || health.OpenCloudTools != 0 {
		t.Fatalf("health %+v", health)
	}
	empty := serve(t, http.StatusOK, "application/json", `{"status":"ok"}`)
	if health := New(empty.URL, now).Check(context.Background()); health.State != StateGroupMissing {
		t.Fatalf("no tools key: %+v", health)
	}
}

func TestCheck_InvalidAnswers(t *testing.T) {
	cases := map[string]struct {
		status      int
		contentType string
		body        string
	}{
		"html":             {http.StatusOK, "text/html", "<html><body>Bienvenue</body></html>"},
		"server error":     {http.StatusInternalServerError, "application/json", `{"status":"ok"}`},
		"not found":        {http.StatusNotFound, "text/plain", "404 page not found"},
		"status not ok":    {http.StatusOK, "application/json", `{"status":"degraded"}`},
		"empty":            {http.StatusOK, "application/json", ""},
		"array":            {http.StatusOK, "application/json", `[1,2,3]`},
		"oversize":         {http.StatusOK, "application/json", `{"status":"ok","pad":"` + strings.Repeat("x", 1<<20) + `"}`},
		"truncated object": {http.StatusOK, "application/json", `{"status":"ok","tools":[`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			server := serve(t, c.status, c.contentType, c.body)
			if health := New(server.URL, now).Check(context.Background()); health.State != StateInvalidAnswer {
				t.Fatalf("health %+v", health)
			}
		})
	}
}

// Une redirection n'est pas suivie : elle ne mène pas une vérification vers
// une autre machine.
func TestCheck_ARedirectIsNotFollowed(t *testing.T) {
	var followed atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		followed.Add(1)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer target.Close()
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/health", http.StatusFound)
	}))
	defer redirecting.Close()
	if health := New(redirecting.URL, now).Check(context.Background()); health.State != StateInvalidAnswer {
		t.Fatalf("health %+v", health)
	}
	if followed.Load() != 0 {
		t.Fatal("the redirect was followed")
	}
}

func TestCheck_ClosedPortIsUnreachable(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	address := server.URL
	server.Close()
	if health := New(address, now).Check(context.Background()); health.State != StateUnreachable {
		t.Fatalf("health %+v", health)
	}
}

func TestCheck_ASlowAnswerIsUnreachable(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})
	checker := New(server.URL, now)
	checker.client.Timeout = 50 * time.Millisecond
	started := time.Now()
	health := checker.Check(context.Background())
	if health.State != StateUnreachable {
		t.Fatalf("health %+v", health)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatalf("the check waited %s", time.Since(started))
	}
}

func TestStates_AreTheClosedList(t *testing.T) {
	seen := map[State]bool{}
	for _, state := range States() {
		if seen[state] || state == "" {
			t.Fatalf("state %q repeated or empty", state)
		}
		seen[state] = true
	}
	if len(seen) != 5 {
		t.Fatalf("states %v", States())
	}
}
