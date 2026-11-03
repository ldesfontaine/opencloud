-- Les parties d'une sauvegarde de service (son dossier, ses volumes, ses
-- bases) : ce que chacune a donné au dernier passage, et à chaque passage de
-- l'historique. Du JSON de agentwire.ResticPartReport, rejugé contre ses
-- listes fermées avant d'entrer : des mots fermés et des noms Docker, jamais
-- le texte d'un agent. « [] » : aucune partie (une VM, ou un rapport d'avant).
ALTER TABLE restic_backups ADD COLUMN last_parts TEXT NOT NULL DEFAULT '[]';
ALTER TABLE restic_runs ADD COLUMN parts TEXT NOT NULL DEFAULT '[]';
