-- Anti-rebond des alertes. clear_since : depuis quand l'état d'un service
-- est redevenu bon ; l'alerte ne se résout qu'après un délai de calme, et
-- un retour du problème avant efface la date. NULL pour toutes les
-- alertes existantes : rien ne change pour elles.
ALTER TABLE alerts ADD COLUMN clear_since INTEGER;

-- Une livraison peut être retenue par la limite de débit de son canal :
-- elle est notée, jamais envoyée, et résumée dans un message groupé.
-- SQLite ne modifie pas un CHECK : la table est recopiée.
CREATE TABLE alert_deliveries_new (
    id         INTEGER PRIMARY KEY,
    alert_id   INTEGER NOT NULL REFERENCES alerts (id) ON DELETE CASCADE,
    channel_id INTEGER NOT NULL REFERENCES alert_channels (id) ON DELETE CASCADE,
    event      TEXT NOT NULL CHECK (event IN ('opened', 'aggravated', 'resolved')),
    status     TEXT NOT NULL CHECK (status IN ('pending', 'delivered', 'failed', 'held')),
    attempts   INTEGER NOT NULL DEFAULT 0,
    reason     TEXT NOT NULL DEFAULT '',
    code       INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (alert_id, channel_id, event)
);
INSERT INTO alert_deliveries_new (id, alert_id, channel_id, event, status, attempts, reason, code, created_at, updated_at)
    SELECT id, alert_id, channel_id, event, status, attempts, reason, code, created_at, updated_at FROM alert_deliveries;
DROP TABLE alert_deliveries;
ALTER TABLE alert_deliveries_new RENAME TO alert_deliveries;
CREATE INDEX alert_deliveries_pending ON alert_deliveries (status) WHERE status = 'pending';

-- Un redémarrage non demandé ouvert se résolvait sur son updated_at ; il se
-- résout désormais sur clear_since, qui part de là.
UPDATE alerts SET clear_since = updated_at WHERE status = 'open' AND kind = 'service_restart';
