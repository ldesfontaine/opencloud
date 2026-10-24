-- Plusieurs plans de sauvegarde, chacun avec sa source. Le plan « database »
-- d'une installation actuelle reste tel quel : mêmes colonnes, source
-- « database », sans machine. Un plan « agent » dit quelle machine produit
-- la sauvegarde (producer) et ce qu'elle en prend (selector : « all » ou des
-- identifiants séparés par des virgules).
-- SQLite ne modifie pas une contrainte CHECK : la table se refait, avec
-- son index et ses clés étrangères.
CREATE TABLE backup_plans_new (
    id                TEXT    PRIMARY KEY,
    -- Le nom lisible et unique du plan, qui préfixe ses fichiers sur la
    -- destination : « database » pour la base, un mot libre pour le reste.
    subject           TEXT    NOT NULL UNIQUE
                              CHECK (length(subject) BETWEEN 1 AND 40 AND subject NOT GLOB '*[^a-z0-9-]*' AND subject NOT GLOB '-*'),
    -- Le libellé affiché ; vide, la page montre le sujet.
    name              TEXT    NOT NULL DEFAULT '',
    source_kind       TEXT    NOT NULL DEFAULT 'database' CHECK (source_kind IN ('database', 'agent')),
    -- RESTRICT : retirer la machine qui produit passe par son plan.
    source_machine_id TEXT    REFERENCES machines (id) ON DELETE RESTRICT,
    producer          TEXT,
    selector          TEXT,
    destination_id    TEXT    NOT NULL REFERENCES backup_destinations (id) ON DELETE RESTRICT,
    schedule          TEXT    NOT NULL,
    keep              INTEGER NOT NULL CHECK (keep BETWEEN 1 AND 365),
    last_attempt_at   INTEGER,
    -- La clé du refus de la dernière tentative qui n'a pas pu partir, vide sinon.
    last_error        TEXT    NOT NULL DEFAULT '',
    -- Le dernier ordre backup.receive, que la purge des runs peut emporter.
    last_run_id       TEXT    REFERENCES action_runs (id) ON DELETE SET NULL,
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL,
    CHECK (source_kind <> 'database' OR (subject = 'database' AND source_machine_id IS NULL AND producer IS NULL AND selector IS NULL)),
    CHECK (source_kind <> 'agent' OR (subject <> 'database' AND source_machine_id IS NOT NULL AND producer IS NOT NULL AND selector IS NOT NULL))
);

INSERT INTO backup_plans_new (id, subject, destination_id, schedule, keep, last_attempt_at, last_error, last_run_id, created_at, updated_at)
    SELECT id, subject, destination_id, schedule, keep, last_attempt_at, last_error, last_run_id, created_at, updated_at FROM backup_plans;

DROP TABLE backup_plans;
ALTER TABLE backup_plans_new RENAME TO backup_plans;

CREATE INDEX backup_plans_destination ON backup_plans (destination_id);
CREATE INDEX backup_plans_source_machine ON backup_plans (source_machine_id);
