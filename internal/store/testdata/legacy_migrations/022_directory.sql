-- L'annuaire : ce que l'opérateur déclare, à côté de ce que Docker montre.
-- Identifiants tirés par le serveur, jamais d'un id Docker. Pas de CHECK sur
-- nature, origine, provenance, genre : ces listes grandiront (deployed,
-- discovery), et SQLite ne modifie pas un CHECK sans reconstruire la table ;
-- le code les valide. Entre objets déclarés : restrict. Secondes Unix.
CREATE TABLE projects (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    title      TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE declared_services (
    id              TEXT PRIMARY KEY,
    -- Supprimer une machine qui porte des services passe par l'opérateur.
    machine_id      TEXT NOT NULL REFERENCES machines (id) ON DELETE RESTRICT,
    name            TEXT NOT NULL,
    title           TEXT NOT NULL DEFAULT '',
    nature          TEXT NOT NULL,
    origin          TEXT NOT NULL,
    compose_project TEXT NOT NULL DEFAULT '',
    public_url      TEXT NOT NULL DEFAULT '',
    declared_by     TEXT NOT NULL,
    needs_review    INTEGER NOT NULL DEFAULT 0 CHECK (needs_review IN (0, 1)),
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);

CREATE UNIQUE INDEX declared_services_name ON declared_services (machine_id, name);

-- Deux services sur le même projet Compose rendraient le rattachement ambigu.
CREATE UNIQUE INDEX declared_services_compose ON declared_services (machine_id, compose_project) WHERE origin = 'compose';

CREATE TABLE service_units (
    id             TEXT PRIMARY KEY,
    service_id     TEXT NOT NULL REFERENCES declared_services (id) ON DELETE CASCADE,
    -- Pour un service Compose : le nom du service dans le projet (niveau 2).
    name           TEXT NOT NULL,
    kind           TEXT NOT NULL,
    image          TEXT NOT NULL DEFAULT '',
    version        TEXT NOT NULL DEFAULT '',
    digest         TEXT NOT NULL DEFAULT '',
    container_name TEXT NOT NULL DEFAULT '',
    systemd_unit   TEXT NOT NULL DEFAULT '',
    ports          TEXT NOT NULL DEFAULT '[]',
    position       INTEGER NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX service_units_name ON service_units (service_id, name);

CREATE TABLE service_provides (
    service_id TEXT NOT NULL REFERENCES declared_services (id) ON DELETE CASCADE,
    capability TEXT NOT NULL,
    PRIMARY KEY (service_id, capability)
) WITHOUT ROWID;

-- Un lien seul : le service reste sans le projet, le projet sans le service.
CREATE TABLE project_services (
    project_id TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    service_id TEXT NOT NULL REFERENCES declared_services (id) ON DELETE CASCADE,
    PRIMARY KEY (project_id, service_id)
) WITHOUT ROWID;

CREATE INDEX project_services_service ON project_services (service_id);
