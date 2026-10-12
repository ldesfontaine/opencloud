// Package lifecycle dit si la branche d'un produit est encore maintenue,
// jusqu'à quand, et quelles branches plus récentes existent. Il lit
// endoflife.date depuis la machine openCloud, une fois par jour, et
// seulement quand l'opérateur l'a activé. Il ne lit aucune image,
// n'interroge aucun registre et n'ouvre aucune alerte.
package lifecycle
