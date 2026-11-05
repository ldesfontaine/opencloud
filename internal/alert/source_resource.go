package alert

import (
	"context"
	"errors"
	"time"

	"github.com/ldesfontaine/opencloud/internal/sampler"
)

const (
	// Une moyenne ne vaut que si les lectures couvrent la fenêtre : la plus
	// ancienne ne date pas de plus de windowCoverageSlack après son début, et
	// au moins la moitié des lectures attendues sont là. Un agent qui vient
	// d'arriver, ou qui a été coupé, ne juge rien : l'alerte reste telle
	// qu'elle est.
	windowCoverageSlack    = time.Minute
	windowCoverageFraction = 2
	// L'agent mesure toutes les 10 s (resource.SampleInterval).
	readingInterval = 10 * time.Second
)

// Ce que le composant resource constate : la dernière lecture d'une
// machine. Chaque ressource est jugée par la règle qui s'applique à cette
// machine (surcharge, sinon réglage global) : le disque sur la dernière
// lecture de chaque volume, le processeur et la mémoire sur la moyenne de
// leur fenêtre. Au-dessus du seuil d'attention l'alerte s'ouvre, au second
// elle s'aggrave, et elle ne se résout que sous le seuil de retour ; un
// volume que la lecture ne nomme plus se résout aussi.
func (e *Engine) ReadingRecorded(ctx context.Context, machineID string, reading sampler.Reading) {
	// Relues à chaque lecture : un seuil changé vaut dès la mesure suivante,
	// sans redémarrer.
	for _, applied := range e.rulesOrDefaults(ctx, machineID) {
		switch applied.Resource {
		case ResourceDisk:
			e.judgeDisks(ctx, machineID, reading, applied.Rule)
		default:
			e.judgeAverage(ctx, machineID, reading.SampledAt, applied.Rule)
		}
	}
}

func (e *Engine) judgeDisks(ctx context.Context, machineID string, reading sampler.Reading, rule Rule) {
	seen := make(map[string]bool, len(reading.Disks))
	for _, disk := range reading.Disks {
		seen[disk.MountPoint] = true
		e.judgeDisk(ctx, machineID, disk, rule)
	}
	open, err := e.store.ListAlerts(ctx, Filter{Status: StatusOpen, Kind: KindDiskFull, MachineID: machineID})
	if err != nil {
		e.fail(ctx, "list disk alerts", err)
		return
	}
	for _, alert := range open {
		if !seen[alert.Details.MountPoint] {
			e.Resolve(ctx, KindDiskFull, alert.Object)
		}
	}
}

func (e *Engine) judgeDisk(ctx context.Context, machineID string, disk sampler.Disk, rule Rule) {
	if disk.Total <= 0 {
		return
	}
	percent := int(disk.Used * 100 / disk.Total)
	object := Object{Kind: ObjectVolume, ID: VolumeID(machineID, disk.MountPoint), Name: disk.MountPoint}
	switch {
	case percent >= rule.Attention:
		e.Open(ctx, Fact{
			Kind: KindDiskFull, Severity: severityOf(percent, rule), Object: object, MachineID: machineID,
			Details: Details{MountPoint: disk.MountPoint, Percent: percent, Threshold: rule.Attention},
		})
	case percent < rule.Recovery():
		e.Resolve(ctx, KindDiskFull, object)
	}
}

// judgeAverage juge le processeur ou la mémoire d'une machine sur la moyenne
// de la fenêtre qui finit à cette lecture.
func (e *Engine) judgeAverage(ctx context.Context, machineID string, at time.Time, rule Rule) {
	kind := kindOf(rule.Resource)
	from := at.Add(-rule.Window)
	average, err := e.store.AverageUsage(ctx, machineID, from, at)
	if err != nil {
		e.fail(ctx, "average usage", err)
		return
	}
	if !average.Covers(from, rule.Window) {
		return
	}
	percent := int(average.Percent(rule.Resource))
	switch {
	case percent >= rule.Attention:
		e.openAverage(ctx, machineID, kind, percent, rule)
	case percent < rule.Recovery():
		e.Resolve(ctx, kind, Object{Kind: ObjectMachine, ID: machineID})
	}
}

// openAverage ouvre l'alerte d'une moyenne ; la machine retirée entre-temps
// n'en a plus, et rien ne s'ouvre.
func (e *Engine) openAverage(ctx context.Context, machineID string, kind Kind, percent int, rule Rule) {
	name, err := e.store.MuteTargetName(ctx, Target{Kind: TargetMachine, ID: machineID})
	if errors.Is(err, ErrNotFound) {
		return
	}
	if err != nil {
		e.fail(ctx, "read machine name", err)
		return
	}
	e.Open(ctx, Fact{
		Kind: kind, Severity: severityOf(percent, rule), MachineID: machineID,
		Object:  Object{Kind: ObjectMachine, ID: machineID, Name: name},
		Details: Details{Percent: percent, WindowMinutes: int(rule.Window.Minutes()), Threshold: rule.Attention},
	})
}

func severityOf(percent int, rule Rule) Severity {
	if percent >= rule.Danger {
		return SeverityDanger
	}
	return SeverityAttention
}

func kindOf(resource Resource) Kind {
	switch resource {
	case ResourceCPU:
		return KindCPUHigh
	case ResourceMemory:
		return KindMemoryHigh
	}
	return KindDiskFull
}

// UsageAverage est la moyenne des lectures d'une machine sur une période.
type UsageAverage struct {
	// Samples : combien de lectures la moyenne couvre ; First, la plus
	// ancienne.
	Samples int
	First   time.Time
	// CPUPercent et MemoryPercent : la moyenne des pourcentages de chaque
	// lecture.
	CPUPercent    float64
	MemoryPercent float64
}

func (a UsageAverage) Percent(resource Resource) float64 {
	if resource == ResourceMemory {
		return a.MemoryPercent
	}
	return a.CPUPercent
}

// Covers dit si les lectures couvrent la fenêtre qui commence à `from` :
// début atteint, et assez de lectures pour que la moyenne ne soit pas celle
// d'un trou.
func (a UsageAverage) Covers(from time.Time, window time.Duration) bool {
	if a.Samples == 0 {
		return false
	}
	expected := int(window / readingInterval)
	return a.Samples >= expected/windowCoverageFraction && !a.First.After(from.Add(windowCoverageSlack))
}
