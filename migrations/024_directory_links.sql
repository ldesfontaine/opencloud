-- Les liens quittent le conteneur observé pour l'annuaire : une sonde et un
-- membre de composant visent le service déclaré, la politique de mise à jour
-- vit sur l'unité. Recréer un conteneur ne casse plus rien.
--
-- Comme toutes, cette migration tourne hors clés étrangères ; elles sont
-- vérifiées avant le COMMIT (store.applyContent). L'étape Go
-- store.linkContainersToDirectory passe juste avant, dans la même
-- transaction : elle crée les services des conteneurs qui portent un lien
-- (declared_by 'migration', needs_review 1) et remplit temp.link_migration,
-- une ligne par fiche rattachée, archivées comprises. Le nommage et le
-- rapprochement sont ceux du code ; les écrire ici les dédoublerait. Après le
-- SQL, store.verifyContainerLinks compte ce qui devait survivre.

-- 1. La politique passe sur l'unité. watch : suivre ; hold : vérifiée,
-- jamais montrée ; ignore : jamais interrogée. Pas de CHECK, comme les
-- autres listes de l'annuaire. Plusieurs fiches pour une unité : la plus
-- récente qui en portait une gagne, une vivante avant une archivée. C'est ce
-- qui rend à un conteneur recréé la politique que sa fiche avait perdue.
ALTER TABLE service_units ADD COLUMN update_policy TEXT NOT NULL DEFAULT 'watch';

UPDATE service_units SET update_policy = (
    SELECT CASE s.update_policy WHEN 'pinned' THEN 'hold' ELSE 'ignore' END
    FROM temp.link_migration l JOIN services s ON s.id = l.container_id
    WHERE l.unit_id = service_units.id AND s.update_policy != ''
    ORDER BY s.archived_at IS NOT NULL, s.last_seen_at DESC, s.id
    LIMIT 1)
WHERE id IN (
    SELECT l.unit_id FROM temp.link_migration l JOIN services s ON s.id = l.container_id
    WHERE s.update_policy != '');

-- 2. Les fiches liées sont rattachées sans attendre l'inventaire, archivées
-- comprises : elles disent « était rattaché à ». Une fiche que l'inventaire
-- avait déjà rattachée garde son unité.
UPDATE services SET
    unit_id     = (SELECT l.unit_id FROM temp.link_migration l WHERE l.container_id = services.id),
    match_level = (SELECT l.match_level FROM temp.link_migration l WHERE l.container_id = services.id)
WHERE unit_id IS NULL AND id IN (SELECT container_id FROM temp.link_migration);

-- La fiche lit désormais la politique de son unité.
ALTER TABLE services DROP COLUMN update_policy;

-- 3. La sonde vise le service déclaré, sur n'importe quelle machine. On ne
-- retire pas un service qu'une sonde surveille sans le dire : restrict. Le
-- reste de la table ne change pas.
CREATE TABLE probes_new (
    id                    TEXT PRIMARY KEY,
    name                  TEXT NOT NULL,
    kind                  TEXT NOT NULL CHECK (kind IN ('http', 'tcp')),
    target                TEXT NOT NULL,
    machine_id            TEXT NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    service_id            TEXT REFERENCES declared_services (id) ON DELETE RESTRICT,
    status                TEXT NOT NULL CHECK (status IN ('new', 'up', 'degraded', 'down', 'paused')),
    interval_seconds      INTEGER NOT NULL,
    timeout_seconds       INTEGER NOT NULL,
    failure_threshold     INTEGER NOT NULL,
    recovery_threshold    INTEGER NOT NULL,
    method                TEXT NOT NULL DEFAULT '',
    expected_status       TEXT NOT NULL DEFAULT '',
    expected_body         TEXT NOT NULL DEFAULT '',
    follow_redirects      INTEGER NOT NULL DEFAULT 0,
    consecutive_failures  INTEGER NOT NULL DEFAULT 0,
    consecutive_successes INTEGER NOT NULL DEFAULT 0,
    last_checked_at       INTEGER,
    last_duration_ms      INTEGER NOT NULL DEFAULT 0,
    last_code             INTEGER,
    last_reason           TEXT NOT NULL DEFAULT '',
    cert_subject          TEXT NOT NULL DEFAULT '',
    cert_issuer           TEXT NOT NULL DEFAULT '',
    cert_not_before       INTEGER,
    cert_not_after        INTEGER,
    cert_fingerprint      TEXT NOT NULL DEFAULT '',
    created_at            INTEGER NOT NULL,
    tls                   INTEGER NOT NULL DEFAULT 0,
    cert_chain_valid      INTEGER NOT NULL DEFAULT 0,
    cert_hostname_match   INTEGER NOT NULL DEFAULT 0,
    cert_ocsp             TEXT NOT NULL DEFAULT '',
    revised_at            INTEGER
);

