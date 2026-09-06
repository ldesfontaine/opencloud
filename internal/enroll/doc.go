// Package enroll amorce la machine openCloud sur elle-même : lanceur, compte,
// sudoers, sshd, clé, puis vérification par le chemin réel — SSH vers
// localhost. Il tient aussi la paire de clés et le known_hosts d'une machine.
// Il ne génère pas la commande d'enrôlement d'une machine distante.
package enroll
