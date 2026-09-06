// Package enroll pose l'accès d'openCloud à une machine : lanceur, compte,
// sudoers, sshd, clé, puis vérification par le chemin réel. La machine
// openCloud s'amorce ici même, en Go ; une machine distante par une commande
// collée dessus, puis une empreinte confirmée. Il tient la paire de clés et le
// known_hosts de chaque machine. Il ne lance aucune action.
package enroll
