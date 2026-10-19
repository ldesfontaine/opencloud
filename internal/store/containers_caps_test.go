package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
)

func seedRemoteMachines(t *testing.T, db *DB) {
	t.Helper()
	exec(t, db, `INSERT INTO machines (id, name, kind, public_key, enrolled_at, created_at) VALUES (?, 'vps-1', 'remote', zeroblob(32), 1, 1)`, m1)
	exec(t, db, `INSERT INTO machines (id, name, kind, public_key, enrolled_at, created_at) VALUES (?, 'vps-2', 'remote', zeroblob(32), 1, 1)`, m2)
}

func capTestContainer(machineID, name string, now time.Time, archivedAt time.Time) container.Container {
	return container.Container{
		ID: container.ID(machineID, name), MachineID: machineID, Kind: container.KindContainer, Name: name, ContainerID: name,
		Image: "nginx:1.27", State: container.StateExited, CreatedAt: now, FirstSeenAt: now, LastSeenAt: now, ArchivedAt: archivedAt,
	}
}

func countArchived(t *testing.T, db *DB, machineID string) int {
	t.Helper()
	return count(t, db, `SELECT COUNT(*) FROM containers WHERE machine_id = ? AND archived_at IS NOT NULL`, machineID)
}

// Au-delà du plafond, les fiches archivées les plus anciennes partent avec
// leurs transitions ; les vivantes et celles d'une autre machine restent.
func TestApplyChanges_CapsTheArchivedContainersOfTheMachine(t *testing.T) {
	db := openTestDB(t)
	seedRemoteMachines(t, db)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)

	other := capTestContainer(m2, "other", now, now)
	living := capTestContainer(m1, "living", now, time.Time{})
	changes := container.Changes{MachineID: m2, Now: now, Containers: []container.Container{other}}
	if err := db.ApplyChanges(ctx, changes); err != nil {
		t.Fatal(err)
	}
	for i := range 6 {
		archived := capTestContainer(m1, fmt.Sprintf("old-%d", i), now, now.Add(time.Duration(i)*time.Minute))
		transition := container.Transition{ContainerID: archived.ID, At: now, Action: "gone"}
		changes := container.Changes{MachineID: m1, Now: now, Containers: []container.Container{archived}, Transitions: []container.Transition{transition}}
		if err := db.ApplyChanges(ctx, changes); err != nil {
			t.Fatal(err)
		}
	}
	changes = container.Changes{MachineID: m1, Now: now, Containers: []container.Container{living}, MaxArchived: 3}
	if err := db.ApplyChanges(ctx, changes); err != nil {
		t.Fatal(err)
	}

	if got := countArchived(t, db, m1); got != 3 {
		t.Fatalf("archivées de la machine = %d, attendu 3", got)
	}
	// Ce sont les trois plus récentes qui restent.
	for _, name := range []string{"old-3", "old-4", "old-5"} {
		if _, err := db.GetContainer(ctx, container.ID(m1, name)); err != nil {
			t.Errorf("%s devait rester : %v", name, err)
		}
	}
	if got := count(t, db, `SELECT COUNT(*) FROM container_events WHERE container_id LIKE ?`, m1+":old-%"); got != 3 {
		t.Fatalf("transitions des archivées gardées = %d, attendu 3 (elles partent avec la fiche)", got)
	}
	if _, err := db.GetContainer(ctx, living.ID); err != nil {
		t.Fatalf("une fiche vivante n'est jamais purgée : %v", err)
	}
	if got := countArchived(t, db, m2); got != 1 {
		t.Fatalf("la machine voisine garde sa fiche archivée : %d", got)
	}
}

