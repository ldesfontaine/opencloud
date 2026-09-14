package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/resource"
	"github.com/ldesfontaine/opencloud/internal/sampler"
)

// Les colonnes d'un seau agrégé, dans l'ordre où scanReading les lit ; les
// trois requêtes de lecture rendent la même forme, le brut groupé compris.
const rollupColumns = `avg_cpu_percent, cpu_cores, avg_load_1, avg_mem_used, avg_mem_total,
	avg_swap_used, avg_swap_total, avg_disk_used, avg_disk_total, avg_net_rx_per_second, avg_net_tx_per_second`

// Les agrégats du brut : moyennes, et le plus grand nombre de cœurs vu.
const aggregateRaw = `AVG(cpu_percent), MAX(cpu_cores), AVG(load_1),
	CAST(AVG(mem_used) AS INTEGER), CAST(AVG(mem_total) AS INTEGER),
	CAST(AVG(swap_used) AS INTEGER), CAST(AVG(swap_total) AS INTEGER),
	CAST(AVG(disk_used) AS INTEGER), CAST(AVG(disk_total) AS INTEGER),
	CAST(AVG(net_rx_per_second) AS INTEGER), CAST(AVG(net_tx_per_second) AS INTEGER)`

// Les agrégats de l'horaire vers le journalier, pondérés par le nombre
// d'échantillons : une heure à 3 échantillons ne pèse pas une heure à 360.
const aggregateHourly = `SUM(avg_cpu_percent * sample_count) / SUM(sample_count), MAX(cpu_cores),
	SUM(avg_load_1 * sample_count) / SUM(sample_count),
	SUM(avg_mem_used * sample_count) / SUM(sample_count), SUM(avg_mem_total * sample_count) / SUM(sample_count),
	SUM(avg_swap_used * sample_count) / SUM(sample_count), SUM(avg_swap_total * sample_count) / SUM(sample_count),
	SUM(avg_disk_used * sample_count) / SUM(sample_count), SUM(avg_disk_total * sample_count) / SUM(sample_count),
	SUM(avg_net_rx_per_second * sample_count) / SUM(sample_count), SUM(avg_net_tx_per_second * sample_count) / SUM(sample_count)`

const rollupUpdate = `ON CONFLICT (machine_id, bucket) DO UPDATE SET
	avg_cpu_percent = excluded.avg_cpu_percent, cpu_cores = excluded.cpu_cores, avg_load_1 = excluded.avg_load_1,
	avg_mem_used = excluded.avg_mem_used, avg_mem_total = excluded.avg_mem_total,
	avg_swap_used = excluded.avg_swap_used, avg_swap_total = excluded.avg_swap_total,
	avg_disk_used = excluded.avg_disk_used, avg_disk_total = excluded.avg_disk_total,
	avg_net_rx_per_second = excluded.avg_net_rx_per_second, avg_net_tx_per_second = excluded.avg_net_tx_per_second,
	sample_count = excluded.sample_count`

// InsertSamples écrit un lot dans une transaction ; un échantillon déjà
// là, rejoué après une coupure, est ignoré.
func (db *DB) InsertSamples(ctx context.Context, machineID string, readings []sampler.Reading) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin insert samples: %w", err)
	}
	defer tx.Rollback()
	for _, reading := range readings {
		_, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO machine_samples (machine_id, sampled_at, cpu_percent, cpu_cores, load_1,
				mem_used, mem_total, swap_used, swap_total, disk_used, disk_total, net_rx_per_second, net_tx_per_second)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			machineID, reading.SampledAt.Unix(), reading.CPUPercent, reading.CPUCores, reading.Load1,
			reading.MemUsed, reading.MemTotal, reading.SwapUsed, reading.SwapTotal,
			reading.DiskUsed, reading.DiskTotal, reading.NetRxPerSecond, reading.NetTxPerSecond)
		if err != nil {
			return fmt.Errorf("insert sample: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit insert samples: %w", err)
	}
	return nil
}

const latestSampleQuery = `
	SELECT machine_id, sampled_at, cpu_percent, cpu_cores, load_1, mem_used, mem_total,
		swap_used, swap_total, disk_used, disk_total, net_rx_per_second, net_tx_per_second
	FROM machine_samples`

func (db *DB) LatestSample(ctx context.Context, machineID string) (resource.Sample, error) {
	row := db.sql.QueryRowContext(ctx, latestSampleQuery+` WHERE machine_id = ? ORDER BY sampled_at DESC LIMIT 1`, machineID)
	sample, err := scanSample(row)
	if errors.Is(err, sql.ErrNoRows) {
		return resource.Sample{}, resource.ErrNoSample
	}
	return sample, err
}

// LatestSamples rend le dernier échantillon de chaque machine qui en a un.
func (db *DB) LatestSamples(ctx context.Context) ([]resource.Sample, error) {
	rows, err := db.sql.QueryContext(ctx, latestSampleQuery+` AS latest
		WHERE sampled_at = (SELECT MAX(sampled_at) FROM machine_samples WHERE machine_id = latest.machine_id)
		ORDER BY machine_id`)
	if err != nil {
		return nil, fmt.Errorf("list latest samples: %w", err)
	}
	defer rows.Close()
	var samples []resource.Sample
	for rows.Next() {
		sample, err := scanSample(rows)
		if err != nil {
			return nil, err
		}
		samples = append(samples, sample)
	}
	return samples, rows.Err()
}

