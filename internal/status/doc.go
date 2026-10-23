// Package status est la page de statut publique : des composants nommés
// pour un visiteur, dont l'état se dérive des objets qu'openCloud surveille
// déjà, et des incidents écrits par l'opérateur. Un incident vise des objets
// de l'annuaire ; une page montre ceux qui touchent l'un de ses composants.
// Il rend un instantané qui ne dit rien de plus que le nom des composants, et
// publie « status » dès que cet instantané change.
package status
