-- Les moniteurs de tâches planifiées, leurs pings bruts et leurs exécutions.
-- Horodatages en secondes depuis l'époque Unix, durées en millisecondes.

CREATE TABLE heartbeats (
    id               TEXT PRIMARY KEY,
    -- Le secret de l'URL de ping, en clair : la page doit le réafficher.
    token            TEXT NOT NULL UNIQUE,
    name             TEXT NOT NULL,
    -- Rattachement facultatif ; la machine retirée, le moniteur reste.
    machine_id       TEXT REFERENCES machines (id) ON DELETE SET NULL,
    status           TEXT NOT NULL CHECK (status IN ('new', 'on_time', 'started', 'late', 'failed', 'paused')),
    interval_seconds INTEGER NOT NULL,
    grace_seconds    INTEGER NOT NULL,
    last_ping_at     INTEGER,
    next_deadline_at INTEGER,
    run_started_at   INTEGER,
    last_exit_code   INTEGER,
    last_duration_ms INTEGER,
    created_at       INTEGER NOT NULL
);

-- Taillé pour la boucle d'échéance : les moniteurs surveillés, par échéance.
CREATE INDEX heartbeats_deadline ON heartbeats (next_deadline_at) WHERE next_deadline_at IS NOT NULL;

-- Chaque requête reçue, telle quelle ; purgée après sept jours.
CREATE TABLE heartbeat_pings (
    id           INTEGER PRIMARY KEY,
    heartbeat_id TEXT NOT NULL REFERENCES heartbeats (id) ON DELETE CASCADE,
    kind         TEXT NOT NULL CHECK (kind IN ('finish', 'start', 'exit_code')),
    exit_code    INTEGER,
    source       TEXT NOT NULL DEFAULT '',
    method       TEXT NOT NULL DEFAULT '',
    payload      TEXT NOT NULL DEFAULT '',
    received_at  INTEGER NOT NULL
);

CREATE INDEX heartbeat_pings_by_heartbeat ON heartbeat_pings (heartbeat_id, received_at DESC);
CREATE INDEX heartbeat_pings_received ON heartbeat_pings (received_at);

-- L'exécution logique : ouverte par un start, close par une fin ou un
-- dépassement ; purgée après quatre-vingt-dix jours.
CREATE TABLE heartbeat_runs (
    id           INTEGER PRIMARY KEY,
    heartbeat_id TEXT NOT NULL REFERENCES heartbeats (id) ON DELETE CASCADE,
    started_at   INTEGER,
    completed_at INTEGER,
    duration_ms  INTEGER,
    exit_code    INTEGER,
    outcome      TEXT NOT NULL CHECK (outcome IN ('in_progress', 'success', 'failure', 'timeout')),
    payload      TEXT NOT NULL DEFAULT ''
);

CREATE INDEX heartbeat_runs_by_heartbeat ON heartbeat_runs (heartbeat_id, id DESC);
-- Une seule exécution ouverte par moniteur : l'invariant casse bruyamment.
CREATE UNIQUE INDEX heartbeat_runs_open ON heartbeat_runs (heartbeat_id) WHERE outcome = 'in_progress';
