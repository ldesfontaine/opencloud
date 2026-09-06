// Package fsx écrit les fichiers de façon atomique : temporaire, fsync, rename,
// fsync du dossier. Le seul endroit qui le fait. Tout chemin est relatif à un
// os.Root, jamais absolu : le répertoire d'état est ouvert une fois et tout
// passe par lui.
package fsx
