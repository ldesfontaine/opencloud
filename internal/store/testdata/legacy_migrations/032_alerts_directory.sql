-- L'alerte vise l'annuaire, et un seul objet fait taire.
--
-- 1. Une alerte ne porte plus son objet deux fois en se contredisant : le
-- genre « service » désignait un conteneur. Il devient « container », et la
-- clé étrangère service_id vise le service déclaré auquel l'alerte
-- appartient : celui de l'unité du conteneur, ou celui que la sonde
-- surveille. Un conteneur présent non déclaré n'a que container_id. La clé
-- de dédup reste (type, genre, identifiant) : un service à plusieurs unités
-- garde une alerte par conteneur, chacun a son propre état de plantage.
-- Plus de CHECK sur le genre : la liste vit dans internal/alert. La colonne
-- silenced part : taire se juge à chaque envoi, par les fenêtres de mutes.
--
-- 2. La livraison échouée n'est plus perdue : next_attempt_at dit quand le
-- balayage la rejoue ; NULL, elle est abandonnée (ou réussie). L'état
-- « muted » note un envoi qu'une fenêtre pour taire a retenu.
--
-- 3. mutes remplace alert_silences et l'effet des maintenances sur les
-- alertes : une fenêtre début-fin sur une machine, un service déclaré, un
-- conteneur non déclaré, une sonde, une tâche, ou tout ; un type d'alerte en
-- plus, ou tous. Clés étrangères en cascade : la fenêtre part avec son
-- objet. Une maintenance reste un incident, ce que l'opérateur dit au
-- public ; elle ne tait plus rien par elle-même.
--
-- Comme toutes, cette migration tourne hors clés étrangères
-- (store.applyContent) : les tables reconstruites gardent leur nom, les
-- livraisons retrouvent leurs alertes, et les clés sont vérifiées avant le
-- COMMIT.

CREATE TABLE alerts_new (
    id              INTEGER PRIMARY KEY,
    kind            TEXT NOT NULL,
    severity        TEXT NOT NULL CHECK (severity IN ('attention', 'danger')),
    status          TEXT NOT NULL CHECK (status IN ('open', 'resolved')),
    -- machine, container, probe, heartbeat, volume : la liste vit dans le code.
    object_kind     TEXT NOT NULL,
    object_id       TEXT NOT NULL,
    -- Recopié : un objet supprimé reste lisible dans l'historique.
    object_name     TEXT NOT NULL,
    -- L'objet en clé étrangère, mise à NULL quand il disparaît : c'est ce qui
    -- dit au balayage qu'une alerte ouverte n'a plus d'objet. Un volume ne
    -- porte que sa machine. service_id dit à quel service déclaré l'alerte
    -- appartient ; il ne fait pas l'objet.
    machine_id      TEXT REFERENCES machines (id) ON DELETE SET NULL,
    service_id      TEXT REFERENCES declared_services (id) ON DELETE SET NULL,
    container_id    TEXT REFERENCES containers (id) ON DELETE SET NULL,
    probe_id        TEXT REFERENCES probes (id) ON DELETE SET NULL,
    heartbeat_id    TEXT REFERENCES heartbeats (id) ON DELETE SET NULL,
    details         TEXT NOT NULL DEFAULT '{}',
    opened_at       INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    resolved_at     INTEGER,
    acknowledged_at INTEGER,
    clear_since     INTEGER
);

INSERT INTO alerts_new (id, kind, severity, status, object_kind, object_id, object_name,
    machine_id, service_id, container_id, probe_id, heartbeat_id,
    details, opened_at, updated_at, resolved_at, acknowledged_at, clear_since)
SELECT a.id, a.kind, a.severity, a.status,
    CASE a.object_kind WHEN 'service' THEN 'container' ELSE a.object_kind END,
    a.object_id, a.object_name,
    a.machine_id,
    CASE a.object_kind
        WHEN 'service' THEN (SELECT u.service_id FROM containers c JOIN service_units u ON u.id = c.unit_id WHERE c.id = a.container_id)
        WHEN 'probe' THEN (SELECT p.service_id FROM probes p WHERE p.id = a.probe_id)
    END,
    a.container_id, a.probe_id, a.heartbeat_id,
    a.details, a.opened_at, a.updated_at, a.resolved_at, a.acknowledged_at, a.clear_since
FROM alerts a;

DROP TABLE alerts;
ALTER TABLE alerts_new RENAME TO alerts;

-- Une seule alerte ouverte par clé.
CREATE UNIQUE INDEX alerts_open_key ON alerts (kind, object_kind, object_id) WHERE status = 'open';
CREATE INDEX alerts_status ON alerts (status, severity, opened_at);
CREATE INDEX alerts_resolved ON alerts (resolved_at) WHERE resolved_at IS NOT NULL;
CREATE INDEX alerts_machine ON alerts (machine_id) WHERE machine_id IS NOT NULL;
CREATE INDEX alerts_service ON alerts (service_id) WHERE service_id IS NOT NULL;

