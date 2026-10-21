-- Les machines gérées par openCloud et les jetons qui les enrôlent.
-- Horodatages en secondes depuis l'époque Unix.

CREATE TABLE machines (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    kind          TEXT NOT NULL CHECK (kind IN ('local', 'remote')),
    -- Clé publique Ed25519, 32 octets ; NULL pour la machine openCloud elle-même.
    public_key    BLOB,
    hostname      TEXT NOT NULL DEFAULT '',
    address       TEXT NOT NULL DEFAULT '',
    os            TEXT NOT NULL DEFAULT '',
    arch          TEXT NOT NULL DEFAULT '',
    agent_version TEXT NOT NULL DEFAULT '',
    enrolled_at   INTEGER NOT NULL,
    last_seen_at  INTEGER,
    created_at    INTEGER NOT NULL
);

CREATE INDEX machines_last_seen ON machines (last_seen_at);

-- Un jeton sert une fois. Seule son empreinte est stockée : une copie de la
-- base ne permet d'enrôler personne. machine_id est renseigné quand le jeton
-- ré-enrôle une machine existante, qui garde alors son id et son historique.
CREATE TABLE enrollment_tokens (
    id           TEXT PRIMARY KEY,
    token_hash   TEXT NOT NULL UNIQUE,
    token_prefix TEXT NOT NULL,
    name         TEXT NOT NULL,
    machine_id   TEXT REFERENCES machines (id) ON DELETE CASCADE,
    created_at   INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    consumed_at  INTEGER,
    consumed_by  TEXT
);
