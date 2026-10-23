package public

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/server/httpx"
)

// Les routes publiques comptent par adresse résolue : la rafale passe, le
// reste reçoit 429 et Retry-After, et une autre source a son propre seau.
func TestStatusPublic_ReadsAreRateLimitedBySource(t *testing.T) {
	server := newBench(t)
	for i := range statusPerSourceBurst {
		if got := server.getFrom("203.0.113.9:4000", "/statut/statut/api/status"); got != http.StatusOK {
			t.Fatalf("request %d: %d", i+1, got)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/statut/statut", nil)
	request.RemoteAddr = "203.0.113.9:4000"
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") != httpx.RetryAfterSeconds || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("beyond the burst: %d, Retry-After %q, Cache-Control %q", recorder.Code, recorder.Header().Get("Retry-After"), recorder.Header().Get("Cache-Control"))
	}
	if got := server.getFrom("203.0.113.10:4000", "/statut/statut"); got != http.StatusOK {
		t.Fatalf("another source: %d", got)
	}
}

// Les limiteurs par adresse passent par la même clé : une même /64 en IPv6
// partage un seau.
func TestStatusPublic_LimitersShareOneBucketPerIPv6SixtyFour(t *testing.T) {
	server := newBench(t)
	first, second := "2001:db8:9:9::1", "2001:db8:9:9::2"
	cases := []struct {
		name  string
		burst int
		allow func(string) bool
	}{
		{"public read", statusPerSourceBurst, server.handler.readLimit.Allow},
		{"public stream opening", statusStreamBurst, server.handler.streamLimit.Allow},
	}
	for _, c := range cases {
		for i := range c.burst {
			if !c.allow(first) {
				t.Fatalf("%s: request %d of the burst refused", c.name, i+1)
			}
		}
		if c.allow(second) {
			t.Errorf("%s: a second address of the same /64 got its own bucket", c.name)
		}
	}
}

// Un seau se remplit avec le temps de l'horloge du banc.
func TestStatusPublic_BucketRefillsWithTheClock(t *testing.T) {
	server := newBench(t)
	for range statusPerSourceBurst {
		server.handler.readLimit.Allow("198.51.100.1")
	}
	if server.handler.readLimit.Allow("198.51.100.1") {
		t.Fatal("the bucket is empty")
	}
	server.handler.readLimit.SetClock(func() time.Time { return testNow.Add(time.Minute) })
	if !server.handler.readLimit.Allow("198.51.100.1") {
		t.Fatal("a minute later the bucket must have refilled")
	}
}
