// Package settings mémorise les réglages de l'opérateur dans settings.toml,
// sous le répertoire d'état. Il lit au démarrage, écrit à chaque changement,
// et ne décide rien : la valeur par défaut d'un réglage vit chez qui l'utilise.
package settings
