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
