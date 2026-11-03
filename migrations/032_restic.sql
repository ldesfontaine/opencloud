-- Les sauvegardes restic d'openCloud, et les tâches vzdump que Proxmox mène
-- de lui-même.
--
-- Une cible est une machine dont l'agent lance rest-server en écriture seule
-- (droit restic.target). L'opérateur y demande une adresse annoncée, un port
-- et le dossier des dépôts ; l'agent rapporte ce qui tourne (état, version,
-- certificat public, disque, plancher de rétention) dans la section restic de
-- son signal. Rien de secret ici : ni mot de passe, ni clé privée.
CREATE TABLE restic_targets (
    machine_id     TEXT    PRIMARY KEY REFERENCES machines (id) ON DELETE CASCADE,
    -- Ce que l'opérateur a demandé.
    requested_host TEXT    NOT NULL,
    requested_port INTEGER NOT NULL CHECK (requested_port BETWEEN 1 AND 65535),
    requested_dir  TEXT    NOT NULL,
    requested_at   INTEGER NOT NULL,
    -- L'ordre restic.target.setup qui l'a demandé.
    setup_run_id   TEXT    NOT NULL DEFAULT '',
    -- Ce que l'agent rapporte ; vide tant qu'il n'a rien dit.
    state          TEXT    NOT NULL DEFAULT '' CHECK (state IN ('', 'running', 'stopped', 'error')),
    reason         TEXT    NOT NULL DEFAULT '',
    version        TEXT    NOT NULL DEFAULT '',
    host           TEXT    NOT NULL DEFAULT '',
    port           INTEGER NOT NULL DEFAULT 0 CHECK (port BETWEEN 0 AND 65535),
    -- Le certificat public que les sources épinglent, et son empreinte SHA-256.
    certificate    TEXT    NOT NULL DEFAULT '',
    fingerprint    TEXT    NOT NULL DEFAULT '',
    repo_bytes     INTEGER NOT NULL DEFAULT 0 CHECK (repo_bytes >= 0),
    disk_total     INTEGER NOT NULL DEFAULT 0 CHECK (disk_total >= 0),
    disk_free      INTEGER NOT NULL DEFAULT 0 CHECK (disk_free >= 0),
    -- Le plancher de rétention que la cible tient et rapporte ; floor_known 0
    -- tant qu'elle ne l'a pas dit.
    floor_known    INTEGER NOT NULL DEFAULT 0 CHECK (floor_known IN (0, 1)),
    floor_daily    INTEGER NOT NULL DEFAULT 0 CHECK (floor_daily >= 0),
    floor_weekly   INTEGER NOT NULL DEFAULT 0 CHECK (floor_weekly >= 0),
    floor_monthly  INTEGER NOT NULL DEFAULT 0 CHECK (floor_monthly >= 0),
    reported_at    INTEGER
);

