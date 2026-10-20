package dockerwatch

import (
	"context"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/dockerapi"
)

type cpuCounters struct {
	total  uint64
	system uint64
}

func (w *Watcher) forgetCounters(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.previous, id)
}

// deliverStats mesure chaque conteneur qui tourne ; la première mesure
// d'un conteneur n'a pas de delta et ne donne rien.
func (w *Watcher) deliverStats(ctx context.Context) {
	list, err := w.client.ListContainers(ctx)
	if err != nil {
		w.logger.Warn("list for stats", "error", err)
		return
	}
	var stats []container.Stat
	for _, summary := range list {
		if summary.State != string(container.StateRunning) || summary.IsOneOff() {
			continue
		}
		measured, err := w.client.Stats(ctx, summary.ID)
		if err != nil {
			continue
		}
		stat, ok := w.toStat(summary.ID, measured)
		if ok {
			stats = append(stats, stat)
		}
	}
	if len(stats) > 0 {
		w.sink.Deliver(container.Report{Stats: stats})
	}
}

// toStat fait le delta de processeur avec la mesure précédente. Un compteur
// qui recule (conteneur relancé) vaut une première mesure : rien ce tour.
func (w *Watcher) toStat(id string, measured dockerapi.Stats) (container.Stat, bool) {
	current := cpuCounters{total: measured.CPU.Usage.Total, system: measured.CPU.System}
	w.mu.Lock()
	previous, hasPrevious := w.previous[id]
	w.previous[id] = current
	w.mu.Unlock()
	if !hasPrevious || current.total < previous.total || current.system <= previous.system {
		return container.Stat{}, false
	}
	cpuDelta := float64(current.total - previous.total)
	systemDelta := float64(current.system - previous.system)
	cores := measured.CPU.OnlineCPUs
	if cores <= 0 {
		cores = 1
	}
	sampledAt := measured.ReadAt()
	if sampledAt.IsZero() {
		sampledAt = time.Now().UTC()
	}
	return container.Stat{
		ContainerID: id,
		SampledAt:   sampledAt.Truncate(time.Second),
		CPUPercent:  cpuDelta / systemDelta * float64(cores) * 100,
		MemUsed:     clampInt64(measured.MemoryUsed()),
		MemLimit:    clampInt64(measured.Memory.Limit),
	}, true
}

func clampInt64(value uint64) int64 {
	if value > 1<<62 {
		return 1 << 62
	}
	return int64(value)
}
