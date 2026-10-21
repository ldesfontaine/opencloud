package server

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func ping(server *Server, method, path, body string, forwardedFor string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if forwardedFor != "" {
		request.Header.Set("X-Forwarded-For", forwardedFor)
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

// Les routes des pings se testent dans le paquet ping ; ici, de bout en
// bout, que la source notée est l'adresse que la racine résout.
func TestPing_SourceIsTheForwardedAddressOnlyFromATrustedProxy(t *testing.T) {
	server := newTestServer(t)
	job := server.createJob(t, "job")
	ping(server.Server, http.MethodGet, "/ping/"+job.Token, "", "203.0.113.9")
	server.trustedProxies = parsePrefixes(t, "192.0.2.0/24")
	ping(server.Server, http.MethodGet, "/ping/"+job.Token, "", "203.0.113.9")
	pings, _ := server.db.ListPings(context.Background(), job.ID, 10)
	if len(pings) != 2 || pings[1].Source != "192.0.2.1" || pings[0].Source != "203.0.113.9" {
		t.Fatalf("pings %+v", pings)
	}
}

// Le client écrit ce qu'il veut à gauche de X-Forwarded-For ; seules les
// entrées ajoutées par nos mandataires sont sûres. La source est la plus à
// droite qui n'en est pas un.
func TestClientAddress_ReadsXForwardedForFromTheRight(t *testing.T) {
	server := newTestServer(t)
	server.trustedProxies = parsePrefixes(t, "192.0.2.0/24", "10.0.0.0/8")
	cases := []struct {
		forwarded []string
		want      string
	}{
		{[]string{"203.0.113.9"}, "203.0.113.9"},
		{[]string{"6.6.6.6, 203.0.113.9"}, "203.0.113.9"},
		{[]string{"6.6.6.6, 203.0.113.9, 10.0.0.7"}, "203.0.113.9"},
		{[]string{"6.6.6.6", "203.0.113.9, 10.0.0.7"}, "203.0.113.9"},
		{[]string{"not-an-ip, 10.0.0.7"}, "not-an-ip"},
		{[]string{"10.0.0.8, 10.0.0.7"}, "10.0.0.8"},
		{[]string{" , "}, "192.0.2.1"},
	}
	for _, c := range cases {
		request := httptest.NewRequest(http.MethodGet, "/ping/x", nil)
		for _, value := range c.forwarded {
			request.Header.Add("X-Forwarded-For", value)
		}
		if got := server.clientAddress(request); got != c.want {
			t.Errorf("%q: got %q, want %q", c.forwarded, got, c.want)
		}
	}
	untrusted := httptest.NewRequest(http.MethodGet, "/ping/x", nil)
	untrusted.RemoteAddr = "198.51.100.4:1234"
	untrusted.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := server.clientAddress(untrusted); got != "198.51.100.4" {
		t.Errorf("untrusted peer: got %q", got)
	}
}
