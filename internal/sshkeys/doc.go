// Package sshkeys relève les clés SSH autorisées d'une machine, en lecture
// seule : l'agent lit les authorized_keys par défaut de chaque compte et
// rapporte des empreintes, des types, des commentaires bornés et le nom des
// options, jamais le contenu d'un fichier ; le serveur garde le dernier
// rapport de chaque machine. Rien n'est écrit sur une machine.
package sshkeys
