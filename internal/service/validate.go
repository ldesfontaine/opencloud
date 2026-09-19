package service

import (
	"math"
	"strings"
	"time"
)

// validate refuse en bloc un rapport hors de ce qu'un agent peut avoir
// observé : trop de conteneurs, un nom vide, une date absurde, une mesure
// négative. Le signal de vie compte quand même : c'est le serveur qui
// répond, pas ce package.
func validate(report Report, now time.Time) error {
	if len(report.Inventory) > MaxContainersPerReport || len(report.Events) > MaxEventsPerReport || len(report.Stats) > MaxStatsPerReport {
		return ErrReportInvalid
	}
	if report.Engine != nil && !report.Engine.Present && report.Engine.Reason == "" {
		return ErrReportInvalid
	}
	for _, container := range report.Inventory {
		if err := validateContainer(container, now); err != nil {
			return err
		}
	}
	for _, event := range report.Events {
		if err := validateEvent(event, now); err != nil {
			return err
		}
	}
	for _, stat := range report.Stats {
		if err := validateStat(stat, now); err != nil {
			return err
		}
	}
	return nil
}

func validateContainer(container Container, now time.Time) error {
	if !isContainerID(container.ContainerID) || container.Name == "" || len(container.Name) > MaxNameLength {
		return ErrReportInvalid
	}
	if len(container.Group) > MaxNameLength || len(container.Image) > MaxImageLength || len(container.ImageID) > MaxImageLength {
		return ErrReportInvalid
	}
	if !isState(container.State) || !isHealth(container.Health) || container.RestartCount < 0 {
		return ErrReportInvalid
	}
	if len(container.Ports) > MaxPortsPerContainer {
		return ErrReportInvalid
	}
	for _, port := range container.Ports {
		if port.HostPort <= 0 || port.HostPort > 65535 || port.ContainerPort <= 0 || port.ContainerPort > 65535 || port.Protocol == "" {
			return ErrReportInvalid
		}
	}
	if container.CreatedAt.IsZero() || isTooLate(container.CreatedAt, now) {
		return ErrReportInvalid
	}
	return nil
}

func validateEvent(event Event, now time.Time) error {
	if !isContainerID(event.ContainerID) || event.Action == "" || event.At.IsZero() || isTooLate(event.At, now) {
		return ErrReportInvalid
	}
	if !isState(event.State) || !isHealth(event.Health) || len(event.Snippet) > MaxSnippetBytes {
		return ErrReportInvalid
	}
	if event.Container != nil {
		if event.Container.ContainerID != event.ContainerID {
			return ErrReportInvalid
		}
		return validateContainer(*event.Container, now)
	}
	return nil
}

func validateStat(stat Stat, now time.Time) error {
	if !isContainerID(stat.ContainerID) || stat.SampledAt.IsZero() || isTooLate(stat.SampledAt, now) || stat.SampledAt.Before(now.Add(-SampleRetention)) {
		return ErrReportInvalid
	}
	if math.IsNaN(stat.CPUPercent) || math.IsInf(stat.CPUPercent, 0) || stat.CPUPercent < 0 || stat.MemUsed < 0 || stat.MemLimit < 0 {
		return ErrReportInvalid
	}
	return nil
}

// Un identifiant Docker : 64 caractères hexadécimaux.
func isContainerID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, r := range id {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

// isState accepte l'état vide : un événement qui n'en change pas.
func isState(state State) bool {
	switch state {
	case "", StateCreated, StateRunning, StatePaused, StateRestarting, StateRemoving, StateExited, StateDead:
		return true
	}
	return false
}

func isHealth(health Health) bool {
	switch health {
	case "", HealthStarting, HealthHealthy, HealthUnhealthy:
		return true
	}
	return false
}

func isTooLate(at, now time.Time) bool {
	return at.After(now.Add(maxFutureSkew))
}
