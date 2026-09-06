// Package migrations embarque les fichiers SQL numérotés qui font évoluer la
// base d'openCloud. Ils sont appliqués dans l'ordre au démarrage par store,
// chacun une seule fois. Nommage : NNN_sujet.sql.
package migrations

import "embed"

//go:embed *.sql
var Files embed.FS
