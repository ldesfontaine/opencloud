-- La page de statut entre dans le projet : un projet est publié quand il a un
-- slug, son adresse sous /statut/. Le titre public est celui du projet ; les
-- composants saisis à la main passent du côté du projet. Une page sans projet
-- devient un projet.
ALTER TABLE projects ADD COLUMN slug TEXT;
ALTER TABLE projects ADD COLUMN announcement TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN language TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN published_at INTEGER;
CREATE UNIQUE INDEX projects_slug ON projects (slug) WHERE slug IS NOT NULL;

-- Une page sans projet reçoit le sien, nommé par son adresse. Un nom déjà
-- pris prend « -statut », puis, si cela se heurte encore à un projet ou à
-- l'adresse d'une autre page, le début de l'identifiant de la page.
INSERT INTO projects (id, name, title, created_at, updated_at)
SELECT 'prj_' || p.id,
       CASE
           WHEN NOT EXISTS (SELECT 1 FROM projects WHERE name = p.slug) THEN p.slug
           WHEN NOT EXISTS (SELECT 1 FROM projects WHERE name = p.slug || '-statut')
                AND NOT EXISTS (SELECT 1 FROM status_pages WHERE slug = p.slug || '-statut') THEN p.slug || '-statut'
           ELSE p.slug || '-' || substr(p.id, 1, 8)
       END,
       substr(p.title, 1, 80), p.created_at, p.updated_at
FROM status_pages p
WHERE p.project_id IS NULL;

UPDATE status_pages SET project_id = 'prj_' || id WHERE project_id IS NULL;

-- Chaque projet reprend sa page. Son titre garde la main ; la page ne le
-- donne que s'il n'en avait pas, coupé à la longueur d'un titre de projet.
UPDATE projects SET
    slug         = (SELECT slug FROM status_pages WHERE project_id = projects.id),
    announcement = (SELECT announcement FROM status_pages WHERE project_id = projects.id),
    language     = (SELECT language FROM status_pages WHERE project_id = projects.id),
    published_at = (SELECT created_at FROM status_pages WHERE project_id = projects.id),
    title        = CASE WHEN title != '' THEN title
                        ELSE (SELECT substr(title, 1, 80) FROM status_pages WHERE project_id = projects.id) END
WHERE id IN (SELECT project_id FROM status_pages);

-- SQLite ne modifie pas une clé étrangère : la table se refait, avec son
-- index. Les membres la visent par son nom, qui reste.
CREATE TABLE status_components_new (
    id         TEXT    PRIMARY KEY,
    project_id TEXT    NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    -- Le nom que le visiteur lit ; le seul mot d'openCloud qui sort en public.
    name       TEXT    NOT NULL,
    -- L'ordre d'affichage sur sa page, à la main.
    position   INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

INSERT INTO status_components_new (id, project_id, name, position, created_at)
SELECT c.id, s.project_id, c.name, c.position, c.created_at
FROM status_components c JOIN status_pages s ON s.id = c.page_id;

DROP TABLE status_components;
ALTER TABLE status_components_new RENAME TO status_components;
CREATE INDEX status_components_project ON status_components (project_id, position);

DROP TABLE status_pages;
