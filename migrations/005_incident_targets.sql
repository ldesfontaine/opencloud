-- Un incident vise des objets de l'annuaire, plus des composants : chaque
-- page de statut montre ceux qui touchent l'un de ses composants, calculé à
-- la lecture. Lien seul : l'objet retiré, l'incident garde son texte.
CREATE TABLE incident_targets (
    incident_id  TEXT NOT NULL REFERENCES incidents (id) ON DELETE CASCADE,
    machine_id   TEXT REFERENCES machines (id) ON DELETE CASCADE,
    service_id   TEXT REFERENCES declared_services (id) ON DELETE CASCADE,
    heartbeat_id TEXT REFERENCES heartbeats (id) ON DELETE CASCADE,
    probe_id     TEXT REFERENCES probes (id) ON DELETE CASCADE,
    CHECK ((machine_id IS NOT NULL) + (service_id IS NOT NULL) + (heartbeat_id IS NOT NULL) + (probe_id IS NOT NULL) = 1)
);

-- Un objet ne compte qu'une fois par incident ; les index par objet servent
-- la cascade et le plan de suppression.
CREATE UNIQUE INDEX incident_targets_machine ON incident_targets (incident_id, machine_id) WHERE machine_id IS NOT NULL;
CREATE UNIQUE INDEX incident_targets_service ON incident_targets (incident_id, service_id) WHERE service_id IS NOT NULL;
CREATE UNIQUE INDEX incident_targets_heartbeat ON incident_targets (incident_id, heartbeat_id) WHERE heartbeat_id IS NOT NULL;
CREATE UNIQUE INDEX incident_targets_probe ON incident_targets (incident_id, probe_id) WHERE probe_id IS NOT NULL;
CREATE INDEX incident_targets_by_machine ON incident_targets (machine_id) WHERE machine_id IS NOT NULL;
CREATE INDEX incident_targets_by_service ON incident_targets (service_id) WHERE service_id IS NOT NULL;
CREATE INDEX incident_targets_by_heartbeat ON incident_targets (heartbeat_id) WHERE heartbeat_id IS NOT NULL;
CREATE INDEX incident_targets_by_probe ON incident_targets (probe_id) WHERE probe_id IS NOT NULL;

-- Chaque composant visé est déplié en ses membres ; un même objet venu de
-- deux composants ne compte qu'une fois (DISTINCT tient les NULL pour égaux).
INSERT INTO incident_targets (incident_id, machine_id, service_id, heartbeat_id, probe_id)
SELECT DISTINCT ic.incident_id, m.machine_id, m.service_id, m.heartbeat_id, m.probe_id
FROM incident_components ic
JOIN status_component_members m ON m.component_id = ic.component_id;

DROP TABLE incident_components;

-- Un silence peut s'annoncer : il paraît comme une maintenance sur les pages
-- dont un composant est touché par sa cible, sous son titre public.
ALTER TABLE mutes ADD COLUMN announce     INTEGER NOT NULL DEFAULT 0 CHECK (announce IN (0, 1));
ALTER TABLE mutes ADD COLUMN public_title TEXT    NOT NULL DEFAULT '';
