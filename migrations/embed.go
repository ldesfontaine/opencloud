// Package migrations embarque les fichiers SQL numérotés du schéma
// d'openCloud. Ils sont appliqués dans l'ordre au démarrage par le package
// store, chacun une seule fois.
package migrations

import "embed"

//go:embed *.sql
var Files embed.FS
