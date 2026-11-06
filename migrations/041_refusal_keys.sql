-- Les refus d'un fait de machine n'ont plus qu'une clé chacun (internal/machine) :
-- une carte qui garderait l'ancienne clé afficherait la clé nue. On réécrit ce
-- que les plans, les sauvegardes restic et leurs historiques ont gardé.
UPDATE backup_plans SET last_error = 'machine.offline' WHERE last_error = 'backup.machine_offline';
UPDATE backup_plans SET last_error = 'machine.unknown' WHERE last_error = 'backup.machine_unknown';
UPDATE backup_runs SET reason = 'machine.offline' WHERE reason = 'backup.machine_offline';
UPDATE backup_runs SET reason = 'machine.unknown' WHERE reason = 'backup.machine_unknown';
UPDATE restic_backups SET last_reason = 'machine.offline' WHERE last_reason = 'restic.machine_offline';
UPDATE restic_backups SET last_reason = 'machine.unknown' WHERE last_reason = 'restic.machine_unknown';
UPDATE restic_runs SET reason = 'machine.offline' WHERE reason = 'restic.machine_offline';
UPDATE restic_runs SET reason = 'machine.unknown' WHERE reason = 'restic.machine_unknown';
