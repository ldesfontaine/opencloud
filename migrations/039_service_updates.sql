-- La montée de version d'un service, avec son retour arrière automatique.
--
-- service_updates garde chaque montée demandée : la version d'avant, celle
-- d'après, et ce qu'elle est devenue. Le serveur y retrouve la version à
-- remettre dans l'annuaire quand l'agent revient seul à l'ancienne, et
-- l'écran montre « 1.3.0 a échoué le ... ». Une ligne porte le run de l'ordre
-- docker.update, sans clé étrangère : la purge des runs ne l'emporte pas.
CREATE TABLE service_updates (
    run_id       TEXT    PRIMARY KEY,
    service_id   TEXT    REFERENCES declared_services (id) ON DELETE CASCADE,
    machine_id   TEXT    NOT NULL,
    unit_id      TEXT    NOT NULL,
    unit_name    TEXT    NOT NULL,
    from_version TEXT    NOT NULL,
    to_version   TEXT    NOT NULL,
    -- pending : l'agent juge encore ; confirmed : la nouvelle version tient ;
    -- rolled_back : l'agent est revenu à la version d'avant ; failed : il n'a
    -- pas pu revenir, ou l'ordre a échoué sans retour. refused : l'agent a
    -- refusé avant d'écrire, rien n'a bougé sur la machine.
    state        TEXT    NOT NULL CHECK (state IN ('pending', 'confirmed', 'rolled_back', 'failed', 'refused')),
    reason       TEXT    NOT NULL DEFAULT '',
    requested_at INTEGER NOT NULL,
    settled_at   INTEGER
);

CREATE INDEX service_updates_service ON service_updates (service_id, requested_at);
CREATE INDEX service_updates_pending ON service_updates (machine_id) WHERE state = 'pending';

-- La version du retour arrière d'un service que l'agent sait faire, redite à
-- chaque ouverture du flux : 0 tant qu'il ne le dit pas. Le serveur refuse la
-- montée de version d'un service dont l'agent est à 0.
ALTER TABLE agents ADD COLUMN service_rollback_version INTEGER NOT NULL DEFAULT 0 CHECK (service_rollback_version >= 0);

-- Joindre les 20 dernières lignes de journal d'une montée ratée aux messages
-- de ce canal. Oui par défaut : l'opérateur coupe la case canal par canal.
ALTER TABLE alert_channels ADD COLUMN attach_logs INTEGER NOT NULL DEFAULT 1 CHECK (attach_logs IN (0, 1));
