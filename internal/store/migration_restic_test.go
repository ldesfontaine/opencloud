package store

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/proxmox"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

// La 032 sur une base existante, à jour de la 030 (les liens) et de la 035
// (les archives illisibles) : elle arrive après elles, comme sur une
// installation qui suit dev. L'hôte Proxmox, ses VM, ses stockages et les
// liens restent tels quels ; l'hôte reçoit la valeur sûre (« tâches
// lisibles ») jusqu'au prochain relevé ; les tables neuves s'écrivent et se
// relisent, le plancher de la cible et les mots de passe scellés d'un lien
// compris.
func TestMigration032_KeepsAnExistingBaseAndAddsTheResticTables(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version != 32 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec(t, db, `INSERT INTO machines (id, name, hostname, created_at) VALUES ('pve-host', 'pve', 'pve', 1), ('nas-host', 'nas', 'nas', 1)`)
	exec(t, db, `INSERT INTO machine_proxmox (machine_id, api_url, token_id, token_secret, verified_at, last_read_at, created_at)
		VALUES ('pve-host', 'https://pve.lan:8006', 'lecteur@pve!lecture', '', 1, 2, 1)`)
	exec(t, db, `INSERT INTO proxmox_guests (machine_id, vmid, kind, name, status, node) VALUES ('pve-host', 101, 'qemu', 'docker1', 'running', 'pve')`)
	exec(t, db, `INSERT INTO proxmox_storages (machine_id, node, storage, type, content, active, backups_unreadable)
		VALUES ('pve-host', 'pve', 'pi-sauvegardes', 'nfs', 'backup', 1, 1)`)
	exec(t, db, `INSERT INTO links (id, from_machine_id, from_key, to_machine_id, to_key, nature, proof, signal, first_seen_at, last_seen_at)
		VALUES ('lnk_1', 'pve-host', 'compose:a/web', 'nas-host', 'compose:a/db', 'order', 'declared', 'operator', 1, 1)`)

	if err := db.apply(ctx, "032_restic.sql", 32); err != nil {
		t.Fatal(err)
	}

	if got := count(t, db, `SELECT COUNT(*) FROM proxmox_guests WHERE machine_id = 'pve-host'`); got != 1 {
		t.Errorf("%d VM, 1 attendue", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM proxmox_storages WHERE backups_unreadable = 1`); got != 1 {
		t.Errorf("%d stockages illisibles, 1 attendu : la 035 reste", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM links`); got != 1 {
		t.Errorf("%d liens, 1 attendu", got)
	}
	if got := count(t, db, `SELECT backup_jobs_unreadable FROM machine_proxmox WHERE machine_id = 'pve-host'`); got != 0 {
		t.Errorf("tâches illisibles %d : la valeur sûre est 0", got)
	}
	if _, err := db.sql.Exec(`UPDATE machine_proxmox SET backup_jobs_unreadable = 2`); err == nil {
		t.Error("la colonne accepte une valeur qui n'est ni 0 ni 1")
	}

	// Les tables neuves s'écrivent par le store et se relisent.
	reading := proxmox.Reading{
		Version: "8.2.5", Node: "pve",
		Guests: []proxmox.Guest{{VMID: 101, Kind: proxmox.KindQEMU, Name: "docker1", Node: "pve"}},
		Jobs: []proxmox.BackupJob{{
			ID: "nuit-pi", Schedule: "03:00", NextRun: time.Unix(1_800_000_000, 0), Storage: "pi-sauvegardes", Enabled: true,
			AllGuests: true, Excluded: []int{999}, Retention: proxmox.Retention{Known: true, Daily: 14, Weekly: 8, Monthly: 3},
		}},
	}
	if err := db.ReplaceProxmoxReading(ctx, "pve-host", reading, time.Unix(3, 0)); err != nil {
		t.Fatal(err)
	}
	view, err := db.GetProxmoxView(ctx, "pve-host")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Jobs) != 1 || view.Jobs[0].Retention.Daily != 14 || view.Jobs[0].Excluded[0] != 999 || !view.Jobs[0].NextRun.Equal(time.Unix(1_800_000_000, 0)) {
		t.Errorf("tâches relues : %+v", view.Jobs)
	}
	record := restic.TargetRecord{MachineID: "nas-host", Requested: restic.TargetSetup{Host: "192.0.2.10", Port: 8000, Dir: "/srv/restic"}, RequestedAt: time.Unix(4, 0)}
	if err := db.SaveResticTargetRequest(ctx, record); err != nil {
		t.Fatal(err)
	}
	floor := &agentwire.ResticRetentionFloor{KeepDaily: 7}
	recorded, err := db.RecordResticTargetReport(ctx, "nas-host", agentwire.ResticTargetReport{State: agentwire.ResticTargetRunning, Port: 8000, RetentionFloor: floor}, time.Unix(5, 0))
	if err != nil || !recorded {
		t.Fatalf("rapport de la cible : %v %v", recorded, err)
	}
	target, err := db.GetResticTarget(ctx, "nas-host")
	if err != nil || target.Reported.State != agentwire.ResticTargetRunning || target.Requested.Dir != "/srv/restic" ||
		target.Reported.RetentionFloor == nil || *target.Reported.RetentionFloor != *floor {
		t.Fatalf("cible relue : %+v %v", target, err)
	}
	// Le lien d'une source vers la cible : ses mots de passe scellés.
	link := restic.Link{ID: "lnk1", SourceMachineID: "pve-host", TargetMachineID: "nas-host", CreatedAt: time.Unix(6, 0),
		Secrets: restic.LinkSecrets{REST: "rest-password-of-the-link-0123456789", Repo: "repo-password-of-the-link-0123456789"}}
	if _, isNew, err := db.EnsureResticLink(ctx, link); err != nil || !isNew {
		t.Fatalf("lien : %v %v", isNew, err)
	}
	if _, err := db.sql.Exec(`SELECT source_hash FROM restic_backups`); err == nil {
		t.Error("l'empreinte de la source n'a plus de colonne")
	}
}
