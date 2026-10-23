-- La sauvegarde de la base : une paire de clés, des destinations (une
-- machine dont l'agent a le droit de recevoir des sauvegardes, et un
-- chemin), un plan. Aucune sauvegarde n'est gardée sur la machine openCloud.

-- Une ligne au plus. La publique chiffre chaque nuit ; la privée est
-- scellée par la phrase de passe (age, scrypt), que la base ne voit jamais.
-- Elle est déposée avec chaque sauvegarde.
CREATE TABLE backup_keyring (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    recipient       TEXT    NOT NULL,
    identity_sealed BLOB    NOT NULL,
    fingerprint     TEXT    NOT NULL,
    created_at      INTEGER NOT NULL
);

-- RESTRICT : retirer la machine qui garde les sauvegardes passe par son plan.
CREATE TABLE backup_destinations (
    id         TEXT    PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE,
    machine_id TEXT    NOT NULL REFERENCES machines (id) ON DELETE RESTRICT,
    path       TEXT    NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE INDEX backup_destinations_machine ON backup_destinations (machine_id);

-- Une sauvegarde planifiée. subject : « database » seulement pour l'instant
-- (les services viendront avec le déploiement). last_error : la clé du
-- refus de la dernière tentative qui n'a pas pu partir, vide sinon ;
-- last_run_id : le dernier ordre backup.receive, que la purge des runs
-- peut emporter.
CREATE TABLE backup_plans (
    id              TEXT    PRIMARY KEY,
    subject         TEXT    NOT NULL UNIQUE,
    destination_id  TEXT    NOT NULL REFERENCES backup_destinations (id) ON DELETE RESTRICT,
    schedule        TEXT    NOT NULL,
    keep            INTEGER NOT NULL CHECK (keep BETWEEN 1 AND 365),
    last_attempt_at INTEGER,
    last_error      TEXT    NOT NULL DEFAULT '',
    last_run_id     TEXT    REFERENCES action_runs (id) ON DELETE SET NULL,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);

CREATE INDEX backup_plans_destination ON backup_plans (destination_id);
