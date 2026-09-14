package sampler

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	defaultProcPath = "/proc"
	defaultRootPath = "/"
)

// Reading est ce qu'une mesure dit de la machine à un instant : le
// processeur et le réseau sont des deltas depuis la mesure précédente, le
// reste est instantané. Les octets sont des octets, les débits par seconde.
type Reading struct {
	SampledAt      time.Time `json:"sampled_at"`
	CPUPercent     float64   `json:"cpu_percent"`
	CPUCores       int       `json:"cpu_cores"`
	Load1          float64   `json:"load_1"`
	MemUsed        int64     `json:"mem_used"`
	MemTotal       int64     `json:"mem_total"`
	SwapUsed       int64     `json:"swap_used"`
	SwapTotal      int64     `json:"swap_total"`
	DiskUsed       int64     `json:"disk_used"`
	DiskTotal      int64     `json:"disk_total"`
	NetRxPerSecond int64     `json:"net_rx_per_second"`
	NetTxPerSecond int64     `json:"net_tx_per_second"`
}

// Sampler lit la machine ; la première lecture n'a pas de delta et ne rend
// rien, les suivantes comparent aux compteurs qu'il garde.
type Sampler struct {
	procPath string
	rootPath string
	prevCPU  cpuCounters
	prevNet  netCounters
	prevAt   time.Time
	primed   bool
}

func New() *Sampler {
	return NewAt(defaultProcPath, defaultRootPath)
}

// NewAt lit un autre /proc et une autre racine : les tests s'en servent.
func NewAt(procPath, rootPath string) *Sampler {
	return &Sampler{procPath: procPath, rootPath: rootPath}
}

// Sample mesure maintenant. Le booléen est faux à la première lecture, qui
// ne fait qu'armer les compteurs, ou si /proc ne se lit pas.
func (s *Sampler) Sample(now time.Time) (Reading, bool, error) {
	cpu, err := readCPU(filepath.Join(s.procPath, "stat"))
	if err != nil {
		return Reading{}, false, fmt.Errorf("read cpu: %w", err)
	}
	net, err := readNet(filepath.Join(s.procPath, "net", "dev"))
	if err != nil {
		return Reading{}, false, fmt.Errorf("read net: %w", err)
	}
	memory, err := readMeminfo(filepath.Join(s.procPath, "meminfo"))
	if err != nil {
		return Reading{}, false, fmt.Errorf("read meminfo: %w", err)
	}
	load, err := readLoad(filepath.Join(s.procPath, "loadavg"))
	if err != nil {
		return Reading{}, false, fmt.Errorf("read loadavg: %w", err)
	}
	if !s.primed {
		s.remember(cpu, net, now)
		return Reading{}, false, nil
	}
	elapsed := now.Sub(s.prevAt)
	reading := Reading{
		SampledAt:  now,
		CPUPercent: cpuPercent(s.prevCPU, cpu),
		CPUCores:   cpu.cores,
		Load1:      load,
		MemUsed:    memory.total - memory.available,
		MemTotal:   memory.total,
		SwapUsed:   memory.swapTotal - memory.swapFree,
		SwapTotal:  memory.swapTotal,
	}
	reading.NetRxPerSecond, reading.NetTxPerSecond = netRates(s.prevNet, net, elapsed)
	reading.DiskUsed, reading.DiskTotal = diskUsage(s.rootPath)
	s.remember(cpu, net, now)
	return reading, true, nil
}

func (s *Sampler) remember(cpu cpuCounters, net netCounters, at time.Time) {
	s.prevCPU = cpu
	s.prevNet = net
	s.prevAt = at
	s.primed = true
}

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path) // #nosec G304 -- un chemin sous /proc, ou une fixture de test.
}
