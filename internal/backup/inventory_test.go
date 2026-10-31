package backup_test

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/backup"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/proxmox"
)

// seedHost déclare un hôte Proxmox sur la machine source et y pose un relevé :
// deux VM dont les archives vzdump sont chacune à leur numéro, un LXC sans
// archive, un modèle, et un stockage de sauvegardes.
func (b *bench) seedHost() time.Time {
	b.t.Helper()
	ctx := context.Background()
	if err := b.db.SaveProxmoxHost(ctx, proxmox.Credentials{
		Host:        proxmox.Host{MachineID: sourceID, APIURL: "https://pve.example.test:8006", TokenID: "reader@pve!read", VerifiedAt: benchNow, CreatedAt: benchNow},
		TokenSecret: "0a1b2c3d-4e5f-6071-8293-a4b5c6d7e8f9", // gitleaks:allow -- faux secret de test
	}); err != nil {
		b.t.Fatal(err)
	}
	newest := benchNow.Add(-2 * time.Hour)
	reading := proxmox.Reading{
		Version: "8.2.5", Node: "pve",
		Guests: []proxmox.Guest{
			{VMID: 100, Kind: proxmox.KindQEMU, Name: "web", Node: "pve", Status: "running",
				LastBackupAt: newest, LastBackupBytes: 300, LastBackupStorage: "nfs", BackupCount: 3, BackupTotalBytes: 800},
			{VMID: 101, Kind: proxmox.KindLXC, Name: "dns", Node: "pve", Status: "running"},
			{VMID: 102, Kind: proxmox.KindQEMU, Name: "db", Node: "pve", Status: "stopped",
				LastBackupAt: newest.Add(-time.Hour), LastBackupBytes: 50, LastBackupStorage: "local", BackupCount: 1, BackupTotalBytes: 50},
			{VMID: 9000, Kind: proxmox.KindQEMU, Name: "template", Node: "pve", Template: true},
		},
		Storages: []proxmox.Storage{
			{Node: "pve", Name: "nfs", Type: "nfs", Total: 1000, Used: 400, Avail: 600, Content: []string{"backup"}, Active: true},
			{Node: "pve", Name: "zfs", Type: "zfspool", Total: 500, Used: 100, Avail: 400, Content: []string{"images", "rootdir"}, Active: true},
		},
	}
	if err := b.db.ReplaceProxmoxReading(ctx, sourceID, reading, benchNow); err != nil {
		b.t.Fatal(err)
	}
	return newest
}

func (b *bench) seedVolumes() {
	b.t.Helper()
	err := b.db.ApplyChanges(context.Background(), container.Changes{
		MachineID: machineID, Now: benchNow, ReplaceVolumes: true,
		Volumes: []container.Volume{{MachineID: machineID, Name: "data", Driver: "local", UsedBy: []string{"web"}, SeenAt: benchNow}},
	})
	if err != nil {
		b.t.Fatal(err)
	}
}

func sourceByKey(t *testing.T, inventory backup.Inventory, key string) backup.Source {
	t.Helper()
	for _, source := range inventory.Sources {
		if source.Key == key {
			return source
		}
	}
	t.Fatalf("no source %s in %+v", key, inventory.Sources)
	return backup.Source{}
}

// Le fichier vzdump vu sur un stockage est rattaché à la VM de son numéro, pas
// à une autre, avec son stockage, son total et sa date ; la VM sans archive n'a
// rien observé. Un plan « 100,101 » couvre ces deux numéros et pas le 102, que
// Proxmox seul sauvegarde.
func TestInventory_AttachesObservedArchivesToTheirOwnSource(t *testing.T) {
	b := newBench(t, rights)
	b.readyAgent("100,101", 5)
	newest := b.seedHost()

	inventory, err := b.backups.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	web := sourceByKey(t, inventory, backup.GuestSourceKey(sourceID, 100))
	if web.Kind != backup.KindVM || web.Name != "web" || web.MachineID != sourceID || web.Observed == nil ||
		web.Observed.Storage != "nfs" || web.Observed.Count != 3 || web.Observed.TotalBytes != 800 ||
		web.Observed.LastBytes != 300 || !web.Observed.LastAt.Equal(newest) {
		t.Fatalf("web %+v observed %+v", web, web.Observed)
	}
	database := sourceByKey(t, inventory, backup.GuestSourceKey(sourceID, 102))
	if database.Observed == nil || database.Observed.Storage != "local" || database.Observed.TotalBytes != 50 || len(database.CoveredBy) != 0 {
		t.Fatalf("db %+v observed %+v", database, database.Observed)
	}
	dns := sourceByKey(t, inventory, backup.GuestSourceKey(sourceID, 101))
	if dns.Kind != backup.KindLXC || dns.Observed != nil || len(dns.CoveredBy) != 1 || dns.CoveredBy[0].Subject != "pve" {
		t.Fatalf("dns %+v", dns)
	}
	if len(web.CoveredBy) != 1 || web.CoveredBy[0].DestinationName != "store-1" {
		t.Fatalf("web covered by %+v", web.CoveredBy)
	}
	for _, source := range inventory.Sources {
		if source.ID == "9000" {
			t.Fatalf("a template is not a source: %+v", source)
		}
	}
}

