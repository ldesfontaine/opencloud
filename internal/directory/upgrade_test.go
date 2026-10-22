package directory_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// reportApp pose le conteneur de l'unité « app » sur la machine, avec le
// constat d'image que l'agent aurait rapporté.
func reportApp(t *testing.T, b *bench, check update.Check) {
	t.Helper()
	ctx := context.Background()
	if err := b.containers.Record(ctx, remoteID, inventoryOf(
		composeContainerOf(containerID1, "nextcloud-app-1", "nextcloud", "app", "nextcloud:29.0.4"),
	)); err != nil {
		t.Fatal(err)
	}
	check.MachineID, check.Image = remoteID, "nextcloud:29.0.4"
	if check.Outcome == "" {
		check.Outcome = update.OutcomeOK
	}
	if err := b.db.ApplyImageChecks(ctx, []update.Check{check}); err != nil {
		t.Fatal(err)
	}
}

// L'offre est le tag plus récent du constat du conteneur rattaché. Pas de
// constat, un constat à jour, une empreinte changée sous le même tag, ou une
// unité qui n'est pas suivie : pas d'offre.
func TestOfferedVersion_IsTheNewerTagOfTheAttachedContainer(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	created := mustCreate(t, b, composeDefinition(remoteID))
	app := created.Units[0].ID

	if version, ok, err := b.directory.OfferedVersion(ctx, app); err != nil || ok || version != "" {
		t.Fatalf("sans conteneur: %q %v %v", version, ok, err)
	}
	reportApp(t, b, update.Check{CheckedAt: testNow, NewerTag: "29.0.5", Kind: update.KindPatch})
	if version, ok, err := b.directory.OfferedVersion(ctx, app); err != nil || !ok || version != "29.0.5" {
		t.Fatalf("avec un tag plus récent: %q %v %v", version, ok, err)
	}

	reportApp(t, b, update.Check{CheckedAt: testNow, Kind: update.KindDigest})
	if version, ok, err := b.directory.OfferedVersion(ctx, app); err != nil || ok || version != "" {
		t.Fatalf("empreinte changée: %q %v %v", version, ok, err)
	}
	reportApp(t, b, update.Check{CheckedAt: testNow})
	if _, ok, err := b.directory.OfferedVersion(ctx, app); err != nil || ok {
		t.Fatalf("à jour: %v %v", ok, err)
	}

	reportApp(t, b, update.Check{CheckedAt: testNow, NewerTag: "29.0.5", Kind: update.KindPatch})
	if _, err := b.directory.SetUnitUpdatePolicy(ctx, app, container.PolicyHold); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := b.directory.OfferedVersion(ctx, app); err != nil || ok {
		t.Fatalf("unité épinglée: %v %v", ok, err)
	}
}

// Un conteneur d'une autre unité ne donne pas son offre à celle-ci, et une
// unité binaire est refusée.
func TestOfferedVersion_ReadsOnlyItsOwnUnit(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	created := mustCreate(t, b, composeDefinition(remoteID))
	binary := mustCreate(t, b, directory.ServiceDefinition{
		MachineID: remoteID, Name: "caddy", Nature: directory.NatureInfra, Origin: directory.OriginBinary,
		Units: []directory.UnitDefinition{{Name: "caddy", SystemdUnit: "caddy.service"}},
	})
	reportApp(t, b, update.Check{CheckedAt: testNow, NewerTag: "29.0.5", Kind: update.KindPatch})

	db := created.Units[1].ID
	if _, ok, err := b.directory.OfferedVersion(ctx, db); err != nil || ok {
		t.Fatalf("la base n'a pas de conteneur: %v %v", ok, err)
	}
	if _, _, err := b.directory.OfferedVersion(ctx, binary.Units[0].ID); !errors.Is(err, directory.ErrUnitNotContainer) {
		t.Fatalf("binaire: %v", err)
	}
	if _, _, err := b.directory.OfferedVersion(ctx, "unit_inconnue"); !errors.Is(err, directory.ErrUnitNotFound) {
		t.Fatalf("inconnue: %v", err)
	}
}

// Le constat le plus récent des conteneurs de l'unité fait foi : un constat
// tout frais « à jour » retire l'offre d'un ancien.
func TestOfferedVersion_TheFreshestCheckWins(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	created := mustCreate(t, b, composeDefinition(remoteID))
	if err := b.containers.Record(ctx, remoteID, inventoryOf(
		composeContainerOf(containerID1, "nextcloud-app-1", "nextcloud", "app", "nextcloud:29.0.4"),
		composeContainerOf(containerID2, "nextcloud-app-2", "nextcloud", "app", "nextcloud:29.0.3"),
	)); err != nil {
		t.Fatal(err)
	}
	checks := []update.Check{
		{MachineID: remoteID, Image: "nextcloud:29.0.4", CheckedAt: testNow.Add(-time.Hour), Outcome: update.OutcomeOK, NewerTag: "29.0.5", Kind: update.KindPatch},
		{MachineID: remoteID, Image: "nextcloud:29.0.3", CheckedAt: testNow, Outcome: update.OutcomeOK},
	}
	if err := b.db.ApplyImageChecks(ctx, checks); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := b.directory.OfferedVersion(ctx, created.Units[0].ID); err != nil || ok {
		t.Fatalf("le plus récent est à jour: %v %v", ok, err)
	}
}

