-- Un retard s'inscrit dans l'historique d'une tâche : une ligne « missed »
-- par épisode de retard, sans début ni code, close à l'échéance dépassée.
-- SQLite ne modifie pas une contrainte CHECK : la table se refait, ses
-- lignes, ses index et sa clé étrangère avec elle.
CREATE TABLE heartbeat_runs_new (
    id           INTEGER PRIMARY KEY,
    heartbeat_id TEXT    NOT NULL REFERENCES heartbeats (id) ON DELETE CASCADE,
    started_at   INTEGER,
    completed_at INTEGER,
    duration_ms  INTEGER,
    exit_code    INTEGER,
    outcome      TEXT    NOT NULL CHECK (outcome IN ('in_progress', 'success', 'failure', 'timeout', 'missed')),
    payload      TEXT    NOT NULL DEFAULT ''
);

INSERT INTO heartbeat_runs_new (id, heartbeat_id, started_at, completed_at, duration_ms, exit_code, outcome, payload)
    SELECT id, heartbeat_id, started_at, completed_at, duration_ms, exit_code, outcome, payload FROM heartbeat_runs;

DROP TABLE heartbeat_runs;
ALTER TABLE heartbeat_runs_new RENAME TO heartbeat_runs;

CREATE INDEX heartbeat_runs_by_heartbeat ON heartbeat_runs (heartbeat_id, id DESC);
-- Une seule exécution ouverte par tâche : l'invariant casse bruyamment.
CREATE UNIQUE INDEX heartbeat_runs_open ON heartbeat_runs (heartbeat_id) WHERE outcome = 'in_progress';