// Un plafond zéro est l'absence de plafond : les écritures sans plafond
// (les autres tests du store) ne perdent rien.
func TestApplyChanges_WithoutCaps_KeepsEverything(t *testing.T) {
	db := openTestDB(t)
	seedRemoteMachines(t, db)
	now := time.Unix(1_800_000_000, 0)
	var items []container.Container
	for i := range 5 {
		items = append(items, capTestContainer(m1, fmt.Sprintf("c-%d", i), now, now))
	}
	if err := db.ApplyChanges(context.Background(), container.Changes{MachineID: m1, Now: now, Containers: items}); err != nil {
		t.Fatal(err)
	}
	if got := countArchived(t, db, m1); got != 5 {
		t.Fatalf("archivées = %d, attendu 5", got)
	}
}

// Le plafond des transitions est celui de la machine, tous conteneurs
// confondus : les plus anciennes partent, quel que soit le conteneur.
func TestApplyChanges_CapsTheTransitionsOfTheMachine(t *testing.T) {
	db := openTestDB(t)
	seedRemoteMachines(t, db)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	first := capTestContainer(m1, "first", now, time.Time{})
	second := capTestContainer(m1, "second", now, time.Time{})
	neighbour := capTestContainer(m2, "neighbour", now, time.Time{})
	if err := db.ApplyChanges(ctx, container.Changes{MachineID: m2, Now: now, Containers: []container.Container{neighbour},
		Transitions: []container.Transition{{ContainerID: neighbour.ID, At: now.Add(-time.Hour), Action: "start"}}}); err != nil {
		t.Fatal(err)
	}
	var transitions []container.Transition
	for i := range 10 {
		owner := first
		if i%2 == 1 {
			owner = second
		}
		transitions = append(transitions, container.Transition{ContainerID: owner.ID, At: now.Add(time.Duration(i) * time.Second), Action: fmt.Sprintf("a%d", i)})
	}
	changes := container.Changes{MachineID: m1, Now: now, Containers: []container.Container{first, second}, Transitions: transitions, MaxTransitions: 4}
	if err := db.ApplyChanges(ctx, changes); err != nil {
		t.Fatal(err)
	}

	got := count(t, db, `SELECT COUNT(*) FROM container_events e JOIN containers c ON c.id = e.container_id WHERE c.machine_id = ?`, m1)
	if got != 4 {
		t.Fatalf("transitions de la machine = %d, attendu 4", got)
	}
	// Les quatre plus récentes : a6 à a9.
	if oldest := count(t, db, `SELECT COUNT(*) FROM container_events WHERE action IN ('a0','a1','a2','a3','a4','a5')`); oldest != 0 {
		t.Fatalf("les plus anciennes devaient partir : %d restent", oldest)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM container_events WHERE container_id = ?`, neighbour.ID); got != 1 {
		t.Fatalf("la machine voisine garde sa transition : %d", got)
	}
}

// Des mesures plus serrées que MinSampleSpacing pour un même conteneur ne
// s'écrivent pas : un agent qui en envoie plus ne stocke rien de plus. Un
// autre conteneur a les siennes.
func TestApplyChanges_SamplesTooCloseToAnother_AreDropped(t *testing.T) {
	db := openTestDB(t)
	seedRemoteMachines(t, db)
	now := time.Unix(1_800_000_000, 0)
	first := capTestContainer(m1, "first", now, time.Time{})
	second := capTestContainer(m1, "second", now, time.Time{})
	var samples []container.Sample
	for _, offset := range []time.Duration{0, 5 * time.Second, 16 * time.Second, 30 * time.Second, 46 * time.Second} {
		samples = append(samples, container.Sample{ContainerID: first.ID, SampledAt: now.Add(offset)})
	}
	samples = append(samples, container.Sample{ContainerID: second.ID, SampledAt: now.Add(time.Second)})
	changes := container.Changes{MachineID: m1, Now: now, Containers: []container.Container{first, second}, Samples: samples}
	if err := db.ApplyChanges(context.Background(), changes); err != nil {
		t.Fatal(err)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM container_samples WHERE container_id = ?`, first.ID); got != 3 {
		t.Fatalf("mesures gardées = %d, attendu 3 (0, 16 et 46 s)", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM container_samples WHERE container_id = ?`, second.ID); got != 1 {
		t.Fatalf("l'autre conteneur garde sa mesure : %d", got)
	}
}
