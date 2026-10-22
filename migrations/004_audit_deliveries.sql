-- Le journal part aussi vers les canaux qui le reçoivent : une base effacée
-- n'efface plus la trace. Une livraison est réservée dans la transaction
-- de l'événement, puis envoyée par le notifieur des alertes, avec les mêmes
-- essais espacés (jusqu'à 24 h).
ALTER TABLE alert_channels ADD COLUMN receives_audit INTEGER NOT NULL DEFAULT 0 CHECK (receives_audit IN (0, 1));

-- pending : réservée, pas encore envoyée (ou rejouée) ; delivered ; failed :
-- en échec, next_attempt_at dit quand la rejouer, NULL quand on abandonne.
-- Elle part avec son événement (purge à un an) et avec son canal.
CREATE TABLE audit_deliveries (
    id              INTEGER PRIMARY KEY,
    audit_id        INTEGER NOT NULL REFERENCES audit_events (id) ON DELETE CASCADE,
    channel_id      INTEGER NOT NULL REFERENCES alert_channels (id) ON DELETE CASCADE,
    status          TEXT    NOT NULL CHECK (status IN ('pending', 'delivered', 'failed')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    reason          TEXT    NOT NULL DEFAULT '',
    code            INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    next_attempt_at INTEGER,
    UNIQUE (audit_id, channel_id)
);

CREATE INDEX audit_deliveries_pending ON audit_deliveries (status) WHERE status = 'pending';
CREATE INDEX audit_deliveries_due ON audit_deliveries (next_attempt_at) WHERE next_attempt_at IS NOT NULL;
CREATE INDEX audit_deliveries_channel ON audit_deliveries (channel_id);
