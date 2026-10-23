-- Le conteneur observé prend son nom : « service » est désormais celui de
-- l'annuaire. Renommer, rien d'autre : ni ligne ni comportement ne change.
-- SQLite récrit les REFERENCES des autres tables. Les index sont refaits
-- sous leur nouveau nom. Le genre d'objet 'service' des alertes et des
-- silences reste, comme les types service_* des alertes : le CHECK ne
-- change pas sans reconstruire la table ; ils partiront avec la revue des
-- alertes. containers.container_id reste l'identifiant Docker ; les
-- container_id des autres tables désignent la fiche (containers.id).
ALTER TABLE services RENAME TO containers;
ALTER TABLE service_transitions RENAME TO container_events;
ALTER TABLE container_events RENAME COLUMN service_id TO container_id;
ALTER TABLE service_samples RENAME TO container_samples;
ALTER TABLE container_samples RENAME COLUMN service_id TO container_id;
ALTER TABLE service_networks RENAME TO container_networks;
ALTER TABLE container_networks RENAME COLUMN service_id TO container_id;
ALTER TABLE alerts RENAME COLUMN service_id TO container_id;

DROP INDEX services_machine;
CREATE INDEX containers_machine ON containers (machine_id);
DROP INDEX services_archived;
CREATE INDEX containers_archived ON containers (archived_at);
DROP INDEX services_unit;
CREATE INDEX containers_unit ON containers (unit_id);
DROP INDEX service_transitions_service;
CREATE INDEX container_events_container ON container_events (container_id, at);
DROP INDEX service_transitions_at;
CREATE INDEX container_events_at ON container_events (at);
DROP INDEX service_samples_sampled;
CREATE INDEX container_samples_sampled ON container_samples (sampled_at);
DROP INDEX service_networks_network;
CREATE INDEX container_networks_network ON container_networks (network_id);
