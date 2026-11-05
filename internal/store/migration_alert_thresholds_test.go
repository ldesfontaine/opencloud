package store

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
)

// baseBefore037 monte une base à jour de la 036, comme une installation
// existante la voit juste avant la mise à jour.
func baseBefore037(t *testing.T) *DB {
	t.Helper()
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version < 37 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	return db
}

// La 037 sur une base existante : les seuils du disque déjà réglés passent au
// global tels quels, le rappel est activé, les alertes ouvertes partent de la
// mise à jour pour leur délai de rappel, et les livraisons déjà réservées
// survivent à la refonte de leur table.
func TestMigration037_KeepsTheDiskThresholdsTheAlertsAndTheDeliveries(t *testing.T) {
	ctx := context.Background()
	db := baseBefore037(t)
	exec(t, db, `INSERT INTO settings (id, language, disk_attention_percent, disk_danger_percent, mail_host, updated_at) VALUES (1, 'en', 70, 85, 'smtp.example.org', 1)`)
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m1', 'vps-1', 1)`)
	exec(t, db, `INSERT INTO alerts (kind, severity, status, object_kind, object_id, object_name, machine_id, opened_at, updated_at)
		VALUES ('disk_full', 'danger', 'open', 'volume', 'm1:/', '/', 'm1', 100, 100)`)
	exec(t, db, `INSERT INTO alerts (kind, severity, status, object_kind, object_id, object_name, machine_id, opened_at, updated_at, resolved_at)
		VALUES ('disk_full', 'attention', 'resolved', 'volume', 'm1:/data', '/data', 'm1', 50, 60, 60)`)
	exec(t, db, `INSERT INTO alert_channels (id, name, url, format, min_severity, created_at) VALUES (1, 'ops', 'x', 'json', 'attention', 1)`)
	exec(t, db, `INSERT INTO alert_deliveries (alert_id, channel_id, event, status, created_at, updated_at) VALUES (1, 1, 'opened', 'delivered', 100, 100)`)
	exec(t, db, `INSERT INTO alert_deliveries (alert_id, channel_id, event, status, attempts, reason, code, created_at, updated_at, next_attempt_at)
		VALUES (2, 1, 'resolved', 'failed', 3, 'status', 503, 60, 61, 90)`)

	before := time.Now().Unix()
	if err := db.apply(ctx, "037_alert_thresholds.sql", 37); err != nil {
		t.Fatal(err)
	}

	rules, err := db.ListThresholds(ctx, "")
	if err != nil || len(rules) != 1 {
		t.Fatalf("thresholds %+v, %v", rules, err)
	}
	if got := rules[0]; got.MachineID != "" || got.Resource != alert.ResourceDisk || got.Attention != 70 || got.Danger != 85 || got.Window != 0 {
		t.Fatalf("the disk thresholds already set must be kept: %+v", got)
	}
	current, err := db.LoadSettings(ctx)
	if err != nil || current.Language != "en" || current.MailHost != "smtp.example.org" || current.DangerReminderHours != 12 {
		t.Fatalf("settings %+v, %v", current, err)
	}

	open, err := db.GetAlert(ctx, 1)
	if err != nil || open.NotifiedAt.Unix() < before || !open.RemindedAt.IsZero() {
		t.Fatalf("an open alert starts its reminder delay at the update: %+v, %v", open, err)
	}
	resolved, err := db.GetAlert(ctx, 2)
	if err != nil || !resolved.NotifiedAt.IsZero() {
		t.Fatalf("a resolved alert has nothing to remind: %+v, %v", resolved, err)
	}

	deliveries, err := db.ListDeliveries(ctx, 2)
	if err != nil || len(deliveries) != 1 || deliveries[0].Status != alert.DeliveryFailed || deliveries[0].Attempts != 3 ||
		deliveries[0].Reason != alert.ReasonStatus || deliveries[0].Code != 503 || deliveries[0].NextAttemptAt.Unix() != 90 {
		t.Fatalf("the failed delivery must survive the new table: %+v, %v", deliveries, err)
	}
	// La table refaite accepte l'événement du rappel, et garde l'unicité.
	exec(t, db, `INSERT INTO alert_deliveries (alert_id, channel_id, event, status, created_at, updated_at) VALUES (1, 1, 'reminded', 'pending', 200, 200)`)
	if _, err := db.sql.Exec(`INSERT INTO alert_deliveries (alert_id, channel_id, event, status, created_at, updated_at) VALUES (1, 1, 'reminded', 'pending', 201, 201)`); err == nil {
		t.Fatal("a second reminder of the same alert on the same channel must be refused")
	}
	if _, err := db.sql.Exec(`INSERT INTO alert_deliveries (alert_id, channel_id, event, status, created_at, updated_at) VALUES (1, 1, 'unknown', 'pending', 202, 202)`); err == nil {
		t.Fatal("the closed list of events must still be enforced")
	}
	if count(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE name IN ('alert_deliveries_pending', 'alert_deliveries_due')`) != 2 {
		t.Fatal("the delivery indexes must be rebuilt")
	}
}

// Une valeur écrite à la main hors des bornes n'était pas lue : elle valait
// les valeurs par défaut, et la migration ne la reprend pas. Sans réglage,
// rien n'est repris non plus.
func TestMigration037_DoesNotCarryAnOutOfBoundsDiskSetting(t *testing.T) {
	for name, values := range map[string][2]int{"attention too low": {40, 95}, "danger too high": {85, 100}, "inverted": {95, 85}, "never set": {0, 0}} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			db := baseBefore037(t)
			exec(t, db, `INSERT INTO settings (id, language, disk_attention_percent, disk_danger_percent, updated_at) VALUES (1, '', ?, ?, 1)`, values[0], values[1])
			if err := db.apply(ctx, "037_alert_thresholds.sql", 37); err != nil {
				t.Fatal(err)
			}
			if rules, err := db.ListThresholds(ctx, ""); err != nil || len(rules) != 0 {
				t.Fatalf("nothing to carry: %+v, %v", rules, err)
			}
		})
	}
}

func TestMigration037_ADatabaseWithoutSettingsGetsTheReminderOnItsFirstWrite(t *testing.T) {
	ctx := context.Background()
	db := baseBefore037(t)
	if err := db.apply(ctx, "037_alert_thresholds.sql", 37); err != nil {
		t.Fatal(err)
	}
	if current, err := db.LoadSettings(ctx); err != nil || current.DangerReminderHours != 12 {
		t.Fatalf("no row yet: %+v, %v", current, err)
	}
}

// Une surcharge part avec sa machine, et chaque ressource n'a qu'une règle
// par périmètre.
func TestThresholds_AreOnePerResourceAndScope_AndLeaveWithTheirMachine(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m1', 'vps-1', 1)`)
	cpu := alert.Rule{Resource: alert.ResourceCPU, Attention: 80, Danger: 90, Window: 10 * time.Minute}
	if err := db.UpsertThreshold(ctx, alert.Threshold{Rule: cpu}); err != nil {
		t.Fatal(err)
	}
	cpu.Attention = 85
	if err := db.UpsertThreshold(ctx, alert.Threshold{Rule: cpu}); err != nil {
		t.Fatal(err)
	}
	own := alert.Rule{Resource: alert.ResourceCPU, Attention: 98, Danger: 100, Window: 30 * time.Minute}
	if err := db.UpsertThreshold(ctx, alert.Threshold{MachineID: "m1", Rule: own}); err != nil {
		t.Fatal(err)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM alert_thresholds`); got != 2 {
		t.Fatalf("one global and one machine rule, got %d rows", got)
	}
	rows, err := db.ListThresholds(ctx, "m1")
	if err != nil || len(rows) != 2 {
		t.Fatalf("%+v, %v", rows, err)
	}
	if other, err := db.ListThresholds(ctx, "m2"); err != nil || len(other) != 1 || other[0].MachineID != "" || other[0].Attention != 85 {
		t.Fatalf("another machine sees the global rule only: %+v, %v", other, err)
	}
	exec(t, db, `DELETE FROM machines WHERE id = 'm1'`)
	if got := count(t, db, `SELECT COUNT(*) FROM alert_thresholds`); got != 1 {
		t.Fatalf("the override must leave with its machine: %d rows", got)
	}
	if err := db.DeleteThreshold(ctx, "", alert.ResourceCPU); err != nil {
		t.Fatal(err)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM alert_thresholds`); got != 0 {
		t.Fatalf("%d rows", got)
	}
}

