package agentapi

import "time"

// Un agent donne un signal toutes les 30 s (agent.DefaultSignalInterval).
// Après une coupure il rattrape son tampon, 360 lectures par lots de 120 :
// trois signaux pleins et un dernier, serrés, soit quatre d'un coup au pire.
// Une rafale de 30 couvre sept rattrapages, et une demi-requête par seconde
// quinze fois la cadence normale : un agent sain ne se voit jamais refuser.
// Au-delà, c'est une machine qui inonde, et chaque signal coûte une
// transaction et jusqu'à un mégaoctet de lignes.
//
// Le seau est celui de la machine, trouvée par sa session : l'adresse ne
// dit rien, des agents derrière un même proxy la partagent. Le seau d'une
// machine retirée part au balayage.
const (
	signalPerMachineRate  = 0.5
	signalPerMachineBurst = 30
	signalIdle            = 10 * time.Minute
)