// Changer la version écrit l'unité et date le service, tient l'audit avec les
// deux versions, prévient le direct et la machine ; une version illisible, un
// binaire ou une unité disparue sont refusés sans rien écrire.
func TestSetUnitVersion_WritesAuditsAndTellsTheMachine(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	assigner := &recordingAssigner{}
	b.directory.SetAssigner(assigner)
	journal := audit.New(b.db, slog.New(slog.DiscardHandler))
	b.directory.SetAuditor(journal)
	created := mustCreate(t, b, composeDefinition(remoteID))
	binary := mustCreate(t, b, directory.ServiceDefinition{
		MachineID: remoteID, Name: "caddy", Nature: directory.NatureInfra, Origin: directory.OriginBinary,
		Units: []directory.UnitDefinition{{Name: "caddy", SystemdUnit: "caddy.service"}},
	})
	app := created.Units[0].ID
	told, changes := len(assigner.told()), b.changeCount()

	later := testNow.Add(time.Hour)
	b.directory.SetClock(func() time.Time { return later })
	if err := b.directory.SetUnitVersion(ctx, app, "29.0.5"); err != nil {
		t.Fatal(err)
	}

	read, err := b.db.GetDeclaredService(ctx, created.ID)
	if err != nil || read.Units[0].Version != "29.0.5" || read.Units[1].Version != "16.4" || !read.UpdatedAt.Equal(later) {
		t.Fatalf("après l'écriture: %+v, %v", read, err)
	}
	if len(assigner.told()) != told+1 || b.changeCount() != changes+1 {
		t.Fatalf("la machine et le direct doivent être prévenus: %v, %d", assigner.told(), b.changeCount())
	}
	events, err := b.db.ListAuditEvents(ctx, audit.Query{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, event := range events {
		if event.Action != audit.ServiceUpdate || event.Details["via"] != "upgrade" {
			continue
		}
		found = true
		if event.ObjectID != created.ID || event.Details["unit"] != "app" ||
			event.Details["version_before"] != "29.0.4" || event.Details["version_after"] != "29.0.5" {
			t.Fatalf("audit: %+v", event)
		}
	}
	if !found {
		t.Fatalf("aucun audit service.update via upgrade: %+v", events)
	}

	for _, refused := range []struct {
		unit, version string
		want          error
	}{
		{app, "", directory.ErrVersionInvalid},
		{app, "29 0 5", directory.ErrVersionInvalid},
		{app, "29.0.5$x", directory.ErrVersionInvalid},
		{app, "29.0.5:x", directory.ErrVersionInvalid},
		{app, "evil.example/x", directory.ErrVersionInvalid},
		{app, "latest", directory.ErrVersionLatest},
		{binary.Units[0].ID, "2.0", directory.ErrUnitNotContainer},
		{"unit_inconnue", "2.0", directory.ErrUnitNotFound},
	} {
		if err := b.directory.SetUnitVersion(ctx, refused.unit, refused.version); !errors.Is(err, refused.want) {
			t.Fatalf("%q sur %s: %v, attendu %v", refused.version, refused.unit, err, refused.want)
		}
	}
	read, _ = b.db.GetDeclaredService(ctx, created.ID)
	if read.Units[0].Version != "29.0.5" {
		t.Fatalf("un refus a écrit: %+v", read.Units)
	}
}

// Le tag vient du registre par l'agent : un tag qui n'a pas la forme d'une
// version (un « $ » que Compose interpolerait, un « : » ou un « / » qui
// changerait l'image, latest, trop long) n'est jamais offert, même posé tel
// quel dans la base.
func TestOfferedVersion_NeverOffersAHostileTag(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	created := mustCreate(t, b, composeDefinition(remoteID))
	app := created.Units[0].ID
	for _, hostile := range []string{
		"29.0.5$x", "29.0.5#x", "29.0.5\nprivileged: true", "29.0.5:x", "evil.example/nextcloud:29.0.5",
		"29.0.5@sha256:" + strings.Repeat("0", 64), "latest", "-29.0.5", strings.Repeat("9", 129),
	} {
		reportApp(t, b, update.Check{CheckedAt: testNow, NewerTag: hostile, Kind: update.KindPatch})
		if version, ok, err := b.directory.OfferedVersion(ctx, app); err != nil || ok || version != "" {
			t.Fatalf("%q offert: %q %v %v", hostile, version, ok, err)
		}
	}
}

// Un conteneur rattaché à l'unité qui tourne une autre image que celle
// qu'elle déclare (un écart « image différente ») ne lui donne pas son tag :
// il nommerait une version d'un autre dépôt.
func TestOfferedVersion_OnlyFromTheDeclaredImage(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	created := mustCreate(t, b, composeDefinition(remoteID))
	if err := b.containers.Record(ctx, remoteID, inventoryOf(
		composeContainerOf(containerID1, "nextcloud-app-1", "nextcloud", "app", "evil.example/nextcloud:29.0.4"),
	)); err != nil {
		t.Fatal(err)
	}
	check := update.Check{MachineID: remoteID, Image: "evil.example/nextcloud:29.0.4", CheckedAt: testNow, Outcome: update.OutcomeOK, NewerTag: "29.0.5", Kind: update.KindPatch}
	if err := b.db.ApplyImageChecks(ctx, []update.Check{check}); err != nil {
		t.Fatal(err)
	}
	if version, ok, err := b.directory.OfferedVersion(ctx, created.Units[0].ID); err != nil || ok {
		t.Fatalf("offre d'une autre image: %q %v %v", version, ok, err)
	}
	// Le même conteneur, sur l'image déclarée : l'offre revient (le refus
	// d'avant tenait bien à l'image, pas au rattachement).
	reportApp(t, b, update.Check{CheckedAt: testNow, NewerTag: "29.0.5", Kind: update.KindPatch})
	if version, ok, err := b.directory.OfferedVersion(ctx, created.Units[0].ID); err != nil || !ok || version != "29.0.5" {
		t.Fatalf("image déclarée: %q %v %v", version, ok, err)
	}
}
