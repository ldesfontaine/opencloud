package store

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/module"
)

// Chaque module d'une portée a sa règle de présence, et elle ne désigne que
// des tables qui existent : sur une base vide, les deux lectures passent.
func TestModules_EveryModuleOfAScopeHasARule(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	for _, id := range module.In(module.ScopeMachine) {
		if _, found := machineModuleRules[id]; !found {
			t.Errorf("module de machine %s sans règle", id)
		}
	}
	for _, id := range module.In(module.ScopeInstallation) {
		if _, found := installationModuleRules[id]; !found {
			t.Errorf("module d'installation %s sans règle", id)
		}
	}
	if len(machineModuleRules) != len(module.In(module.ScopeMachine)) || len(installationModuleRules) != len(module.In(module.ScopeInstallation)) {
		t.Error("une règle vise un module qui n'est pas de cette portée")
	}
	if _, err := db.MachineModules(ctx); err != nil {
		t.Fatal(err)
	}
	states, err := db.InstallationModules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range states {
		if state.Configured {
			t.Errorf("%s configuré sur une base vide", state.ID)
		}
	}
}

// Le rôle apporte ses modules ; un fait observé en apporte d'autres, sans
// rôle ; une machine sans rôle ni fait n'en porte aucun.
func TestMachineModules_RoleAndObservedFacts(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	exec(t, db, `INSERT INTO machines (id, name, role, created_at) VALUES
		('plain', 'plain', 'generic', 1), ('dock', 'dock', 'docker', 1), ('pve', 'pve', 'proxmox', 1),
		('seen', 'seen', 'generic', 1), ('vm', 'vm', 'generic', 1), ('declared', 'declared', 'generic', 1)`)
	exec(t, db, `INSERT INTO ssh_reports (machine_id, reported_at) VALUES ('seen', 1)`)
	exec(t, db, `INSERT INTO machine_proxmox (machine_id, api_url, token_id, token_secret, verified_at, created_at)
		VALUES ('declared', 'https://pve.lan:8006', 'oc@pve!r', 'x', 1, 1)`)
	exec(t, db, `INSERT INTO proxmox_guests (machine_id, vmid, kind, node, linked_machine_id) VALUES ('declared', 100, 'qemu', 'n1', 'vm')`)

	got, err := db.MachineModules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]module.ID{
		"plain":    {},
		"dock":     {module.Containers},
		"pve":      {module.Hypervisor},
		"seen":     {module.SSHKeys},
		"vm":       {module.Guest},
		"declared": {module.Hypervisor},
	}
	for machineID, modules := range want {
		if !slices.Equal(got[machineID], modules) {
			t.Errorf("%s : %v, attendu %v", machineID, got[machineID], modules)
		}
	}
}

// Une sonde apporte son module à la machine qu'elle vise, sans rôle.
func TestMachineModules_AProbeBelongsToItsMachine(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m', 'm', 1)`)
	exec(t, db, `INSERT INTO probes (id, name, kind, target, machine_id, status, interval_seconds, timeout_seconds, failure_threshold, recovery_threshold, created_at)
		VALUES ('p', 'p', 'tcp', 'm:22', 'm', 'new', 60, 5, 3, 2, 1)`)
	got, err := db.MachineModules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got["m"], []module.ID{module.Probes}) {
		t.Fatalf("modules %v", got["m"])
	}
}

// Un module d'installation est configuré dès qu'un de ses objets existe.
func TestInstallationModules_ConfiguredOnceAnObjectExists(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m', 'm', 1)`)
	exec(t, db, `INSERT INTO probes (id, name, kind, target, machine_id, status, interval_seconds, timeout_seconds, failure_threshold, recovery_threshold, created_at)
		VALUES ('p', 'p', 'tcp', 'm:22', 'm', 'new', 60, 5, 3, 2, 1)`)
	states, err := db.InstallationModules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	configured := map[module.ID]bool{}
	for _, state := range states {
		configured[state.ID] = state.Configured
	}
	if !configured[module.Probes] || configured[module.Firewall] || configured[module.Jobs] || configured[module.Backups] || configured[module.Status] {
		t.Fatalf("états %v", configured)
	}
}

