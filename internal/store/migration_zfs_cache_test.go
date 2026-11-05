package store

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/sampler"
)

// La 038 sur une base existante : l'échantillon déjà là reste, sans cache
// ZFS (NULL, pas zéro) ; un nouveau le porte.
func TestMigration038_KeepsOldSamplesWithoutZFSCache(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version < 38 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec(t, db, `INSERT INTO machines (id, name, hostname, created_at) VALUES ('pi', 'Pi', 'pi', 1)`)
	exec(t, db, `INSERT INTO machine_samples (machine_id, sampled_at, cpu_percent, cpu_cores, load_1, mem_used, mem_total,
		swap_used, swap_total, disk_used, disk_total, net_rx_per_second, net_tx_per_second) VALUES ('pi', 100, 1, 4, 0, 5, 10, 0, 0, 0, 0, 0, 0)`)

	if err := db.apply(ctx, "038_zfs_cache.sql", 38); err != nil {
		t.Fatal(err)
	}
	old, err := db.LatestSample(ctx, "pi")
	if err != nil || old.MemUsed != 5 || old.ZFSCache != nil {
		t.Fatalf("old sample: %+v %v", old, err)
	}

	cache := int64(9900)
	fresh := sampler.Reading{SampledAt: time.Unix(200, 0).UTC(), CPUCores: 4, MemUsed: 1, MemTotal: 10, ZFSCache: &cache}
	if err := db.InsertSamples(ctx, "pi", []sampler.Reading{fresh}); err != nil {
		t.Fatal(err)
	}
	latest, err := db.LatestSample(ctx, "pi")
	if err != nil || latest.ZFSCache == nil || *latest.ZFSCache != 9900 {
		t.Fatalf("new sample: %+v %v", latest, err)
	}
}
