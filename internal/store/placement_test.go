package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/hostinfo"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/proxmox"
)

// La 026 sur une base qui a déjà des machines : le type d'une machine
// rapprochée d'un invité vient de l'hôte, les autres restent inconnues, et rien
// n'est perdu.
func TestMigration026_DeducesTheKindOfAlreadyLinkedMachines(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version < 26 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES
		('pve', 'pve', 1), ('web', 'web', 1), ('ct', 'ct', 1), ('alone', 'alone', 1)`)
	exec(t, db, `INSERT INTO machine_proxmox (machine_id, api_url, token_id, token_secret, verified_at, created_at)
		VALUES ('pve', 'https://pve.lan:8006', 'oc@pve!r', 'x', 1, 1)`)
	exec(t, db, `INSERT INTO proxmox_guests (machine_id, vmid, kind, name, node, linked_machine_id) VALUES
		('pve', 100, 'qemu', 'web', 'pve', 'web'), ('pve', 101, 'lxc', 'ct', 'pve', 'ct')`)

	if err := db.apply(ctx, "026_machine_placement.sql", 26); err != nil {
		t.Fatal(err)
	}

	want := map[string]machine.Kind{"web": machine.KindVM, "ct": machine.KindLXC, "pve": machine.KindUnknown, "alone": machine.KindUnknown}
	for id, kind := range want {
		var got string
		if err := db.sql.QueryRow(`SELECT kind FROM machines WHERE id = ?`, id).Scan(&got); err != nil || got != string(kind) {
			t.Errorf("%s : type %q (%v), attendu %q", id, got, err, kind)
		}
	}
	if _, err := db.sql.Exec(`UPDATE machines SET kind = 'vps' WHERE id = 'web'`); err == nil {
		t.Error("« vps » se déduit, il ne se rapporte pas : la base le refuse")
	}
	if _, err := db.sql.Exec(`UPDATE machines SET kind_override = 'vps' WHERE id = 'web'`); err != nil {
		t.Errorf("la correction « vps » est refusée : %v", err)
	}
	if rows := count(t, db, `SELECT COUNT(*) FROM proxmox_guests`); rows != 2 {
		t.Errorf("%d invités, 2 attendus : la migration n'efface rien", rows)
	}
}

func hostReportOf(disks ...hostinfo.Disk) hostinfo.Report {
	return hostinfo.Report{
		Virt:       "kvm",
		Interfaces: []hostinfo.Interface{{Name: "eth0", MAC: "bc:24:11:aa:bb:cc"}},
		Storage: hostinfo.Storage{
			Disks: disks,
			Pools: []hostinfo.Pool{{Name: "md0", Kind: hostinfo.PoolMD, Level: "raid1", State: hostinfo.PoolOK, Size: 100, Members: []string{"sda", "sdb"}}},
		},
	}
}

func TestReplaceHostReport_KeepsKindMacsDisksAndPools(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('web', 'web', 1)`)
	report := hostReportOf(hostinfo.Disk{Name: "sda", Model: "WDC", Size: 1000, Media: hostinfo.MediaHDD})

	changed, err := db.ReplaceHostReport(ctx, "web", machine.KindVM, report)
	if err != nil || !changed {
		t.Fatalf("premier rapport : %v %v", changed, err)
	}
	found, err := db.GetMachine(ctx, "web")
	if err != nil {
		t.Fatal(err)
	}
	if found.Kind != machine.KindVM || !reflect.DeepEqual(found.MACs, []string{"bc:24:11:aa:bb:cc"}) {
		t.Fatalf("machine : %+v", found)
	}
	storage, err := db.MachineStorage(ctx, "web")
	if err != nil || !reflect.DeepEqual(storage, report.Storage) {
		t.Fatalf("stockage : %+v %v", storage, err)
	}

	changed, err = db.ReplaceHostReport(ctx, "web", machine.KindVM, report)
	if err != nil || changed {
		t.Fatalf("le même rapport ne change rien : %v %v", changed, err)
	}

	smaller := hostReportOf()
	smaller.Pools = []hostinfo.Pool{}
	changed, err = db.ReplaceHostReport(ctx, "web", machine.KindVM, smaller)
	if err != nil || !changed {
		t.Fatalf("un disque retiré change le rapport : %v %v", changed, err)
	}
	storage, _ = db.MachineStorage(ctx, "web")
	if len(storage.Disks) != 0 || len(storage.Pools) != 0 {
		t.Fatalf("ce que l'agent ne voit plus disparaît : %+v", storage)
	}
}

func TestReplaceHostReport_UnknownMachine_IsNotFound(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.ReplaceHostReport(context.Background(), "nope", machine.KindVM, hostinfo.Report{}); err == nil {
		t.Fatal("un rapport pour une machine inconnue est accepté")
	}
}

