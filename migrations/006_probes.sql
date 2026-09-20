-- Les sondes : vérifier de l'extérieur qu'une URL ou qu'un port répond.
-- La machine qui sonde porte la sonde et l'exécute ; la machine openCloud
-- par défaut, qui est son propre agent. Horodatages en secondes depuis
-- l'époque Unix, durées en millisecondes.

CREATE TABLE probes (
    id                    TEXT PRIMARY KEY,
    name                  TEXT NOT NULL,
    kind                  TEXT NOT NULL CHECK (kind IN ('http', 'tcp')),
    -- L'URL d'une sonde HTTP, « hôte:port » d'une sonde TCP.
    target                TEXT NOT NULL,
    -- La machine retirée emporte ses sondes : plus personne ne les exécute.
    machine_id            TEXT NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    -- Rattachement facultatif ; la fiche du service effacée, la sonde reste.
    service_id            TEXT REFERENCES services (id) ON DELETE SET NULL,
    status                TEXT NOT NULL CHECK (status IN ('new', 'up', 'degraded', 'down', 'paused')),
    interval_seconds      INTEGER NOT NULL,
    timeout_seconds       INTEGER NOT NULL,
    -- Les seuils amortissent la bascule d'état, jamais l'uptime.
    failure_threshold     INTEGER NOT NULL,
    recovery_threshold    INTEGER NOT NULL,
    -- Propres à HTTP ; vides pour une sonde TCP.
    method                TEXT NOT NULL DEFAULT '',
    expected_status       TEXT NOT NULL DEFAULT '',
    expected_body         TEXT NOT NULL DEFAULT '',
    follow_redirects      INTEGER NOT NULL DEFAULT 0,
    consecutive_failures  INTEGER NOT NULL DEFAULT 0,
    consecutive_successes INTEGER NOT NULL DEFAULT 0,
    last_checked_at       INTEGER,
    last_duration_ms      INTEGER NOT NULL DEFAULT 0,
    last_code             INTEGER,
    -- Un mot d'une liste fermée, traduit par le front ; jamais une phrase.
    last_reason           TEXT NOT NULL DEFAULT '',
    -- Ce que la sonde a vu de la chaîne présentée, sans la juger : la
    -- fonctionnalité certificats lira ces colonnes.
    cert_subject          TEXT NOT NULL DEFAULT '',
    cert_issuer           TEXT NOT NULL DEFAULT '',
    cert_not_before       INTEGER,
    cert_not_after        INTEGER,
    cert_fingerprint      TEXT NOT NULL DEFAULT '',
    created_at            INTEGER NOT NULL
);

CREATE INDEX probes_machine ON probes (machine_id);
CREATE INDEX probes_service ON probes (service_id);

-- Chaque essai, tel que la machine l'a rapporté ; purgé après sept jours.
-- La clé primaire porte l'idempotence : un essai rejoué après une coupure
-- tombe sur la même ligne et ne compte pas deux fois dans l'uptime.
CREATE TABLE probe_results (
    probe_id    TEXT NOT NULL REFERENCES probes (id) ON DELETE CASCADE,
    checked_at  INTEGER NOT NULL,
    -- Dégradé est un succès : l'hôte répond, sa chaîne est refusée.
    outcome     TEXT NOT NULL CHECK (outcome IN ('up', 'degraded', 'down')),
    duration_ms INTEGER NOT NULL,
    code        INTEGER,
    reason      TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (probe_id, checked_at)
) WITHOUT ROWID;

CREATE INDEX probe_results_checked ON probe_results (checked_at);

-- L'agrégat d'un jour UTC, réécrit tant que le brut le couvre entièrement ;
-- gardé un an. Des comptes, pas un pourcentage : c'est ce qui permet à une
-- fenêtre de trente jours d'additionner ses jours.
CREATE TABLE probe_days (
    probe_id    TEXT NOT NULL REFERENCES probes (id) ON DELETE CASCADE,
    day         INTEGER NOT NULL,
    total       INTEGER NOT NULL,
    success     INTEGER NOT NULL,
    degraded    INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL,
    PRIMARY KEY (probe_id, day)
) WITHOUT ROWID;

CREATE INDEX probe_days_day ON probe_days (day);