CREATE TABLE alert_deliveries_new (
    id              INTEGER PRIMARY KEY,
    alert_id        INTEGER NOT NULL REFERENCES alerts (id) ON DELETE CASCADE,
    channel_id      INTEGER NOT NULL REFERENCES alert_channels (id) ON DELETE CASCADE,
    event           TEXT NOT NULL CHECK (event IN ('opened', 'aggravated', 'resolved')),
    status          TEXT NOT NULL CHECK (status IN ('pending', 'delivered', 'failed', 'held', 'muted')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    -- Le motif du dernier échec, un mot d'une liste fermée, et le code reçu.
    reason          TEXT NOT NULL DEFAULT '',
    code            INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    -- Le prochain essai d'une livraison en échec ; NULL : plus d'essai.
    next_attempt_at INTEGER,
    UNIQUE (alert_id, channel_id, event)
);

-- Les échecs d'avant restent perdus, comme ils l'étaient : NULL.
INSERT INTO alert_deliveries_new (id, alert_id, channel_id, event, status, attempts, reason, code, created_at, updated_at)
SELECT id, alert_id, channel_id, event, status, attempts, reason, code, created_at, updated_at FROM alert_deliveries;

DROP TABLE alert_deliveries;
ALTER TABLE alert_deliveries_new RENAME TO alert_deliveries;
CREATE INDEX alert_deliveries_pending ON alert_deliveries (status) WHERE status = 'pending';
CREATE INDEX alert_deliveries_due ON alert_deliveries (next_attempt_at) WHERE next_attempt_at IS NOT NULL;

CREATE TABLE mutes (
    id           INTEGER PRIMARY KEY,
    -- Le type d'alerte tu ; NULL : tous.
    alert_kind   TEXT,
    -- Ce qui est tu : un objet au plus ; aucun, tout. Une machine tait aussi
    -- ce qui vit sur elle, un service déclaré ses conteneurs et ses sondes.
    machine_id   TEXT REFERENCES machines (id) ON DELETE CASCADE,
    service_id   TEXT REFERENCES declared_services (id) ON DELETE CASCADE,
    container_id TEXT REFERENCES containers (id) ON DELETE CASCADE,
    probe_id     TEXT REFERENCES probes (id) ON DELETE CASCADE,
    heartbeat_id TEXT REFERENCES heartbeats (id) ON DELETE CASCADE,
    reason       TEXT NOT NULL DEFAULT '',
    -- La fenêtre : immédiate (starts_at à la création) ou planifiée.
    starts_at    INTEGER NOT NULL,
    ends_at      INTEGER NOT NULL,
    -- L'acteur du journal d'audit, « genre:nom » ; vide pour ce que la
    -- migration a recopié.
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    CHECK ((machine_id IS NOT NULL) + (service_id IS NOT NULL) + (container_id IS NOT NULL)
        + (probe_id IS NOT NULL) + (heartbeat_id IS NOT NULL) <= 1),
    CHECK (ends_at > starts_at)
);

CREATE INDEX mutes_ends ON mutes (ends_at);
CREATE INDEX mutes_machine ON mutes (machine_id) WHERE machine_id IS NOT NULL;
CREATE INDEX mutes_service ON mutes (service_id) WHERE service_id IS NOT NULL;
CREATE INDEX mutes_container ON mutes (container_id) WHERE container_id IS NOT NULL;
CREATE INDEX mutes_probe ON mutes (probe_id) WHERE probe_id IS NOT NULL;
CREATE INDEX mutes_heartbeat ON mutes (heartbeat_id) WHERE heartbeat_id IS NOT NULL;

-- Les silences, recopiés avec leur identifiant (le journal d'audit le
-- cite). Un silence dont l'objet n'existe plus ne taisait plus rien : il ne
-- passe pas. Un silence sur un volume tait le disque de sa machine : la
-- fenêtre ne vise pas un point de montage.
INSERT INTO mutes (id, alert_kind, machine_id, container_id, probe_id, heartbeat_id, reason, starts_at, ends_at, created_at)
SELECT s.id,
    CASE
        WHEN s.object_kind = 'volume' THEN 'disk_full'
        ELSE NULLIF(s.kind, '')
    END,
    CASE s.object_kind
        WHEN 'machine' THEN s.object_id
        WHEN 'volume' THEN substr(s.object_id, 1, instr(s.object_id, ':') - 1)
    END,
    CASE WHEN s.object_kind = 'service' THEN s.object_id END,
    CASE WHEN s.object_kind = 'probe' THEN s.object_id END,
    CASE WHEN s.object_kind = 'heartbeat' THEN s.object_id END,
    s.reason, s.starts_at, s.ends_at, s.created_at
FROM alert_silences s
WHERE s.ends_at > s.starts_at AND (
    s.object_kind = ''
    OR (s.object_kind = 'machine' AND s.object_id IN (SELECT id FROM machines))
    OR (s.object_kind = 'volume' AND substr(s.object_id, 1, instr(s.object_id, ':') - 1) IN (SELECT id FROM machines))
    OR (s.object_kind = 'service' AND s.object_id IN (SELECT id FROM containers))
    OR (s.object_kind = 'probe' AND s.object_id IN (SELECT id FROM probes))
    OR (s.object_kind = 'heartbeat' AND s.object_id IN (SELECT id FROM heartbeats)));

DROP TABLE alert_silences;

-- Une maintenance pas encore résolue taisait les alertes des membres de ses
-- composants : chaque membre reçoit une fenêtre sur la même plage, au titre
-- de la maintenance. L'incident reste, pour le public.
INSERT INTO mutes (machine_id, service_id, probe_id, heartbeat_id, reason, starts_at, ends_at, created_at)
SELECT DISTINCT m.machine_id, m.service_id, m.probe_id, m.heartbeat_id,
    substr(i.title, 1, 200), i.starts_at, i.ends_at, i.created_at
FROM incidents i
JOIN incident_components ic ON ic.incident_id = i.id
JOIN status_component_members m ON m.component_id = ic.component_id
WHERE i.impact = 'maintenance' AND i.status != 'resolved'
    AND i.starts_at IS NOT NULL AND i.ends_at > i.starts_at;
