-- Les alertes : un fait par objet, dédupliqué par (type, objet), aggravé
-- ou résolu par le composant qui l'a constaté, tu par un silence ou une
-- maintenance, acquitté par l'opérateur. Horodatages en secondes depuis
-- l'époque Unix.

CREATE TABLE alerts (
    id              INTEGER PRIMARY KEY,
    kind            TEXT NOT NULL,
    severity        TEXT NOT NULL CHECK (severity IN ('attention', 'danger')),
    status          TEXT NOT NULL CHECK (status IN ('open', 'resolved')),
    -- Ouverte sous un silence ou une maintenance : montrée, jamais livrée.
    silenced        INTEGER NOT NULL DEFAULT 0,
    object_kind     TEXT NOT NULL CHECK (object_kind IN ('machine', 'service', 'heartbeat', 'probe', 'volume')),
    object_id       TEXT NOT NULL,
    -- Le nom est gardé : un objet supprimé reste lisible dans l'historique.
    object_name     TEXT NOT NULL,
    -- L'objet, en clé étrangère mise à NULL quand il disparaît : c'est ce
    -- qui dit au balayage qu'une alerte ouverte n'a plus d'objet. Un
    -- volume ne porte que sa machine.
    machine_id      TEXT REFERENCES machines (id) ON DELETE SET NULL,
    service_id      TEXT REFERENCES services (id) ON DELETE SET NULL,
    heartbeat_id    TEXT REFERENCES heartbeats (id) ON DELETE SET NULL,
    probe_id        TEXT REFERENCES probes (id) ON DELETE SET NULL,
    -- Les chiffres du fait, en JSON ; le navigateur et le canal en font une phrase.
    details         TEXT NOT NULL DEFAULT '{}',
    opened_at       INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    resolved_at     INTEGER,
    acknowledged_at INTEGER
);

-- Une seule alerte ouverte par clé.
CREATE UNIQUE INDEX alerts_open_key ON alerts (kind, object_kind, object_id) WHERE status = 'open';
CREATE INDEX alerts_status ON alerts (status, severity, opened_at);
CREATE INDEX alerts_resolved ON alerts (resolved_at) WHERE resolved_at IS NOT NULL;
CREATE INDEX alerts_machine ON alerts (machine_id) WHERE machine_id IS NOT NULL;

-- Un canal est un webhook : une URL, un format de corps, un secret de
-- signature facultatif qui ne sort jamais du processus.
CREATE TABLE alert_channels (
    id             INTEGER PRIMARY KEY,
    name           TEXT NOT NULL,
    url            TEXT NOT NULL,
    format         TEXT NOT NULL CHECK (format IN ('json', 'text', 'discord', 'slack')),
    secret         TEXT NOT NULL DEFAULT '',
    min_severity   TEXT NOT NULL CHECK (min_severity IN ('attention', 'danger')),
    notify_resolve INTEGER NOT NULL DEFAULT 1,
    enabled        INTEGER NOT NULL DEFAULT 1,
    created_at     INTEGER NOT NULL
);

-- Une livraison est réservée avant d'être envoyée : une coupure entre les
-- deux laisse une ligne en attente que le démarrage rejoue, et l'index
-- unique empêche d'envoyer deux fois. Elle part avec son alerte ou son canal.
CREATE TABLE alert_deliveries (
    id         INTEGER PRIMARY KEY,
    alert_id   INTEGER NOT NULL REFERENCES alerts (id) ON DELETE CASCADE,
    channel_id INTEGER NOT NULL REFERENCES alert_channels (id) ON DELETE CASCADE,
    event      TEXT NOT NULL CHECK (event IN ('opened', 'aggravated', 'resolved')),
    status     TEXT NOT NULL CHECK (status IN ('pending', 'delivered', 'failed')),
    attempts   INTEGER NOT NULL DEFAULT 0,
    -- Le motif du dernier échec, un mot d'une liste fermée, et le code HTTP reçu.
    reason     TEXT NOT NULL DEFAULT '',
    code       INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (alert_id, channel_id, event)
);

CREATE INDEX alert_deliveries_pending ON alert_deliveries (status) WHERE status = 'pending';

-- Un silence tait, pendant une fenêtre, un type, un objet, ou un type sur
-- un objet ; jamais tout. Expiré, il reste lisible ; supprimé, il cesse.
CREATE TABLE alert_silences (
    id          INTEGER PRIMARY KEY,
    kind        TEXT NOT NULL DEFAULT '',
    object_kind TEXT NOT NULL DEFAULT '',
    object_id   TEXT NOT NULL DEFAULT '',
    object_name TEXT NOT NULL DEFAULT '',
    reason      TEXT NOT NULL DEFAULT '',
    starts_at   INTEGER NOT NULL,
    ends_at     INTEGER NOT NULL,
    created_at  INTEGER NOT NULL,
    CHECK (kind != '' OR object_id != '')
);
