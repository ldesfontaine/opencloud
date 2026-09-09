// Le lanceur root-owned : la seule commande que sudoers autorise au compte
// opencloud. Il reçoit un identifiant d'action, revalide ce que sudoers ne
// sait pas valider, purge les dossiers d'action au-delà des trente derniers,
// puis exécute systemd-run lui-même, sans shell et avec un environnement
// fixe. Il n'est pas setuid : c'est sudo qui élève
// (15-catalogue-actions.md §1, 05-execution.md).
package main
