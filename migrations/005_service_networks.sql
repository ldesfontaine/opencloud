-- Le réseau des services : ce qui relie les conteneurs d'une machine et ce
-- qui est exposé au monde. Les constats d'exposition ne sont pas stockés :
-- ils se calculent à la lecture depuis ces faits.

-- Sur la fiche : le mode réseau tel que Docker le dit (bridge, host, none,
-- container:<id>, ou le nom du premier réseau joint), le mode privilégié,
-- et les dépendances déclarées en JSON (label Compose ou lien hérité),
-- lues telles quelles, jamais interrogées.
ALTER TABLE services ADD COLUMN network_mode TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN privileged INTEGER NOT NULL DEFAULT 0;
ALTER TABLE services ADD COLUMN depends_on TEXT NOT NULL DEFAULT '[]';

-- Les réseaux Docker de chaque machine : la liste complète de l'inventaire
-- remplace la précédente, un réseau détruit disparaît. La machine retirée
-- emporte tout.
CREATE TABLE machine_networks (
    machine_id TEXT NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    network_id TEXT NOT NULL,
    name       TEXT NOT NULL,
    -- bridge, host, null, macvlan… ; overlay laissé de côté.
    driver     TEXT NOT NULL,
    internal   INTEGER NOT NULL DEFAULT 0,
    -- Le projet Compose qui l'a créé ; vide sinon.
    group_name TEXT NOT NULL DEFAULT '',
    seen_at    INTEGER NOT NULL,
    PRIMARY KEY (machine_id, network_id)
) WITHOUT ROWID;

-- L'appartenance d'un service à un réseau, lue sur le conteneur : elle
-- reste quand il est arrêté, l'adresse se vide. Le nom est recopié pour
-- qu'un réseau pas encore listé ait quand même le sien. La fiche supprimée
-- emporte ses appartenances ; le proxy lira ici quels services il joint.
CREATE TABLE service_networks (
    service_id TEXT NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    network_id TEXT NOT NULL,
    name       TEXT NOT NULL,
    ip         TEXT NOT NULL DEFAULT '',
    -- Les alias DNS du service sur ce réseau, en JSON.
    aliases    TEXT NOT NULL DEFAULT '[]',
    PRIMARY KEY (service_id, network_id)
) WITHOUT ROWID;

CREATE INDEX service_networks_network ON service_networks (network_id);
