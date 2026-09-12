// Package fsx est le seul endroit qui écrit un fichier de manière atomique :
// temporaire dans le même dossier, fsync, rename, fsync du dossier. Tout
// passe par un os.Root ; il ne connaît aucun chemin absolu.
package fsx
