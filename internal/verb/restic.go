package verb

// Les gestes de restic qu'un jeton peut cocher : lancer une sauvegarde (sans
// purge), vérifier un dépôt et le relire en test de restauration, parcourir
// un dépôt, restaurer à côté de l'original. Le cadrage #366 ferme le reste :
// aucun verbe ne crée, ne modifie ni ne supprime une cible, une sauvegarde ou
// une rétention. Ces gestes restent à l'opérateur, dans l'interface (une
// session humaine), et leurs routes sont refusées à tout jeton ;
// verb_test.go et server/api_token_hostile_test.go le gardent. La
// purge n'a aucun verbe : seule la nuit du serveur l'ordonne, à la cible.
// Lancer une sauvegarde n'efface rien, comme agent.ping : il est son propre
// inverse. Vérifier non plus (la cible relit en mémoire, n'écrit rien).
// Les identifiants gardent la convention du registre, « restic.backup.* »
// (« restic » dit le moteur : « backup.* » nommait les archives chiffrées).
// Le ticket #374 distingue « vérifier » et « test de restauration » : c'est
// un seul ordre (restic.verify : restic check, puis relecture de fichiers du
// dernier instantané), donc un seul verbe, restic.backup.verify.
func init() {
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
	// Restaurer écrit sur la machine source, à côté de l'original, sans rien
	// effacer : il n'y a pourtant pas de verbe qui défasse cette copie. Un geste
	// sans retour par verbe est à confirmation : le jeton dépose une demande que
	// l'opérateur confirme dans l'interface, avec le chemin exact de la copie.
	Default.MustRegister(Verb{
		ID: "restic.backup.restore", Route: "POST /api/machines/{id}/restic-backups/{backup}/actions/restore",
		Params: []string{"id", "backup"}, Body: []string{"snapshot", "path", "stamp"}, Target: TargetMachine,
		Label: "api_token.verb_restic_backup_restore", Summary: "api_token.verb_restic_backup_restore_text", Confirm: true,
	})
	// Parcourir un dépôt n'écrit ni n'efface rien : il est son propre inverse.
	Default.MustRegister(Verb{
		ID: "restic.backup.browse", Route: "POST /api/machines/{id}/restic-backups/{backup}/actions/browse",
		Params: []string{"id", "backup"}, Body: []string{"snapshot", "path"}, Target: TargetMachine,
		Label: "api_token.verb_restic_backup_browse", Summary: "api_token.verb_restic_backup_browse_text", Reverse: "restic.backup.browse",
	})
}
