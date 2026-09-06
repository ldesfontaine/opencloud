package web

import (
	"context"
	"time"

	"github.com/ldesfontaine/opencloud/internal/store"
)

// Les états qu'un sondage écrit dans MachineHealth.
const (
	probeReachable      = "reachable"
	probeSSHFailed      = "ssh-failed"
	probeLauncherFailed = "launcher-failed"
)

// Les états du statut d'une machine. La valeur est aussi la classe du badge.
const (
	statusNotEnrolled    = "not-enrolled"
	statusRunning        = "running"
	statusReachable      = "reachable"
	statusSSHFailed      = "ssh-failed"
	statusLauncherFailed = "launcher-failed"
	statusStale          = "stale"
	statusNeverProbed    = "never-probed"
)

// Passé ce délai, un sondage réussi ne dit plus que la machine est joignable :
// il dit une date. L'âge de l'information est toujours affiché
// (01-perimetre.md).
const freshProbe = 15 * time.Minute

type machineStatus struct {
	State string
	Label string
}

// newMachineStatus tient le statut à quatre états de 02-roles.md — joignable,
// SSH en échec, action en cours, dernière remontée datée. « Non enrôlée »
// prime sur tout, et le mot « en ligne » n'apparaît nulle part.
func newMachineStatus(enrolled bool, health MachineHealth, recent []store.Action, now time.Time) machineStatus {
	if !enrolled {
		return machineStatus{State: statusNotEnrolled, Label: labelNotEnrolled}
	}
	if hasRunningAction(recent) {
		return machineStatus{State: statusRunning, Label: labelActionRunning}
	}
	if health.ProbedAt.IsZero() {
		return machineStatus{State: statusNeverProbed, Label: labelNeverProbed}
	}

	switch health.ProbeState {
	case probeSSHFailed:
		return machineStatus{State: statusSSHFailed, Label: labelProbeFailed(labelSSHFailedSince, health)}
	case probeLauncherFailed:
		return machineStatus{State: statusLauncherFailed, Label: labelProbeFailed(labelLauncherFailedSince, health)}
	case probeReachable:
		if now.Sub(health.ProbedAt) < freshProbe {
			return machineStatus{State: statusReachable, Label: labelReachableSince(now.Sub(health.ProbedAt))}
		}
		return machineStatus{State: statusStale, Label: labelLastReport(health.ProbedAt)}
	}
	// Un état que le sondage n'écrit pas : on ne sait rien de la joignabilité,
	// et on le dit plutôt que de l'inventer.
	return machineStatus{State: statusNeverProbed, Label: labelNeverProbed}
}

func hasRunningAction(actions []store.Action) bool {
	for _, action := range actions {
		if action.State == store.StatePrepared || action.State == store.StateRunning {
			return true
		}
	}
	return false
}

// machineHealth relit le dernier sondage. Sans sonde branchée, aucune machine
// n'a été sondée — c'est l'état vrai.
func (s *Server) machineHealth(ctx context.Context, machineID string) MachineHealth {
	if s.prober == nil {
		return MachineHealth{}
	}
	health, err := s.prober.Health(ctx, machineID)
	if err != nil {
		s.logger.Warn("read machine health", "machine", machineID, "error", err)
		return MachineHealth{}
	}
	return health
}