func TestSetPlacement_PinsAndClears(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('web', 'web', 1), ('pve', 'pve', 1)`)
	if err := db.SetPlacement(ctx, "web", machine.KindVPS, "pve"); err != nil {
		t.Fatal(err)
	}
	found, _ := db.GetMachine(ctx, "web")
	if found.KindOverride != machine.KindVPS || found.HostOverride != "pve" {
		t.Fatalf("got %+v", found)
	}
	// L'hôte retiré, la correction d'hôte s'efface : la clé étrangère la remet à NULL.
	if err := db.DeleteMachine(ctx, "pve"); err != nil {
		t.Fatal(err)
	}
	found, _ = db.GetMachine(ctx, "web")
	if found.HostOverride != "" {
		t.Fatalf("hôte retiré mais désigné encore : %+v", found)
	}
	if err := db.SetPlacement(ctx, "web", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.SetPlacement(ctx, "nope", "", ""); err == nil {
		t.Fatal("une machine inconnue est acceptée")
	}
}

func TestProxmoxReading_ClustersAreSharedAndPrunedWithTheirLastHost(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	for _, id := range []string{"pve1", "pve2"} {
		exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES (?, ?, 1)`, id, id)
		exec(t, db, `INSERT INTO machine_proxmox (machine_id, api_url, token_id, token_secret, verified_at, created_at)
			VALUES (?, 'https://'||?||':8006', 'oc@pve!r', 'x', 1, 1)`, id, id)
	}
	at := time.Unix(1_800_000_000, 0)
	for _, id := range []string{"pve1", "pve2"} {
		reading := proxmox.Reading{Version: "8.2", Node: id, Cluster: "homelab"}
		if err := db.ReplaceProxmoxReading(ctx, id, reading, at); err != nil {
			t.Fatal(err)
		}
	}
	if rows := countRows(t, db, "hypervisor_clusters"); rows != 1 {
		t.Fatalf("%d grappes, une seule attendue pour deux hôtes", rows)
	}
	summaries, err := db.ProxmoxSummaries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, second := summaries["pve1"], summaries["pve2"]
	if first.Cluster == nil || second.Cluster == nil || first.Cluster.ID != second.Cluster.ID || first.Cluster.Name != "homelab" {
		t.Fatalf("grappes : %+v %+v", first.Cluster, second.Cluster)
	}
	if first.Node != "pve1" {
		t.Fatalf("nœud %q", first.Node)
	}

	// Un hôte retiré laisse la grappe à l'autre ; le dernier la prend avec lui.
	if err := db.DeleteProxmoxHost(ctx, "pve1"); err != nil {
		t.Fatal(err)
	}
	if rows := countRows(t, db, "hypervisor_clusters"); rows != 1 {
		t.Fatalf("%d grappes après un retrait, une attendue", rows)
	}
	if err := db.DeleteProxmoxHost(ctx, "pve2"); err != nil {
		t.Fatal(err)
	}
	if rows := countRows(t, db, "hypervisor_clusters"); rows != 0 {
		t.Fatalf("%d grappes orphelines", rows)
	}
	// Un nœud seul n'a pas de grappe.
	exec(t, db, `INSERT INTO machine_proxmox (machine_id, api_url, token_id, token_secret, verified_at, created_at)
		VALUES ('pve1', 'https://pve1:8006', 'oc@pve!r', 'x', 1, 1)`)
	if err := db.ReplaceProxmoxReading(ctx, "pve1", proxmox.Reading{Node: "pve1"}, at); err != nil {
		t.Fatal(err)
	}
	summaries, _ = db.ProxmoxSummaries(ctx)
	if summaries["pve1"].Cluster != nil || countRows(t, db, "hypervisor_clusters") != 0 {
		t.Fatalf("un nœud seul a une grappe : %+v", summaries["pve1"].Cluster)
	}
}

func TestProxmoxSummaries_AnOverrideElsewhereHidesTheLink(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('pve', 'pve', 1), ('other', 'other', 1), ('web', 'web', 1)`)
	exec(t, db, `INSERT INTO machine_proxmox (machine_id, api_url, token_id, token_secret, verified_at, created_at)
		VALUES ('pve', 'https://pve:8006', 'oc@pve!r', 'x', 1, 1)`)
	exec(t, db, `INSERT INTO proxmox_guests (machine_id, vmid, kind, name, node, linked_machine_id) VALUES ('pve', 100, 'qemu', 'web', 'pve', 'web')`)

	summaries, _ := db.ProxmoxSummaries(ctx)
	if summaries["web"].Hosted == nil {
		t.Fatal("le rapprochement manque")
	}
	if err := db.SetPlacement(ctx, "web", "", "other"); err != nil {
		t.Fatal(err)
	}
	summaries, _ = db.ProxmoxSummaries(ctx)
	if summaries["web"].Hosted != nil {
		t.Fatal("l'opérateur a posé la machine ailleurs : l'invité ne la décrit plus")
	}
	if len(summaries["pve"].Members) != 1 {
		t.Fatalf("l'invité reste dans la liste de son hôte : %+v", summaries["pve"].Members)
	}
}