func TestAverageUsage_AveragesPercentagesOfTheWindowOnly(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m1', 'vps-1', 1)`)
	sample := func(at int64, cpu float64, memUsed, memTotal int64) {
		exec(t, db, `INSERT INTO machine_samples (machine_id, sampled_at, cpu_percent, cpu_cores, load_1, mem_used, mem_total, swap_used, swap_total, disk_used, disk_total, net_rx_per_second, net_tx_per_second)
			VALUES ('m1', ?, ?, 2, 0, ?, ?, 0, 0, 0, 0, 0, 0)`, at, cpu, memUsed, memTotal)
	}
	sample(100, 10, 50, 100)
	sample(200, 30, 90, 100)
	sample(300, 50, 40, 200)
	sample(900, 99, 100, 100)
	got, err := db.AverageUsage(ctx, "m1", time.Unix(100, 0), time.Unix(300, 0))
	if err != nil {
		t.Fatal(err)
	}
	if got.Samples != 3 || got.First.Unix() != 100 || got.CPUPercent != 30 || got.MemoryPercent != (50.0+90.0+20.0)/3 {
		t.Fatalf("%+v", got)
	}
	empty, err := db.AverageUsage(ctx, "m1", time.Unix(400, 0), time.Unix(500, 0))
	if err != nil || empty.Samples != 0 || !empty.First.IsZero() {
		t.Fatalf("no reading: %+v, %v", empty, err)
	}
}

