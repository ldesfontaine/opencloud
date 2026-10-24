package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/proxmox"
	"github.com/ldesfontaine/opencloud/internal/seal"
)

var proxmoxAt = time.Unix(1_800_000_000, 0)

func seedProxmoxMachines(t *testing.T, db *DB) {
	t.Helper()
	exec(t, db, `INSERT INTO machines (id, name, hostname, created_at) VALUES ('pve-host', 'Proxmox', 'pve', 1)`)
	exec(t, db, `INSERT INTO machines (id, name, hostname, created_at) VALUES ('docker', 'Docker', 'docker1', 1)`)
}

func declareProxmox(t *testing.T, db *DB, url string) {
	t.Helper()
	err := db.SaveProxmoxHost(context.Background(), proxmox.Credentials{
		Host:        proxmox.Host{MachineID: "pve-host", APIURL: url, TokenID: "lecteur@pve!lecture", Pin: "ab", Version: "8.2.4", VerifiedAt: proxmoxAt, CreatedAt: proxmoxAt},
		TokenSecret: plainProxmoxToken,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func sampleReading() proxmox.Reading {
	return proxmox.Reading{
		Version: "8.2.5",
		Node:    "pve",
		Guests: []proxmox.Guest{
			{VMID: 5012, Kind: proxmox.KindQEMU, Name: "docker1", Status: "running", CPUs: 2, MaxMem: 2 << 30, MaxDisk: 32 << 30, Node: "pve",
				LinkedMachineID: "docker", LastBackupAt: proxmoxAt.Add(-time.Hour), LastBackupBytes: 244318208},
			{VMID: 200, Kind: proxmox.KindLXC, Name: "dns", Status: "stopped", CPUs: 0.5, MaxMem: 512 << 20, Node: "pve"},
			{VMID: 9000, Kind: proxmox.KindQEMU, Name: "debian-12", Status: "stopped", Template: true, Node: "pve"},
		},
		Storages: []proxmox.Storage{
			{Node: "pve", Name: "local", Type: "dir", Total: 1000, Used: 400, Avail: 600, Content: []string{"iso", "backup"}, Active: true},
			{Node: "pve", Name: "nfs", Type: "nfs", Content: []string{}, Active: false},
		},
	}
}

func TestProxmox_DeclareReadAndView(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedProxmoxMachines(t, db)

	if _, err := db.GetProxmoxView(ctx, "pve-host"); !errors.Is(err, proxmox.ErrNotFound) {
		t.Fatalf("view of an undeclared host: %v", err)
	}
	declareProxmox(t, db, "https://pve.lan:8006")
	view, err := db.GetProxmoxView(ctx, "pve-host")
	if err != nil {
		t.Fatal(err)
	}
	if view.Host.APIURL != "https://pve.lan:8006" || view.Host.TokenID != "lecteur@pve!lecture" || view.Host.Pin != "ab" || view.Host.Version != "8.2.4" ||
		!view.Host.LastReadAt.IsZero() || view.Host.LastError != "" || len(view.Guests) != 0 || len(view.Storages) != 0 {
		t.Fatalf("host before any reading: %+v", view)
	}

	if err := db.ReplaceProxmoxReading(ctx, "pve-host", sampleReading(), proxmoxAt); err != nil {
		t.Fatal(err)
	}
	view, err = db.GetProxmoxView(ctx, "pve-host")
	if err != nil {
		t.Fatal(err)
	}
	if view.Host.Version != "8.2.5" || view.Host.Node != "pve" || !view.Host.LastReadAt.Equal(proxmoxAt) {
		t.Errorf("host after the reading: %+v", view.Host)
	}
	if len(view.Guests) != 3 || view.Guests[0].VMID != 200 || view.Guests[1].VMID != 5012 || view.Guests[2].VMID != 9000 {
		t.Fatalf("guests must come by vmid: %+v", view.Guests)
	}
	docker := view.Guests[1]
	if docker.LinkedMachineID != "docker" || docker.CPUs != 2 || docker.MaxMem != 2<<30 || docker.LastBackupBytes != 244318208 || !docker.LastBackupAt.Equal(proxmoxAt.Add(-time.Hour)) {
		t.Errorf("docker guest: %+v", docker)
	}
	if view.Guests[0].CPUs != 0.5 || !view.Guests[0].LastBackupAt.IsZero() || view.Guests[0].LinkedMachineID != "" || !view.Guests[2].Template {
		t.Errorf("other guests: %+v", view.Guests)
	}
	if want := []string{"iso", "backup"}; len(view.Storages) != 2 || view.Storages[0].Name != "local" || !reflect.DeepEqual(view.Storages[0].Content, want) || !view.Storages[0].Active {
		t.Errorf("storages: %+v", view.Storages)
	}
	if view.Storages[1].Active || view.Storages[1].Content == nil || len(view.Storages[1].Content) != 0 {
		t.Errorf("an inactive storage without content must read as an empty list: %+v", view.Storages[1])
	}
}

func TestProxmox_AReadingReplacesTheLastOneAndAFailureKeepsIt(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedProxmoxMachines(t, db)
	declareProxmox(t, db, "https://pve.lan:8006")
	if err := db.ReplaceProxmoxReading(ctx, "pve-host", sampleReading(), proxmoxAt); err != nil {
		t.Fatal(err)
	}

	if err := db.NoteProxmoxFailure(ctx, "pve-host", "proxmox.unreachable"); err != nil {
		t.Fatal(err)
	}
	view, _ := db.GetProxmoxView(ctx, "pve-host")
	if view.Host.LastError != "proxmox.unreachable" || len(view.Guests) != 3 || !view.Host.LastReadAt.Equal(proxmoxAt) {
		t.Fatalf("a failure must keep the last reading: %+v", view)
	}

	smaller := proxmox.Reading{Version: "8.2.5", Guests: sampleReading().Guests[:1]}
	later := proxmoxAt.Add(5 * time.Minute)
	if err := db.ReplaceProxmoxReading(ctx, "pve-host", smaller, later); err != nil {
		t.Fatal(err)
	}
	view, _ = db.GetProxmoxView(ctx, "pve-host")
	if len(view.Guests) != 1 || len(view.Storages) != 0 || view.Host.LastError != "" || !view.Host.LastReadAt.Equal(later) {
		t.Fatalf("a reading replaces everything: %+v", view)
	}

	if err := db.ReplaceProxmoxReading(ctx, "docker", smaller, later); !errors.Is(err, proxmox.ErrNotFound) {
		t.Fatalf("reading for an undeclared host: %v", err)
	}
	if err := db.NoteProxmoxFailure(ctx, "docker", "proxmox.unreachable"); !errors.Is(err, proxmox.ErrNotFound) {
		t.Fatalf("failure for an undeclared host: %v", err)
	}
}

func TestProxmox_AnotherAddressIsAnotherHostAndTheSameAddressKeepsTheReading(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedProxmoxMachines(t, db)
	declareProxmox(t, db, "https://pve.lan:8006")
	if err := db.ReplaceProxmoxReading(ctx, "pve-host", sampleReading(), proxmoxAt); err != nil {
		t.Fatal(err)
	}
	if err := db.NoteProxmoxFailure(ctx, "pve-host", "proxmox.unauthorized"); err != nil {
		t.Fatal(err)
	}

	declareProxmox(t, db, "https://pve.lan:8006")
	view, _ := db.GetProxmoxView(ctx, "pve-host")
	if len(view.Guests) != 3 || view.Host.LastError != "" || !view.Host.LastReadAt.Equal(proxmoxAt) {
		t.Fatalf("a new token for the same host keeps what was read and clears the failure: %+v", view)
	}

	declareProxmox(t, db, "https://other.lan:8006")
	view, _ = db.GetProxmoxView(ctx, "pve-host")
	if len(view.Guests) != 0 || len(view.Storages) != 0 || !view.Host.LastReadAt.IsZero() {
		t.Fatalf("another address must not show the old host's VMs: %+v", view)
	}
}

func TestProxmox_RemoveAndCascade(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedProxmoxMachines(t, db)
	declareProxmox(t, db, "https://pve.lan:8006")
	if err := db.ReplaceProxmoxReading(ctx, "pve-host", sampleReading(), proxmoxAt); err != nil {
		t.Fatal(err)
	}
	ids, err := db.ListProxmoxHostIDs(ctx)
	if err != nil || !reflect.DeepEqual(ids, []string{"pve-host"}) {
		t.Fatalf("ids %v, %v", ids, err)
	}

	if err := db.DeleteProxmoxHost(ctx, "pve-host"); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteProxmoxHost(ctx, "pve-host"); !errors.Is(err, proxmox.ErrNotFound) {
		t.Fatalf("second removal: %v", err)
	}
	for _, table := range []string{"machine_proxmox", "proxmox_guests", "proxmox_storages"} {
		if got := count(t, db, `SELECT COUNT(*) FROM `+table); got != 0 {
			t.Errorf("%s keeps %d row(s) after the host is removed", table, got)
		}
	}
}

func TestProxmox_Summaries(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedProxmoxMachines(t, db)
	if summaries, err := db.ProxmoxSummaries(ctx); err != nil || len(summaries) != 0 {
		t.Fatalf("no host: %v, %v", summaries, err)
	}
	declareProxmox(t, db, "https://pve.lan:8006")
	if err := db.ReplaceProxmoxReading(ctx, "pve-host", sampleReading(), proxmoxAt); err != nil {
		t.Fatal(err)
	}
	if err := db.NoteProxmoxFailure(ctx, "pve-host", "proxmox.unreachable"); err != nil {
		t.Fatal(err)
	}

	summaries, err := db.ProxmoxSummaries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("only the host and the linked VM have a summary: %+v", summaries)
	}
	host := summaries["pve-host"]
	if !host.Declared || host.Guests != 3 || host.LastError != "proxmox.unreachable" || host.Hosted != nil {
		t.Errorf("host summary: %+v", host)
	}
	vm := summaries["docker"]
	if vm.Declared || vm.Hosted == nil {
		t.Fatalf("vm summary: %+v", vm)
	}
	if vm.Hosted.HostMachineID != "pve-host" || vm.Hosted.HostName != "Proxmox" || vm.Hosted.VMID != 5012 || vm.Hosted.Kind != proxmox.KindQEMU ||
		vm.Hosted.CPUs != 2 || vm.Hosted.MaxMem != 2<<30 || vm.Hosted.LastBackupBytes != 244318208 {
		t.Errorf("hosted: %+v", vm.Hosted)
	}
}

func TestProxmox_RemovingAMachineTakesItsHostAndDetachesTheVMsLinkedToIt(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedProxmoxMachines(t, db)
	declareProxmox(t, db, "https://pve.lan:8006")
	if err := db.ReplaceProxmoxReading(ctx, "pve-host", sampleReading(), proxmoxAt); err != nil {
		t.Fatal(err)
	}

	plan, err := db.MachineDeletionPlan(ctx, "docker")
	if err != nil {
		t.Fatal(err)
	}
	if want := []deletion.Count{{Kind: deletion.KindProxmoxLinks, Count: 1}}; !reflect.DeepEqual(plan.Detaches, want) || len(plan.Removes) != 0 {
		t.Fatalf("plan of the VM's machine: %+v", plan)
	}
	if err := db.DeleteMachine(ctx, "docker"); err != nil {
		t.Fatal(err)
	}
	view, _ := db.GetProxmoxView(ctx, "pve-host")
	if len(view.Guests) != 3 || view.Guests[1].LinkedMachineID != "" {
		t.Fatalf("the VM must stay, unlinked: %+v", view.Guests)
	}

	plan, err = db.MachineDeletionPlan(ctx, "pve-host")
	if err != nil {
		t.Fatal(err)
	}
	if want := []deletion.Count{{Kind: deletion.KindProxmoxHosts, Count: 1}}; !reflect.DeepEqual(plan.Removes, want) {
		t.Fatalf("plan of the host's machine: %+v", plan)
	}
	if err := db.DeleteMachine(ctx, "pve-host"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"machine_proxmox", "proxmox_guests", "proxmox_storages"} {
		if got := count(t, db, `SELECT COUNT(*) FROM `+table); got != 0 {
			t.Errorf("%s keeps %d row(s)", table, got)
		}
	}
}

func TestProxmox_TheSecretIsSealedAndBoundToItsMachine(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedProxmoxMachines(t, db)
	declareProxmox(t, db, "https://pve.lan:8006")
	stored := rawColumn(t, db, `SELECT token_secret FROM machine_proxmox`)
	if stored == plainProxmoxToken || !seal.IsSealed(stored) {
		t.Fatalf("the token is not sealed: %q", stored)
	}

	// Le même scellé recopié sur une autre machine ne s'ouvre pas.
	exec(t, db, `INSERT INTO machine_proxmox (machine_id, api_url, token_id, token_secret, verified_at, created_at)
		VALUES ('docker', 'https://x.lan:8006', 'a@b!c', ?, 1, 1)`, stored)
	if _, err := db.GetProxmoxCredentials(ctx, "docker"); !errors.Is(err, ErrSecretUnreadable) {
		t.Fatalf("a sealed token copied to another machine: %v", err)
	}
}
