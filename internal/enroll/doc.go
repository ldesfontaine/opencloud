// Package enroll pose l'accès d'openCloud à une machine. La séquence — compte,
// sudo, sshd, clé — est celle du script de l'action Enrôler : la machine
// openCloud le joue sur elle-même, en root ; une machine distante le reçoit
// collé, puis une empreinte confirmée. Autour, il tient la paire de clés et le
// known_hosts de chaque machine, pose le lanceur et vérifie par le chemin réel.
// Il ne lance aucune action.
package enroll