// Le jeton porte le rôle jusqu'à l'enrôlement, qui l'inscrit sur la machine ;
// un ré-enrôlement garde celui de la machine.
func TestEnroll_TheTokenRoleBecomesTheMachineRole(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	now := time.Unix(1_800_000_000, 0)
	cleartext, token, err := machine.NewToken("pve-1", "")
	if err != nil {
		t.Fatal(err)
	}
	token.Role, token.CreatedAt, token.ExpiresAt = machine.RoleProxmox, now, now.Add(time.Hour)
	if err := db.InsertToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	enrolled, err := db.Enroll(ctx, machine.HashToken(cleartext), candidate(t), newAgent(t, now), now)
	if err != nil {
		t.Fatal(err)
	}
	if enrolled.Role != machine.RoleProxmox {
		t.Fatalf("rôle %q", enrolled.Role)
	}
	if err := db.SetRole(ctx, enrolled.ID, machine.RoleDocker); err != nil {
		t.Fatal(err)
	}
	again := issueToken(t, db, "pve-1", enrolled.ID, now)
	reenrolled, err := db.Enroll(ctx, machine.HashToken(again), candidate(t), newAgent(t, now), now)
	if err != nil {
		t.Fatal(err)
	}
	if reenrolled.Role != machine.RoleDocker {
		t.Fatalf("le ré-enrôlement a changé le rôle : %q", reenrolled.Role)
	}
	if err := db.SetRole(ctx, "inconnue", machine.RoleDocker); err == nil {
		t.Fatal("un rôle posé sur une machine inconnue est accepté")
	}
}

// La 024 donne un rôle aux machines déjà enrôlées d'après les faits : Proxmox
// l'emporte sur Docker, Docker sur générique ; un jeton en attente reste
// générique.
func TestMigration024_DeducesTheRoleFromTheFacts(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version < 24 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES
		('declared', 'declared', 1), ('seen-pve', 'seen-pve', 1), ('both', 'both', 1), ('dock', 'dock', 1),
		('no-docker', 'no-docker', 1), ('plain', 'plain', 1)`)
	exec(t, db, `INSERT INTO machine_proxmox (machine_id, api_url, token_id, token_secret, verified_at, created_at)
		VALUES ('declared', 'https://pve.lan:8006', 'oc@pve!r', 'x', 1, 1)`)
	exec(t, db, `INSERT INTO machine_roles (machine_id, role, version) VALUES ('seen-pve', 'proxmox_ve', '8.2'), ('both', 'proxmox_ve', '')`)
	exec(t, db, `INSERT INTO machine_engines (machine_id, present, checked_at) VALUES
		('both', 1, 1), ('dock', 1, 1), ('no-docker', 0, 1)`)
	exec(t, db, `INSERT INTO enrollment_tokens (id, token_hash, token_prefix, name, created_at, expires_at) VALUES ('t', 'h', 'p', 'attente', 1, 2)`)

	if err := db.apply(ctx, "024_machine_role.sql", 24); err != nil {
		t.Fatal(err)
	}

	want := map[string]machine.Role{
		"declared": machine.RoleProxmox, "seen-pve": machine.RoleProxmox, "both": machine.RoleProxmox,
		"dock": machine.RoleDocker, "no-docker": machine.RoleGeneric, "plain": machine.RoleGeneric,
	}
	for id, role := range want {
		// GetMachine lit des colonnes d'une migration plus récente : on lit la ligne.
		var got string
		if err := db.sql.QueryRow(`SELECT role FROM machines WHERE id = ?`, id).Scan(&got); err != nil || got != string(role) {
			t.Errorf("%s : rôle %q (%v), attendu %q", id, got, err, role)
		}
	}
	if role := count(t, db, `SELECT COUNT(*) FROM enrollment_tokens WHERE role = 'generic'`); role != 1 {
		t.Fatalf("le jeton en attente n'est pas générique")
	}
	if _, err := db.sql.Exec(`UPDATE machines SET role = 'kubernetes' WHERE id = 'plain'`); err == nil {
		t.Fatal("un rôle hors liste est accepté")
	}
}
