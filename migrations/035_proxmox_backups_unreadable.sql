-- Le jeton d'un hôte Proxmox lit-il les archives de sauvegarde de ce stockage ?
-- Avec le seul rôle PVEAuditor, Proxmox répond 200 et une liste vide : sans
-- cette colonne, « aucune archive » et « archives illisibles » se confondent.
-- 1 : le dernier relevé a vu que le jeton n'a pas les droits qui rendent la
-- liste complète (pve-storage, check_volume_access) ; 0 : lisible, ou sans objet
-- (un stockage qui ne reçoit pas de sauvegardes). 0 est la valeur sûre : les
-- lignes déjà relevées y restent jusqu'au prochain relevé, qui les réécrit (au
-- démarrage, puis toutes les cinq minutes).
ALTER TABLE proxmox_storages ADD COLUMN backups_unreadable INTEGER NOT NULL DEFAULT 0 CHECK (backups_unreadable IN (0, 1));
