package alert

import (
	"context"

	"github.com/ldesfontaine/opencloud/internal/sampler"
)

// Ce que le composant resource constate : la dernière lecture d'une
// machine, avec ses volumes. Un volume au-dessus du seuil alerte, s'aggrave
// au second, et ne se résout que sous le seuil de retour ; un volume que
// la lecture ne nomme plus se résout aussi.
func (e *Engine) ReadingRecorded(ctx context.Context, machineID string, reading sampler.Reading) {
	// Relus à chaque lecture : un seuil changé sous Paramètres vaut dès la
	// mesure suivante, sans redémarrer.
	thresholds := e.diskThresholds()
	seen := make(map[string]bool, len(reading.Disks))
	for _, disk := range reading.Disks {
		seen[disk.MountPoint] = true
		e.judgeDisk(ctx, machineID, disk, thresholds)
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

func (e *Engine) judgeDisk(ctx context.Context, machineID string, disk sampler.Disk, thresholds DiskThresholds) {
	if disk.Total <= 0 {
		return
	}
	percent := int(disk.Used * 100 / disk.Total)
	object := Object{Kind: ObjectVolume, ID: VolumeID(machineID, disk.MountPoint), Name: disk.MountPoint}
	switch {
	case percent >= thresholds.Attention:
		severity := SeverityAttention
		if percent >= thresholds.Danger {
			severity = SeverityDanger
		}
		e.Open(ctx, Fact{
			Kind: KindDiskFull, Severity: severity, Object: object, MachineID: machineID,
			Details: Details{MountPoint: disk.MountPoint, Percent: percent, Threshold: thresholds.Attention},
		})
	case percent < thresholds.Recovery():
		e.Resolve(ctx, KindDiskFull, object)
	}
}
