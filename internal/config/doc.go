// Package config charge le fichier TOML, le valide, puis seulement le publie.
// Une clé inconnue est un avertissement, jamais un silence ; une valeur
// invalide est un refus qui nomme la clé. Jamais d'état à moitié appliqué :
// Load rend une Config complète ou une erreur.
//
// Aucun chemin de machine n'est écrit ici : le fichier de configuration dit
// où vit l'état, en absolu ou relativement à lui-même. Le paquet .deb pose
// le sien avec /var/lib/opencloud ; le dépôt a dev/config.toml avec state.
package config
