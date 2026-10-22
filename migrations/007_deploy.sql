-- Déployer un service déclaré : une unité peut monter un dossier de données
-- (./data/<unité> vers ce chemin dans le conteneur), et un ordre se rattache
-- au service qu'il vise ; le run survit au service (SET NULL).
ALTER TABLE service_units ADD COLUMN data_path TEXT NOT NULL DEFAULT '';
ALTER TABLE action_runs ADD COLUMN service_id TEXT REFERENCES declared_services (id) ON DELETE SET NULL;

-- Le dernier déploiement réussi d'un service : une lecture par fiche.
CREATE INDEX action_runs_service ON action_runs (service_id, requested_at) WHERE service_id IS NOT NULL;
