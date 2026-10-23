-- La page de statut publique : des composants nommés pour un visiteur, faits
-- d'objets qu'openCloud surveille déjà ; des incidents écrits par
-- l'opérateur. Rien ici n'est calculé : l'état d'un composant se dérive à la
-- lecture de ses objets, et un incident ouvert le remplace.

CREATE TABLE status_components (
    id         TEXT PRIMARY KEY,
    -- Le nom que le visiteur lit ; le seul mot d'openCloud qui sort en public.
    name       TEXT NOT NULL,
    -- L'ordre d'affichage, à la main.
    position   INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

-- Un objet rattaché à un composant : exactement une des quatre colonnes.
-- Quatre clés étrangères plutôt qu'un couple (genre, id) : l'objet supprimé
-- retire le lien tout seul, rien ne pend.
CREATE TABLE status_component_members (
    component_id TEXT NOT NULL REFERENCES status_components (id) ON DELETE CASCADE,
    machine_id   TEXT REFERENCES machines (id) ON DELETE CASCADE,
    service_id   TEXT REFERENCES services (id) ON DELETE CASCADE,
    heartbeat_id TEXT REFERENCES heartbeats (id) ON DELETE CASCADE,
    probe_id     TEXT REFERENCES probes (id) ON DELETE CASCADE,
    CHECK ((machine_id IS NOT NULL) + (service_id IS NOT NULL) + (heartbeat_id IS NOT NULL) + (probe_id IS NOT NULL) = 1)
);

-- Un objet ne compte qu'une fois par composant.
CREATE UNIQUE INDEX status_members_machine ON status_component_members (component_id, machine_id) WHERE machine_id IS NOT NULL;
CREATE UNIQUE INDEX status_members_service ON status_component_members (component_id, service_id) WHERE service_id IS NOT NULL;
CREATE UNIQUE INDEX status_members_heartbeat ON status_component_members (component_id, heartbeat_id) WHERE heartbeat_id IS NOT NULL;
CREATE UNIQUE INDEX status_members_probe ON status_component_members (component_id, probe_id) WHERE probe_id IS NOT NULL;

-- Un incident est ce que l'opérateur dit au public. Une maintenance est un
-- incident d'impact « maintenance » avec une fenêtre : la boucle l'ouvre à
-- starts_at et le résout à ends_at. Purgé un an après sa résolution.
CREATE TABLE incidents (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    -- Ce que les composants touchés affichent tant que l'incident est ouvert.
    impact      TEXT NOT NULL CHECK (impact IN ('degraded', 'down', 'maintenance')),
    status      TEXT NOT NULL CHECK (status IN ('scheduled', 'investigating', 'identified', 'monitoring', 'in_progress', 'resolved')),
    -- La fenêtre d'une maintenance ; NULL pour un incident.
    starts_at   INTEGER,
    ends_at     INTEGER,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    resolved_at INTEGER
);

CREATE INDEX incidents_open ON incidents (status) WHERE status != 'resolved';
CREATE INDEX incidents_resolved ON incidents (resolved_at) WHERE resolved_at IS NOT NULL;

CREATE TABLE incident_components (
    incident_id  TEXT NOT NULL REFERENCES incidents (id) ON DELETE CASCADE,
    component_id TEXT NOT NULL REFERENCES status_components (id) ON DELETE CASCADE,
    PRIMARY KEY (incident_id, component_id)
) WITHOUT ROWID;

CREATE INDEX incident_components_component ON incident_components (component_id);

-- Le fil d'un incident : chaque entrée porte le statut qu'elle a donné et
-- un message, éventuellement vide quand c'est la boucle qui parle.
CREATE TABLE incident_updates (
    id          INTEGER PRIMARY KEY,
    incident_id TEXT NOT NULL REFERENCES incidents (id) ON DELETE CASCADE,
    status      TEXT NOT NULL,
    message     TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);

CREATE INDEX incident_updates_incident ON incident_updates (incident_id, created_at);
