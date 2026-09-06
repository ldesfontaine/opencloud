package catalog

import "time"

// Diagnostiquer lit la machine et n'écrit rien : cinq minutes suffisent, et
// RuntimeMaxSec la tue passé ce délai même si openCloud est éteint.
const diagnostiquerTimeout = 5 * time.Minute

// Definitions rend le catalogue dans un ordre stable. Chaque appel construit
// sa tranche : rien de partagé, donc rien de modifiable par un appelant.
func Definitions() []Definition {
	return []Definition{
		{
			Kind:       KindDiagnostiquer,
			Label:      "Diagnostiquer",
			Summary:    "Lit l'état de la machine — système, horloge, espace, unités, ports, lanceur — sans rien y changer.",
			Scope:      ScopeMachine,
			Place:      PlaceTarget,
			Reversible: true,
			Interrupts: false,
			Timeout:    diagnostiquerTimeout,
		},
	}
}

// Lookup rend la définition d'une action, et dit si elle existe.
func Lookup(kind Kind) (Definition, bool) {
	for _, definition := range Definitions() {
		if definition.Kind == kind {
			return definition, true
		}
	}
	return Definition{}, false
}
