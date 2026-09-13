// Package server est la couche HTTP d'openCloud : l'API JSON du front, les
// routes de l'agent et des pings, les en-têtes de sécurité, le front
// embarqué. Les handlers lisent, appellent un composant et répondent ;
// aucune logique métier ne vit ici.
package server