// ListSamples groupe le brut par pas : un point par seau de step, daté du
// début du seau, même forme que les seaux agrégés.
func (db *DB) ListSamples(ctx context.Context, machineID string, from, to time.Time, step time.Duration) ([]sampler.Reading, error) {
	stepSeconds := int64(step / time.Second)
	if stepSeconds <= 0 {
		stepSeconds = 1
	}
	return db.queryReadings(ctx, `
		SELECT (sampled_at / ?) * ? AS bucket, `+aggregateRaw+`
		FROM machine_samples WHERE machine_id = ? AND sampled_at >= ? AND sampled_at <= ?
		GROUP BY bucket ORDER BY bucket`,
		stepSeconds, stepSeconds, machineID, from.Unix(), to.Unix())
}

func (db *DB) ListHourly(ctx context.Context, machineID string, from, to time.Time) ([]sampler.Reading, error) {
	return db.queryReadings(ctx, `SELECT bucket, `+rollupColumns+` FROM machine_samples_hourly
		WHERE machine_id = ? AND bucket >= ? AND bucket <= ? ORDER BY bucket`,
		machineID, from.Unix(), to.Unix())
}

func (db *DB) ListDaily(ctx context.Context, machineID string, from, to time.Time) ([]sampler.Reading, error) {
	return db.queryReadings(ctx, `SELECT bucket, `+rollupColumns+` FROM machine_samples_daily
		WHERE machine_id = ? AND bucket >= ? AND bucket <= ? ORDER BY bucket`,
		machineID, from.Unix(), to.Unix())
}

func (db *DB) queryReadings(ctx context.Context, query string, args ...any) ([]sampler.Reading, error) {
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list readings: %w", err)
	}
	defer rows.Close()
	readings := []sampler.Reading{}
	for rows.Next() {
		reading, err := scanReading(rows)
		if err != nil {
			return nil, err
		}
		readings = append(readings, reading)
	}
	return readings, rows.Err()
}

// RollupHourly agrège un seau du brut en une seule instruction ; le seau
// existant est réécrit, jamais dupliqué.
func (db *DB) RollupHourly(ctx context.Context, bucketStart, bucketEnd time.Time) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO machine_samples_hourly (machine_id, bucket, `+rollupColumns+`, sample_count)
		SELECT machine_id, ?, `+aggregateRaw+`, COUNT(*)
		FROM machine_samples WHERE sampled_at >= ? AND sampled_at < ?
		GROUP BY machine_id `+rollupUpdate,
		bucketStart.Unix(), bucketStart.Unix(), bucketEnd.Unix())
	if err != nil {
		return fmt.Errorf("rollup hourly: %w", err)
	}
	return nil
}

// RollupDaily agrège les seaux horaires d'un jour, pondérés par leur
// nombre d'échantillons.
func (db *DB) RollupDaily(ctx context.Context, bucketStart, bucketEnd time.Time) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO machine_samples_daily (machine_id, bucket, `+rollupColumns+`, sample_count)
		SELECT machine_id, ?, `+aggregateHourly+`, SUM(sample_count)
		FROM machine_samples_hourly WHERE bucket >= ? AND bucket < ?
		GROUP BY machine_id `+rollupUpdate,
		bucketStart.Unix(), bucketStart.Unix(), bucketEnd.Unix())
	if err != nil {
		return fmt.Errorf("rollup daily: %w", err)
	}
	return nil
}

func (db *DB) PurgeSamples(ctx context.Context, rawBefore, hourlyBefore, dailyBefore time.Time) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin purge samples: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM machine_samples WHERE sampled_at < ?`, rawBefore.Unix()); err != nil {
		return fmt.Errorf("purge samples: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM machine_samples_hourly WHERE bucket < ?`, hourlyBefore.Unix()); err != nil {
		return fmt.Errorf("purge hourly: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM machine_samples_daily WHERE bucket < ?`, dailyBefore.Unix()); err != nil {
		return fmt.Errorf("purge daily: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit purge samples: %w", err)
	}
	return nil
}

func scanSample(row scanner) (resource.Sample, error) {
	var sample resource.Sample
	var sampledAt int64
	err := row.Scan(&sample.MachineID, &sampledAt, &sample.CPUPercent, &sample.CPUCores, &sample.Load1,
		&sample.MemUsed, &sample.MemTotal, &sample.SwapUsed, &sample.SwapTotal,
		&sample.DiskUsed, &sample.DiskTotal, &sample.NetRxPerSecond, &sample.NetTxPerSecond)
	if err != nil {
		return resource.Sample{}, err
	}
	sample.SampledAt = time.Unix(sampledAt, 0).UTC()
	return sample, nil
}

// Les agrégats reviennent en REAL : la moyenne d'entiers en SQLite.
func scanReading(row scanner) (sampler.Reading, error) {
	var reading sampler.Reading
	var bucket int64
	var memUsed, memTotal, swapUsed, swapTotal, diskUsed, diskTotal, rx, tx float64
	err := row.Scan(&bucket, &reading.CPUPercent, &reading.CPUCores, &reading.Load1,
		&memUsed, &memTotal, &swapUsed, &swapTotal, &diskUsed, &diskTotal, &rx, &tx)
	if err != nil {
		return sampler.Reading{}, err
	}
	reading.SampledAt = time.Unix(bucket, 0).UTC()
	reading.MemUsed, reading.MemTotal = int64(memUsed), int64(memTotal)
	reading.SwapUsed, reading.SwapTotal = int64(swapUsed), int64(swapTotal)
	reading.DiskUsed, reading.DiskTotal = int64(diskUsed), int64(diskTotal)
	reading.NetRxPerSecond, reading.NetTxPerSecond = int64(rx), int64(tx)
	return reading, nil
}
