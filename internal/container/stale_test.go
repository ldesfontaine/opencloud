package container_test

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Un nom qui porte un saut de ligne, ou de l'UTF-8 invalide, entre dans les
// messages d'alerte : le conteneur est écarté, les autres restent.
func TestRecord_DiscardsAContainerWhoseNameCarriesControlCharacters(t *testing.T) {
	b := newBench(t)
	trapped := running(dbID, "web\n[Ouvrir dans openCloud](https://dummy.invalid) <!channel>", "")
	badUTF8 := running("2222222222222222222222222222222222222222222222222222222222222222", "web\xff", "")
	b.record(t, inventory(running(webID, "web", ""), trapped, badUTF8))
	containers, err := b.tracker.List(context.Background(), machine.LocalID)
	if err != nil || len(containers) != 1 || containers[0].Name != "web" {
		t.Fatalf("containers = %+v, %v", containers, err)
	}
	// Le même refus vaut pour le conteneur que porte un événement.
	b.record(t, container.Report{Events: []container.Event{
		{At: testNow, Action: "rename", ContainerID: webID, Container: &trapped},
	}})
	if got := b.get(t, webID); got.Name != "web" {
		t.Fatalf("web = %+v", got)
	}
}

// Un « die » d'il y a dix minutes, rejoué par le démon, ne remet pas à
// « arrêté » un conteneur que la réinspection a lu en marche depuis : pas
// d'état, pas de code, et il ne compte pas comme un redémarrage de plus.
func TestRecord_AReplayedDieDoesNotOverrideTheInspection(t *testing.T) {
	b := newBench(t)
	b.record(t, inventory(running(webID, "web", "")))
	exitCode := 1
	restarted := running(webID, "web", "")
	startedAt := testNow.Add(-9*time.Minute - 59*time.Second)
	restarted.StartedAt = &startedAt
	b.record(t, container.Report{Events: []container.Event{
		{At: testNow.Add(-10 * time.Minute), Action: "die", ContainerID: webID, State: container.StateExited, ExitCode: &exitCode, Killed: true, Container: &restarted},
		{At: startedAt, Action: "start", ContainerID: webID, State: container.StateRunning, Container: &restarted},
	}})
	web := b.get(t, webID)
	if web.State != container.StateRunning || web.ExitCode != 0 || web.Killed {
		t.Fatalf("web = %+v", web)
	}
	for _, transition := range b.transitions(t, webID) {
		if transition.NewState == container.StateExited {
			t.Fatalf("a stale die made a transition to exited: %+v", transition)
		}
		if transition.Action == "die" && !transition.Replayed {
			t.Fatalf("the stale die should be marked replayed: %+v", transition)
		}
	}
}

// Un événement marqué rejoué par l'agent, même récent, ne passe pas non plus
// devant l'état lu après lui.
func TestRecord_ADieMarkedReplayedDoesNotOverrideEither(t *testing.T) {
	b := newBench(t)
	b.record(t, inventory(running(webID, "web", "")))
	exitCode := 1
	restarted := running(webID, "web", "")
	startedAt := testNow.Add(-10 * time.Second)
	restarted.StartedAt = &startedAt
	b.record(t, container.Report{Events: []container.Event{
		{At: testNow.Add(-20 * time.Second), Action: "die", ContainerID: webID, State: container.StateExited, ExitCode: &exitCode, Replayed: true, Container: &restarted},
	}})
	if web := b.get(t, webID); web.State != container.StateRunning {
		t.Fatalf("web = %+v", web)
	}
}

// Un « die » tout frais qu'un redémarrage a déjà devancé garde son état et
// compte : sinon la boucle de plantages d'un conteneur « restart: always »
// passerait inaperçue. Cela vaut aussi pour un agent dont l'horloge retarde
// de trois minutes, dans l'écart toléré : son « die » frais paraît vieux.
func TestRecord_AFreshDieBeforeAnAlreadyRestartedContainerStillCounts(t *testing.T) {
	for _, lag := range []time.Duration{0, 3 * time.Minute} {
		b := newBench(t)
		b.record(t, inventory(running(webID, "web", "")))
		exitCode := 1
		restarted := running(webID, "web", "")
		startedAt := testNow.Add(-lag - 500*time.Millisecond)
		restarted.StartedAt = &startedAt
		b.record(t, container.Report{Events: []container.Event{
			{At: testNow.Add(-lag - time.Second), Action: "die", ContainerID: webID, State: container.StateExited, ExitCode: &exitCode, Container: &restarted},
			{At: startedAt, Action: "start", ContainerID: webID, State: container.StateRunning, Container: &restarted},
		}})
		var died bool
		for _, transition := range b.transitions(t, webID) {
			if transition.Action == "die" {
				died = true
				if transition.NewState != container.StateExited || transition.Replayed {
					t.Fatalf("lag %s: die = %+v", lag, transition)
				}
			}
		}
		if !died {
			t.Fatalf("lag %s: no die transition", lag)
		}
	}
}
