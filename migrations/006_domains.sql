-- Les hôtes virtuels publiés, un par nom (03-modele.md, « Domaine »). La ligne
-- est écrite quand « Créer un hôte virtuel » conclut « fait » ou « inchangé »,
-- et retirée quand « Supprimer un hôte virtuel » conclut : c'est ce que la
-- machine porte vraiment, jamais ce qu'on a demandé.
--
-- Un nom n'est servi que par une machine à la fois : il est la clé.
CREATE TABLE domains (
  name        TEXT    PRIMARY KEY,
  machine_id  TEXT    NOT NULL REFERENCES machines(id),
  environment TEXT    NOT NULL,
  service     TEXT    NOT NULL,
  -- Le port constaté sur la machine, lu dans la définition du service : il
  -- n'est jamais saisi (15-catalogue-actions.md §3).
  port        INTEGER NOT NULL,
  created_at  TEXT    NOT NULL,
  updated_at  TEXT    NOT NULL
);

CREATE INDEX domains_machine ON domains(machine_id);