// Une base à jour de la 035 monte dans l'ordre 036, 037, 038 : les seuils
// disque réglés, les alertes et les mesures déjà écrites survivent, et la
// moyenne de la mémoire lit mem_used tel quel (l'ARC n'y est pas ajouté).
func TestMigrations_036_037_038_RunInOrderOnAnExistingDatabase(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version < 36 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec(t, db, `INSERT INTO settings (id, language, disk_attention_percent, disk_danger_percent, updated_at) VALUES (1, 'fr', 70, 85, 1)`)
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m1', 'vps-1', 1)`)
	exec(t, db, `INSERT INTO machine_samples (machine_id, sampled_at, cpu_percent, cpu_cores, load_1, mem_used, mem_total, swap_used, swap_total, disk_used, disk_total, net_rx_per_second, net_tx_per_second)
		VALUES ('m1', 100, 10, 2, 0, 25, 100, 0, 0, 0, 0, 0, 0)`)
	for _, step := range []struct {
		name    string
		version int
	}{{"036_agent_anchors.sql", 36}, {"037_alert_thresholds.sql", 37}, {"038_zfs_cache.sql", 38}} {
		if err := db.apply(ctx, step.name, step.version); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
	}
	versions, err := db.AppliedVersions(ctx)
	if err != nil || len(versions) < 3 || versions[len(versions)-3] != 36 || versions[len(versions)-2] != 37 || versions[len(versions)-1] != 38 {
		t.Fatalf("versions %v, %v", versions, err)
	}
	rules, err := db.ListThresholds(ctx, "")
	if err != nil || len(rules) != 1 || rules[0].Attention != 70 || rules[0].Danger != 85 {
		t.Fatalf("disk thresholds %+v, %v", rules, err)
	}
	exec(t, db, `UPDATE machine_samples SET zfs_cache = 9999 WHERE machine_id = 'm1'`)
	got, err := db.AverageUsage(ctx, "m1", time.Unix(0, 0), time.Unix(200, 0))
	if err != nil || got.MemoryPercent != 25 {
		t.Fatalf("memory average must read mem_used only: %+v, %v", got, err)
	}
}