INSERT INTO probes_new (id, name, kind, target, machine_id, service_id, status,
    interval_seconds, timeout_seconds, failure_threshold, recovery_threshold,
    method, expected_status, expected_body, follow_redirects,
    consecutive_failures, consecutive_successes, last_checked_at, last_duration_ms, last_code, last_reason,
    cert_subject, cert_issuer, cert_not_before, cert_not_after, cert_fingerprint, created_at,
    tls, cert_chain_valid, cert_hostname_match, cert_ocsp, revised_at)
SELECT p.id, p.name, p.kind, p.target, p.machine_id,
    (SELECT l.service_id FROM temp.link_migration l WHERE l.container_id = p.service_id),
    p.status, p.interval_seconds, p.timeout_seconds, p.failure_threshold, p.recovery_threshold,
    p.method, p.expected_status, p.expected_body, p.follow_redirects,
    p.consecutive_failures, p.consecutive_successes, p.last_checked_at, p.last_duration_ms, p.last_code, p.last_reason,
    p.cert_subject, p.cert_issuer, p.cert_not_before, p.cert_not_after, p.cert_fingerprint, p.created_at,
    p.tls, p.cert_chain_valid, p.cert_hostname_match, p.cert_ocsp, p.revised_at
FROM probes p;

-- Hors clés étrangères, le DROP n'emporte ni résultats, ni jours, ni
-- membres : ils retrouvent la table par son nom.
DROP TABLE probes;
ALTER TABLE probes_new RENAME TO probes;
CREATE INDEX probes_machine ON probes (machine_id);
CREATE INDEX probes_service ON probes (service_id);
CREATE INDEX probes_cert_expiry ON probes (cert_not_after) WHERE cert_fingerprint != '';

-- 4. Le membre « service » d'un composant vise le service déclaré. Deux
-- conteneurs d'un même projet donnent le même service : un seul membre, le
-- premier dans l'ordre. Pas de OR IGNORE : il tairait aussi un CHECK
-- violé, donc un lien perdu. Les trois autres clés restent en cascade
-- jusqu'aux plans de suppression.
CREATE TABLE status_component_members_new (
    component_id TEXT NOT NULL REFERENCES status_components (id) ON DELETE CASCADE,
    machine_id   TEXT REFERENCES machines (id) ON DELETE CASCADE,
    service_id   TEXT REFERENCES declared_services (id) ON DELETE RESTRICT,
    heartbeat_id TEXT REFERENCES heartbeats (id) ON DELETE CASCADE,
    probe_id     TEXT REFERENCES probes (id) ON DELETE CASCADE,
    CHECK ((machine_id IS NOT NULL) + (service_id IS NOT NULL) + (heartbeat_id IS NOT NULL) + (probe_id IS NOT NULL) = 1)
);

INSERT INTO status_component_members_new (component_id, machine_id, service_id, heartbeat_id, probe_id)
SELECT m.component_id, m.machine_id, l.service_id, m.heartbeat_id, m.probe_id
FROM status_component_members m
LEFT JOIN temp.link_migration l ON l.container_id = m.service_id
WHERE m.service_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM status_component_members earlier
    JOIN temp.link_migration le ON le.container_id = earlier.service_id
    WHERE earlier.component_id = m.component_id AND le.service_id = l.service_id AND earlier.rowid < m.rowid)
ORDER BY m.rowid;

DROP TABLE status_component_members;
ALTER TABLE status_component_members_new RENAME TO status_component_members;
CREATE UNIQUE INDEX status_members_machine ON status_component_members (component_id, machine_id) WHERE machine_id IS NOT NULL;
CREATE UNIQUE INDEX status_members_service ON status_component_members (component_id, service_id) WHERE service_id IS NOT NULL;
CREATE UNIQUE INDEX status_members_heartbeat ON status_component_members (component_id, heartbeat_id) WHERE heartbeat_id IS NOT NULL;
CREATE UNIQUE INDEX status_members_probe ON status_component_members (component_id, probe_id) WHERE probe_id IS NOT NULL;

DROP TABLE temp.link_migration;
