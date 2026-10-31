-- Jusqu'où un jeton d'API en écriture porte ses verbes : toutes les machines
-- (le cas des jetons d'avant, rien ne change pour eux), ou une liste. Une liste
-- peut être vide : le jeton n'agit alors sur aucune machine. Le drapeau dit
-- « limité » ; sans lui, la liste ne compte pas. Même règle pour les invités
-- Proxmox (le numéro de VM ou de conteneur), jugés en plus de la machine hôte.
--
-- Révoquer le jeton emporte ses listes. Une machine supprimée sort de la
-- liste et la liste ne s'élargit pas pour autant : un jeton limité à une
-- machine qui disparaît n'agit plus sur rien.
ALTER TABLE api_tokens ADD COLUMN machines_limited INTEGER NOT NULL DEFAULT 0 CHECK (machines_limited IN (0, 1));
ALTER TABLE api_tokens ADD COLUMN guests_limited INTEGER NOT NULL DEFAULT 0 CHECK (guests_limited IN (0, 1));

CREATE TABLE api_token_machines (
    token_id   TEXT NOT NULL REFERENCES api_tokens (id) ON DELETE CASCADE,
    machine_id TEXT NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    PRIMARY KEY (token_id, machine_id)
) WITHOUT ROWID;

CREATE TABLE api_token_guests (
    token_id TEXT    NOT NULL REFERENCES api_tokens (id) ON DELETE CASCADE,
    vmid     INTEGER NOT NULL CHECK (vmid > 0),
    PRIMARY KEY (token_id, vmid)
) WITHOUT ROWID;
