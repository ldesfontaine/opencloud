package action_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

// La borne des attentes de ces tests : large, elle ne sert qu'à ne pas bloquer
// la CI si Wait ne se réveille jamais.
const waitBound = 30 * time.Second

func report(t *testing.T, b *bench, runID string, state agentwire.ActionState, reason string) {
	t.Helper()
	result := agentwire.ActionResult{RunID: runID, State: state, Reason: reason}
	if err := b.service.Record(context.Background(), machineID, agentwire.ActionReport{Results: []agentwire.ActionResult{result}}); err != nil {
		t.Fatal(err)
	}
}

// Wait rend le run dès que l'agent le finit, avec son état et sa raison ; il
// n'a pas à relire à intervalle.
func TestWait_ReturnsTheRunAsSoonAsTheAgentFinishesIt(t *testing.T) {
	b := newBench(t)
	run, err := b.service.Ping(operator(), machineID)
	if err != nil {
		t.Fatal(err)
	}
	type waited struct {
		run action.Run
		err error
	}
	done := make(chan waited, 1)
	ctx, cancel := context.WithTimeout(context.Background(), waitBound)
	defer cancel()
	go func() {
		got, err := b.service.Wait(ctx, run.ID)
		done <- waited{got, err}
	}()
	report(t, b, run.ID, agentwire.ActionAccepted, "")
	report(t, b, run.ID, agentwire.ActionFailed, agentwire.ReasonProducerFailed)
	got := <-done
	if got.err != nil || got.run.State != action.StateFailed || got.run.Reason != agentwire.ReasonProducerFailed {
		t.Fatalf("wait: %+v %v", got.run, got.err)
	}
}

// Un run déjà fini se rend sans attendre ; une expiration décidée par le
// serveur, sans aucun rapport de l'agent, réveille aussi.
func TestWait_SeesARunAlreadyFinishedAndAnExpiry(t *testing.T) {
	b := newBench(t)
	ctx, cancel := context.WithTimeout(context.Background(), waitBound)
	defer cancel()
	done, err := b.service.Ping(operator(), machineID)
	if err != nil {
		t.Fatal(err)
	}
	report(t, b, done.ID, agentwire.ActionSucceeded, "")
	if got, err := b.service.Wait(ctx, done.ID); err != nil || got.State != action.StateSucceeded {
		t.Fatalf("finished: %+v %v", got, err)
	}

	pending, err := b.service.Ping(operator(), machineID)
	if err != nil {
		t.Fatal(err)
	}
	waiting := make(chan action.Run, 1)
	go func() {
		got, _ := b.service.Wait(ctx, pending.ID)
		waiting <- got
	}()
	b.now = b.now.Add(action.OrderLifetime + time.Second)
	if err := b.service.Expire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := <-waiting; got.State != action.StateExpired {
		t.Fatalf("expiry: %+v", got)
	}
}

// L'appelant borne l'attente : son contexte la coupe, et un run que la purge a
// emporté est une erreur, pas un silence.
func TestWait_StopsWithTheContextAndFailsOnAVanishedRun(t *testing.T) {
	b := newBench(t)
	run, err := b.service.Ping(operator(), machineID)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.service.Wait(cancelled, run.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	if _, err := b.service.Wait(context.Background(), "inconnu"); err == nil {
		t.Fatal("an unknown run was waited for")
	}
}
