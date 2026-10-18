package store

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Les deux labels que l'agent a lus font l'aller-retour, et un conteneur
// recréé sans label les perd : l'inventaire fait foi.
func TestUpsertContainer_RoundTripsItsLabels(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	declared := declaredOnLocal(t, db)
	now := time.Unix(1_800_000_000, 0)
	item := container.Container{
		ID: "local:web", MachineID: machine.LocalID, Kind: container.KindContainer, Name: "web", ContainerID: "web",
		Image: "nextcloud:29", State: container.StateRunning, CreatedAt: now, FirstSeenAt: now, LastSeenAt: now,
		Labels: container.Labels{Service: declared.ID, Unit: "whoami"},
	}
	if err := db.ApplyChanges(ctx, container.Changes{MachineID: machine.LocalID, Now: now, Containers: []container.Container{item}}); err != nil {
		t.Fatal(err)
	}
	// Le niveau « label » s'écrit : match_level n'a pas de CHECK.
	if err := db.SetContainerUnits(ctx, []container.UnitAssignment{{ContainerID: item.ID, UnitID: declared.Units[0].ID, Level: container.MatchLabel}}); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetContainer(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Labels != item.Labels || got.MatchLevel != container.MatchLabel || got.UnitID != declared.Units[0].ID {
		t.Fatalf("fiche relue = labels %+v, niveau %q, unité %q", got.Labels, got.MatchLevel, got.UnitID)
	}
	listed, err := db.ListContainers(ctx, machine.LocalID)
	if err != nil || len(listed) != 1 || listed[0].Labels != item.Labels {
		t.Fatalf("liste = %+v, %v", listed, err)
	}

	// Un conteneur d'un seul label : la moitié ne s'invente pas l'autre.
	item.Labels = container.Labels{Service: declared.ID}
	if err := db.ApplyChanges(ctx, container.Changes{MachineID: machine.LocalID, Now: now, Containers: []container.Container{item}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.GetContainer(ctx, item.ID); got.Labels != (container.Labels{Service: declared.ID}) {
		t.Fatalf("labels après réécriture = %+v", got.Labels)
	}
	// Des labels mal formés : ni service ni unité, mais pas « sans label ».
	item.Labels = container.Labels{Invalid: true}
	if err := db.ApplyChanges(ctx, container.Changes{MachineID: machine.LocalID, Now: now, Containers: []container.Container{item}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.GetContainer(ctx, item.ID); got.Labels != (container.Labels{Invalid: true}) || got.Labels.IsEmpty() {
		t.Fatalf("labels mal formés après réécriture = %+v", got.Labels)
	}
	item.Labels = container.Labels{}
	if err := db.ApplyChanges(ctx, container.Changes{MachineID: machine.LocalID, Now: now, Containers: []container.Container{item}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.GetContainer(ctx, item.ID); !got.Labels.IsEmpty() {
		t.Fatalf("labels après un inventaire sans label = %+v", got.Labels)
	}
}

// La 027 est un ajout pur : les conteneurs déjà là gardent tout, sans label.
func TestMigration027_ExistingContainersHaveNoLabels(t *testing.T) {
	db := openAtVersion(t, 26)
	seedMachines(t, db)
	exec(t, db, `
		INSERT INTO containers (id, machine_id, kind, name, container_id, image, state, ports, created_at, first_seen_at, last_seen_at, compose_service, group_name)
		VALUES ('c1', ?, 'container', 'web', 'c1', 'nginx:1.27', 'running', '[]', 1, 1, 1, 'web', 'blog')`, m1)

	migrateToTheEnd(t, db)

	if got := count(t, db, `SELECT COUNT(*) FROM containers WHERE id = 'c1' AND label_service = '' AND label_unit = '' AND label_invalid = 0 AND compose_service = 'web' AND group_name = 'blog'`); got != 1 {
		t.Fatalf("la fiche existante a changé ou ses labels ne sont pas vides : %d", got)
	}
	got, err := db.GetContainer(context.Background(), "c1")
	if err != nil || !got.Labels.IsEmpty() || got.Name != "web" {
		t.Fatalf("fiche relue = %+v, %v", got, err)
	}
	if violations := count(t, db, `SELECT COUNT(*) FROM pragma_foreign_key_check`); violations != 0 {
		t.Fatalf("%d clés étrangères pendantes", violations)
	}
}
