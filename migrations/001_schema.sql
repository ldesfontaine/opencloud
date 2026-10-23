-- Le schéma d'openCloud, en un fichier : ce qu'une base neuve reçoit au
-- premier démarrage. Les migrations suivantes partent de 002.
--
-- Conventions : horodatages en secondes Unix (sauf mention), durées en
-- millisecondes, tailles en octets, débits en octets par seconde. Les listes
-- fermées qui grandiront (genres, natures, états d'un journal) n'ont pas de
-- CHECK : SQLite ne modifie pas un CHECK sans reconstruire la table, le code
-- les valide.
--
-- Suppression : entre objets déclarés, RESTRICT, la suppression passe par son
-- plan ; d'un objet vers son propre historique, CASCADE ; des alertes et du
-- journal vers un objet, SET NULL, avec le nom recopié.

---------------------------------------------------------------------------
-- Machines et agents
---------------------------------------------------------------------------

-- L'hôte : il survit à un ré-enrôlement. La machine openCloud se reconnaît
-- à son identifiant stable, 'local'.
CREATE TABLE machines (
    id         TEXT    PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE,
    hostname   TEXT    NOT NULL DEFAULT '',
    address    TEXT    NOT NULL DEFAULT '',
    os         TEXT    NOT NULL DEFAULT '',
    arch       TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);

-- Le logiciel installé sur la machine. Ré-enrôler ajoute un agent et révoque
-- l'ancien ; révoquer pose revoked_at, la machine garde son historique.
-- root_dir : la racine des services qu'il déploiera.
CREATE TABLE agents (
    id           TEXT    PRIMARY KEY,
    machine_id   TEXT    NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    public_key   BLOB    NOT NULL CHECK (length(public_key) = 32),
    version      TEXT    NOT NULL DEFAULT '',
    root_dir     TEXT    NOT NULL DEFAULT '/srv/workspace',
    enrolled_at  INTEGER NOT NULL,
    last_seen_at INTEGER,
    revoked_at   INTEGER
);

CREATE INDEX agents_machine ON agents (machine_id);
-- Une machine a au plus un agent actif.
CREATE UNIQUE INDEX agents_one_active ON agents (machine_id) WHERE revoked_at IS NULL;

-- Le dernier rapport de l'agent sur son fichier, remplacé à chaque
-- connexion : le demandé reste sur le jeton, l'écart se voit. scope porte
-- les chemins d'un droit borné, séparés par « : ».
CREATE TABLE agent_capabilities (
    agent_id    TEXT    NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    capability  TEXT    NOT NULL,
    scope       TEXT    NOT NULL DEFAULT '',
    reported_at INTEGER NOT NULL,
    PRIMARY KEY (agent_id, capability)
);

-- Un jeton sert une fois, et seule son empreinte est stockée : une copie de
-- la base n'enrôle personne. machine_id : le jeton ré-enrôle cette machine.
-- requested_capabilities garde les droits demandés sous la forme de la ligne
-- d'installation ; agent_id dit quel agent l'a consommé.
CREATE TABLE enrollment_tokens (
    id                     TEXT    PRIMARY KEY,
    token_hash             TEXT    NOT NULL UNIQUE,
    token_prefix           TEXT    NOT NULL,
    name                   TEXT    NOT NULL,
    machine_id             TEXT    REFERENCES machines (id) ON DELETE CASCADE,
    agent_id               TEXT    REFERENCES agents (id) ON DELETE SET NULL,
    requested_capabilities TEXT    NOT NULL DEFAULT '',
    created_at             INTEGER NOT NULL,
    expires_at             INTEGER NOT NULL,
    consumed_at            INTEGER
);

-- Un agent naît d'un seul jeton : c'est par lui qu'on relit ce qui était demandé.
CREATE UNIQUE INDEX enrollment_tokens_agent ON enrollment_tokens (agent_id);

---------------------------------------------------------------------------
-- Mesures des machines
---------------------------------------------------------------------------

-- Le brut, toutes les 10 s, gardé 48 h. disk_used et disk_total font la
-- somme des volumes : l'agrégat les lit sans relire machine_disks.
CREATE TABLE machine_samples (
    machine_id        TEXT    NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    sampled_at        INTEGER NOT NULL,
    cpu_percent       REAL    NOT NULL,
    cpu_cores         INTEGER NOT NULL,
    load_1            REAL    NOT NULL,
    mem_used          INTEGER NOT NULL,
    mem_total         INTEGER NOT NULL,
    swap_used         INTEGER NOT NULL,
    swap_total        INTEGER NOT NULL,
    disk_used         INTEGER NOT NULL,
    disk_total        INTEGER NOT NULL,
    net_rx_per_second INTEGER NOT NULL,
    net_tx_per_second INTEGER NOT NULL,
    -- Un échantillon rejoué après une coupure tombe sur la clé : ignoré.
    PRIMARY KEY (machine_id, sampled_at)
) WITHOUT ROWID;

CREATE INDEX machine_samples_sampled ON machine_samples (sampled_at);

-- Un volume par ligne, attaché à son échantillon brut : il part avec lui, à
-- la purge comme au retrait de la machine.
CREATE TABLE machine_disks (
    machine_id  TEXT    NOT NULL,
    sampled_at  INTEGER NOT NULL,
    mount_point TEXT    NOT NULL,
    device      TEXT    NOT NULL,
    disk_used   INTEGER NOT NULL,
    disk_total  INTEGER NOT NULL,
    PRIMARY KEY (machine_id, sampled_at, mount_point),
    FOREIGN KEY (machine_id, sampled_at) REFERENCES machine_samples (machine_id, sampled_at) ON DELETE CASCADE
) WITHOUT ROWID;

CREATE INDEX machine_disks_sampled ON machine_disks (sampled_at);

-- Les seaux agrégés : l'heure (moyennes du brut, gardée 90 jours) et le jour
-- (moyennes des heures pondérées par sample_count, gardé un an). La clé fait
-- l'idempotence de l'agrégation : un seau se réécrit, jamais ne se double.
CREATE TABLE machine_rollups (
    machine_id            TEXT    NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    granularity           TEXT    NOT NULL CHECK (granularity IN ('hour', 'day')),
    bucket                INTEGER NOT NULL,
    avg_cpu_percent       REAL    NOT NULL,
    cpu_cores             INTEGER NOT NULL,
    avg_load_1            REAL    NOT NULL,
    avg_mem_used          INTEGER NOT NULL,
    avg_mem_total         INTEGER NOT NULL,
    avg_swap_used         INTEGER NOT NULL,
    avg_swap_total        INTEGER NOT NULL,
    avg_disk_used         INTEGER NOT NULL,
    avg_disk_total        INTEGER NOT NULL,
    avg_net_rx_per_second INTEGER NOT NULL,
    avg_net_tx_per_second INTEGER NOT NULL,
    sample_count          INTEGER NOT NULL,
    PRIMARY KEY (machine_id, granularity, bucket)
) WITHOUT ROWID;

-- La purge et l'agrégation du jour lisent une granularité par date.
CREATE INDEX machine_rollups_bucket ON machine_rollups (granularity, bucket);

-- Ce que la machine dit de son Docker : présent ou non, et sinon pourquoi
-- (no_socket, denied, down, too_old). L'absence n'est pas une erreur.
CREATE TABLE machine_engines (
    machine_id  TEXT    PRIMARY KEY REFERENCES machines (id) ON DELETE CASCADE,
    present     INTEGER NOT NULL,
    reason      TEXT    NOT NULL DEFAULT '',
    version     TEXT    NOT NULL DEFAULT '',
    api_version TEXT    NOT NULL DEFAULT '',
    checked_at  INTEGER NOT NULL
);

-- Les réseaux Docker de la machine : chaque inventaire remplace la liste,
-- un réseau détruit disparaît.
CREATE TABLE machine_networks (
    machine_id TEXT    NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    network_id TEXT    NOT NULL,
    name       TEXT    NOT NULL,
    -- bridge, host, null, macvlan… ; overlay laissé de côté.
    driver     TEXT    NOT NULL,
    internal   INTEGER NOT NULL DEFAULT 0,
    -- Le projet Compose qui l'a créé ; vide sinon.
    group_name TEXT    NOT NULL DEFAULT '',
    seen_at    INTEGER NOT NULL,
    PRIMARY KEY (machine_id, network_id)
) WITHOUT ROWID;

---------------------------------------------------------------------------
-- Annuaire : ce que l'opérateur déclare
---------------------------------------------------------------------------

-- Identifiants tirés par le serveur, jamais d'un id Docker : recréer un
-- conteneur ne casse ni sonde, ni composant, ni politique.

CREATE TABLE projects (
    id         TEXT    PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE,
    title      TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- needs_review : un service que le code a deviné, pas l'opérateur ;
-- declared_by dit qui l'a écrit.
CREATE TABLE declared_services (
    id              TEXT    PRIMARY KEY,
    -- Supprimer une machine qui porte des services passe par l'opérateur.
    machine_id      TEXT    NOT NULL REFERENCES machines (id) ON DELETE RESTRICT,
    name            TEXT    NOT NULL,
    title           TEXT    NOT NULL DEFAULT '',
    nature          TEXT    NOT NULL,
    origin          TEXT    NOT NULL,
    compose_project TEXT    NOT NULL DEFAULT '',
    public_url      TEXT    NOT NULL DEFAULT '',
    declared_by     TEXT    NOT NULL,
    needs_review    INTEGER NOT NULL DEFAULT 0 CHECK (needs_review IN (0, 1)),
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);

CREATE UNIQUE INDEX declared_services_name ON declared_services (machine_id, name);
-- Deux services sur le même projet Compose rendraient le rattachement ambigu.
CREATE UNIQUE INDEX declared_services_compose ON declared_services (machine_id, compose_project) WHERE origin = 'compose';

-- Ce qui tourne dans le service : un service Compose, un conteneur ou un
-- binaire. La politique de mise à jour vit ici, pas sur le conteneur : elle
-- survit à sa recréation (watch, hold, ignore).
CREATE TABLE service_units (
    id             TEXT    PRIMARY KEY,
    service_id     TEXT    NOT NULL REFERENCES declared_services (id) ON DELETE CASCADE,
    -- Pour un service Compose : le nom du service dans le projet (niveau 2).
    name           TEXT    NOT NULL,
    kind           TEXT    NOT NULL,
    image          TEXT    NOT NULL DEFAULT '',
    version        TEXT    NOT NULL DEFAULT '',
    digest         TEXT    NOT NULL DEFAULT '',
    container_name TEXT    NOT NULL DEFAULT '',
    systemd_unit   TEXT    NOT NULL DEFAULT '',
    ports          TEXT    NOT NULL DEFAULT '[]',
    update_policy  TEXT    NOT NULL DEFAULT 'watch',
    position       INTEGER NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX service_units_name ON service_units (service_id, name);

-- Ce que fournit un outil d'infra : sso, proxy, tls, backup-target.
CREATE TABLE service_provides (
    service_id TEXT NOT NULL REFERENCES declared_services (id) ON DELETE CASCADE,
    capability TEXT NOT NULL,
    PRIMARY KEY (service_id, capability)
) WITHOUT ROWID;

-- Un lien seul : le service reste sans le projet, le projet sans le service.
CREATE TABLE project_services (
    project_id TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    service_id TEXT NOT NULL REFERENCES declared_services (id) ON DELETE CASCADE,
    PRIMARY KEY (project_id, service_id)
) WITHOUT ROWID;

CREATE INDEX project_services_service ON project_services (service_id);

-- « Présent non déclaré, et c'est voulu » : une clé d'observation
-- (compose_project ou container_name), que la recréation du conteneur ne
-- perd pas.
CREATE TABLE ignored_keys (
    machine_id TEXT    NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    kind       TEXT    NOT NULL,
    value      TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (machine_id, kind, value)
) WITHOUT ROWID;

---------------------------------------------------------------------------
-- Conteneurs : ce que Docker montre
---------------------------------------------------------------------------

-- Le conteneur observé. L'identifiant est dérivé (machine:id Docker) : un
-- inventaire rejoué tombe sur la même fiche ; container_id est l'id Docker.
-- archived_at : le conteneur a été détruit.
CREATE TABLE containers (
    id              TEXT    PRIMARY KEY,
    machine_id      TEXT    NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    name            TEXT    NOT NULL,
    -- Le projet Compose ; vide pour un conteneur seul.
    group_name      TEXT    NOT NULL DEFAULT '',
    container_id    TEXT    NOT NULL,
    image           TEXT    NOT NULL DEFAULT '',
    image_id        TEXT    NOT NULL DEFAULT '',
    state           TEXT    NOT NULL,
    exit_code       INTEGER NOT NULL DEFAULT 0,
    -- Un arrêt sort en 137 que ce soit stop, kill ou le noyau faute de
    -- mémoire : seuls ces deux faits les distinguent.
    oom_killed      INTEGER NOT NULL DEFAULT 0,
    killed          INTEGER NOT NULL DEFAULT 0,
    -- healthy, unhealthy, starting ; vide sans HEALTHCHECK.
    health          TEXT    NOT NULL DEFAULT '',
    restart_count   INTEGER NOT NULL DEFAULT 0,
    -- Lus tels quels, jamais interrogés : JSON. Les constats d'exposition se
    -- calculent à la lecture depuis ces faits.
    ports           TEXT    NOT NULL DEFAULT '[]',
    network_mode    TEXT    NOT NULL DEFAULT '',
    privileged      INTEGER NOT NULL DEFAULT 0,
    depends_on      TEXT    NOT NULL DEFAULT '[]',
    -- Ce que Compose dit du service : de quoi fabriquer la commande à copier.
    compose_service TEXT    NOT NULL DEFAULT '',
    compose_dir     TEXT    NOT NULL DEFAULT '',
    compose_file    TEXT    NOT NULL DEFAULT '',
    -- Les labels de la marque, préfixe retiré : le niveau 1 du rattachement.
    -- label_invalid : des labels de la marque étaient là, mal formés.
    label_service   TEXT    NOT NULL DEFAULT '',
    label_unit      TEXT    NOT NULL DEFAULT '',
    label_invalid   INTEGER NOT NULL DEFAULT 0,
    -- Le rattachement à l'annuaire, recalculé à chaque rapport ; NULL :
    -- présent non déclaré. match_level n'a de sens qu'avec unit_id.
    unit_id         TEXT    REFERENCES service_units (id) ON DELETE SET NULL,
    match_level     TEXT    NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    started_at      INTEGER,
    finished_at     INTEGER,
    first_seen_at   INTEGER NOT NULL,
    last_seen_at    INTEGER NOT NULL,
    archived_at     INTEGER
);

CREATE INDEX containers_machine ON containers (machine_id);
CREATE INDEX containers_archived ON containers (archived_at);
CREATE INDEX containers_unit ON containers (unit_id);

-- Un changement d'état ou de santé, daté par Docker. snippet garde les
-- dernières lignes du journal sur un arrêt anormal, en clair.
CREATE TABLE container_events (
    id              INTEGER PRIMARY KEY,
    container_id    TEXT    NOT NULL REFERENCES containers (id) ON DELETE CASCADE,
    at              INTEGER NOT NULL,
    action          TEXT    NOT NULL,
    previous_state  TEXT    NOT NULL DEFAULT '',
    new_state       TEXT    NOT NULL DEFAULT '',
    previous_health TEXT    NOT NULL DEFAULT '',
    new_health      TEXT    NOT NULL DEFAULT '',
    exit_code       INTEGER,
    oom_killed      INTEGER NOT NULL DEFAULT 0,
    killed          INTEGER NOT NULL DEFAULT 0,
    replayed        INTEGER NOT NULL DEFAULT 0,
    snippet         TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX container_events_container ON container_events (container_id, at);
CREATE INDEX container_events_at ON container_events (at);

-- Les mesures brutes, toutes les 30 s, gardées 48 h ; pas d'agrégat.
CREATE TABLE container_samples (
    container_id TEXT    NOT NULL REFERENCES containers (id) ON DELETE CASCADE,
    sampled_at   INTEGER NOT NULL,
    cpu_percent  REAL    NOT NULL,
    mem_used     INTEGER NOT NULL,
    mem_limit    INTEGER NOT NULL,
    PRIMARY KEY (container_id, sampled_at)
) WITHOUT ROWID;

CREATE INDEX container_samples_sampled ON container_samples (sampled_at);

-- L'appartenance à un réseau, lue sur le conteneur : elle reste quand il est
-- arrêté, l'adresse se vide. Le nom est recopié pour qu'un réseau pas encore
-- listé ait quand même le sien.
CREATE TABLE container_networks (
    container_id TEXT NOT NULL REFERENCES containers (id) ON DELETE CASCADE,
    network_id   TEXT NOT NULL,
    name         TEXT NOT NULL,
    ip           TEXT NOT NULL DEFAULT '',
    -- Les alias DNS du service sur ce réseau, en JSON.
    aliases      TEXT NOT NULL DEFAULT '[]',
    PRIMARY KEY (container_id, network_id)
) WITHOUT ROWID;

CREATE INDEX container_networks_network ON container_networks (network_id);

-- Le dernier constat de l'agent sur chaque image, écrasé à chaque
-- vérification. kind est déduit par le serveur à l'écriture.
CREATE TABLE image_checks (
    machine_id    TEXT    NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    image         TEXT    NOT NULL,
    checked_at    INTEGER NOT NULL,
    outcome       TEXT    NOT NULL CHECK (outcome IN ('ok', 'local', 'unreachable', 'unauthorized', 'not_found', 'unsupported')),
    local_digest  TEXT    NOT NULL DEFAULT '',
    remote_digest TEXT    NOT NULL DEFAULT '',
    newer_tag     TEXT    NOT NULL DEFAULT '',
    newer_digest  TEXT    NOT NULL DEFAULT '',
    kind          TEXT    NOT NULL DEFAULT '' CHECK (kind IN ('', 'major', 'minor', 'patch', 'digest')),
    PRIMARY KEY (machine_id, image)
) WITHOUT ROWID;

CREATE INDEX image_checks_checked ON image_checks (checked_at);

---------------------------------------------------------------------------
-- Sondes
---------------------------------------------------------------------------

-- Vérifier de l'extérieur qu'une URL ou un port répond, depuis la machine
-- qui lance la sonde, pour un service déclaré où qu'il tourne.
CREATE TABLE probes (
    id                    TEXT    PRIMARY KEY,
    name                  TEXT    NOT NULL,
    kind                  TEXT    NOT NULL CHECK (kind IN ('http', 'tcp')),
    -- L'URL d'une sonde HTTP, « hôte:port » d'une sonde TCP.
    target                TEXT    NOT NULL,
    machine_id            TEXT    NOT NULL REFERENCES machines (id) ON DELETE RESTRICT,
    service_id            TEXT    REFERENCES declared_services (id) ON DELETE RESTRICT,
    status                TEXT    NOT NULL CHECK (status IN ('new', 'up', 'degraded', 'down', 'paused')),
    interval_seconds      INTEGER NOT NULL,
    timeout_seconds       INTEGER NOT NULL,
    -- Les seuils amortissent la bascule d'état, jamais l'uptime.
    failure_threshold     INTEGER NOT NULL,
    recovery_threshold    INTEGER NOT NULL,
    -- Propres à HTTP ; vides pour une sonde TCP.
    method                TEXT    NOT NULL DEFAULT '',
    expected_status       TEXT    NOT NULL DEFAULT '',
    expected_body         TEXT    NOT NULL DEFAULT '',
    follow_redirects      INTEGER NOT NULL DEFAULT 0,
    -- Une sonde TCP qui fait une poignée de main : c'est ce qui donne son
    -- certificat à un port chiffré qui ne parle pas HTTP.
    tls                   INTEGER NOT NULL DEFAULT 0,
    consecutive_failures  INTEGER NOT NULL DEFAULT 0,
    consecutive_successes INTEGER NOT NULL DEFAULT 0,
    last_checked_at       INTEGER,
    last_duration_ms      INTEGER NOT NULL DEFAULT 0,
    last_code             INTEGER,
    -- Un mot d'une liste fermée, traduit par le front ; jamais une phrase.
    last_reason           TEXT    NOT NULL DEFAULT '',
    created_at            INTEGER NOT NULL,
    -- En millisecondes : un essai de l'ancien réglage pris dans la seconde de
    -- l'édition ne doit pas écraser l'état remis à zéro. NULL : jamais révisée.
    revised_at            INTEGER
);

CREATE INDEX probes_machine ON probes (machine_id);
CREATE INDEX probes_service ON probes (service_id);

-- Le dernier certificat que la sonde a vu ; pas de ligne, pas de certificat.
-- Pas d'historique : « renouvelé il y a six jours » se lit sur not_before, un
-- renouvellement au changement d'empreinte. L'échéance et la confiance sont
-- deux faits, jamais l'un déduit de l'autre. ocsp : ce que l'agrafe a dit
-- (good, revoked, unknown) ; vide, la cible n'agrafe rien.
CREATE TABLE probe_certificates (
    probe_id       TEXT    PRIMARY KEY REFERENCES probes (id) ON DELETE CASCADE,
    subject        TEXT    NOT NULL DEFAULT '',
    issuer         TEXT    NOT NULL DEFAULT '',
    not_before     INTEGER,
    not_after      INTEGER,
    fingerprint    TEXT    NOT NULL,
    chain_valid    INTEGER NOT NULL DEFAULT 0,
    hostname_match INTEGER NOT NULL DEFAULT 0,
    ocsp           TEXT    NOT NULL DEFAULT ''
) WITHOUT ROWID;

-- Chaque essai, gardé sept jours. La clé porte l'idempotence : un essai
-- rejoué après une coupure ne compte pas deux fois dans l'uptime.
CREATE TABLE probe_results (
    probe_id    TEXT    NOT NULL REFERENCES probes (id) ON DELETE CASCADE,
    checked_at  INTEGER NOT NULL,
    -- Dégradé est un succès : l'hôte répond, sa chaîne est refusée.
    outcome     TEXT    NOT NULL CHECK (outcome IN ('up', 'degraded', 'down')),
    duration_ms INTEGER NOT NULL,
    code        INTEGER,
    reason      TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (probe_id, checked_at)
) WITHOUT ROWID;

CREATE INDEX probe_results_checked ON probe_results (checked_at);

-- L'agrégat d'un jour UTC, gardé un an. Des comptes, pas un pourcentage :
-- une fenêtre de trente jours additionne ses jours.
CREATE TABLE probe_days (
    probe_id    TEXT    NOT NULL REFERENCES probes (id) ON DELETE CASCADE,
    day         INTEGER NOT NULL,
    total       INTEGER NOT NULL,
    success     INTEGER NOT NULL,
    degraded    INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL,
    PRIMARY KEY (probe_id, day)
) WITHOUT ROWID;

CREATE INDEX probe_days_day ON probe_days (day);

---------------------------------------------------------------------------
-- Tâches surveillées
---------------------------------------------------------------------------

-- Le jeton de ping est chiffré (la page le réaffiche) ; un chiffré change à
-- chaque écriture, un ping cherche donc la tâche par token_lookup, le
-- SHA-256 du jeton. La machine est facultative.
CREATE TABLE heartbeats (
    id               TEXT    PRIMARY KEY,
    name             TEXT    NOT NULL,
    token_sealed     TEXT    NOT NULL,
    token_lookup     TEXT    NOT NULL UNIQUE,
    machine_id       TEXT    REFERENCES machines (id) ON DELETE RESTRICT,
    status           TEXT    NOT NULL CHECK (status IN ('new', 'on_time', 'started', 'late', 'failed', 'paused')),
    interval_seconds INTEGER NOT NULL,
    grace_seconds    INTEGER NOT NULL,
    last_ping_at     INTEGER,
    next_deadline_at INTEGER,
    run_started_at   INTEGER,
    last_exit_code   INTEGER,
    last_duration_ms INTEGER,
    created_at       INTEGER NOT NULL
);

-- Taillé pour la boucle d'échéance : les tâches surveillées, par échéance.
CREATE INDEX heartbeats_deadline ON heartbeats (next_deadline_at) WHERE next_deadline_at IS NOT NULL;

-- Chaque requête reçue, telle quelle ; purgée après sept jours.
CREATE TABLE heartbeat_pings (
    id           INTEGER PRIMARY KEY,
    heartbeat_id TEXT    NOT NULL REFERENCES heartbeats (id) ON DELETE CASCADE,
    kind         TEXT    NOT NULL CHECK (kind IN ('finish', 'start', 'exit_code')),
    exit_code    INTEGER,
    source       TEXT    NOT NULL DEFAULT '',
    method       TEXT    NOT NULL DEFAULT '',
    payload      TEXT    NOT NULL DEFAULT '',
    received_at  INTEGER NOT NULL
);

CREATE INDEX heartbeat_pings_by_heartbeat ON heartbeat_pings (heartbeat_id, received_at DESC);
CREATE INDEX heartbeat_pings_received ON heartbeat_pings (received_at);

-- L'exécution logique : ouverte par un start, close par une fin ou un
-- dépassement ; purgée après quatre-vingt-dix jours.
CREATE TABLE heartbeat_runs (
    id           INTEGER PRIMARY KEY,
    heartbeat_id TEXT    NOT NULL REFERENCES heartbeats (id) ON DELETE CASCADE,
    started_at   INTEGER,
    completed_at INTEGER,
    duration_ms  INTEGER,
    exit_code    INTEGER,
    outcome      TEXT    NOT NULL CHECK (outcome IN ('in_progress', 'success', 'failure', 'timeout')),
    payload      TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX heartbeat_runs_by_heartbeat ON heartbeat_runs (heartbeat_id, id DESC);
-- Une seule exécution ouverte par tâche : l'invariant casse bruyamment.
CREATE UNIQUE INDEX heartbeat_runs_open ON heartbeat_runs (heartbeat_id) WHERE outcome = 'in_progress';

---------------------------------------------------------------------------
-- Pages de statut
---------------------------------------------------------------------------

-- Rien ici n'est calculé : l'état d'un composant se dérive à la lecture de
-- ses objets, et un incident ouvert le remplace.

-- Une page par adresse, /statut/<slug>, rangée dans un projet au plus ;
-- un projet a une page au plus (l'unicité admet plusieurs NULL).
CREATE TABLE status_pages (
    id           TEXT    PRIMARY KEY,
    project_id   TEXT    UNIQUE REFERENCES projects (id) ON DELETE RESTRICT,
    -- L'adresse publique, sous /statut/.
    slug         TEXT    NOT NULL UNIQUE,
    title        TEXT    NOT NULL DEFAULT '',
    announcement TEXT    NOT NULL DEFAULT '',
    language     TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);

CREATE TABLE status_components (
    id         TEXT    PRIMARY KEY,
    page_id    TEXT    NOT NULL REFERENCES status_pages (id) ON DELETE CASCADE,
    -- Le nom que le visiteur lit ; le seul mot d'openCloud qui sort en public.
    name       TEXT    NOT NULL,
    -- L'ordre d'affichage sur sa page, à la main.
    position   INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

CREATE INDEX status_components_page ON status_components (page_id, position);

-- Un objet du composant : exactement une des quatre colonnes. Quatre clés
-- étrangères plutôt qu'un couple (genre, id) : rien ne pend. RESTRICT : un
-- objet ne disparaît pas d'une page publique sans le dire. L'ordre des
-- lignes est celui des membres dans le composant.
CREATE TABLE status_component_members (
    component_id TEXT NOT NULL REFERENCES status_components (id) ON DELETE CASCADE,
    machine_id   TEXT REFERENCES machines (id) ON DELETE RESTRICT,
    service_id   TEXT REFERENCES declared_services (id) ON DELETE RESTRICT,
    heartbeat_id TEXT REFERENCES heartbeats (id) ON DELETE RESTRICT,
    probe_id     TEXT REFERENCES probes (id) ON DELETE RESTRICT,
    CHECK ((machine_id IS NOT NULL) + (service_id IS NOT NULL) + (heartbeat_id IS NOT NULL) + (probe_id IS NOT NULL) = 1)
);

-- Un objet ne compte qu'une fois par composant.
CREATE UNIQUE INDEX status_members_machine ON status_component_members (component_id, machine_id) WHERE machine_id IS NOT NULL;
CREATE UNIQUE INDEX status_members_service ON status_component_members (component_id, service_id) WHERE service_id IS NOT NULL;
CREATE UNIQUE INDEX status_members_heartbeat ON status_component_members (component_id, heartbeat_id) WHERE heartbeat_id IS NOT NULL;
CREATE UNIQUE INDEX status_members_probe ON status_component_members (component_id, probe_id) WHERE probe_id IS NOT NULL;

-- Ce que l'opérateur dit au public. Une maintenance est un incident d'impact
-- « maintenance » avec une fenêtre : la boucle l'ouvre à starts_at et le
-- résout à ends_at. Elle ne tait aucune alerte : c'est mutes qui le fait.
-- Purgé un an après sa résolution.
CREATE TABLE incidents (
    id          TEXT    PRIMARY KEY,
    title       TEXT    NOT NULL,
    -- Ce que les composants touchés affichent tant que l'incident est ouvert.
    impact      TEXT    NOT NULL CHECK (impact IN ('degraded', 'down', 'maintenance')),
    status      TEXT    NOT NULL CHECK (status IN ('scheduled', 'investigating', 'identified', 'monitoring', 'in_progress', 'resolved')),
    -- La fenêtre d'une maintenance ; NULL pour un incident.
    starts_at   INTEGER,
    ends_at     INTEGER,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    resolved_at INTEGER
);

CREATE INDEX incidents_open ON incidents (status) WHERE status != 'resolved';
CREATE INDEX incidents_resolved ON incidents (resolved_at) WHERE resolved_at IS NOT NULL;

CREATE TABLE incident_components (
    incident_id  TEXT NOT NULL REFERENCES incidents (id) ON DELETE CASCADE,
    component_id TEXT NOT NULL REFERENCES status_components (id) ON DELETE CASCADE,
    PRIMARY KEY (incident_id, component_id)
) WITHOUT ROWID;

CREATE INDEX incident_components_component ON incident_components (component_id);

-- Le fil d'un incident : chaque entrée porte le statut qu'elle a donné ; le
-- message est vide quand c'est la boucle qui parle.
CREATE TABLE incident_updates (
    id          INTEGER PRIMARY KEY,
    incident_id TEXT    NOT NULL REFERENCES incidents (id) ON DELETE CASCADE,
    status      TEXT    NOT NULL,
    message     TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);

CREATE INDEX incident_updates_incident ON incident_updates (incident_id, created_at);

---------------------------------------------------------------------------
-- Alertes
---------------------------------------------------------------------------

-- Un fait par objet, dédupliqué par (type, genre, objet), aggravé ou résolu
-- par le composant qui l'a constaté, acquitté par l'opérateur. Taire se juge
-- à chaque envoi, par les fenêtres de mutes.
CREATE TABLE alerts (
    id              INTEGER PRIMARY KEY,
    kind            TEXT    NOT NULL,
    severity        TEXT    NOT NULL CHECK (severity IN ('attention', 'danger')),
    status          TEXT    NOT NULL CHECK (status IN ('open', 'resolved')),
    -- machine, container, probe, heartbeat, volume : la liste vit dans le code.
    object_kind     TEXT    NOT NULL,
    object_id       TEXT    NOT NULL,
    -- Recopié : un objet supprimé reste lisible dans l'historique.
    object_name     TEXT    NOT NULL,
    -- L'objet en clé étrangère, mise à NULL quand il disparaît : c'est ce qui
    -- dit au balayage qu'une alerte ouverte n'a plus d'objet. Un volume ne
    -- porte que sa machine. service_id dit à quel service déclaré l'alerte
    -- appartient ; il ne fait pas l'objet.
    machine_id      TEXT    REFERENCES machines (id) ON DELETE SET NULL,
    service_id      TEXT    REFERENCES declared_services (id) ON DELETE SET NULL,
    container_id    TEXT    REFERENCES containers (id) ON DELETE SET NULL,
    probe_id        TEXT    REFERENCES probes (id) ON DELETE SET NULL,
    heartbeat_id    TEXT    REFERENCES heartbeats (id) ON DELETE SET NULL,
    -- Les chiffres du fait, en JSON ; le navigateur et le canal en font une phrase.
    details         TEXT    NOT NULL DEFAULT '{}',
    opened_at       INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    resolved_at     INTEGER,
    acknowledged_at INTEGER,
    -- Depuis quand l'état est redevenu bon : l'alerte ne se résout qu'après
    -- un délai de calme, un retour du problème efface la date.
    clear_since     INTEGER
);

-- Une seule alerte ouverte par clé.
CREATE UNIQUE INDEX alerts_open_key ON alerts (kind, object_kind, object_id) WHERE status = 'open';
CREATE INDEX alerts_status ON alerts (status, severity, opened_at);
CREATE INDEX alerts_resolved ON alerts (resolved_at) WHERE resolved_at IS NOT NULL;
CREATE INDEX alerts_machine ON alerts (machine_id) WHERE machine_id IS NOT NULL;
CREATE INDEX alerts_service ON alerts (service_id) WHERE service_id IS NOT NULL;

-- Un webhook, un e-mail ou un salon Matrix. url et secret sont chiffrés :
-- il faut les rejouer au récepteur, un haché ne servirait à rien.
-- destination porte ce que l'URL ne dit pas (destinataires, salon). Le
-- format n'a pas de CHECK : la liste vit dans internal/alert.
CREATE TABLE alert_channels (
    id             INTEGER PRIMARY KEY,
    name           TEXT    NOT NULL,
    url            TEXT    NOT NULL,
    format         TEXT    NOT NULL,
    destination    TEXT    NOT NULL DEFAULT '',
    secret         TEXT    NOT NULL DEFAULT '',
    min_severity   TEXT    NOT NULL CHECK (min_severity IN ('attention', 'danger')),
    notify_resolve INTEGER NOT NULL DEFAULT 1,
    enabled        INTEGER NOT NULL DEFAULT 1,
    created_at     INTEGER NOT NULL
);

-- Une livraison est réservée avant d'être envoyée : une coupure entre les
-- deux laisse une ligne en attente que le démarrage rejoue, et l'unicité
-- empêche d'envoyer deux fois. held : retenue par la limite du canal ;
-- muted : retenue par une fenêtre pour taire.
CREATE TABLE alert_deliveries (
    id              INTEGER PRIMARY KEY,
    alert_id        INTEGER NOT NULL REFERENCES alerts (id) ON DELETE CASCADE,
    channel_id      INTEGER NOT NULL REFERENCES alert_channels (id) ON DELETE CASCADE,
    event           TEXT    NOT NULL CHECK (event IN ('opened', 'aggravated', 'resolved')),
    status          TEXT    NOT NULL CHECK (status IN ('pending', 'delivered', 'failed', 'held', 'muted')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    -- Le motif du dernier échec, un mot d'une liste fermée, et le code reçu.
    reason          TEXT    NOT NULL DEFAULT '',
    code            INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    -- Le prochain essai d'une livraison en échec ; NULL : plus d'essai.
    next_attempt_at INTEGER,
    UNIQUE (alert_id, channel_id, event)
);

CREATE INDEX alert_deliveries_pending ON alert_deliveries (status) WHERE status = 'pending';
CREATE INDEX alert_deliveries_due ON alert_deliveries (next_attempt_at) WHERE next_attempt_at IS NOT NULL;

-- Taire : une fenêtre sur un objet au plus, un type d'alerte, les deux, ou
-- tout. La fenêtre part avec son objet.
CREATE TABLE mutes (
    id           INTEGER PRIMARY KEY,
    -- Le type d'alerte tu ; NULL : tous.
    alert_kind   TEXT,
    -- Une machine tait aussi ce qui vit sur elle, un service déclaré ses
    -- conteneurs et ses sondes.
    machine_id   TEXT    REFERENCES machines (id) ON DELETE CASCADE,
    service_id   TEXT    REFERENCES declared_services (id) ON DELETE CASCADE,
    container_id TEXT    REFERENCES containers (id) ON DELETE CASCADE,
    probe_id     TEXT    REFERENCES probes (id) ON DELETE CASCADE,
    heartbeat_id TEXT    REFERENCES heartbeats (id) ON DELETE CASCADE,
    reason       TEXT    NOT NULL DEFAULT '',
    -- La fenêtre : immédiate (starts_at à la création) ou planifiée.
    starts_at    INTEGER NOT NULL,
    ends_at      INTEGER NOT NULL,
    -- L'acteur du journal d'audit, « genre:nom ».
    created_by   TEXT    NOT NULL DEFAULT '',
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

---------------------------------------------------------------------------
-- Journaux : apply et audit
---------------------------------------------------------------------------

-- Pas de clé étrangère vers les objets touchés : les journaux leur
-- survivent. Ni fichier ni secret : un jeton n'y entre jamais.

-- Un passage d'apply. Une ligne restée confirmed au démarrage est un arrêt
-- brutal : elle passe failed.
CREATE TABLE applies (
    id           TEXT    PRIMARY KEY,                -- apl_ + 16 hex
    at           INTEGER NOT NULL,                   -- confirmé : début de l'écriture
    finished_at  INTEGER,
    actor_kind   TEXT    NOT NULL,                   -- cli | operator
    actor_name   TEXT    NOT NULL DEFAULT '',
    file_hash    TEXT    NOT NULL,                   -- sha256:<64 hex>
    file_name    TEXT    NOT NULL DEFAULT '',        -- dit par la CLI, pour l'opérateur
    prune        INTEGER NOT NULL CHECK (prune IN (0, 1)),
    plan_hash    TEXT    NOT NULL,
    plan         TEXT    NOT NULL,                   -- JSON des changements, dans l'ordre d'écriture
    state        TEXT    NOT NULL,                   -- confirmed | applied | failed
    created      INTEGER NOT NULL DEFAULT 0,
    updated      INTEGER NOT NULL DEFAULT 0,
    deleted      INTEGER NOT NULL DEFAULT 0,
    unchanged    INTEGER NOT NULL DEFAULT 0,
    done         INTEGER NOT NULL DEFAULT 0,
    error        TEXT    NOT NULL DEFAULT '',        -- clé du catalogue
    error_object TEXT    NOT NULL DEFAULT ''         -- genre/nom du changement en échec
);

CREATE INDEX applies_at ON applies (at);

-- Qui a fait quoi, quand, sur quel objet déclaré ou quelle autorité : une
-- ligne par geste réussi. Les écritures d'observation n'y vont pas. Ajout
-- seul ; une ligne ne part que par la purge à un an.
CREATE TABLE audit_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT, -- jamais réutilisé : sert de curseur
    at          INTEGER NOT NULL,
    actor_kind  TEXT    NOT NULL,                  -- operator | mcp | cli | rule | agent | unknown
    actor_name  TEXT    NOT NULL DEFAULT '',
    action      TEXT    NOT NULL,                  -- <object_kind>.<verbe>
    object_kind TEXT    NOT NULL,
    object_id   TEXT    NOT NULL DEFAULT '',
    object_name TEXT    NOT NULL DEFAULT '',       -- recopié au moment du geste
    details     TEXT    NOT NULL DEFAULT '{}',     -- JSON plat de textes, jamais un secret
    apply_id    TEXT    REFERENCES applies (id) ON DELETE SET NULL
);

CREATE INDEX audit_events_at ON audit_events (at);
CREATE INDEX audit_events_object ON audit_events (object_kind, object_id, id);
CREATE INDEX audit_events_apply ON audit_events (apply_id) WHERE apply_id IS NOT NULL;

-- Le déclencheur refuse toute modification, hors la mise à nul du lien que
-- fait la clé étrangère quand le passage d'apply est purgé.
CREATE TRIGGER audit_events_append_only
BEFORE UPDATE ON audit_events
WHEN NEW.apply_id IS NOT NULL
    OR NEW.id IS NOT OLD.id
    OR NEW.at IS NOT OLD.at
    OR NEW.actor_kind IS NOT OLD.actor_kind
    OR NEW.actor_name IS NOT OLD.actor_name
    OR NEW.action IS NOT OLD.action
    OR NEW.object_kind IS NOT OLD.object_kind
    OR NEW.object_id IS NOT OLD.object_id
    OR NEW.object_name IS NOT OLD.object_name
    OR NEW.details IS NOT OLD.details
BEGIN
    SELECT RAISE(ABORT, 'audit_events is append-only');
END;

---------------------------------------------------------------------------
-- Accès et réglages
---------------------------------------------------------------------------

-- L'accès de l'opérateur, une ligne. Le mot de passe n'est stocké qu'en
-- empreinte bcrypt ; en mode forward, seul le nom de l'en-tête est gardé,
-- l'identité arrive avec chaque requête.
CREATE TABLE auth_settings (
    id             INTEGER PRIMARY KEY CHECK (id = 1),
    mode           TEXT    NOT NULL CHECK (mode IN ('none', 'basic', 'forward')),
    username       TEXT    NOT NULL DEFAULT '',
    password_hash  TEXT    NOT NULL DEFAULT '',
    forward_header TEXT    NOT NULL DEFAULT '',
    enabled_at     INTEGER,
    updated_at     INTEGER NOT NULL
);

-- Ce que l'opérateur change dans l'interface, une ligne. Vide ou zéro : rien
-- n'est réglé, chaque composant prend sa valeur par défaut. mail_password est
-- chiffré : il faut le rejouer au relais.
CREATE TABLE settings (
    id                     INTEGER PRIMARY KEY CHECK (id = 1),
    language               TEXT    NOT NULL DEFAULT '',
    disk_attention_percent INTEGER NOT NULL DEFAULT 0,
    disk_danger_percent    INTEGER NOT NULL DEFAULT 0,
    mail_host              TEXT    NOT NULL DEFAULT '',
    mail_port              INTEGER NOT NULL DEFAULT 0,
    mail_tls               TEXT    NOT NULL DEFAULT '',
    mail_username          TEXT    NOT NULL DEFAULT '',
    mail_password          TEXT    NOT NULL DEFAULT '',
    mail_from              TEXT    NOT NULL DEFAULT '',
    lifecycle_disabled     INTEGER NOT NULL DEFAULT 0,
    updated_at             INTEGER NOT NULL
);

-- L'accès d'un agent IA par MCP. Codes, jetons et secret ne sont stockés
-- que hachés : une copie de la base n'ouvre rien.

-- Une seule ligne, tant que MCP est activé. redirect_uris : un par ligne,
-- en plus de la boucle locale, toujours acceptée.
CREATE TABLE mcp_clients (
    id            TEXT    PRIMARY KEY,
    secret_hash   TEXT    NOT NULL,
    secret_prefix TEXT    NOT NULL,
    redirect_uris TEXT    NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL
);

-- Un code sert une fois, dix minutes au plus : la consommation est un UPDATE
-- conditionnel, un seul échange concurrent gagne. scope : la portée que
-- l'opérateur a consentie.
CREATE TABLE mcp_codes (
    code_hash    TEXT    PRIMARY KEY,
    client_id    TEXT    NOT NULL,
    redirect_uri TEXT    NOT NULL,
    challenge    TEXT    NOT NULL,
    scope        TEXT    NOT NULL DEFAULT 'read' CHECK (scope IN ('read', 'write')),
    expires_at   INTEGER NOT NULL,
    used         INTEGER NOT NULL DEFAULT 0,
    created_at   INTEGER NOT NULL
);

-- access (1 h), refresh (30 j), api. Un rafraîchissement rejoué révoque
-- toute la famille ; un jeton révoqué reste jusqu'à son échéance, c'est ce
-- qui trahit le rejeu.
CREATE TABLE mcp_tokens (
    id           TEXT    PRIMARY KEY,
    token_hash   TEXT    NOT NULL UNIQUE,
    token_prefix TEXT    NOT NULL,
    kind         TEXT    NOT NULL CHECK (kind IN ('access', 'refresh', 'api')),
    scope        TEXT    NOT NULL DEFAULT 'write' CHECK (scope IN ('read', 'write')),
    name         TEXT    NOT NULL DEFAULT '',
    family_id    TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    expires_at   INTEGER,
    last_used_at INTEGER,
    revoked_at   INTEGER
);

CREATE INDEX mcp_tokens_family ON mcp_tokens (family_id) WHERE family_id != '';
CREATE INDEX mcp_tokens_expires ON mcp_tokens (expires_at) WHERE expires_at IS NOT NULL;
