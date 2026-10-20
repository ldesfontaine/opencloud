package manifest_test

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/manifest"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
	"github.com/ldesfontaine/opencloud/internal/update"
)

var updateGolden = flag.Bool("update", false, "réécrit les fichiers de référence dans testdata/")

var testNow = time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)

const (
	remoteID = "11111111-2222-4333-8444-555555555555"
	// Un faux jeton de ping : l'export ne doit en laisser passer ni le texte,
	// ni l'empreinte.
	pingSecret = "hb_aaaaaaaaaaaaaaaaaaaaaaaaaa" // gitleaks:allow -- faux secret de test
)

// L'export se teste sur la vraie base SQLite, temporaire, avec la machine
// openCloud et une machine distante nommée app-1.
type bench struct {
	db         *store.DB
	manifest   *manifest.Manifest
	localID    string
	directory  *directory.Directory
	probes     *probe.Service
	heartbeats *heartbeat.Service
	machines   *machine.Service
	containers *container.Tracker
	// writes note chaque geste d'écriture que le fichier fait ; il peut aussi
	// en faire échouer un.
	writes *spy
}

func newBench(t *testing.T) *bench {
	t.Helper()
	db := storetest.Open(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	machines := machine.New(db, machine.NewSessions(), logger)
	machines.SetClock(func() time.Time { return testNow })
	storetest.EnsureLocalMachine(t, machines)
	storetest.EnrollMachine(t, machines, remoteID, "app-1")
	declared := directory.New(db, logger)
	containers := container.New(db, logger)
	containers.SetClock(func() time.Time { return testNow })
	containers.SetUnits(declared)
	declared.SetObserved(containers, update.New(db, logger))
	declared.SetRematcher(containers)
	probes := probe.New(db, logger)
	heartbeats := heartbeat.New(db, logger)
	writes := &spy{}
	component := manifest.New(manifest.Dependencies{
		Directory:  spyDirectory{Directory: declared, spy: writes},
		Probes:     spyProbes{Probes: probes, spy: writes},
		Heartbeats: spyHeartbeats{Heartbeats: heartbeats, spy: writes},
		Machines:   db,
		History:    db,
		Journal:    db,
	}, logger)
	return &bench{db: db, manifest: component, localID: machine.LocalID, directory: declared, probes: probes, heartbeats: heartbeats, machines: machines, containers: containers, writes: writes}
}

// inOrder rend la liste telle quelle, ou renversée : le contenu de la base ne
// doit pas dépendre de l'ordre où il a été écrit.
func inOrder[T any](items []T, reversed bool) []T {
	ordered := slices.Clone(items)
	if reversed {
		slices.Reverse(ordered)
	}
	return ordered
}

// seed pose un annuaire varié, avec des identifiants fixes pour que deux
// bases se comparent octet pour octet : Compose, conteneur et binaire ; une
// politique par unité ; une sonde HTTP d'une autre machine que son service,
// une sonde TCP en pause ; deux tâches ; des clés ignorées.
func (b *bench) seed(t *testing.T, reversed bool) {
	t.Helper()
	ctx := context.Background()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	projects := []directory.Project{
		{ID: "prj_00000000000000a1", Name: "docs", Title: "Documentation", CreatedAt: testNow, UpdatedAt: testNow},
		{ID: "prj_00000000000000a2", Name: "infra", CreatedAt: testNow, UpdatedAt: testNow},
	}
	for _, item := range inOrder(projects, reversed) {
		check(b.db.InsertProject(ctx, item))
	}
	unit := func(id, serviceID, name string, kind directory.UnitKind, policy container.UpdatePolicy) directory.Unit {
		return directory.Unit{ID: id, ServiceID: serviceID, Name: name, Kind: kind, UpdatePolicy: policy}
	}
	wikiApp := unit("unit_00000000000000a1", "svc_00000000000000b1", "app", directory.UnitContainer, container.PolicyWatch)
	wikiApp.Image, wikiApp.Version, wikiApp.Ports = "ghcr.io/example/wiki", "2.4.1", []string{"8080:3000/tcp", "80"}
	wikiDB := unit("unit_00000000000000a2", "svc_00000000000000b1", "db", directory.UnitContainer, container.PolicyHold)
	wikiDB.Image, wikiDB.Version = "docker.io/library/postgres", "16.4"
	wikiDB.Digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	proxyUnit := unit("unit_00000000000000a3", "svc_00000000000000b2", "proxy", directory.UnitContainer, container.PolicyIgnore)
	proxyUnit.Image, proxyUnit.Version, proxyUnit.ContainerName = "docker.io/library/traefik", "3.1.2", "traefik"
	resticUnit := unit("unit_00000000000000a4", "svc_00000000000000b3", "restic", directory.UnitBinary, container.PolicyWatch)
	resticUnit.Version, resticUnit.SystemdUnit = "0.17.1", "restic.service"
	legacyUnit := unit("unit_00000000000000a5", "svc_00000000000000b4", "legacy", directory.UnitContainer, container.PolicyWatch)
	legacyUnit.Image, legacyUnit.ContainerName = "docker.io/library/nginx", "legacy"
	services := []directory.Service{
		{
			ID: "svc_00000000000000b1", MachineID: remoteID, Name: "wiki", Title: "Wiki", Nature: directory.NatureApplication,
			Origin: directory.OriginCompose, ComposeProject: "wiki", PublicURL: "https://wiki.example.org",
			DeclaredBy: directory.ByOperator, CreatedAt: testNow, UpdatedAt: testNow,
			// Pas dans l'ordre alphabétique : l'ordre des unités se garde.
			Units:      []directory.Unit{wikiDB, wikiApp},
			ProjectIDs: inOrder([]string{"prj_00000000000000a1"}, reversed),
		},
		{
			ID: "svc_00000000000000b2", MachineID: remoteID, Name: "proxy", Nature: directory.NatureInfra,
			Origin: directory.OriginContainer, DeclaredBy: directory.ByOperator, CreatedAt: testNow, UpdatedAt: testNow,
			Units:      []directory.Unit{proxyUnit},
			Provides:   inOrder([]directory.Capability{directory.CapabilityProxy, directory.CapabilityTLS}, reversed),
			ProjectIDs: inOrder([]string{"prj_00000000000000a1", "prj_00000000000000a2"}, reversed),
		},
		{
			ID: "svc_00000000000000b3", MachineID: remoteID, Name: "backup", Nature: directory.NatureInfra,
			Origin: directory.OriginBinary, DeclaredBy: directory.ByOperator, CreatedAt: testNow, UpdatedAt: testNow,
			Units:    []directory.Unit{resticUnit},
			Provides: []directory.Capability{directory.CapabilityBackupTarget},
		},
		{
			ID: "svc_00000000000000b4", MachineID: b.localID, Name: "legacy", Nature: directory.NatureApplication,
			Origin: directory.OriginContainer, DeclaredBy: directory.ByMigration, NeedsReview: true,
			CreatedAt: testNow, UpdatedAt: testNow, Units: []directory.Unit{legacyUnit},
		},
	}
	for _, item := range inOrder(services, reversed) {
		check(b.db.InsertDeclaredService(ctx, item))
	}
	probes := []probe.Probe{
		{
			ID: "00000000000000c1", Name: "wiki-http", Kind: probe.KindHTTP, Target: "https://wiki.example.org/healthz",
			MachineID: b.localID, ServiceID: "svc_00000000000000b1", Status: probe.StatusUp,
			Interval: time.Minute, Timeout: 10 * time.Second, FailureThreshold: 3, RecoveryThreshold: 2,
			Method: "GET", ExpectedStatus: "2xx", ExpectedBody: "ok", FollowRedirects: true, CreatedAt: testNow,
		},
		{
			ID: "00000000000000c2", Name: "mail-imaps", Kind: probe.KindTCP, Target: "mail.example.org:993",
			MachineID: remoteID, Status: probe.StatusPaused,
			Interval: 90 * time.Second, Timeout: 5 * time.Second, FailureThreshold: 3, RecoveryThreshold: 2,
			TLS: true, CreatedAt: testNow,
		},
		// Une sonde HTTP loin des valeurs par défaut.
		{
			ID: "00000000000000c5", Name: "api-post", Kind: probe.KindHTTP, Target: "https://api.example.org/hook",
			MachineID: remoteID, Status: probe.StatusUp,
			Interval: 5 * time.Minute, Timeout: 20 * time.Second, FailureThreshold: 5, RecoveryThreshold: 4,
			Method: "POST", ExpectedStatus: "200,204", ExpectedBody: "accepted", FollowRedirects: false, CreatedAt: testNow,
		},
		// Deux sondes du même nom : l'identifiant les départage.
		{
			ID: "00000000000000c4", Name: "twin", Kind: probe.KindTCP, Target: "db:5432", MachineID: b.localID, Status: probe.StatusNew,
			Interval: time.Minute, Timeout: 10 * time.Second, FailureThreshold: 3, RecoveryThreshold: 2, CreatedAt: testNow,
		},
		{
			ID: "00000000000000c3", Name: "twin", Kind: probe.KindTCP, Target: "db:5433", MachineID: b.localID, Status: probe.StatusNew,
			Interval: time.Minute, Timeout: 10 * time.Second, FailureThreshold: 3, RecoveryThreshold: 2, CreatedAt: testNow,
		},
	}
	for _, item := range inOrder(probes, reversed) {
		check(b.db.InsertProbe(ctx, item))
	}
	heartbeats := []heartbeat.Heartbeat{
		{
			ID: "00000000000000d1", Token: pingSecret, Name: "wiki-dump", MachineID: remoteID, Status: heartbeat.StatusLate,
			Interval: 24 * time.Hour, Grace: time.Hour, NextDeadlineAt: testNow, CreatedAt: testNow,
		},
		{
			ID: "00000000000000d2", Token: "hb_bbbbbbbbbbbbbbbbbbbbbbbbbb", Name: "certbot", Status: heartbeat.StatusNew, // gitleaks:allow -- faux secret de test
			Interval: 90 * time.Minute, NextDeadlineAt: testNow, CreatedAt: testNow,
		},
	}
	for _, item := range inOrder(heartbeats, reversed) {
		check(b.db.InsertHeartbeat(ctx, item))
	}
	ignored := []directory.IgnoredKey{
		{MachineID: remoteID, Kind: directory.IgnoreComposeProject, Value: "scratch", CreatedAt: testNow},
		{MachineID: remoteID, Kind: directory.IgnoreContainerName, Value: "tmp-debug", CreatedAt: testNow},
		{MachineID: b.localID, Kind: directory.IgnoreContainerName, Value: "portainer", CreatedAt: testNow},
	}
	for _, item := range inOrder(ignored, reversed) {
		check(b.db.InsertIgnoredKey(ctx, item))
	}
}

func compareGolden(t *testing.T, golden string, got []byte) {
	t.Helper()
	if *updateGolden {
		if err := os.WriteFile(golden, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (lancer go test ./internal/manifest -update)", err)
	}
	if string(got) != string(want) {
		t.Fatalf("export différent de %s :\n%s\n(lancer go test ./internal/manifest -update après vérification)", filepath.Base(golden), got)
	}
}
