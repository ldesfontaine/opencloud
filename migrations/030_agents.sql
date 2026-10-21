-- La machine et l'agent se séparent (24-modele-cible.md, « Machine et
-- agent ») : la machine est l'hôte, avec son nom et son historique ;
-- l'agent est le logiciel installé dessus, avec sa clé, sa version et son
-- dernier signe de vie. Ré-enrôler ajoute un agent et révoque l'ancien, la
-- machine reste. Révoquer pose revoked_at : l'agent est refusé, la machine
-- garde son historique et ses services.
--
-- La machine perd aussi son genre (kind) : la machine openCloud se reconnaît
-- à son identifiant stable, 'local', et rien d'autre.
--
-- Comme toutes, cette migration tourne hors clés étrangères
-- (store.applyContent) : reconstruire machines ne vide pas les tables qui la
-- visent, et les clés sont vérifiées avant le COMMIT.

CREATE TABLE machines_new (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    hostname   TEXT NOT NULL DEFAULT '',
    address    TEXT NOT NULL DEFAULT '',
    os         TEXT NOT NULL DEFAULT '',
    arch       TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);

-- Un agent par installation. public_key : Ed25519, 32 octets. root_dir :
-- la racine des services qu'il déploiera, réglable plus tard dans son
-- fichier. server_key_fingerprint, desired_revision et acked_revision
-- attendent la vérification réciproque et l'état désiré (#146).
CREATE TABLE agents (
    id                     TEXT PRIMARY KEY,
    machine_id             TEXT NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    public_key             BLOB NOT NULL CHECK (length(public_key) = 32),
    version                TEXT NOT NULL DEFAULT '',
    root_dir               TEXT NOT NULL DEFAULT '/srv/workspace',
    server_key_fingerprint TEXT,
    desired_revision       INTEGER NOT NULL DEFAULT 0,
    acked_revision         INTEGER NOT NULL DEFAULT 0,
    enrolled_at            INTEGER NOT NULL,
    last_seen_at           INTEGER,
    revoked_at             INTEGER
);

-- Une machine a au plus un agent actif : les autres sont révoqués.
CREATE UNIQUE INDEX agents_one_active ON agents (machine_id) WHERE revoked_at IS NULL;
CREATE INDEX agents_machine ON agents (machine_id);

-- Les droits que l'agent lit dans son fichier et rapporte à l'ouverture de
-- son flux ; scope porte les chemins d'un droit borné (backup.receive,
-- files.write), séparés par « : ». Stockés et affichés, pas encore
-- appliqués (#146).
CREATE TABLE agent_capabilities (
    agent_id    TEXT NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    capability  TEXT NOT NULL,
    scope       TEXT NOT NULL DEFAULT '',
    reported_at INTEGER NOT NULL,
    PRIMARY KEY (agent_id, capability)
);

-- Le jeton garde les droits demandés à son émission, sous la forme que la
-- ligne d'installation passe à l'agent (« observe,files.write=/srv/a ») ;
-- vide pour un jeton d'avant les droits. agent_id dit quel agent l'a
-- consommé et remplace consumed_by (l'identifiant de la machine).
CREATE TABLE enrollment_tokens_new (
    id                     TEXT PRIMARY KEY,
    token_hash             TEXT NOT NULL UNIQUE,
    token_prefix           TEXT NOT NULL,
    name                   TEXT NOT NULL,
    machine_id             TEXT REFERENCES machines (id) ON DELETE CASCADE,
    agent_id               TEXT REFERENCES agents (id) ON DELETE SET NULL,
    requested_capabilities TEXT NOT NULL DEFAULT '',
    created_at             INTEGER NOT NULL,
    expires_at             INTEGER NOT NULL,
    consumed_at            INTEGER
);

-- Un agent naît d'un seul jeton : c'est par lui qu'on relit ce qui était
-- demandé.
CREATE UNIQUE INDEX enrollment_tokens_agent ON enrollment_tokens_new (agent_id);

INSERT INTO machines_new (id, name, hostname, address, os, arch, created_at)
SELECT id, name, hostname, address, os, arch, created_at FROM machines;

-- Chaque machine enrôlée reçoit l'agent qu'elle avait : sa clé, sa version,
-- son dernier signe de vie. La machine openCloud n'avait pas de clé : elle
-- n'a pas d'agent.
INSERT INTO agents (id, machine_id, public_key, version, enrolled_at, last_seen_at)
SELECT 'agt_' || lower(hex(randomblob(8))), id, public_key, agent_version, enrolled_at, last_seen_at
FROM machines
WHERE kind = 'remote' AND public_key IS NOT NULL;

-- Les jetons d'avant ne disaient rien des droits : ils gardent leur trace,
-- sans agent rattaché.
INSERT INTO enrollment_tokens_new (id, token_hash, token_prefix, name, machine_id, created_at, expires_at, consumed_at)
SELECT id, token_hash, token_prefix, name, machine_id, created_at, expires_at, consumed_at FROM enrollment_tokens;

DROP TABLE enrollment_tokens;
DROP TABLE machines;
ALTER TABLE machines_new RENAME TO machines;
ALTER TABLE enrollment_tokens_new RENAME TO enrollment_tokens;
