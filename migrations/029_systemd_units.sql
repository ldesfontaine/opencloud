-- Les unités systemd que l'agent d'une machine relève : un reflet de son
-- dernier rapport, remplacé à chaque fois, sans historique. Le nom et les trois
-- états de chaque unité de service, rien d'autre : ni description, ni fichier,
-- ni environnement, ni processus.

-- Le rapport d'une machine : quand, si systemd a pu être lu (sinon pourquoi),
-- et s'il est coupé (plus de 512 unités). Une machine sans ligne ici n'a rien
-- rapporté (un agent plus ancien).
CREATE TABLE systemd_reports (
    machine_id  TEXT    PRIMARY KEY REFERENCES machines (id) ON DELETE CASCADE,
    available   INTEGER NOT NULL CHECK (available IN (0, 1)),
    reason      TEXT    NOT NULL DEFAULT '' CHECK (reason IN ('', 'no_systemctl', 'not_booted', 'unreadable')),
    truncated   INTEGER NOT NULL DEFAULT 0 CHECK (truncated IN (0, 1)),
    reported_at INTEGER NOT NULL
);

CREATE TABLE systemd_units (
    machine_id   TEXT NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    load_state   TEXT NOT NULL,
    active_state TEXT NOT NULL,
    sub_state    TEXT NOT NULL,
    PRIMARY KEY (machine_id, name)
) WITHOUT ROWID;
