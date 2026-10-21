-- Entre objets déclarés, restrict : supprimer une machine, une sonde ou une
-- tâche ne retire plus en silence ce qui la vise. La suppression passe par
-- son plan (ce qui la retient, ce qu'elle emporte, ce qu'elle détache) et
-- refuse tant qu'un objet déclaré la retient ; l'opérateur le supprime ou le
-- détache d'abord. Ce qui appartient à l'objet (résultats, pings, mesures,
-- conteneurs) reste en cascade ; les alertes restent en set null, avec le nom
-- recopié.
--
-- SQLite ne change pas une clé étrangère sans reconstruire la table. Comme
-- toutes, cette migration tourne hors clés étrangères (store.applyContent) :
-- le DROP n'emporte rien, les tables filles retrouvent la nouvelle par son
-- nom, et les clés sont vérifiées avant le COMMIT.

-- 1. La sonde vit sur la machine qui la lance : restrict (était cascade, qui
-- emportait 90 jours de résultats sans prévenir). Le plan propose de changer
-- de machine ou de supprimer la sonde.
CREATE TABLE probes_new (
    id                    TEXT PRIMARY KEY,
    name                  TEXT NOT NULL,
    kind                  TEXT NOT NULL CHECK (kind IN ('http', 'tcp')),
    target                TEXT NOT NULL,
    machine_id            TEXT NOT NULL REFERENCES machines (id) ON DELETE RESTRICT,
    service_id            TEXT REFERENCES declared_services (id) ON DELETE RESTRICT,
    status                TEXT NOT NULL CHECK (status IN ('new', 'up', 'degraded', 'down', 'paused')),
    interval_seconds      INTEGER NOT NULL,
    timeout_seconds       INTEGER NOT NULL,
    failure_threshold     INTEGER NOT NULL,
    recovery_threshold    INTEGER NOT NULL,
    method                TEXT NOT NULL DEFAULT '',
    expected_status       TEXT NOT NULL DEFAULT '',
    expected_body         TEXT NOT NULL DEFAULT '',
    follow_redirects      INTEGER NOT NULL DEFAULT 0,
    consecutive_failures  INTEGER NOT NULL DEFAULT 0,
    consecutive_successes INTEGER NOT NULL DEFAULT 0,
    last_checked_at       INTEGER,
    last_duration_ms      INTEGER NOT NULL DEFAULT 0,
    last_code             INTEGER,
    last_reason           TEXT NOT NULL DEFAULT '',
    cert_subject          TEXT NOT NULL DEFAULT '',
    cert_issuer           TEXT NOT NULL DEFAULT '',
    cert_not_before       INTEGER,
    cert_not_after        INTEGER,
    cert_fingerprint      TEXT NOT NULL DEFAULT '',
    created_at            INTEGER NOT NULL,
    tls                   INTEGER NOT NULL DEFAULT 0,
    cert_chain_valid      INTEGER NOT NULL DEFAULT 0,
    cert_hostname_match   INTEGER NOT NULL DEFAULT 0,
    cert_ocsp             TEXT NOT NULL DEFAULT '',
    revised_at            INTEGER
);

INSERT INTO probes_new (id, name, kind, target, machine_id, service_id, status,
    interval_seconds, timeout_seconds, failure_threshold, recovery_threshold,
    method, expected_status, expected_body, follow_redirects,
    consecutive_failures, consecutive_successes, last_checked_at, last_duration_ms, last_code, last_reason,
    cert_subject, cert_issuer, cert_not_before, cert_not_after, cert_fingerprint, created_at,
    tls, cert_chain_valid, cert_hostname_match, cert_ocsp, revised_at)
SELECT id, name, kind, target, machine_id, service_id, status,
    interval_seconds, timeout_seconds, failure_threshold, recovery_threshold,
    method, expected_status, expected_body, follow_redirects,
    consecutive_failures, consecutive_successes, last_checked_at, last_duration_ms, last_code, last_reason,
    cert_subject, cert_issuer, cert_not_before, cert_not_after, cert_fingerprint, created_at,
    tls, cert_chain_valid, cert_hostname_match, cert_ocsp, revised_at
FROM probes;

