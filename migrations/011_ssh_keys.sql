-- Les clés SSH autorisées que l'agent d'une machine relève, compte par
-- compte : un reflet de son dernier rapport, remplacé à chaque fois, sans
-- historique. Rien n'y est un secret : des clés publiques, leur empreinte,
-- un commentaire et le nom des options, jamais leur valeur.

-- Le rapport d'une machine : quand, et s'il est coupé (plus de 64 comptes).
-- Une machine sans ligne ici n'a rien rapporté (un agent plus ancien).
CREATE TABLE ssh_reports (
    machine_id  TEXT    PRIMARY KEY REFERENCES machines (id) ON DELETE CASCADE,
    truncated   INTEGER NOT NULL DEFAULT 0 CHECK (truncated IN (0, 1)),
    reported_at INTEGER NOT NULL
);

CREATE TABLE ssh_accounts (
    machine_id  TEXT    NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    account     TEXT    NOT NULL,
    uid         INTEGER NOT NULL CHECK (uid BETWEEN 0 AND 4294967295),
    -- read : lu ; none : aucun fichier ; denied : illisible pour le compte de
    -- l'agent ; unsafe : lien, fichier spécial ou modifiable par d'autres
    -- (sshd l'ignorerait) ; too_large : plus de 256 Kio.
    state       TEXT    NOT NULL CHECK (state IN ('read', 'none', 'denied', 'unsafe', 'too_large')),
    -- Plus de 64 clés (ou 512 pour la machine) : les suivantes manquent.
    truncated   INTEGER NOT NULL DEFAULT 0 CHECK (truncated IN (0, 1)),
    -- Les lignes qui ne sont pas une clé d'un type connu.
    ignored     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (machine_id, account)
);

CREATE TABLE ssh_keys (
    machine_id  TEXT    NOT NULL,
    account     TEXT    NOT NULL,
    file        TEXT    NOT NULL CHECK (file IN ('authorized_keys', 'authorized_keys2')),
    fingerprint TEXT    NOT NULL,
    key_type    TEXT    NOT NULL,
    bits        INTEGER NOT NULL DEFAULT 0,
    comment     TEXT    NOT NULL DEFAULT '',
    -- Les noms des options de la ligne, séparés par « , ».
    options     TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (machine_id, account, file, fingerprint),
    FOREIGN KEY (machine_id, account) REFERENCES ssh_accounts (machine_id, account) ON DELETE CASCADE
);

CREATE INDEX ssh_keys_by_fingerprint ON ssh_keys (fingerprint);