-- Une sauvegarde restic : un invité Proxmox (vzdump en flux) ou le dossier
-- d'un service déclaré, d'une machine source vers une cible, chaque nuit. Le
-- dépôt est l'utilisateur de la source sur la cible : toutes les sauvegardes
-- d'une source vers une même cible le partagent (restic_links). La rétention
-- est appliquée par la cible après la nuit, sur le dépôt entier : la plus
-- large des sauvegardes qui le partagent.
CREATE TABLE restic_backups (
    id                TEXT    PRIMARY KEY,
    name              TEXT    NOT NULL,
    source_machine_id TEXT    NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    scope             TEXT    NOT NULL CHECK (scope IN ('vm', 'service')),
    vmid              INTEGER NOT NULL DEFAULT 0,
    guest_type        TEXT    NOT NULL DEFAULT '' CHECK (guest_type IN ('', 'qemu', 'lxc')),
    service_id        TEXT    REFERENCES declared_services (id) ON DELETE CASCADE,
    target_machine_id TEXT    NOT NULL REFERENCES restic_targets (machine_id) ON DELETE CASCADE,
    repo_user         TEXT    NOT NULL,
    schedule          TEXT    NOT NULL DEFAULT 'daily' CHECK (schedule IN ('daily')),
    keep_last         INTEGER NOT NULL DEFAULT 0 CHECK (keep_last >= 0),
    keep_daily        INTEGER NOT NULL DEFAULT 0 CHECK (keep_daily >= 0),
    keep_weekly       INTEGER NOT NULL DEFAULT 0 CHECK (keep_weekly >= 0),
    keep_monthly      INTEGER NOT NULL DEFAULT 0 CHECK (keep_monthly >= 0),
    keep_yearly       INTEGER NOT NULL DEFAULT 0 CHECK (keep_yearly >= 0),
    created_at        INTEGER NOT NULL,
    -- La dernière exécution : l'ordre, son état, et ce que la source a dit du
    -- chemin pris. Des mots fermés, jamais le texte d'un agent.
    last_attempt_at   INTEGER,
    last_run_id       TEXT    NOT NULL DEFAULT '',
    last_state        TEXT    NOT NULL DEFAULT '' CHECK (last_state IN ('', 'sent', 'running', 'succeeded', 'failed', 'refused')),
    last_reason       TEXT    NOT NULL DEFAULT '',
    last_path         TEXT    NOT NULL DEFAULT '' CHECK (last_path IN ('', 'direct', 'relay')),
    last_path_reason  TEXT    NOT NULL DEFAULT '',
    last_target_host  TEXT    NOT NULL DEFAULT '',
    last_target_port  INTEGER NOT NULL DEFAULT 0 CHECK (last_target_port BETWEEN 0 AND 65535),
    last_snapshot     TEXT    NOT NULL DEFAULT '',
    last_bytes_added  INTEGER NOT NULL DEFAULT 0 CHECK (last_bytes_added >= 0),
    last_duration     INTEGER NOT NULL DEFAULT 0 CHECK (last_duration >= 0),
    last_finished_at  INTEGER,
    last_success_at   INTEGER,
    CHECK ((scope = 'vm' AND vmid >= 100 AND guest_type <> '' AND service_id IS NULL)
        OR (scope = 'service' AND vmid = 0 AND guest_type = '' AND service_id IS NOT NULL))
);

CREATE UNIQUE INDEX restic_backups_name ON restic_backups (name);
CREATE UNIQUE INDEX restic_backups_guest ON restic_backups (source_machine_id, vmid, target_machine_id) WHERE scope = 'vm';
CREATE UNIQUE INDEX restic_backups_service ON restic_backups (service_id, target_machine_id) WHERE scope = 'service';
CREATE INDEX restic_backups_by_run ON restic_backups (last_run_id);

-- L'historique des exécutions finies, borné à 60 par sauvegarde
-- (store.MaxResticRunsPerBackup) : le chemin pris, sa raison, ce qui a été écrit.
CREATE TABLE restic_runs (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    backup_id        TEXT    NOT NULL REFERENCES restic_backups (id) ON DELETE CASCADE,
    run_id           TEXT    NOT NULL,
    finished_at      INTEGER NOT NULL,
    state            TEXT    NOT NULL CHECK (state IN ('succeeded', 'failed', 'refused')),
    reason           TEXT    NOT NULL DEFAULT '',
    path             TEXT    NOT NULL DEFAULT '' CHECK (path IN ('', 'direct', 'relay')),
    path_reason      TEXT    NOT NULL DEFAULT '',
    bytes_added      INTEGER NOT NULL DEFAULT 0 CHECK (bytes_added >= 0),
    duration_seconds INTEGER NOT NULL DEFAULT 0 CHECK (duration_seconds >= 0)
);

CREATE INDEX restic_runs_by_backup ON restic_runs (backup_id, id DESC);

