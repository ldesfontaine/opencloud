-- Les volumes Docker de la machine, tels que l'agent les liste : leur nom, leur
-- pilote et les conteneurs qui les montent. Rien du contenu ni de la taille.
-- Chaque inventaire remplace la liste, un volume détruit disparaît.
CREATE TABLE machine_volumes (
    machine_id TEXT    NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    name       TEXT    NOT NULL,
    driver     TEXT    NOT NULL,
    -- Les noms des conteneurs qui le montent, en JSON ; « [] » s'il est seul.
    used_by    TEXT    NOT NULL DEFAULT '[]',
    seen_at    INTEGER NOT NULL,
    PRIMARY KEY (machine_id, name)
) WITHOUT ROWID;
