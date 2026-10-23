-- Les ressources mesurées par machine, en trois étages : le brut toutes les
-- 10 s gardé 48 h, l'horaire gardé 90 jours, le journalier gardé un an.
-- Horodatages en secondes depuis l'époque Unix, octets en octets, débits en
-- octets par seconde. La machine retirée emporte son historique.
-- disk_used et disk_total font la somme des volumes ; le détail par volume
-- est dans machine_disks, avec le brut seulement.

CREATE TABLE machine_samples (
    machine_id        TEXT NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    sampled_at        INTEGER NOT NULL,
    cpu_percent       REAL NOT NULL,
    cpu_cores         INTEGER NOT NULL,
    load_1            REAL NOT NULL,
    mem_used          INTEGER NOT NULL,
    mem_total         INTEGER NOT NULL,
    swap_used         INTEGER NOT NULL,
    swap_total        INTEGER NOT NULL,
    disk_used         INTEGER NOT NULL,
    disk_total        INTEGER NOT NULL,
    net_rx_per_second INTEGER NOT NULL,
    net_tx_per_second INTEGER NOT NULL,
    -- Un échantillon rejoué après une coupure tombe sur la clé : ignoré.
    PRIMARY KEY (machine_id, sampled_at)
) WITHOUT ROWID;

CREATE INDEX machine_samples_sampled ON machine_samples (sampled_at);

-- Un volume réel par ligne, attaché à son échantillon brut : il part avec
-- lui, à la purge comme au retrait de la machine. Pas d'agrégat par volume.
CREATE TABLE machine_disks (
    machine_id  TEXT NOT NULL,
    sampled_at  INTEGER NOT NULL,
    mount_point TEXT NOT NULL,
    device      TEXT NOT NULL,
    disk_used   INTEGER NOT NULL,
    disk_total  INTEGER NOT NULL,
    PRIMARY KEY (machine_id, sampled_at, mount_point),
    FOREIGN KEY (machine_id, sampled_at) REFERENCES machine_samples (machine_id, sampled_at) ON DELETE CASCADE
) WITHOUT ROWID;

CREATE INDEX machine_disks_sampled ON machine_disks (sampled_at);

-- Les seaux horaires : moyennes du brut, et le nombre d'échantillons qui
-- pondère le journalier. La clé fait l'idempotence du rollup.
CREATE TABLE machine_samples_hourly (
    machine_id            TEXT NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    bucket                INTEGER NOT NULL,
    avg_cpu_percent       REAL NOT NULL,
    cpu_cores             INTEGER NOT NULL,
    avg_load_1            REAL NOT NULL,
    avg_mem_used          INTEGER NOT NULL,
    avg_mem_total         INTEGER NOT NULL,
    avg_swap_used         INTEGER NOT NULL,
    avg_swap_total        INTEGER NOT NULL,
    avg_disk_used         INTEGER NOT NULL,
    avg_disk_total        INTEGER NOT NULL,
    avg_net_rx_per_second INTEGER NOT NULL,
    avg_net_tx_per_second INTEGER NOT NULL,
    sample_count          INTEGER NOT NULL,
    PRIMARY KEY (machine_id, bucket)
) WITHOUT ROWID;

CREATE INDEX machine_samples_hourly_bucket ON machine_samples_hourly (bucket);

CREATE TABLE machine_samples_daily (
    machine_id            TEXT NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    bucket                INTEGER NOT NULL,
    avg_cpu_percent       REAL NOT NULL,
    cpu_cores             INTEGER NOT NULL,
    avg_load_1            REAL NOT NULL,
    avg_mem_used          INTEGER NOT NULL,
    avg_mem_total         INTEGER NOT NULL,
    avg_swap_used         INTEGER NOT NULL,
    avg_swap_total        INTEGER NOT NULL,
    avg_disk_used         INTEGER NOT NULL,
    avg_disk_total        INTEGER NOT NULL,
    avg_net_rx_per_second INTEGER NOT NULL,
    avg_net_tx_per_second INTEGER NOT NULL,
    sample_count          INTEGER NOT NULL,
    PRIMARY KEY (machine_id, bucket)
) WITHOUT ROWID;

CREATE INDEX machine_samples_daily_bucket ON machine_samples_daily (bucket);
