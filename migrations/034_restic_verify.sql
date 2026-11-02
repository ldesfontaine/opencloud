-- La dernière vérification du dépôt d'un lien (restic.verify) : l'ordre, son
-- état, une raison fermée, l'instant, et ce qu'elle a relu (des comptes :
-- instantanés, fichiers, octets, jamais un nom ni un contenu). La dernière
-- réussite est gardée à part : un essai raté ne l'efface pas, c'est elle que
-- l'alerte « test de restauration périmé » mesure. verify_state vide : jamais
-- tentée.
ALTER TABLE restic_links ADD COLUMN verify_run_id TEXT NOT NULL DEFAULT '';
ALTER TABLE restic_links ADD COLUMN verify_state TEXT NOT NULL DEFAULT '' CHECK (verify_state IN ('', 'sent', 'succeeded', 'failed', 'refused'));
ALTER TABLE restic_links ADD COLUMN verify_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE restic_links ADD COLUMN verify_at INTEGER;
ALTER TABLE restic_links ADD COLUMN verify_success_at INTEGER;
ALTER TABLE restic_links ADD COLUMN verify_snapshots INTEGER NOT NULL DEFAULT 0;
ALTER TABLE restic_links ADD COLUMN verify_files INTEGER NOT NULL DEFAULT 0;
ALTER TABLE restic_links ADD COLUMN verify_bytes INTEGER NOT NULL DEFAULT 0;
