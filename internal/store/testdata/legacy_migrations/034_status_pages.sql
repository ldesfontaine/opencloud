-- Plusieurs pages de statut, créées à la main, chacune à son adresse
-- /statut/<slug>. Une page porte son titre, son annonce et sa langue, qui
-- vivaient dans la ligne settings ; un composant appartient à une page et
-- part avec elle. Une page peut appartenir à un projet, une au plus par
-- projet : supprimer le projet passe alors par son plan (restrict).
--
-- Comme toutes, cette migration tourne hors clés étrangères
-- (store.applyContent) : les reconstructions n'emportent rien, et les clés
-- sont vérifiées avant le COMMIT.

-- 1. Les pages. project_id reste nul tant que la page n'est pas rangée dans
-- un projet ; l'index unique admet plusieurs NULL. Le titre et la langue
-- peuvent rester vides : le catalogue donne le titre, l'interface la langue.
CREATE TABLE status_pages (
    id           TEXT PRIMARY KEY,
    project_id   TEXT UNIQUE REFERENCES projects (id) ON DELETE RESTRICT,
    -- L'adresse publique, sous /statut/.
    slug         TEXT NOT NULL UNIQUE,
    title        TEXT NOT NULL DEFAULT '',
    announcement TEXT NOT NULL DEFAULT '',
    language     TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);

-- 2. La page unique d'avant devient une page « statut », sans projet, si elle
-- montrait quelque chose : un composant, un titre ou une annonce. Une page
-- vide ne se reprend pas : elle ne disait rien, et une page se crée à la
-- main. La ligne settings peut manquer (rien de réglé) : les composants
-- suffisent alors.
INSERT INTO status_pages (id, project_id, slug, title, announcement, language, created_at, updated_at)
SELECT 'page_statut', NULL, 'statut',
       COALESCE((SELECT status_title FROM settings WHERE id = 1), ''),
       COALESCE((SELECT status_announcement FROM settings WHERE id = 1), ''),
       COALESCE((SELECT status_language FROM settings WHERE id = 1), ''),
       CAST(strftime('%s', 'now') AS INTEGER), CAST(strftime('%s', 'now') AS INTEGER)
WHERE EXISTS (SELECT 1 FROM status_components)
   OR EXISTS (SELECT 1 FROM settings WHERE id = 1 AND (status_title != '' OR status_announcement != ''));

-- 3. Un composant appartient à une page, et part avec elle. Les membres et
-- les incidents qui le visent le retrouvent par son nom après le RENAME.
CREATE TABLE status_components_new (
    id         TEXT PRIMARY KEY,
    page_id    TEXT NOT NULL REFERENCES status_pages (id) ON DELETE CASCADE,
    -- Le nom que le visiteur lit ; le seul mot d'openCloud qui sort en public.
    name       TEXT NOT NULL,
    -- L'ordre d'affichage sur sa page, à la main.
    position   INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

INSERT INTO status_components_new (id, page_id, name, position, created_at)
SELECT id, 'page_statut', name, position, created_at FROM status_components;

DROP TABLE status_components;
ALTER TABLE status_components_new RENAME TO status_components;
CREATE INDEX status_components_page ON status_components (page_id, position);

-- 4. La ligne settings perd les clés de la page : reconstruite sans elles.
CREATE TABLE settings_new (
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

INSERT INTO settings_new (id, language, disk_attention_percent, disk_danger_percent,
    mail_host, mail_port, mail_tls, mail_username, mail_password, mail_from, lifecycle_disabled, updated_at)
SELECT id, language, disk_attention_percent, disk_danger_percent,
    mail_host, mail_port, mail_tls, mail_username, mail_password, mail_from, lifecycle_disabled, updated_at
FROM settings;

DROP TABLE settings;
ALTER TABLE settings_new RENAME TO settings;