// « all » couvre chaque invité de l'hôte du plan, et seulement lui ; la base
// est couverte par son plan ; un volume n'est couvert par aucun plan (aucun
// producteur ne le sauvegarde encore). Les moins protégées viennent en tête.
func TestInventory_CoverageAndOrder(t *testing.T) {
	b := newBench(t, rights)
	b.readyAgent("all", 5)
	b.seedHost()
	b.seedVolumes()

	inventory, err := b.backups.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, vmid := range []int{100, 101, 102} {
		if source := sourceByKey(t, inventory, backup.GuestSourceKey(sourceID, vmid)); len(source.CoveredBy) != 1 {
			t.Fatalf("guest %d covered by %+v", vmid, source.CoveredBy)
		}
	}
	if source := sourceByKey(t, inventory, backup.SourceDatabaseKey); source.Kind != backup.KindDatabase || len(source.CoveredBy) != 1 || source.CoveredBy[0].Subject != backup.SubjectDatabase || source.MachineID != machine.LocalID {
		t.Fatalf("database %+v", source)
	}
	volume := sourceByKey(t, inventory, backup.VolumeSourceKey(machineID, "data"))
	if volume.Kind != backup.KindVolume || volume.Driver != "local" || len(volume.UsedBy) != 1 || len(volume.CoveredBy) != 0 {
		t.Fatalf("volume %+v", volume)
	}
	if inventory.Sources[0].Key != volume.Key {
		t.Fatalf("the uncovered volume must come first: %+v", inventory.Sources[0])
	}
}

// Les cibles sont les destinations d'openCloud et les stockages de Proxmox qui
// acceptent des sauvegardes, séparés ; la dernière exécution d'une source se lit
// dans l'historique.
func TestInventory_TargetsAndLastRuns(t *testing.T) {
	b := newBench(t, rights)
	destination := b.readyAgent("100", 5)
	b.seedHost()
	playAgents(t, b)
	if _, err := b.backups.RunNow(operator(), "pve"); err != nil {
		t.Fatal(err)
	}
	b.backups.Settle()

	inventory, err := b.backups.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Targets) != 2 {
		t.Fatalf("targets %+v", inventory.Targets)
	}
	stored, storage := inventory.Targets[0], inventory.Targets[1]
	if stored.Kind != backup.TargetDestination || stored.Name != destination.Name || stored.Path != destination.Path || stored.Right != backup.RightOK || len(stored.Plans) != 2 {
		t.Fatalf("destination %+v", stored)
	}
	if storage.Kind != backup.TargetStorage || storage.Name != "nfs" || storage.Type != "nfs" || storage.Total != 1000 || !storage.Active || storage.MachineID != sourceID {
		t.Fatalf("storage %+v", storage)
	}
	web := sourceByKey(t, inventory, backup.GuestSourceKey(sourceID, 100))
	if web.LastRun == nil || web.LastOK == nil || web.LastRun.Result != backup.ResultOK || web.LastOK.SizeBytes == nil || web.LastOK.DestinationName != destination.Name {
		t.Fatalf("last runs %+v %+v", web.LastRun, web.LastOK)
	}
	if other := sourceByKey(t, inventory, backup.GuestSourceKey(sourceID, 101)); other.LastRun != nil {
		t.Fatalf("a source no plan ran has no last run: %+v", other.LastRun)
	}
}
