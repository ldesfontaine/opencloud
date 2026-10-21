// Package reader déclare Reader : les lectures de la base sans règle
// métier (List, Get, Count) que plusieurs consommateurs partagent. Il ne
// porte aucune requête ni aucune règle : *store.DB le satisfait, et le
// serveur, la page de statut et le fichier de l'annuaire lisent par lui
// au lieu de redéclarer chaque méthode. Les écritures passent toujours par
// le composant du domaine.
package reader