-- Le lien d'une source vers une cible : un dépôt, et ses deux mots de passe,
-- tirés par le serveur (crypto/rand) et scellés par sa clé, liés à la table,
-- à la colonne et à la ligne (store.secretColumns). Ils partent à la source
-- (restic.provision) et à la cible (restic.user.add : le REST seul ;
-- restic.retain : celui du dépôt), jamais ailleurs. La dernière sauvegarde
-- du lien partie, la cible le ferme et le lien est retiré (retired_at) : ses
-- mots de passe restent scellés, pour rouvrir le même dépôt si le couple est
-- redéclaré. La dernière rétention : l'ordre, son état, une raison fermée.
CREATE TABLE restic_links (
    id                TEXT    PRIMARY KEY,
    source_machine_id TEXT    NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    target_machine_id TEXT    NOT NULL REFERENCES restic_targets (machine_id) ON DELETE CASCADE,
    rest_password     TEXT    NOT NULL DEFAULT '',
    repo_password     TEXT    NOT NULL DEFAULT '',
    created_at        INTEGER NOT NULL,
    retired_at        INTEGER,
    retain_at         INTEGER,
    retain_run_id     TEXT    NOT NULL DEFAULT '',
    retain_state      TEXT    NOT NULL DEFAULT '' CHECK (retain_state IN ('', 'sent', 'succeeded', 'failed', 'refused')),
    retain_reason     TEXT    NOT NULL DEFAULT '',
    UNIQUE (source_machine_id, target_machine_id)
);

-- Les tâches de sauvegarde que Proxmox mène lui-même (/cluster/backup), telles
-- que le dernier relevé les a lues : openCloud les montre, ne les lance pas.
-- La sélection est « tous les invités » (moins excluded) ou la liste vmids ;
-- une sélection par pool n'est pas lue (ni all ni vmids : elle ne couvre rien) ;
-- la rétention vient de prune-backups, inconnue sans lui (retention_known 0 :
-- la tâche suit alors la règle du stockage).
CREATE TABLE proxmox_backup_jobs (
    machine_id      TEXT    NOT NULL REFERENCES machine_proxmox (machine_id) ON DELETE CASCADE,
    job_id          TEXT    NOT NULL,
    schedule        TEXT    NOT NULL DEFAULT '',
    next_run_at     INTEGER,
    storage         TEXT    NOT NULL DEFAULT '',
    enabled         INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    -- Une tâche peut se borner aux invités d'un nœud ; vide : toute la grappe.
    node            TEXT    NOT NULL DEFAULT '',
    all_guests      INTEGER NOT NULL CHECK (all_guests IN (0, 1)),
    -- Des numéros séparés par « , ».
    vmids           TEXT    NOT NULL DEFAULT '',
    excluded        TEXT    NOT NULL DEFAULT '',
    retention_known INTEGER NOT NULL DEFAULT 0 CHECK (retention_known IN (0, 1)),
    keep_all        INTEGER NOT NULL DEFAULT 0 CHECK (keep_all IN (0, 1)),
    keep_last       INTEGER NOT NULL DEFAULT 0 CHECK (keep_last >= 0),
    keep_daily      INTEGER NOT NULL DEFAULT 0 CHECK (keep_daily >= 0),
    keep_weekly     INTEGER NOT NULL DEFAULT 0 CHECK (keep_weekly >= 0),
    keep_monthly    INTEGER NOT NULL DEFAULT 0 CHECK (keep_monthly >= 0),
    keep_yearly     INTEGER NOT NULL DEFAULT 0 CHECK (keep_yearly >= 0),
    comment         TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (machine_id, job_id)
);

-- Le jeton lit-il les tâches de sauvegarde ? /cluster/backup demande Sys.Audit
-- sur « / » ; sans lui, 403 : « inconnu », jamais « aucune tâche ». 0 est la
-- valeur sûre jusqu'au prochain relevé.
ALTER TABLE machine_proxmox ADD COLUMN backup_jobs_unreadable INTEGER NOT NULL DEFAULT 0 CHECK (backup_jobs_unreadable IN (0, 1));
