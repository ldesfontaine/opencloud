// Package systemdunits relève les unités systemd d'une machine, en lecture
// seule et sans droit de plus : l'agent lit la liste des unités de service par
// systemctl (la lecture est ouverte à tout compte) et ne garde que le nom et
// l'état de chacune. Description, chemin du fichier, environnement et
// processus ne sont jamais lus ni envoyés ; le serveur garde le dernier
// rapport de chaque machine. Rien n'est écrit sur une machine.
package systemdunits
