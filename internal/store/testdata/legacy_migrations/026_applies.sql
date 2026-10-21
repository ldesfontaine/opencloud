-- Le journal des passages d'apply : qui, quand, quel fichier (son empreinte),
-- ce que le plan confirmé changeait, et comment ça s'est fini. Ni le fichier
-- ni aucun secret : le plan ne porte que des valeurs de l'annuaire, jamais un
-- jeton. Sans clé étrangère vers les objets touchés : le journal leur survit.
-- Pas de CHECK sur state ni actor_kind : planned (réservé), mcp, rule
-- viendront ; le code valide. Une ligne restée confirmed au démarrage est un
-- arrêt brutal : elle passe failed (manifest.interrupted). Secondes Unix.
CREATE TABLE applies (
    id           TEXT PRIMARY KEY,               -- apl_ + 16 hex
    at           INTEGER NOT NULL,               -- confirmé : début de l'écriture
    finished_at  INTEGER,
    actor_kind   TEXT NOT NULL,                  -- cli | operator
    actor_name   TEXT NOT NULL DEFAULT '',
    file_hash    TEXT NOT NULL,                  -- sha256:<64 hex>
    file_name    TEXT NOT NULL DEFAULT '',       -- dit par la CLI, pour l'opérateur
    prune        INTEGER NOT NULL CHECK (prune IN (0, 1)),
    plan_hash    TEXT NOT NULL,
    plan         TEXT NOT NULL,                  -- JSON des changements, dans l'ordre d'écriture
    state        TEXT NOT NULL,                  -- confirmed | applied | failed
    created      INTEGER NOT NULL DEFAULT 0,
    updated      INTEGER NOT NULL DEFAULT 0,
    deleted      INTEGER NOT NULL DEFAULT 0,
    unchanged    INTEGER NOT NULL DEFAULT 0,
    done         INTEGER NOT NULL DEFAULT 0,
    error        TEXT NOT NULL DEFAULT '',       -- clé du catalogue
    error_object TEXT NOT NULL DEFAULT ''        -- genre/nom du changement en échec
);

CREATE INDEX applies_at ON applies (at);
