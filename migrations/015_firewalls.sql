-- Les pare-feu que l'opérateur déclare, et ce que leur API en dit : la
-- version, les interfaces, les règles, les baux DHCP, les passerelles. Un
-- pare-feu n'appartient à aucune machine. La clé d'API est l'identifiant de la
-- paire ; le secret est scellé. Les quatre tables de relevé sont un reflet du
-- dernier relevé réussi, remplacé en entier à chaque lecture, sans historique.

CREATE TABLE firewalls (
    id           TEXT    PRIMARY KEY,
    name         TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    -- Le connecteur qui sait lire cette marque de pare-feu.
    kind         TEXT    NOT NULL CHECK (kind IN ('opnsense')),
    -- https://hôte[:port], sans chemin.
    api_url      TEXT    NOT NULL,
    api_key      TEXT    NOT NULL,
    api_secret   TEXT    NOT NULL,
    -- Empreinte SHA-256 du certificat, en hexadécimal ; vide : le magasin du
    -- système juge (un pare-feu auto-signé demande l'empreinte).
    pin          TEXT    NOT NULL DEFAULT '',
    version      TEXT    NOT NULL DEFAULT '',
    -- Quand la paire de clés a répondu à la déclaration.
    verified_at  INTEGER NOT NULL,
    -- Le dernier relevé réussi ; nul tant qu'aucun ne l'a été.
    last_read_at INTEGER,
    -- La clé du refus du dernier relevé qui a échoué, vide s'il a réussi.
    last_error   TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL
);

CREATE TABLE firewall_interfaces (
    firewall_id TEXT NOT NULL REFERENCES firewalls (id) ON DELETE CASCADE,
    -- L'identifiant de l'interface chez le pare-feu (« lan », « opt1 »).
    name        TEXT NOT NULL,
    device      TEXT NOT NULL DEFAULT '',
    -- Le rôle que le pare-feu lui donne (sa description : « WAN », « LAN »).
    role        TEXT NOT NULL DEFAULT '',
    address     TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (firewall_id, name)
);

CREATE TABLE firewall_rules (
    firewall_id      TEXT    NOT NULL REFERENCES firewalls (id) ON DELETE CASCADE,
    -- L'ordre où le pare-feu les rend, qui est celui où elles s'évaluent.
    position         INTEGER NOT NULL,
    uuid             TEXT    NOT NULL DEFAULT '',
    interface        TEXT    NOT NULL DEFAULT '',
    action           TEXT    NOT NULL DEFAULT '',
    direction        TEXT    NOT NULL DEFAULT '',
    protocol         TEXT    NOT NULL DEFAULT '',
    source           TEXT    NOT NULL DEFAULT '',
    source_port      TEXT    NOT NULL DEFAULT '',
    destination      TEXT    NOT NULL DEFAULT '',
    destination_port TEXT    NOT NULL DEFAULT '',
    description      TEXT    NOT NULL DEFAULT '',
    enabled          INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    PRIMARY KEY (firewall_id, position)
);

CREATE TABLE firewall_leases (
    firewall_id TEXT    NOT NULL REFERENCES firewalls (id) ON DELETE CASCADE,
    position    INTEGER NOT NULL,
    address     TEXT    NOT NULL,
    mac         TEXT    NOT NULL DEFAULT '',
    hostname    TEXT    NOT NULL DEFAULT '',
    interface   TEXT    NOT NULL DEFAULT '',
    -- Le serveur DHCP qui a donné le bail : dnsmasq, kea ou isc.
    server      TEXT    NOT NULL DEFAULT '',
    -- Un bail réservé à une adresse MAC ou donné au hasard.
    is_static   INTEGER NOT NULL DEFAULT 0 CHECK (is_static IN (0, 1)),
    -- Nul : le pare-feu ne dit pas quand il expire.
    expires_at  INTEGER,
    PRIMARY KEY (firewall_id, position)
);

CREATE TABLE firewall_gateways (
    firewall_id  TEXT NOT NULL REFERENCES firewalls (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    address      TEXT NOT NULL DEFAULT '',
    -- online, offline, delay… tel que le pare-feu le dit.
    status       TEXT NOT NULL DEFAULT '',
    -- Nuls : la passerelle n'est pas surveillée.
    delay_ms     REAL,
    loss_percent REAL,
    PRIMARY KEY (firewall_id, name)
);
