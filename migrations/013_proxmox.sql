-- L'hôte Proxmox qu'une machine porte, et ce que son API en dit : les VM et
-- conteneurs, les stockages, la dernière sauvegarde connue de chaque VM. Le
-- jeton est facultatif et en lecture seule (PVEAuditor) ; une machine sans
-- ligne ici n'a rien changé. Les trois tables sont un reflet du dernier relevé
-- réussi, remplacé en entier à chaque lecture, sans historique.

CREATE TABLE machine_proxmox (
    machine_id   TEXT    PRIMARY KEY REFERENCES machines (id) ON DELETE CASCADE,
    -- https://hôte:8006, sans chemin.
    api_url      TEXT    NOT NULL,
    -- utilisateur@domaine!nom du jeton ; le secret, lui, est scellé.
    token_id     TEXT    NOT NULL,
    token_secret TEXT    NOT NULL,
    -- Empreinte SHA-256 du certificat, en hexadécimal ; vide : le magasin du
    -- système juge (un Proxmox auto-signé demande l'empreinte).
    pin          TEXT    NOT NULL DEFAULT '',
    -- Le nom du nœud de la machine ; vide quand l'API ne permet pas de le dire.
    node         TEXT    NOT NULL DEFAULT '',
    version      TEXT    NOT NULL DEFAULT '',
    -- Quand le jeton a répondu à la déclaration.
    verified_at  INTEGER NOT NULL,
    -- Le dernier relevé réussi ; nul tant qu'aucun ne l'a été.
    last_read_at INTEGER,
    -- La clé du refus du dernier relevé qui a échoué, vide s'il a réussi.
    last_error   TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL
);

CREATE TABLE proxmox_guests (
    machine_id        TEXT    NOT NULL REFERENCES machine_proxmox (machine_id) ON DELETE CASCADE,
    vmid              INTEGER NOT NULL,
    kind              TEXT    NOT NULL CHECK (kind IN ('qemu', 'lxc')),
    name              TEXT    NOT NULL DEFAULT '',
    status            TEXT    NOT NULL DEFAULT '',
    template          INTEGER NOT NULL DEFAULT 0 CHECK (template IN (0, 1)),
    -- Ce qui est alloué, pas ce qui est consommé.
    cpus              REAL    NOT NULL DEFAULT 0,
    maxmem            INTEGER NOT NULL DEFAULT 0,
    maxdisk           INTEGER NOT NULL DEFAULT 0,
    node              TEXT    NOT NULL,
    -- La machine enrôlée qui porte le même nom d'hôte ; elle reste si l'hôte
    -- part, le lien non.
    linked_machine_id TEXT    REFERENCES machines (id) ON DELETE SET NULL,
    -- La plus récente sauvegarde que les stockages montrent pour cette VM ;
    -- nul : aucune.
    last_backup_at    INTEGER,
    last_backup_bytes INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (machine_id, vmid)
);

CREATE INDEX proxmox_guests_by_linked_machine ON proxmox_guests (linked_machine_id);

CREATE TABLE proxmox_storages (
    machine_id TEXT    NOT NULL REFERENCES machine_proxmox (machine_id) ON DELETE CASCADE,
    -- Un stockage local existe une fois par nœud d'une grappe.
    node       TEXT    NOT NULL,
    storage    TEXT    NOT NULL,
    type       TEXT    NOT NULL DEFAULT '',
    total      INTEGER NOT NULL DEFAULT 0,
    used       INTEGER NOT NULL DEFAULT 0,
    avail      INTEGER NOT NULL DEFAULT 0,
    -- Les contenus que le stockage accepte, séparés par « , » (backup, iso…).
    content    TEXT    NOT NULL DEFAULT '',
    active     INTEGER NOT NULL DEFAULT 0 CHECK (active IN (0, 1)),
    PRIMARY KEY (machine_id, node, storage)
);
