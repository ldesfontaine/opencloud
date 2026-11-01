package store

import (
	"context"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/proxmox"
)

// La 035 sur une base qui a déjà un hôte Proxmox relevé : l'hôte, ses VM et ses
// stockages restent tels quels, chaque stockage reçoit la valeur sûre
// (« lisible », 0) jusqu'au prochain relevé, la colonne refuse autre chose que
// 0 et 1, et le relevé suivant l'écrit.
func TestMigration035_KeepsTheReadingAndStartsEveryStorageReadable(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version < 35 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec(t, db, `INSERT INTO machines (id, name, hostname, created_at) VALUES ('pve-host', 'Proxmox', 'pve', 1)`)
	exec(t, db, `INSERT INTO machine_proxmox (machine_id, api_url, token_id, token_secret, verified_at, last_read_at, last_error, created_at)
		VALUES ('pve-host', 'https://pve.lan:8006', 'lecteur@pve!lecture', '', 1, 2, 'proxmox.unreachable', 1)`)
	exec(t, db, `INSERT INTO proxmox_guests (machine_id, vmid, kind, name, status, node, backup_count) VALUES ('pve-host', 200, 'lxc', 'dns', 'running', 'pve', 0)`)
	exec(t, db, `INSERT INTO proxmox_storages (machine_id, node, storage, type, content, active) VALUES
		('pve-host', 'pve', 'pi-sauvegardes', 'nfs', 'backup', 1), ('pve-host', 'pve', 'local', 'dir', 'iso', 1)`)

	if err := db.apply(ctx, "035_proxmox_backups_unreadable.sql", 35); err != nil {
		t.Fatal(err)
	}

	if got := count(t, db, `SELECT COUNT(*) FROM proxmox_storages WHERE machine_id = 'pve-host' AND backups_unreadable = 0`); got != 2 {
		t.Errorf("%d stockages lisibles, 2 attendus : la valeur sûre est « lisible »", got)
	}
	if got := text(t, db, `SELECT last_error FROM machine_proxmox WHERE machine_id = 'pve-host'`); got != "proxmox.unreachable" {
		t.Errorf("last_error %q : la clé du refus reste la seule chose que la colonne porte", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM proxmox_guests WHERE machine_id = 'pve-host'`); got != 1 {
		t.Errorf("%d VM, 1 attendue", got)
	}
	if _, err := db.sql.Exec(`UPDATE proxmox_storages SET backups_unreadable = 2`); err == nil {
		t.Error("la colonne accepte une valeur qui n'est ni 0 ni 1")
	}

	// Le relevé suivant dit ce qu'il a vu, et un échec ne l'efface pas.
	reading := proxmox.Reading{
		Version: "8.2.5", Node: "pve",
		Guests:   []proxmox.Guest{{VMID: 200, Kind: proxmox.KindLXC, Name: "dns", Node: "pve"}},
		Storages: []proxmox.Storage{{Node: "pve", Name: "pi-sauvegardes", Type: "nfs", Content: []string{"backup"}, Active: true, BackupsUnreadable: true}},
	}
	if err := db.ReplaceProxmoxReading(ctx, "pve-host", reading, proxmoxAt); err != nil {
		t.Fatal(err)
	}
	if err := db.NoteProxmoxFailure(ctx, "pve-host", "proxmox.unreachable"); err != nil {
		t.Fatal(err)
	}
	view, err := db.GetProxmoxView(ctx, "pve-host")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Host.BlindStorages) != 1 || view.Host.BlindStorages[0] != "pi-sauvegardes" || !view.Guests[0].BackupUnknown || view.Host.LastError != "proxmox.unreachable" {
		t.Errorf("après un échec : %+v, invité %+v", view.Host, view.Guests[0])
	}
}
