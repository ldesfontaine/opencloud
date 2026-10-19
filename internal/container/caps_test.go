package container_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// freshID est un identifiant Docker que personne n'a vu : 64 hexadécimaux.
func freshID(n int) string {
	return fmt.Sprintf("%064x", n+1000)
}

// Un agent qui redonne un inventaire complet aux identifiants neufs, encore
// et encore, archive les précédents : le nombre de fiches archivées de la
// machine plafonne, les plus récentes restent.
func TestRecord_FreshInventoriesAgainAndAgain_CapTheArchivedContainers(t *testing.T) {
	b := newBench(t)
	rounds := container.MaxArchivedPerMachine/container.MaxContainersPerReport + 2
	for round := range rounds {
		items := make([]container.Observed, 0, container.MaxContainersPerReport)
		for i := range container.MaxContainersPerReport {
			n := round*container.MaxContainersPerReport + i
			items = append(items, running(freshID(n), fmt.Sprintf("c-%d", n), ""))
		}
		b.now = testNow.Add(time.Duration(round) * time.Minute)
		b.record(t, inventory(items...))
	}
	all, err := b.db.ListContainers(context.Background(), machine.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	archived, living := 0, 0
	for _, found := range all {
		if found.IsArchived() {
			archived++
		} else {
			living++
		}
	}
	if archived != container.MaxArchivedPerMachine || living != container.MaxContainersPerReport {
		t.Fatalf("archivées = %d (plafond %d), vivantes = %d", archived, container.MaxArchivedPerMachine, living)
	}
	// Les plus anciennes archivées partent, la dernière vague reste entière.
	excess := (rounds-1)*container.MaxContainersPerReport - container.MaxArchivedPerMachine
	oldWave, lastWave := 0, 0
	for _, found := range all {
		for i := range container.MaxContainersPerReport {
			switch found.ContainerID {
			case freshID(i):
				oldWave++
			case freshID((rounds-2)*container.MaxContainersPerReport + i):
				lastWave++
			}
		}
	}
	if oldWave != container.MaxContainersPerReport-excess || lastWave != container.MaxContainersPerReport {
		t.Fatalf("première vague = %d (attendu %d), dernière archivée = %d (attendu %d)", oldWave, container.MaxContainersPerReport-excess, lastWave, container.MaxContainersPerReport)
	}
}

// Un conteneur qui alterne d'état sans fin écrit une transition par
// événement : celles de la machine plafonnent, les plus récentes restent.
func TestRecord_AlternatingStates_CapTheTransitionsOfTheMachine(t *testing.T) {
	b := newBench(t)
	b.record(t, inventory(running(webID, "web", "")))
	total := container.MaxTransitionsPerMachine + 1000
	start := testNow.Add(-24 * time.Hour)
	for sent := 0; sent < total; {
		events := make([]container.Event, 0, container.MaxEventsPerReport)
		for len(events) < container.MaxEventsPerReport && sent < total {
			state := container.StateExited
			if sent%2 == 1 {
				state = container.StateRunning
			}
			events = append(events, container.Event{At: start.Add(time.Duration(sent) * time.Second), Action: "die", ContainerID: webID, State: state, Snippet: "x"})
			sent++
		}
		b.record(t, container.Report{Events: events})
	}
	transitions, err := b.tracker.Transitions(context.Background(), container.ID(machine.LocalID, webID), container.MaxTransitionsPerMachine+100)
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != container.MaxTransitionsPerMachine {
		t.Fatalf("transitions gardées = %d, attendu %d", len(transitions), container.MaxTransitionsPerMachine)
	}
	// L'inventaire, écrit à l'heure du serveur, est la plus récente ; viennent
	// ensuite les derniers événements, et les 1 001 premiers sont partis.
	newest := start.Add(time.Duration(total-1) * time.Second)
	oldest := start.Add(time.Duration(total+1-container.MaxTransitionsPerMachine) * time.Second)
	if !transitions[0].At.Equal(testNow) || !transitions[1].At.Equal(newest) || !transitions[len(transitions)-1].At.Equal(oldest) {
		t.Fatalf("plus récentes %s puis %s, plus ancienne %s ; attendu %s, %s, %s", transitions[0].At, transitions[1].At, transitions[len(transitions)-1].At, testNow, newest, oldest)
	}
}

// L'instant d'un événement écrit n'est jamais après l'heure du serveur :
// l'avance tolérée à la validation ne se garde pas.
func TestRecord_EventAheadOfTheServerClock_IsStoredAtTheServerTime(t *testing.T) {
	b := newBench(t)
	b.record(t, inventory(running(webID, "web", "")))
	b.record(t, container.Report{Events: []container.Event{
		{At: testNow.Add(4 * time.Minute), Action: "die", ContainerID: webID, State: container.StateExited},
	}})
	transitions := b.transitions(t, webID)
	if len(transitions) != 2 || !transitions[0].At.Equal(testNow) {
		t.Fatalf("transitions = %+v", transitions)
	}
	b.record(t, container.Report{Events: []container.Event{
		{At: testNow.Add(4 * time.Minute), Action: container.ActionDestroy, ContainerID: webID},
	}})
	if archived := b.get(t, webID); !archived.IsArchived() || !archived.ArchivedAt.Equal(testNow) {
		t.Fatalf("archived at %s", archived.ArchivedAt)
	}
}

// Des événements qui décrivent des conteneurs neufs, sans inventaire, ne
// remplissent pas la machine : au-delà du plafond de fiches vivantes, ils
// sont écartés.
func TestRecord_NewContainersFromEvents_StopAtTheLiveCap(t *testing.T) {
	b := newBench(t)
	b.record(t, inventory(running(webID, "web", "")))
	b.record(t, container.Report{Events: []container.Event{{At: testNow, Action: container.ActionDestroy, ContainerID: webID}}})
	wanted := container.MaxLiveContainersPerMachine + 100
	for sent := 0; sent < wanted; {
		events := make([]container.Event, 0, container.MaxEventsPerReport)
		for len(events) < container.MaxEventsPerReport && sent < wanted {
			observed := running(freshID(sent), fmt.Sprintf("e-%d", sent), "")
			events = append(events, container.Event{At: testNow, Action: "start", ContainerID: observed.ContainerID, State: container.StateRunning, Container: &observed})
			sent++
		}
		b.record(t, container.Report{Events: events})
	}
	living, err := b.tracker.List(context.Background(), machine.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	if len(living) != container.MaxLiveContainersPerMachine {
		t.Fatalf("vivantes = %d, plafond %d", len(living), container.MaxLiveContainersPerMachine)
	}
	// Une archivée qu'un événement ranimerait compte comme une neuve.
	revived := running(webID, "web", "")
	b.record(t, container.Report{Events: []container.Event{{At: testNow, Action: "start", ContainerID: webID, State: container.StateRunning, Container: &revived}}})
	if found := b.get(t, webID); !found.IsArchived() {
		t.Fatal("une fiche archivée est revenue à la vie au-delà du plafond")
	}
	// Une fiche déjà connue continue d'être suivie, plafond atteint.
	b.record(t, container.Report{Events: []container.Event{{At: testNow, Action: "die", ContainerID: freshID(0), State: container.StateExited}}})
	if found := b.get(t, freshID(0)); found.State != container.StateExited {
		t.Fatalf("state = %s", found.State)
	}
}
