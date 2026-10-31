-- Où tourne la machine, et ce qu'elle a comme disques.
--
-- kind : ce que l'agent rapporte (systemd-detect-virt). « vps » n'y est jamais :
-- il se déduit à la lecture (virtualisée, sans hôte connu). kind_override est la
-- correction de l'opérateur ; host_override, l'hôte qu'il désigne (une autre
-- machine), même quand Proxmox ne l'a pas rapprochée.
ALTER TABLE machines ADD COLUMN kind TEXT NOT NULL DEFAULT 'unknown' CHECK (kind IN ('physical', 'vm', 'lxc', 'unknown'));
ALTER TABLE machines ADD COLUMN kind_override TEXT CHECK (kind_override IN ('physical', 'vm', 'lxc', 'vps', 'unknown'));
ALTER TABLE machines ADD COLUMN host_override TEXT REFERENCES machines (id) ON DELETE SET NULL;

-- Les machines déjà rapprochées d'un invité en tirent leur type : l'hôte le dit.
UPDATE machines SET kind = (
    SELECT CASE g.kind WHEN 'qemu' THEN 'vm' ELSE 'lxc' END
    FROM proxmox_guests g WHERE g.linked_machine_id = machines.id ORDER BY g.machine_id, g.vmid LIMIT 1)
WHERE id IN (SELECT linked_machine_id FROM proxmox_guests WHERE linked_machine_id IS NOT NULL);

-- Les cartes réseau que l'agent rapporte : de quoi reconnaître la machine dans
-- la configuration de son hôte. Chaque rapport remplace la liste.
CREATE TABLE machine_interfaces (
    machine_id TEXT NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    -- En minuscules, deux-points compris.
    mac        TEXT NOT NULL,
    PRIMARY KEY (machine_id, name)
) WITHOUT ROWID;

-- Les disques physiques et les pools que l'agent rapporte, sans relevé SMART.
-- Chaque rapport remplace les deux listes.
CREATE TABLE machine_block_devices (
    machine_id TEXT    NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    name       TEXT    NOT NULL,
    model      TEXT    NOT NULL DEFAULT '',
    size       INTEGER NOT NULL DEFAULT 0,
    media      TEXT    NOT NULL CHECK (media IN ('nvme', 'ssd', 'hdd', 'virtual', 'unknown')),
    removable  INTEGER NOT NULL DEFAULT 0 CHECK (removable IN (0, 1)),
    PRIMARY KEY (machine_id, name)
) WITHOUT ROWID;

CREATE TABLE machine_storage_pools (
    machine_id TEXT    NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    name       TEXT    NOT NULL,
    kind       TEXT    NOT NULL CHECK (kind IN ('md', 'lvm', 'zfs')),
    level      TEXT    NOT NULL DEFAULT '',
    state      TEXT    NOT NULL CHECK (state IN ('ok', 'degraded', 'failed', 'unknown')),
    -- 0 quand le noyau ne le dit pas sans commande (ZFS).
    size       INTEGER NOT NULL DEFAULT 0,
    -- Les noms des disques, séparés par « , » (aucun nom n'en contient).
    members    TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (machine_id, name)
) WITHOUT ROWID;

-- Le cluster d'hyperviseurs : un hôte déclaré lit toute la grappe par
-- /cluster/resources, plusieurs hôtes d'une même grappe partagent une ligne.
CREATE TABLE hypervisor_clusters (
    id   TEXT NOT NULL PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('proxmox')),
    name TEXT NOT NULL,
    UNIQUE (kind, name)
);

ALTER TABLE machine_proxmox ADD COLUMN cluster_id TEXT REFERENCES hypervisor_clusters (id) ON DELETE SET NULL;

