-- Les liens entre instances, stockés avec leur preuve et leur fraîcheur.
-- Une extrémité est une instance : une machine et une clé stable
-- (« compose:projet/service », « container:nom », « systemd:unité.service »),
-- jamais l'identifiant Docker, qui change à la recréation.
--
-- La preuve dit d'où vient le lien : déclaré (écrit par l'opérateur ou par la
-- configuration d'un outil), déduit ou observé. Seuls les liens déclarés sont
-- écrits aujourd'hui ; la liste est posée tout de suite pour que les liens
-- déduits et observés n'obligent pas à refaire la table. Le signal dit
-- lequel : depends_on de Compose, --link de Docker, ou l'opérateur.
--
-- Un lien n'est jamais effacé parce qu'on ne le revoit plus : last_seen_at
-- vieillit, et l'écran le dit « ancien ». Les liens existants se refont au
-- démarrage depuis les conteneurs déjà connus, sans attendre les agents.
CREATE TABLE links (
    id              TEXT    PRIMARY KEY,
    from_machine_id TEXT    NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    from_key        TEXT    NOT NULL,
    to_machine_id   TEXT    NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    to_key          TEXT    NOT NULL,
    nature          TEXT    NOT NULL CHECK (nature IN ('order', 'config', 'route', 'traffic')),
    proof           TEXT    NOT NULL CHECK (proof IN ('declared', 'deduced', 'observed')),
    signal          TEXT    NOT NULL CHECK (signal IN ('depends_on', 'container_link', 'operator')),
    port            INTEGER NOT NULL DEFAULT 0 CHECK (port BETWEEN 0 AND 65535),
    first_seen_at   INTEGER NOT NULL,
    last_seen_at    INTEGER NOT NULL,
    CHECK (NOT (from_machine_id = to_machine_id AND from_key = to_key))
);

CREATE UNIQUE INDEX links_pair ON links (from_machine_id, from_key, to_machine_id, to_key, nature);
CREATE INDEX links_target ON links (to_machine_id, to_key);
