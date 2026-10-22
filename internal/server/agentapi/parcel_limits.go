package agentapi

import "time"

// Un colis se télécharge une fois par ordre : une sauvegarde par nuit, une
// mise à jour de loin en loin. 30 par minute laissent passer un agent qui
// réessaie et arrêtent une machine qui boucle sur un fichier de dizaines de
// Mio. Le seau est celui de la machine, trouvée par sa session.
const (
	parcelPerMachineRate  = 0.5
	parcelPerMachineBurst = 30
	parcelIdle            = 10 * time.Minute
)
