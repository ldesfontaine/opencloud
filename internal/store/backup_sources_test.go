package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/backup"
	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// La 012 refait backup_plans pour y loger les sources : le plan « database »
// d'une installation actuelle garde son identifiant, sa destination, son
// historique et son lien vers le dernier ordre, avec la source « database » ;
// les clés étrangères et l'index de la destination sont refaits avec elle.
func TestMigration012_KeepsTheDatabasePlanAndGivesItItsSource(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version < 12 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m1', 'vps', 1)`)
	exec(t, db, `INSERT INTO backup_destinations (id, name, machine_id, path, created_at) VALUES ('bkd_1', 'store', 'm1', '/srv/backups', 1)`)
	exec(t, db, `INSERT INTO action_runs (id, action, machine_name, state, not_after, seq, requested_by, requested_at)
		VALUES ('run1', 'backup.receive', 'vps', 'succeeded', 1, 1, 'operator:lucas', 1)`)
	exec(t, db, `INSERT INTO backup_plans (id, subject, destination_id, schedule, keep, last_attempt_at, last_error, last_run_id, created_at, updated_at)
		VALUES ('bkp_1', 'database', 'bkd_1', 'daily', 14, 1700000000, 'backup.machine_offline', 'run1', 5, 6)`)

	if err := db.apply(ctx, "012_backup_sources.sql", 12); err != nil {
		t.Fatal(err)
	}

	plan, err := db.GetBackupPlan(ctx, backup.SubjectDatabase)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ID != "bkp_1" || plan.SourceKind != backup.SourceDatabase || plan.Name != "" || plan.SourceMachineID != "" || plan.Producer != "" ||
		plan.Selector != "" || plan.DestinationID != "bkd_1" || plan.Keep != 14 || plan.Schedule != backup.ScheduleDaily ||
		plan.LastError != "backup.machine_offline" || plan.LastRunID != "run1" || plan.LastAttemptAt.Unix() != 1700000000 ||
		plan.CreatedAt.Unix() != 5 || plan.UpdatedAt.Unix() != 6 {
		t.Fatalf("plan %+v", plan)
	}
	// Les clés étrangères sont refaites avec la table : la destination reste
	// retenue, la purge du run vide le lien sans défaire le plan.
	if _, err := db.sql.Exec(`DELETE FROM backup_destinations WHERE id = 'bkd_1'`); err == nil {
		t.Fatal("la destination du plan peut être retirée : le restrict a disparu")
	}
	exec(t, db, `DELETE FROM action_runs WHERE id = 'run1'`)
	if got := text(t, db, `SELECT COALESCE(last_run_id, 'vide') FROM backup_plans WHERE id = 'bkp_1'`); got != "vide" {
		t.Fatalf("last_run_id = %s", got)
	}
	indexes := text(t, db, `SELECT group_concat(name, ',') FROM (SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'backup_plans' AND sql IS NOT NULL ORDER BY name)`)
	if indexes != "backup_plans_destination,backup_plans_source_machine" {
		t.Fatalf("index : %s", indexes)
	}
}

