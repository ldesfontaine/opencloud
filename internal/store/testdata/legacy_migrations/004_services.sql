-- Les services : ce qui tourne sur une machine. Aujourd'hui un conteneur
-- Docker par fiche ; kind laisse la place à d'autres genres. L'identifiant
-- est dérivé (machine:id Docker) : un inventaire rejoué tombe sur la même
-- fiche. Horodatages en secondes depuis l'époque Unix ; archived_at posé
-- quand le conteneur est détruit. La machine retirée emporte tout.

CREATE TABLE services (
    id            TEXT PRIMARY KEY,
    machine_id    TEXT NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN ('container')),
    name          TEXT NOT NULL,
    -- Le projet Compose ; vide pour un conteneur seul.
    group_name    TEXT NOT NULL DEFAULT '',
    container_id  TEXT NOT NULL,
    image         TEXT NOT NULL DEFAULT '',
    image_id      TEXT NOT NULL DEFAULT '',
    state         TEXT NOT NULL,
    exit_code     INTEGER NOT NULL DEFAULT 0,
    -- healthy, unhealthy, starting ; vide sans HEALTHCHECK.
    health        TEXT NOT NULL DEFAULT '',
    restart_count INTEGER NOT NULL DEFAULT 0,
    -- Les ports publiés, en JSON : lus tels quels, jamais interrogés.
    ports         TEXT NOT NULL DEFAULT '[]',
    created_at    INTEGER NOT NULL,
    started_at    INTEGER,
    finished_at   INTEGER,
    first_seen_at INTEGER NOT NULL,
    last_seen_at  INTEGER NOT NULL,
    archived_at   INTEGER
);

CREATE INDEX services_machine ON services (machine_id);
CREATE INDEX services_archived ON services (archived_at);

-- Un changement d'état ou de santé, daté par Docker. exit_code n'est là
-- que sur un arrêt ; snippet garde les dernières lignes du journal sur un
-- arrêt anormal, en clair. Une fiche supprimée emporte ses transitions.
CREATE TABLE service_transitions (
    id              INTEGER PRIMARY KEY,
    service_id      TEXT NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    at              INTEGER NOT NULL,
    action          TEXT NOT NULL,
    previous_state  TEXT NOT NULL DEFAULT '',
    new_state       TEXT NOT NULL DEFAULT '',
    previous_health TEXT NOT NULL DEFAULT '',
    new_health      TEXT NOT NULL DEFAULT '',
    exit_code       INTEGER,
    replayed        INTEGER NOT NULL DEFAULT 0,
    snippet         TEXT NOT NULL DEFAULT ''
);

CREATE INDEX service_transitions_service ON service_transitions (service_id, at);
CREATE INDEX service_transitions_at ON service_transitions (at);

-- Les mesures par service, brutes, toutes les 30 s, gardées 48 h. Pas
-- d'agrégat : l'historique long par service attendra.
CREATE TABLE service_samples (
    service_id  TEXT NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    sampled_at  INTEGER NOT NULL,
    cpu_percent REAL NOT NULL,
    mem_used    INTEGER NOT NULL,
    mem_limit   INTEGER NOT NULL,
    PRIMARY KEY (service_id, sampled_at)
) WITHOUT ROWID;

CREATE INDEX service_samples_sampled ON service_samples (sampled_at);

-- Ce que chaque machine dit de son Docker : présent ou non, et sinon
-- pourquoi (no_socket, denied, down, too_old). L'absence n'est pas une
-- erreur : la machine n'a simplement aucun service.
CREATE TABLE machine_engines (
    machine_id  TEXT PRIMARY KEY REFERENCES machines (id) ON DELETE CASCADE,
    present     INTEGER NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    version     TEXT NOT NULL DEFAULT '',
    api_version TEXT NOT NULL DEFAULT '',
    checked_at  INTEGER NOT NULL
);
