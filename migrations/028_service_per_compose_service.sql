-- Un service par service Compose : plusieurs services déclarés peuvent
-- désigner le même projet Compose d'une machine (traefik, authelia et
-- crowdsec du projet « edge »). Le rattachement se fait par (projet, service
-- Compose, machine) : l'unité porte déjà le nom du service Compose, aucune
-- ligne existante ne change. L'index unique devient un index de lecture.
DROP INDEX declared_services_compose;
CREATE INDEX declared_services_project ON declared_services (machine_id, compose_project) WHERE origin = 'compose';

-- La catégorie corrigée par l'opérateur (web, sso, database, security,
-- monitoring, application). Vide : elle se déduit de l'image à la lecture ;
-- les services existants restent donc intacts et se classent seuls.
ALTER TABLE declared_services ADD COLUMN category TEXT NOT NULL DEFAULT '';
