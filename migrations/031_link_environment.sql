-- Un lien lu dans l'environnement d'un conteneur (droit observe.env) : le
-- signal « environment ». La liste fermée des signaux est un CHECK, que SQLite
-- ne modifie pas en place : la table est refaite avec la liste élargie, ses
-- lignes recopiées telles quelles, ses index remis. Rien d'autre ne change.
--
-- Ce que ces liens portent : deux clés d'instance de la machine, la nature
-- « config », la preuve « déduit » et un port. Jamais le nom d'une variable ni
-- sa valeur : l'agent ne les envoie pas, le serveur n'a rien à en écrire.
CREATE TABLE links_next (
    id              TEXT    PRIMARY KEY,
    from_machine_id TEXT    NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    from_key        TEXT    NOT NULL,
    to_machine_id   TEXT    NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    to_key          TEXT    NOT NULL,
    nature          TEXT    NOT NULL CHECK (nature IN ('order', 'config', 'route', 'traffic')),
    proof           TEXT    NOT NULL CHECK (proof IN ('declared', 'deduced', 'observed')),
    signal          TEXT    NOT NULL CHECK (signal IN ('depends_on', 'container_link', 'operator', 'environment')),
    port            INTEGER NOT NULL DEFAULT 0 CHECK (port BETWEEN 0 AND 65535),
    first_seen_at   INTEGER NOT NULL,
    last_seen_at    INTEGER NOT NULL,
    CHECK (NOT (from_machine_id = to_machine_id AND from_key = to_key))
);

INSERT INTO links_next (id, from_machine_id, from_key, to_machine_id, to_key, nature, proof, signal, port, first_seen_at, last_seen_at)
SELECT id, from_machine_id, from_key, to_machine_id, to_key, nature, proof, signal, port, first_seen_at, last_seen_at FROM links;

DROP TABLE links;
ALTER TABLE links_next RENAME TO links;

CREATE UNIQUE INDEX links_pair ON links (from_machine_id, from_key, to_machine_id, to_key, nature);
CREATE INDEX links_target ON links (to_machine_id, to_key);
