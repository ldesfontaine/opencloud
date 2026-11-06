package machine

// RightState dit où en est un droit sur l'agent d'une machine.
type RightState string

const (
	RightOK RightState = "ok"
	// Pas de droit, pas d'agent actif, ou un chemin ou un producteur que le
	// droit ne couvre pas.
	RightMissing RightState = "missing"
	// Le droit est coché, mais le compte de l'agent ne peut pas l'exercer
	// (RightFit.Reason dit pourquoi). Le serveur l'affiche sans refuser le
	// geste : le rapport date de l'ouverture du flux, et l'agent refuse, avec
	// la raison, au moment de l'ordre.
	RightUnavailable RightState = "unavailable"
	// L'agent n'a pas encore rapporté ses droits : c'est lui qui jugera.
	RightUnknown RightState = "unknown"
)

// RightFit est un droit tel que l'agent d'une machine le tient : de quoi
// griser une machine à l'écran et proposer de la ré-enrôler avec lui.
type RightFit struct {
	Right string
	State RightState
	// La raison d'un droit coché mais pas exerçable (UnavailableReasons).
	Reason string
	// Le dossier d'un droit à un seul chemin (restic.target) ; vide pour les
	// autres.
	Path string
}

// Fit juge un droit sur cet agent : la seule règle de « ok, manquant, inconnu,
// inexerçable ». scope est le chemin à couvrir, ou le producteur pour
// backup.produce (le droit porte une liste de producteurs, pas un chemin) ;
// vide, seul le droit compte.
func (a Agent) Fit(right, scope string) RightFit {
	fit := RightFit{Right: right, State: RightMissing}
	if !a.IsActive() {
		return fit
	}
	if a.ReportedAt.IsZero() {
		fit.State = RightUnknown
		return fit
	}
	capability, held := a.heldCapability(right)
	if !held || (scope != "" && !a.coversScope(right, scope)) {
		return fit
	}
	fit.State = RightOK
	if right != CapabilityBackupProduce && len(capability.Paths) == 1 {
		fit.Path = capability.Paths[0]
	}
	if reason := a.UnavailableReason(right); reason != "" {
		fit.State, fit.Reason = RightUnavailable, reason
	}
	return fit
}

func (a Agent) heldCapability(right string) (Capability, bool) {
	for _, capability := range a.Capabilities {
		if capability.Name == right {
			return capability, true
		}
	}
	return Capability{}, false
}

func (a Agent) coversScope(right, scope string) bool {
	if right == CapabilityBackupProduce {
		return CoversProducer(a.Capabilities, scope)
	}
	return CoversPath(a.Capabilities, right, scope)
}
