-- L'historique des exécutions des plans de sauvegarde d'openCloud : une ligne
-- par cible d'un passage (la base compte pour une), écrite à la fin de la cible,
-- reçue ou non, ou à son refus.
--
-- Pas de clé étrangère : l'historique survit au plan et à la destination qu'il
-- nomme (le nom de la destination y est recopié). Il est borné à 400 lignes par
-- plan (store.MaxBackupRunsPerPlan), les plus anciennes partent à l'écriture.
CREATE TABLE backup_runs (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    subject          TEXT NOT NULL,
    -- L'instant de la tentative du plan (backup_plans.last_attempt_at) : les
    -- lignes d'un même passage le partagent, c'est ce qui compte ses cibles.
    pass_at          INTEGER NOT NULL,
    -- La clé de la source (« database », « guest:<machine>:<vmid> ») ; vide
    -- quand le plan entier est refusé avant toute cible.
    source           TEXT NOT NULL,
    started_at       INTEGER NOT NULL,
    finished_at      INTEGER NOT NULL CHECK (finished_at >= started_at),
    result           TEXT NOT NULL CHECK (result IN ('ok', 'failed', 'refused')),
    -- Un mot fermé ou la clé d'un refus ; jamais le texte d'un agent.
    reason           TEXT NOT NULL DEFAULT '',
    -- Inconnue (NULL) tant que l'archive n'a pas été vue passer.
    size_bytes       INTEGER CHECK (size_bytes IS NULL OR size_bytes >= 0),
    destination_id   TEXT NOT NULL,
    destination_name TEXT NOT NULL,
    action_run_id    TEXT NOT NULL DEFAULT ''
);

CREATE INDEX backup_runs_by_subject ON backup_runs (subject, id DESC);
CREATE INDEX backup_runs_by_source ON backup_runs (source, id DESC);
