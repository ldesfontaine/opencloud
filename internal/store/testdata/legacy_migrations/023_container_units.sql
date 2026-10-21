-- Le rattachement d'un conteneur à une unité de l'annuaire, recalculé à
-- chaque rapport et après chaque écriture de l'annuaire. unit_id NULL :
-- présent non déclaré. match_level : 'compose' (machine + projet + service
-- Compose) ou 'name' (machine + nom, faible) ; 'label' viendra avec les
-- labels. Pas de CHECK : la liste grandira. Une unité supprimée rend ses
-- conteneurs non déclarés ; match_level n'a de sens qu'avec unit_id, la
-- lecture l'ignore sinon.
ALTER TABLE services ADD COLUMN unit_id TEXT REFERENCES service_units (id) ON DELETE SET NULL;
ALTER TABLE services ADD COLUMN match_level TEXT NOT NULL DEFAULT '';
CREATE INDEX services_unit ON services (unit_id);

-- « Présent non déclaré, et c'est voulu » : une clé d'observation, jamais
-- un id Docker, que la recréation du conteneur ne perd pas. kind :
-- 'compose_project' (tout le projet) ou 'container_name'.
CREATE TABLE ignored_keys (
    machine_id TEXT NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    value      TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (machine_id, kind, value)
) WITHOUT ROWID;
