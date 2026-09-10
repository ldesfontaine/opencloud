-- Les zones Cloudflare enregistrées dans openCloud, une par nom de zone
-- (06-reseau-et-certificats.md, « Le jeton DNS »). Le jeton lui-même n'est
-- pas ici : il vit dans un fichier 0600 sous <state_dir>/zones/, écrit une
-- fois et jamais réaffiché (08-securite-et-secrets.md).
CREATE TABLE zones (
  name          TEXT PRIMARY KEY,
  -- L'identifiant de la zone chez Cloudflare, relevé à l'ajout : les appels
  -- DNS passent par lui, jamais par le nom.
  cloudflare_id TEXT NOT NULL,
  added_at      TEXT NOT NULL,
  -- Date de la dernière rotation du jeton ; égale à added_at tant qu'il n'a
  -- pas tourné.
  rotated_at    TEXT NOT NULL
);

-- Où le jeton d'une zone est posé. La ligne est écrite quand « Poser le jeton
-- DNS » conclut sur une machine : c'est ce que la machine porte vraiment.
-- Après une rotation, une empreinte qui n'est plus celle du jeton courant dit
-- où l'ancien traîne encore.
CREATE TABLE zone_machines (
  zone        TEXT NOT NULL REFERENCES zones(name) ON DELETE CASCADE,
  machine_id  TEXT NOT NULL REFERENCES machines(id),
  placed_at   TEXT NOT NULL,
  -- L'empreinte SHA-256 du jeton posé, tronquée telle que le script l'écrit
  -- (« info: jeton=… ») : jamais le jeton.
  fingerprint TEXT NOT NULL,
  PRIMARY KEY (zone, machine_id)
);

CREATE INDEX zone_machines_machine ON zone_machines(machine_id);
