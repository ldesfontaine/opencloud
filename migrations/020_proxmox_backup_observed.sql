-- Les archives vzdump qu'un relevé voit sur les stockages de l'hôte, par
-- invité : combien, leur taille totale, et le stockage de la plus récente. La
-- plus récente (date, taille) y était déjà ; le relevé remplace ces colonnes
-- avec le reste, il n'en reste rien d'un hôte retiré.
ALTER TABLE proxmox_guests ADD COLUMN backup_count INTEGER NOT NULL DEFAULT 0 CHECK (backup_count >= 0);
ALTER TABLE proxmox_guests ADD COLUMN backup_total_bytes INTEGER NOT NULL DEFAULT 0 CHECK (backup_total_bytes >= 0);
ALTER TABLE proxmox_guests ADD COLUMN last_backup_storage TEXT NOT NULL DEFAULT '';
