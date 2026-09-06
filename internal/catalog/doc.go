// Package catalog décrit chaque action : attributs, paramètres, script,
// délai. Il valide et rend des fichiers ; il ne touche jamais au réseau et
// ne lance rien. Ce fichier-ci et types.go sont le contrat que runner et web
// consomment ; le reste du package les met en œuvre.
package catalog
