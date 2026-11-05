-- Les seuils des alertes de ressources : une seule notion pour le disque, le
-- processeur et la mémoire (ressource × niveaux × fenêtre), au niveau global
-- ou surchargée pour une machine. Une machine sans ligne suit le global ; sans
-- ligne globale, le moteur prend les valeurs du produit.
-- machine_id NULL : le réglage global. window_seconds 0 : la dernière lecture
-- seule (le disque) ; sinon la moyenne sur cette durée. Le seuil de retour
-- n'est pas stocké : il se tient à un écart fixe sous l'attention.
CREATE TABLE alert_thresholds (
    machine_id        TEXT    REFERENCES machines (id) ON DELETE CASCADE,
    resource          TEXT    NOT NULL CHECK (resource IN ('cpu', 'memory', 'disk')),
    attention_percent INTEGER NOT NULL,
    danger_percent    INTEGER NOT NULL,
    window_seconds    INTEGER NOT NULL CHECK (window_seconds >= 0),
    CHECK (attention_percent < danger_percent)
);

CREATE UNIQUE INDEX alert_thresholds_global ON alert_thresholds (resource) WHERE machine_id IS NULL;
CREATE UNIQUE INDEX alert_thresholds_machine ON alert_thresholds (machine_id, resource) WHERE machine_id IS NOT NULL;

-- Les seuils du disque déjà réglés passent au global, tels quels. Une valeur
-- hors des bornes (50 à 99) n'était pas lue : elle valait les valeurs par
-- défaut, et ici elle n'est pas reprise.
INSERT INTO alert_thresholds (machine_id, resource, attention_percent, danger_percent, window_seconds)
SELECT NULL, 'disk', disk_attention_percent, disk_danger_percent, 0
FROM settings
WHERE disk_attention_percent >= 50 AND disk_attention_percent < disk_danger_percent AND disk_danger_percent <= 99;

ALTER TABLE settings DROP COLUMN disk_attention_percent;
ALTER TABLE settings DROP COLUMN disk_danger_percent;

-- Le rappel d'une alerte de danger ouverte, non lue et non tue : au bout de ce
-- nombre d'heures, 0 : coupé.
ALTER TABLE settings ADD COLUMN danger_reminder_hours INTEGER NOT NULL DEFAULT 12 CHECK (danger_reminder_hours >= 0);

-- notified_at : la dernière annonce d'ouverture ou d'aggravation, d'où part le
-- délai du rappel ; reminded_at : le rappel parti, un seul par alerte. Les
-- alertes déjà ouvertes partent de la mise à jour, pour qu'aucun rappel ne
-- sorte d'un coup au démarrage.
ALTER TABLE alerts ADD COLUMN notified_at INTEGER;
ALTER TABLE alerts ADD COLUMN reminded_at INTEGER;
UPDATE alerts SET notified_at = CAST(strftime('%s', 'now') AS INTEGER) WHERE status = 'open';

-- Une livraison peut maintenant annoncer un rappel : la liste fermée des
-- événements change, la table se refait.
CREATE TABLE alert_deliveries_new (
    id              INTEGER PRIMARY KEY,
    alert_id        INTEGER NOT NULL REFERENCES alerts (id) ON DELETE CASCADE,
    channel_id      INTEGER NOT NULL REFERENCES alert_channels (id) ON DELETE CASCADE,
    event           TEXT    NOT NULL CHECK (event IN ('opened', 'aggravated', 'resolved', 'reminded')),
    status          TEXT    NOT NULL CHECK (status IN ('pending', 'delivered', 'failed', 'held', 'muted')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    reason          TEXT    NOT NULL DEFAULT '',
    code            INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    next_attempt_at INTEGER,
    UNIQUE (alert_id, channel_id, event)
);

INSERT INTO alert_deliveries_new (id, alert_id, channel_id, event, status, attempts, reason, code, created_at, updated_at, next_attempt_at)
SELECT id, alert_id, channel_id, event, status, attempts, reason, code, created_at, updated_at, next_attempt_at FROM alert_deliveries;

DROP TABLE alert_deliveries;
ALTER TABLE alert_deliveries_new RENAME TO alert_deliveries;

CREATE INDEX alert_deliveries_pending ON alert_deliveries (status) WHERE status = 'pending';
CREATE INDEX alert_deliveries_due ON alert_deliveries (next_attempt_at) WHERE next_attempt_at IS NOT NULL;