DROP TABLE probes;
ALTER TABLE probes_new RENAME TO probes;
CREATE INDEX probes_machine ON probes (machine_id);
CREATE INDEX probes_service ON probes (service_id);
CREATE INDEX probes_cert_expiry ON probes (cert_not_after) WHERE cert_fingerprint != '';

-- 2. La tâche rattachée à une machine : restrict (était set null). Son jeton
-- sert à un cron de cette machine ; la retirer sans le dire laissait une
-- tâche sans machine. Une tâche sans machine reste admise. Le jeton est
-- chiffré, lié à l'identifiant de la ligne (020) : la recopie le garde.
CREATE TABLE heartbeats_new (
    id               TEXT PRIMARY KEY,
    token            TEXT NOT NULL UNIQUE,
    name             TEXT NOT NULL,
    machine_id       TEXT REFERENCES machines (id) ON DELETE RESTRICT,
    status           TEXT NOT NULL CHECK (status IN ('new', 'on_time', 'started', 'late', 'failed', 'paused')),
    interval_seconds INTEGER NOT NULL,
    grace_seconds    INTEGER NOT NULL,
    last_ping_at     INTEGER,
    next_deadline_at INTEGER,
    run_started_at   INTEGER,
    last_exit_code   INTEGER,
    last_duration_ms INTEGER,
    created_at       INTEGER NOT NULL,
    token_hash       TEXT
);

INSERT INTO heartbeats_new (id, token, name, machine_id, status, interval_seconds, grace_seconds,
    last_ping_at, next_deadline_at, run_started_at, last_exit_code, last_duration_ms, created_at, token_hash)
SELECT id, token, name, machine_id, status, interval_seconds, grace_seconds,
    last_ping_at, next_deadline_at, run_started_at, last_exit_code, last_duration_ms, created_at, token_hash
FROM heartbeats;

DROP TABLE heartbeats;
ALTER TABLE heartbeats_new RENAME TO heartbeats;
CREATE INDEX heartbeats_deadline ON heartbeats (next_deadline_at) WHERE next_deadline_at IS NOT NULL;
CREATE UNIQUE INDEX heartbeats_token_hash ON heartbeats (token_hash) WHERE token_hash IS NOT NULL;

-- 3. Le membre d'un composant de statut : restrict vers la machine, la tâche
-- et la sonde (étaient cascade), comme vers le service (024). Un objet ne
-- disparaît plus d'une page publique sans le dire. Le composant supprimé
-- emporte toujours ses membres.
CREATE TABLE status_component_members_new (
    component_id TEXT NOT NULL REFERENCES status_components (id) ON DELETE CASCADE,
    machine_id   TEXT REFERENCES machines (id) ON DELETE RESTRICT,
    service_id   TEXT REFERENCES declared_services (id) ON DELETE RESTRICT,
    heartbeat_id TEXT REFERENCES heartbeats (id) ON DELETE RESTRICT,
    probe_id     TEXT REFERENCES probes (id) ON DELETE RESTRICT,
    CHECK ((machine_id IS NOT NULL) + (service_id IS NOT NULL) + (heartbeat_id IS NOT NULL) + (probe_id IS NOT NULL) = 1)
);

-- L'ordre des lignes est celui des membres dans le composant : on le garde.
INSERT INTO status_component_members_new (rowid, component_id, machine_id, service_id, heartbeat_id, probe_id)
SELECT rowid, component_id, machine_id, service_id, heartbeat_id, probe_id
FROM status_component_members;

DROP TABLE status_component_members;
ALTER TABLE status_component_members_new RENAME TO status_component_members;
CREATE UNIQUE INDEX status_members_machine ON status_component_members (component_id, machine_id) WHERE machine_id IS NOT NULL;
CREATE UNIQUE INDEX status_members_service ON status_component_members (component_id, service_id) WHERE service_id IS NOT NULL;
CREATE UNIQUE INDEX status_members_heartbeat ON status_component_members (component_id, heartbeat_id) WHERE heartbeat_id IS NOT NULL;
CREATE UNIQUE INDEX status_members_probe ON status_component_members (component_id, probe_id) WHERE probe_id IS NOT NULL;
