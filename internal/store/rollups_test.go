package store

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/resource"
	"github.com/ldesfontaine/opencloud/internal/sampler"
)

// Les heures et les jours partagent machine_rollups sans se mêler : chaque
// lecture, chaque agrégation et chaque purge ne touche que sa granularité,
// et repasser sur un seau le réécrit sans le doubler.
func TestMachineRollups_KeepHoursAndDaysApart(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	day := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	hour := day.Add(9 * time.Hour)
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES (?, 'opencloud', 1)`, machine.LocalID)
	readings := []sampler.Reading{
		{SampledAt: hour.Add(10 * time.Second), CPUPercent: 20, CPUCores: 2, MemTotal: 8},
		{SampledAt: hour.Add(20 * time.Second), CPUPercent: 40, CPUCores: 4, MemTotal: 8},
	}
	if err := db.InsertSamples(ctx, machine.LocalID, readings); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if err := db.RollupHourly(ctx, hour, hour.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	hours, err := db.ListRollups(ctx, machine.LocalID, resource.GranularityHour, day, day.Add(24*time.Hour))
	if err != nil || len(hours) != 1 || hours[0].CPUPercent != 30 || hours[0].CPUCores != 4 || !hours[0].SampledAt.Equal(hour) {
		t.Fatalf("heures = %+v (%v)", hours, err)
	}
	if days, _ := db.ListRollups(ctx, machine.LocalID, resource.GranularityDay, day, day.Add(24*time.Hour)); len(days) != 0 {
		t.Fatalf("un jour agrégé avant l'heure : %+v", days)
	}

	for range 2 {
		if err := db.RollupDaily(ctx, day, day.Add(24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	days, err := db.ListRollups(ctx, machine.LocalID, resource.GranularityDay, day, day.Add(24*time.Hour))
	if err != nil || len(days) != 1 || days[0].CPUPercent != 30 || !days[0].SampledAt.Equal(day) {
		t.Fatalf("jours = %+v (%v)", days, err)
	}
	if got := countRows(t, db, "machine_rollups"); got != 2 {
		t.Fatalf("%d seaux, 2 attendus : une heure, un jour", got)
	}

	// Les heures d'avant 10 h partent, le jour reste : il n'est pas d'avant lui-même.
	if err := db.PurgeSamples(ctx, day, hour.Add(time.Hour), day); err != nil {
		t.Fatal(err)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM machine_rollups WHERE granularity = 'hour'`); got != 0 {
		t.Fatalf("%d heure(s) après la purge", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM machine_rollups WHERE granularity = 'day'`); got != 1 {
		t.Fatalf("%d jour(s) après la purge, 1 attendu", got)
	}
}
