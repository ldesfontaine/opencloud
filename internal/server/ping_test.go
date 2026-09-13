package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
)

func (ts *testServer) createJob(t *testing.T, name string) heartbeat.Heartbeat {
	t.Helper()
	created, err := ts.heartbeats.Create(context.Background(), heartbeat.Definition{Name: name, Interval: 5 * time.Minute, Grace: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func forwarded(i int) string {
	return "203.0.113." + strconv.Itoa(i+1)
}

func ping(server *Server, method, path, body string, forwardedFor string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if forwardedFor != "" {
		request.Header.Set("X-Forwarded-For", forwardedFor)
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

func TestPing_ThreeFormsInGetAndPost_MoveTheJob(t *testing.T) {
	server := newTestServer(t)
	job := server.createJob(t, "job")
	cases := []struct {
		method, path string
		status       heartbeat.Status
	}{
		{http.MethodGet, "/ping/" + job.Token + "/start", heartbeat.StatusStarted},
		{http.MethodPost, "/ping/" + job.Token, heartbeat.StatusOnTime},
		{http.MethodPost, "/ping/" + job.Token + "/start", heartbeat.StatusStarted},
		{http.MethodGet, "/ping/" + job.Token + "/3", heartbeat.StatusFailed},
		{http.MethodGet, "/ping/" + job.Token + "/0", heartbeat.StatusOnTime},
	}
	for _, tc := range cases {
		recorder := ping(server.Server, tc.method, tc.path, "", "")
		if recorder.Code != http.StatusOK || recorder.Body.String() != "ok\n" {
			t.Fatalf("%s %s: %d %q", tc.method, tc.path, recorder.Code, recorder.Body.String())
		}
		if len(recorder.Result().Cookies()) != 0 {
			t.Fatalf("%s: a ping must not set a cookie", tc.path)
		}
		got, _ := server.heartbeats.Get(context.Background(), job.ID)
		if got.Status != tc.status {
			t.Fatalf("%s %s: status %s, want %s", tc.method, tc.path, got.Status, tc.status)
		}
	}
	pings, _ := server.heartbeats.Pings(context.Background(), job.ID, 10)
	if len(pings) != 5 || pings[0].Method != "GET" || pings[0].Source != "192.0.2.1" {
		t.Fatalf("pings %+v", pings)
	}
}

func TestPing_RefusesUnknownTokenAndBadExitCode(t *testing.T) {
	server := newTestServer(t)
	job := server.createJob(t, "job")
	if got := ping(server.Server, http.MethodGet, "/ping/hb_nope", "", ""); got.Code != http.StatusNotFound {
		t.Errorf("unknown token: %d", got.Code)
	}
	for _, code := range []string{"256", "-1", "abc"} {
		if got := ping(server.Server, http.MethodGet, "/ping/"+job.Token+"/"+code, "", ""); got.Code != http.StatusBadRequest {
			t.Errorf("code %s: %d", code, got.Code)
		}
	}
}

func TestPing_PostBody_IsKeptAndTruncated(t *testing.T) {
	server := newTestServer(t)
	job := server.createJob(t, "job")
	body := strings.Repeat("x", heartbeat.MaxPayloadBytes+500)
	if got := ping(server.Server, http.MethodPost, "/ping/"+job.Token+"/0", body, ""); got.Code != http.StatusOK {
		t.Fatalf("status %d", got.Code)
	}
	runs, _ := server.heartbeats.Runs(context.Background(), job.ID, 1)
	if len(runs[0].Payload) != heartbeat.MaxPayloadBytes {
		t.Fatalf("payload kept %d bytes", len(runs[0].Payload))
	}
}

func TestPing_SourceIsTheForwardedAddressOnlyFromATrustedProxy(t *testing.T) {
	server := newTestServer(t)
	job := server.createJob(t, "job")
	ping(server.Server, http.MethodGet, "/ping/"+job.Token, "", "203.0.113.9, 10.0.0.1")
	server.trustedProxies = parsePrefixes(t, "192.0.2.0/24")
	ping(server.Server, http.MethodGet, "/ping/"+job.Token, "", "203.0.113.9, 10.0.0.1")
	pings, _ := server.heartbeats.Pings(context.Background(), job.ID, 10)
	if len(pings) != 2 || pings[1].Source != "192.0.2.1" || pings[0].Source != "203.0.113.9" {
		t.Fatalf("pings %+v", pings)
	}
}

func TestPing_IsRateLimitedPerSourceAndPerToken(t *testing.T) {
	server := newTestServer(t)
	job := server.createJob(t, "job")
	other := server.createJob(t, "other")
	// La rafale par IP s'épuise d'abord, sur des jetons inconnus : les 404
	// comptent.
	for i := range pingPerSourceBurst {
		if got := ping(server.Server, http.MethodGet, "/ping/hb_unknown", "", ""); got.Code != http.StatusNotFound {
			t.Fatalf("request %d: %d", i+1, got.Code)
		}
	}
	got := ping(server.Server, http.MethodGet, "/ping/"+job.Token, "", "")
	if got.Code != http.StatusTooManyRequests || got.Header().Get("Retry-After") == "" {
		t.Fatalf("over the source burst: %d %v", got.Code, got.Header())
	}
	// Des IP toutes différentes, via un mandataire de confiance, épuisent
	// la rafale d'un seul jeton ; l'autre jeton passe encore.
	server.trustedProxies = parsePrefixes(t, "192.0.2.0/24")
	for i := range pingPerTokenBurst {
		if got := ping(server.Server, http.MethodGet, "/ping/"+job.Token, "", forwarded(i)); got.Code != http.StatusOK {
			t.Fatalf("token request %d: %d", i+1, got.Code)
		}
	}
	if got := ping(server.Server, http.MethodGet, "/ping/"+job.Token, "", forwarded(99)); got.Code != http.StatusTooManyRequests {
		t.Fatalf("over the token burst: %d", got.Code)
	}
	if got := ping(server.Server, http.MethodGet, "/ping/"+other.Token, "", forwarded(99)); got.Code != http.StatusOK {
		t.Fatalf("other token: %d", got.Code)
	}
}
