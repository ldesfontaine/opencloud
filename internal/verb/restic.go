package verb

// Les gestes de restic. Poser une cible (et relever son plancher de
// rétention) et la retirer se défont l'un l'autre. Créer une sauvegarde lie
// la source à la cible (un ordre à chacune, les mots de passe du lien dans
// leur secret) et la supprimer ferme le dépôt : deux gestes que l'opérateur
// confirme. Lancer une sauvegarde n'efface rien, comme agent.ping : il est
// son propre inverse. Vérifier un dépôt non plus (la cible relit en mémoire,
// n'écrit rien) : même chose. Modifier une sauvegarde (son nom, sa rétention) reste à
// l'opérateur : une rétention plus courte purge plus.
func init() {
	Default.MustRegister(Verb{
		ID: "restic.target.setup", Route: "POST /api/machines/{id}/actions/restic-target-setup",
		Params: []string{"id"}, Body: []string{"host", "port", "dir", "retention_floor"}, Target: TargetMachine,
		Label: "api_token.verb_restic_target_setup", Summary: "api_token.verb_restic_target_setup_text", Reverse: "restic.target.remove",
	})
	Default.MustRegister(Verb{
		ID: "restic.target.remove", Route: "POST /api/machines/{id}/actions/restic-target-remove",
		Params: []string{"id"}, Body: []string{}, Target: TargetMachine,
		Label: "api_token.verb_restic_target_remove", Summary: "api_token.verb_restic_target_remove_text", Reverse: "restic.target.setup",
	})
	Default.MustRegister(Verb{
		ID: "restic.backup.create", Route: "POST /api/machines/{id}/restic-backups",
		Params: []string{"id"}, Body: []string{"name", "scope", "vmid", "service_id", "target_machine_id", "retention"}, Target: TargetMachine,
		Label: "api_token.verb_restic_backup_create", Summary: "api_token.verb_restic_backup_create_text", Confirm: true,
	})
	Default.MustRegister(Verb{
		ID: "restic.backup.delete", Route: "DELETE /api/machines/{id}/restic-backups/{backup}",
		Params: []string{"id", "backup"}, Body: []string{}, Target: TargetMachine,
		Label: "api_token.verb_restic_backup_delete", Summary: "api_token.verb_restic_backup_delete_text", Confirm: true,
	})
	Default.MustRegister(Verb{
		ID: "restic.backup.run", Route: "POST /api/machines/{id}/restic-backups/{backup}/actions/run",
		Params: []string{"id", "backup"}, Body: []string{}, Target: TargetMachine,
		Label: "api_token.verb_restic_backup_run", Summary: "api_token.verb_restic_backup_run_text", Reverse: "restic.backup.run",
	})
	Default.MustRegister(Verb{
		ID: "restic.backup.verify", Route: "POST /api/machines/{id}/restic-backups/{backup}/actions/verify",
		Params: []string{"id", "backup"}, Body: []string{}, Target: TargetMachine,
		Label: "api_token.verb_restic_backup_verify", Summary: "api_token.verb_restic_backup_verify_text", Reverse: "restic.backup.verify",
	})
}
