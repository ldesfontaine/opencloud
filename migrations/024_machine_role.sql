-- Ce que l'opérateur fait de la machine : générique, Docker ou Proxmox. Il se
-- choisit à l'ajout (le jeton le porte jusqu'à l'enrôlement) et fixe les
-- modules que la machine porte. La liste fermée est machine.Roles().
--
-- Les machines déjà enrôlées reçoivent leur rôle des faits : Proxmox si un hôte
-- est déclaré ou si l'agent a reconnu Proxmox VE (il l'emporte sur Docker : un
-- hôte Proxmox fait souvent tourner Docker aussi), Docker si l'agent a trouvé
-- Docker, sinon générique. Le rôle se change sur la fiche.
ALTER TABLE machines ADD COLUMN role TEXT NOT NULL DEFAULT 'generic' CHECK (role IN ('generic', 'docker', 'proxmox'));
ALTER TABLE enrollment_tokens ADD COLUMN role TEXT NOT NULL DEFAULT 'generic' CHECK (role IN ('generic', 'docker', 'proxmox'));

UPDATE machines SET role = 'proxmox'
WHERE id IN (SELECT machine_id FROM machine_proxmox)
   OR id IN (SELECT machine_id FROM machine_roles WHERE role = 'proxmox_ve');

UPDATE machines SET role = 'docker'
WHERE role = 'generic' AND id IN (SELECT machine_id FROM machine_engines WHERE present = 1);