// Le schéma garde seul ce que le service juge : la base n'a ni machine, ni
// producteur, ni sélecteur ; un plan agent a les trois et ne s'appelle pas
// « database » ; un sujet est un mot de minuscules, chiffres et tirets.
func TestBackupPlans_TheSchemaRefusesASourceThatContradictsItself(t *testing.T) {
	db := openTestDB(t)
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m1', 'vps', 1), ('m2', 'pve', 1)`)
	exec(t, db, `INSERT INTO backup_destinations (id, name, machine_id, path, created_at) VALUES ('bkd_1', 'store', 'm1', '/srv/backups', 1)`)
	insert := func(subject, kind, machine, producer, selector string) error {
		_, err := db.sql.Exec(`INSERT INTO backup_plans (id, subject, source_kind, source_machine_id, producer, selector, destination_id, schedule, keep, created_at, updated_at)
			VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), 'bkd_1', 'daily', 14, 1, 1)`, "bkp_"+subject, subject, kind, machine, producer, selector)
		return err
	}
	for name, run := range map[string]func() error{
		"the database with a machine":     func() error { return insert("database", "database", "m2", "", "") },
		"the database with a producer":    func() error { return insert("database", "database", "", "proxmox-vzdump", "") },
		"a database plan of another name": func() error { return insert("pve", "database", "", "", "") },
		"an agent plan called database":   func() error { return insert("database", "agent", "m2", "proxmox-vzdump", "all") },
		"an agent plan without machine":   func() error { return insert("pve", "agent", "", "proxmox-vzdump", "all") },
		"an agent plan without producer":  func() error { return insert("pve", "agent", "m2", "", "all") },
		"an agent plan without selector":  func() error { return insert("pve", "agent", "m2", "proxmox-vzdump", "") },
		"a source of another kind":        func() error { return insert("pve", "nfs", "m2", "proxmox-vzdump", "all") },
		"a subject in capitals":           func() error { return insert("Pve", "agent", "m2", "proxmox-vzdump", "all") },
		"a subject with a slash":          func() error { return insert("pve/vm", "agent", "m2", "proxmox-vzdump", "all") },
		"a subject starting with a dash":  func() error { return insert("-pve", "agent", "m2", "proxmox-vzdump", "all") },
		"an empty subject":                func() error { return insert("", "agent", "m2", "proxmox-vzdump", "all") },
	} {
		if err := run(); err == nil {
			t.Errorf("%s: le schéma l'accepte", name)
		}
	}
	for name, run := range map[string]func() error{
		"the database":   func() error { return insert("database", "database", "", "", "") },
		"an agent plan":  func() error { return insert("pve-vm", "agent", "m2", "proxmox-vzdump", "all") },
		"a second agent": func() error { return insert("pve-ct", "agent", "m2", "proxmox-vzdump", "200") },
	} {
		if err := run(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Les plans se lisent la base d'abord, puis par sujet ; une source agent
// garde sa machine, son producteur et son sélecteur, avec le nom de la
// machine ; le sujet est l'identité, refaire un plan garde son histoire.
func TestBackupPlans_ListsTheDatabaseFirstAndRoundTripsAnAgentSource(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	destination := seedBackupPlan(t, db)
	source := enrolledMachine(t, db, "pve")
	for _, subject := range []string{"zeta", "alpha"} {
		plan := backup.Plan{
			ID: "bkp_" + subject, Subject: subject, Name: "Libellé " + subject, SourceKind: backup.SourceAgent, SourceMachineID: source,
			Producer: machine.ProducerProxmoxVzdump, Selector: "5010,5012", DestinationID: destination.ID, Keep: 5,
			Schedule: backup.ScheduleDaily, CreatedAt: backupsNow, UpdatedAt: backupsNow,
		}
		if err := db.SaveBackupPlan(ctx, plan); err != nil {
			t.Fatal(err)
		}
	}
	plans, err := db.ListBackupPlans(ctx)
	if err != nil || len(plans) != 3 || plans[0].Subject != backup.SubjectDatabase || plans[1].Subject != "alpha" || plans[2].Subject != "zeta" {
		t.Fatalf("plans %+v %v", plans, err)
	}
	if plans[0].SourceMachineID != "" || plans[0].SourceMachineName != "" || plans[0].Producer != "" || plans[0].Selector != "" {
		t.Fatalf("the database has no source machine: %+v", plans[0])
	}
	alpha := plans[1]
	if alpha.Name != "Libellé alpha" || alpha.SourceKind != backup.SourceAgent || alpha.SourceMachineID != source || alpha.SourceMachineName != "pve" ||
		alpha.Producer != machine.ProducerProxmoxVzdump || alpha.Selector != "5010,5012" || alpha.Keep != 5 {
		t.Fatalf("alpha %+v", alpha)
	}

	if err := db.NoteBackupAttempt(ctx, "alpha", backupsNow, "backup.source_not_implemented", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.NoteBackupAttempt(ctx, "inconnu", backupsNow, "", ""); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("an unknown subject: %v", err)
	}
	changed := alpha
	changed.ID, changed.Name, changed.Selector, changed.Keep = "bkp_ignored", "", "all", 30
	if err := db.SaveBackupPlan(ctx, changed); err != nil {
		t.Fatal(err)
	}
	again, err := db.GetBackupPlan(ctx, "alpha")
	if err != nil || again.ID != "bkp_alpha" || again.Name != "" || again.Selector != "all" || again.Keep != 30 || again.LastError != "backup.source_not_implemented" {
		t.Fatalf("again %+v %v", again, err)
	}
	// Les autres plans n'ont pas bougé.
	if zeta, _ := db.GetBackupPlan(ctx, "zeta"); zeta.LastError != "" || zeta.Selector != "5010,5012" {
		t.Fatalf("zeta %+v", zeta)
	}
	if err := db.DeleteBackupPlan(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetBackupPlan(ctx, "alpha"); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
	if plans, _ := db.ListBackupPlans(ctx); len(plans) != 2 {
		t.Fatalf("%d plans left, want 2", len(plans))
	}
}

// La machine qui produit les sauvegardes d'un plan est retenue par son plan
// de suppression, nommée plan par plan (le libellé, ou le sujet à défaut), et
// le refus a sa clé ; la clé étrangère le ferait aussi en restrict.
func TestDeleteMachine_IsHeldBackByThePlansItProduces(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	destination := seedBackupPlan(t, db)
	source := enrolledMachine(t, db, "pve")
	for _, plan := range []backup.Plan{
		{ID: "bkp_a", Subject: "pve-vm", Name: "Machines virtuelles"},
		{ID: "bkp_b", Subject: "pve-ct"},
	} {
		plan.SourceKind, plan.SourceMachineID, plan.Producer, plan.Selector = backup.SourceAgent, source, machine.ProducerProxmoxVzdump, "all"
		plan.DestinationID, plan.Keep, plan.Schedule, plan.CreatedAt, plan.UpdatedAt = destination.ID, 14, backup.ScheduleDaily, backupsNow, backupsNow
		if err := db.SaveBackupPlan(ctx, plan); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.sql.Exec(`DELETE FROM machines WHERE id = ?`, source); err == nil {
		t.Fatal("la clé étrangère doit refuser la suppression de la machine source")
	}
	err := db.DeleteMachine(ctx, source)
	var refused *deletion.RefusedError
	if !errors.Is(err, machine.ErrHasBackupPlans) || !errors.As(err, &refused) {
		t.Fatalf("got %v", err)
	}
	want := []deletion.Blocker{{Kind: deletion.KindBackupPlans, Objects: []deletion.Object{{ID: "bkp_b", Name: "pve-ct"}, {ID: "bkp_a", Name: "Machines virtuelles"}}}}
	if !reflect.DeepEqual(refused.Plan.Blockers, want) {
		t.Fatalf("blockers %+v", refused.Plan.Blockers)
	}
	for _, subject := range []string{"pve-vm", "pve-ct"} {
		if err := db.DeleteBackupPlan(ctx, subject); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.DeleteMachine(ctx, source); err != nil {
		t.Fatalf("released: %v", err)
	}
}
