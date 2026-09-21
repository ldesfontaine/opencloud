package status

import (
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/service"
)

// Ce que chaque objet apporte à son composant. Le booléen dit s'il apporte
// quelque chose : un objet nouveau, en pause ou jamais lancé ne dit rien
// de la cible, il ne compte pas.

// Une machine hors ligne est une panne : plus personne ne mesure ni ne
// sonde depuis elle.
func machineState(status machine.Status) (State, bool) {
	if status.Online {
		return StateOperational, true
	}
	return StateDown, true
}

// Un service arrêté est une panne, quelle que soit la façon dont il s'est
// arrêté : pour le visiteur, il ne répond pas. Un service qui redémarre ou
// démarre est dégradé. Un conteneur créé sans avoir tourné ne compte pas ;
// un conteneur disparu est une panne, tant que sa fiche existe.
func serviceState(found service.Service) (State, bool) {
	if !found.ArchivedAt.IsZero() {
		return StateDown, true
	}
	switch found.State {
	case service.StateRunning:
		switch found.Health {
		case service.HealthUnhealthy:
			return StateDown, true
		case service.HealthStarting:
			return StateDegraded, true
		}
		return StateOperational, true
	case service.StateRestarting:
		return StateDegraded, true
	case service.StateCreated:
		return StateUnknown, false
	}
	return StateDown, true
}

// Une tâche en retard ou en échec dégrade : ce qu'elle fait n'est pas
// fait, mais le service, lui, répond encore.
func heartbeatState(found heartbeat.Heartbeat) (State, bool) {
	switch found.Status {
	case heartbeat.StatusLate, heartbeat.StatusFailed:
		return StateDegraded, true
	case heartbeat.StatusOnTime, heartbeat.StatusStarted:
		return StateOperational, true
	}
	return StateUnknown, false
}

// Une sonde hors ligne est une panne. Dégradée, ou en ligne avec un
// certificat à renouveler ou expiré, elle dégrade : la cible répond, on
// ne peut plus prouver à qui on parle, ou plus pour longtemps.
func probeState(found probe.Probe, now time.Time) (State, bool) {
	switch found.Status {
	case probe.StatusDown:
		return StateDown, true
	case probe.StatusDegraded:
		return StateDegraded, true
	case probe.StatusUp:
		if certificateDegrades(found.Certificate, now) {
			return StateDegraded, true
		}
		return StateOperational, true
	}
	return StateUnknown, false
}

// facts est ce que les composants lisent des objets : tout d'un coup,
// rangé par identifiant, pour ne pas relire la base membre par membre.
type facts struct {
	machines   map[string]machine.Status
	services   map[string]service.Service
	heartbeats map[string]heartbeat.Heartbeat
	probes     map[string]probe.Probe
	now        time.Time
}

// fill donne à chaque membre son nom et ce qu'il apporte.
func (f facts) fill(member *Member) {
	member.Present = true
	switch member.Kind {
	case KindMachine:
		found, ok := f.machines[member.ID]
		if !ok {
			member.Present = false
			return
		}
		member.Name = found.Name
		member.State, member.Counts = machineState(found)
	case KindService:
		found, ok := f.services[member.ID]
		if !ok {
			member.Present = false
			return
		}
		member.Name = found.Name
		member.State, member.Counts = serviceState(found)
	case KindHeartbeat:
		found, ok := f.heartbeats[member.ID]
		if !ok {
			member.Present = false
			return
		}
		member.Name = found.Name
		member.State, member.Counts = heartbeatState(found)
	case KindProbe:
		found, ok := f.probes[member.ID]
		if !ok {
			member.Present = false
			return
		}
		member.Name = found.Name
		member.State, member.Counts = probeState(found, f.now)
	}
}

// derive rend le pire des objets qui comptent ; Unknown quand aucun ne
// compte.
func derive(members []Member) State {
	states := make([]State, 0, len(members))
	for _, member := range members {
		if member.Present && member.Counts {
			states = append(states, member.State)
		}
	}
	return Worst(states)
}

// override rend l'état imposé par les incidents qui s'appliquent au
// composant : le pire d'entre eux ; Unknown quand aucun ne le touche.
func override(componentID string, open []Incident) State {
	states := []State{}
	for _, incident := range open {
		if !incident.Applies() {
			continue
		}
		for _, touched := range incident.Components {
			if touched.ID == componentID {
				states = append(states, incident.Impact.State())
			}
		}
	}
	return Worst(states)
}

// effective est ce que le public voit : l'incident d'abord, l'état dérivé
// sinon.
func effective(derived, imposed State) State {
	if imposed != StateUnknown {
		return imposed
	}
	return derived
}
