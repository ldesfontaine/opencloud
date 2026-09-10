package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/migrations"
)

func registeredZone() Zone {
	return Zone{
		Name:         "exemple.fr",
		CloudflareID: "023e105f4ecef8ad9ca31a8372d0c353",
		AddedAt:      time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC),
	}
}

func TestZones_FreshDatabase_HoldsNone(t *testing.T) {
	database := openTestStore(t, openTestRoot(t), migrations.Files)

	zones, err := database.Zones(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(zones) != 0 {
		t.Fatalf("zones = %v, attendu aucune", zones)
	}
}

func TestInsertZone_ThenZone_ReadsItBackWithoutAnyToken(t *testing.T) {
	database := openTestStore(t, openTestRoot(t), migrations.Files)
	if err := database.InsertZone(context.Background(), registeredZone()); err != nil {
		t.Fatal(err)
	}

	read, err := database.Zone(context.Background(), "exemple.fr")
	if err != nil {
		t.Fatal(err)
	}

	if read.CloudflareID != "023e105f4ecef8ad9ca31a8372d0c353" {
		t.Errorf("CloudflareID = %q", read.CloudflareID)
	}
	if !read.RotatedAt.Equal(read.AddedAt) {
		t.Errorf("RotatedAt = %s, attendu la date d'ajout tant que rien n'a tourné", read.RotatedAt)
	}
}

func TestZone_Unknown_SaysNotFound(t *testing.T) {
	database := openTestStore(t, openTestRoot(t), migrations.Files)

	_, err := database.Zone(context.Background(), "inconnue.fr")

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Zone = %v, attendu ErrNotFound", err)
	}
}

func TestMarkZoneRotated_NotesTheDateAndTheIdentifier(t *testing.T) {
	database := openTestStore(t, openTestRoot(t), migrations.Files)
	if err := database.InsertZone(context.Background(), registeredZone()); err != nil {
		t.Fatal(err)
	}
	rotated := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)

	if err := database.MarkZoneRotated(context.Background(), "exemple.fr", "023e", rotated); err != nil {
		t.Fatal(err)
	}

	read, err := database.Zone(context.Background(), "exemple.fr")
	if err != nil {
		t.Fatal(err)
	}
	if !read.RotatedAt.Equal(rotated) || read.CloudflareID != "023e" {
		t.Errorf("zone = %+v", read)
	}
}

func TestMarkZoneRotated_UnknownZone_SaysNotFound(t *testing.T) {
	database := openTestStore(t, openTestRoot(t), migrations.Files)

	err := database.MarkZoneRotated(context.Background(), "inconnue.fr", "023e", time.Now())

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("MarkZoneRotated = %v, attendu ErrNotFound", err)
	}
}

func TestRecordZoneMachine_ReplayedPose_UpdatesTheLineInPlace(t *testing.T) {
	database := openTestStore(t, openTestRoot(t), migrations.Files)
	if err := database.InsertZone(context.Background(), registeredZone()); err != nil {
		t.Fatal(err)
	}

	for _, fingerprint := range []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb"} {
		placement := ZoneMachine{
			Zone: "exemple.fr", MachineID: LocalMachineID,
			PlacedAt: time.Now().UTC(), Fingerprint: fingerprint,
		}
		if err := database.RecordZoneMachine(context.Background(), placement); err != nil {
			t.Fatal(err)
		}
	}

	placements, err := database.ZoneMachines(context.Background(), "exemple.fr")
	if err != nil {
		t.Fatal(err)
	}
	if len(placements) != 1 || placements[0].Fingerprint != "bbbbbbbbbbbb" {
		t.Fatalf("placements = %+v", placements)
	}
}

// Retirer une zone emporte les machines qui la portaient : la clé étrangère
// est en cascade, la table ne garde pas d'orphelin.
func TestDeleteZone_TakesItsMachinesAway(t *testing.T) {
	database := openTestStore(t, openTestRoot(t), migrations.Files)
	if err := database.InsertZone(context.Background(), registeredZone()); err != nil {
		t.Fatal(err)
	}
	placement := ZoneMachine{
		Zone: "exemple.fr", MachineID: LocalMachineID,
		PlacedAt: time.Now().UTC(), Fingerprint: "aaaaaaaaaaaa",
	}
	if err := database.RecordZoneMachine(context.Background(), placement); err != nil {
		t.Fatal(err)
	}

	if err := database.DeleteZone(context.Background(), "exemple.fr"); err != nil {
		t.Fatal(err)
	}

	placements, err := database.ZoneMachines(context.Background(), "exemple.fr")
	if err != nil {
		t.Fatal(err)
	}
	if len(placements) != 0 {
		t.Fatalf("placements = %+v, attendu aucun", placements)
	}
}
