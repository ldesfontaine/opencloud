-- Un canal n'est plus seulement une URL de webhook : un e-mail vise des
-- destinataires, un salon Matrix vit derrière son serveur d'accueil.
-- destination porte ce que l'URL ne dit pas. Le format quitte le CHECK,
-- comme le type d'une alerte : la liste fermée vit dans internal/alert,
-- et un format de plus ne demandera plus de reconstruire la table.
--
-- SQLite ne retire pas un CHECK : on reconstruit. Les livraisons sont
-- reconstruites avec, sinon supprimer l'ancienne table des canaux les
-- emporterait par la cascade.

ALTER TABLE alert_deliveries RENAME TO alert_deliveries_old;
ALTER TABLE alert_channels RENAME TO alert_channels_old;

CREATE TABLE alert_channels (
    id             INTEGER PRIMARY KEY,
    name           TEXT NOT NULL,
    url            TEXT NOT NULL,
    format         TEXT NOT NULL,
    destination    TEXT NOT NULL DEFAULT '',
    secret         TEXT NOT NULL DEFAULT '',
    min_severity   TEXT NOT NULL CHECK (min_severity IN ('attention', 'danger')),
    notify_resolve INTEGER NOT NULL DEFAULT 1,
    enabled        INTEGER NOT NULL DEFAULT 1,
    created_at     INTEGER NOT NULL
);

INSERT INTO alert_channels (id, name, url, format, secret, min_severity, notify_resolve, enabled, created_at)
SELECT id, name, url, format, secret, min_severity, notify_resolve, enabled, created_at FROM alert_channels_old;

CREATE TABLE alert_deliveries (
    id         INTEGER PRIMARY KEY,
    alert_id   INTEGER NOT NULL REFERENCES alerts (id) ON DELETE CASCADE,
    channel_id INTEGER NOT NULL REFERENCES alert_channels (id) ON DELETE CASCADE,
    event      TEXT NOT NULL CHECK (event IN ('opened', 'aggravated', 'resolved')),
    status     TEXT NOT NULL CHECK (status IN ('pending', 'delivered', 'failed')),
    attempts   INTEGER NOT NULL DEFAULT 0,
    -- Le motif du dernier échec, un mot d'une liste fermée, et le code reçu.
    reason     TEXT NOT NULL DEFAULT '',
    code       INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (alert_id, channel_id, event)
);

INSERT INTO alert_deliveries SELECT * FROM alert_deliveries_old;

DROP TABLE alert_deliveries_old;
DROP TABLE alert_channels_old;

CREATE INDEX alert_deliveries_pending ON alert_deliveries (status) WHERE status = 'pending';
