-- L'élagage fait en écrasant les 33 migrations en 001_schema.sql, écrit
-- comme une migration de plus. Rejouées dans l'ordre, les anciennes
-- migrations puis ce fichier doivent donner exactement 001_schema.sql : c'est
-- ce que vérifie TestSchema_MatchesTheLegacyMigrationsAndTheTrim. Une base
-- vide, pas de données à recopier.

-- 1. Le certificat quitte la sonde pour sa table : huit colonnes que seule
-- la sonde qui fait du TLS remplit.
DROP INDEX probes_cert_expiry;
ALTER TABLE probes DROP COLUMN cert_subject;
ALTER TABLE probes DROP COLUMN cert_issuer;
ALTER TABLE probes DROP COLUMN cert_not_before;
ALTER TABLE probes DROP COLUMN cert_not_after;
ALTER TABLE probes DROP COLUMN cert_fingerprint;
ALTER TABLE probes DROP COLUMN cert_chain_valid;
ALTER TABLE probes DROP COLUMN cert_hostname_match;
ALTER TABLE probes DROP COLUMN cert_ocsp;

CREATE TABLE probe_certificates (
    probe_id       TEXT    PRIMARY KEY REFERENCES probes (id) ON DELETE CASCADE,
    subject        TEXT    NOT NULL DEFAULT '',
    issuer         TEXT    NOT NULL DEFAULT '',
    not_before     INTEGER,
    not_after      INTEGER,
    fingerprint    TEXT    NOT NULL,
    chain_valid    INTEGER NOT NULL DEFAULT 0,
    hostname_match INTEGER NOT NULL DEFAULT 0,
    ocsp           TEXT    NOT NULL DEFAULT ''
) WITHOUT ROWID;

-- 2. Le conteneur n'a qu'un genre : la colonne ne disait rien.
ALTER TABLE containers DROP COLUMN kind;

-- 3. Les deux tables d'agrégat, aux 14 colonnes identiques, n'en font qu'une.
DROP TABLE machine_samples_hourly;
DROP TABLE machine_samples_daily;

CREATE TABLE machine_rollups (
    machine_id            TEXT    NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    granularity           TEXT    NOT NULL CHECK (granularity IN ('hour', 'day')),
    bucket                INTEGER NOT NULL,
    avg_cpu_percent       REAL    NOT NULL,
    cpu_cores             INTEGER NOT NULL,
    avg_load_1            REAL    NOT NULL,
    avg_mem_used          INTEGER NOT NULL,
    avg_mem_total         INTEGER NOT NULL,
    avg_swap_used         INTEGER NOT NULL,
    avg_swap_total        INTEGER NOT NULL,
    avg_disk_used         INTEGER NOT NULL,
    avg_disk_total        INTEGER NOT NULL,
    avg_net_rx_per_second INTEGER NOT NULL,
    avg_net_tx_per_second INTEGER NOT NULL,
    sample_count          INTEGER NOT NULL,
    PRIMARY KEY (machine_id, granularity, bucket)
) WITHOUT ROWID;

CREATE INDEX machine_rollups_bucket ON machine_rollups (granularity, bucket);

-- 4. Le jeton de ping en une seule forme : chiffré, et cherché par son
-- empreinte, toujours posée. L'unicité du chiffré ne garantissait rien.
-- Le patron de SQLite : la nouvelle table prend le nom de l'ancienne, que
-- les tables filles retrouvent.
CREATE TABLE heartbeats_new (
    id               TEXT    PRIMARY KEY,
    name             TEXT    NOT NULL,
    token_sealed     TEXT    NOT NULL,
    token_lookup     TEXT    NOT NULL UNIQUE,
    machine_id       TEXT    REFERENCES machines (id) ON DELETE RESTRICT,
    status           TEXT    NOT NULL CHECK (status IN ('new', 'on_time', 'started', 'late', 'failed', 'paused')),
    interval_seconds INTEGER NOT NULL,
    grace_seconds    INTEGER NOT NULL,
    last_ping_at     INTEGER,
    next_deadline_at INTEGER,
    run_started_at   INTEGER,
    last_exit_code   INTEGER,
    last_duration_ms INTEGER,
    created_at       INTEGER NOT NULL
);
DROP TABLE heartbeats;
ALTER TABLE heartbeats_new RENAME TO heartbeats;
CREATE INDEX heartbeats_deadline ON heartbeats (next_deadline_at) WHERE next_deadline_at IS NOT NULL;

-- 5. Les colonnes que personne ne lit ni n'écrit : elles reviendront avec la
-- vérification réciproque et l'état désiré (#146), le jour où elles servent.
ALTER TABLE agents DROP COLUMN server_key_fingerprint;
ALTER TABLE agents DROP COLUMN desired_revision;
ALTER TABLE agents DROP COLUMN acked_revision;
