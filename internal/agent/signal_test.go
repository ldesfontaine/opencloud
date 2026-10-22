package agent

import (
	"context"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/sampler"
	"github.com/ldesfontaine/opencloud/internal/sshkeys"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// Un serveur qui répond toujours la même chose au signal.
func answeringServer(t *testing.T, status int, body string) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(agentwire.HeaderSignalSections) == "" {
			t.Error("l'agent doit annoncer qu'il lit un refus par section")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "", "v", false)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// Un tampon avec de quoi remplir chaque section.
func filledPending() *pending {
	filled := &pending{readings: &buffer{}, reports: &reports{}, results: &results{}, images: &images{}, actions: newActionResults(), sshKeys: &sshReports{}}
	filled.readings.push(sampler.Reading{SampledAt: time.Now(), CPUPercent: 1})
	filled.reports.Deliver(container.Report{Events: []container.Event{{Action: "start"}}})
	filled.results.Deliver(probe.Report{Results: []probe.Result{{ProbeID: "p"}}})
	filled.images.Deliver(update.Report{Results: []update.Result{{Image: "nginx:1.27"}}})
	filled.sshKeys.update(sshkeys.Report{Accounts: []sshkeys.Account{{Name: "root", State: sshkeys.StateNone, Keys: []sshkeys.Key{}}}})
	return filled
}

// leftover dit quelles sections sont restées dans le tampon.
func leftover(left *pending) map[string]bool {
	return map[string]bool{
		agentwire.SectionReadings: len(left.readings.take(10)) > 0,
		agentwire.SectionServices: left.reports.take() != nil,
		agentwire.SectionProbes:   left.results.take() != nil,
		agentwire.SectionImages:   left.images.take() != nil,
		agentwire.SectionSSHKeys:  left.sshKeys.take() != nil,
	}
}

func TestSignal_SectionRefusedByTheServer_NothingIsReplayed(t *testing.T) {
	client := answeringServer(t, http.StatusOK, `{"rejected":{"probes":"bad_probes"}}`)
	left := filledPending()
	if err := signal(context.Background(), client, "session", left, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	for section, isLeft := range leftover(left) {
		if isLeft {
			t.Errorf("%s rejoué alors que le serveur l'a écrit ou refusé", section)
		}
	}
}

func TestSignal_OlderServerRefusesASection_OnlyWhatFollowsIsReplayed(t *testing.T) {
	client := answeringServer(t, http.StatusBadRequest, `{"error":"bad_services"}`)
	left := filledPending()
	if err := signal(context.Background(), client, "session", left, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{agentwire.SectionReadings: false, agentwire.SectionServices: false, agentwire.SectionProbes: true, agentwire.SectionImages: true, agentwire.SectionSSHKeys: true}
	if got := leftover(left); !maps.Equal(got, want) {
		t.Fatalf("tampon = %v, attendu %v", got, want)
	}
}

func TestSignal_ServerFails_EverythingIsReplayed(t *testing.T) {
	client := answeringServer(t, http.StatusInternalServerError, `{"error":"internal"}`)
	left := filledPending()
	if err := signal(context.Background(), client, "session", left, slog.New(slog.DiscardHandler)); err == nil {
		t.Fatal("une panne du serveur doit remonter")
	}
	for section, isLeft := range leftover(left) {
		if !isLeft {
			t.Errorf("%s perdu sur une panne du serveur", section)
		}
	}
}

// Un 429 est un conseil d'attendre : rien ne se perd, tout repart au signal
// suivant, et l'erreur ne remonte pas (elle ferait fermer le flux).
func TestSignal_ServerThrottles_EverythingIsReplayedWithoutAnError(t *testing.T) {
	client := answeringServer(t, http.StatusTooManyRequests, `{"error":"too_many_requests"}`)
	left := filledPending()
	if err := signal(context.Background(), client, "session", left, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("un 429 ne doit pas rompre le flux : %v", err)
	}
	for section, isLeft := range leftover(left) {
		if !isLeft {
			t.Errorf("%s perdu sur un 429", section)
		}
	}
}
