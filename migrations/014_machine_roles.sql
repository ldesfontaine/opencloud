-- Les rôles que l'agent d'une machine reconnaît sur elle (un hôte Proxmox VE),
-- à des signes locaux : un reflet de son dernier rapport, remplacé à chaque
-- fois. Un rôle n'ouvre aucun droit ; une machine sans ligne n'a aucun rôle vu.
CREATE TABLE machine_roles (
    machine_id TEXT NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    -- La liste fermée de internal/role.
    role       TEXT NOT NULL CHECK (role IN ('proxmox_ve')),
    -- La version trouvée ; vide si les signes ne la disent pas.
    version    TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (machine_id, role)
);
