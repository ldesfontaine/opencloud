package restic

import (
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// RightState dit où en est un droit sur l'agent d'une machine.
type RightState string

const (
	RightOK          RightState = "ok"
	RightMissing     RightState = "missing"
	RightUnavailable RightState = "unavailable"
	// L'agent n'a pas encore rapporté ses droits : c'est lui qui jugera.
	RightUnknown RightState = "unknown"
)

// RightFit est un droit de restic tel que l'agent d'une machine le tient : de
// quoi griser une machine à l'écran et proposer de la ré-enrôler avec lui.
type RightFit struct {
	Right string
	State RightState
	// La raison d'un droit coché mais pas exerçable (UnavailableReasons).
	Reason string
	// Le dossier du droit restic.target ; vide pour les autres.
	Path string
}

// RightOf juge un droit sur l'agent d'une machine. Pour backup.produce, c'est
// le producteur vzdump qui compte : c'est lui qu'une sauvegarde d'invité lance.
func RightOf(target machine.Status, right string) RightFit {
	fit := RightFit{Right: right, State: RightMissing}
	agent := target.Agent
	if !agent.IsActive() {
		return fit
	}
	if agent.ReportedAt.IsZero() {
		fit.State = RightUnknown
		return fit
	}
	for _, capability := range agent.Capabilities {
		if capability.Name != right {
			continue
		}
		if right == machine.CapabilityResticTarget && len(capability.Paths) == 1 {
			fit.Path = capability.Paths[0]
		}
		if right == machine.CapabilityBackupProduce && !machine.CoversProducer(agent.Capabilities, machine.ProducerProxmoxVzdump) {
			return fit
		}
		fit.State = RightOK
		if reason := agent.UnavailableReason(right); reason != "" {
			fit.State, fit.Reason = RightUnavailable, reason
		}
		return fit
	}
	return fit
}
