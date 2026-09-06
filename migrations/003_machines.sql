-- Les machines de l'infrastructure. L'identifiant est celui qui apparaîtra
-- dans le nom d'unité et le chemin d'une action : même forme que
-- actiondir.ValidID, donc ni majuscule ni point.
CREATE TABLE machines (
  id         TEXT    PRIMARY KEY,
  name       TEXT    NOT NULL,
  address    TEXT    NOT NULL,
  port       INTEGER NOT NULL,
  account    TEXT    NOT NULL,
  created_at TEXT    NOT NULL
);

-- La machine openCloud existe dès la première ouverture de la base : openCloud
-- est installé dessus, il n'y a rien à déclarer. Elle passe par SSH vers
-- localhost comme les autres, et s'enrôle par « sudo opencloud enroll-local ».
INSERT INTO machines (id, name, address, port, account, created_at)
VALUES ('local', 'machine openCloud', '127.0.0.1', 22, 'opencloud',
        strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));
