package sampler

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const statBefore = `cpu  1000 10 200 8000 50 0 40 0 0 0
cpu0 500 5 100 4000 25 0 20 0 0 0
cpu1 500 5 100 4000 25 0 20 0 0 0
intr 12345
`

// 250 jiffies actifs de plus sur 1000 écoulés : 25 %.
const statAfter = `cpu  1250 10 240 8750 50 0 0 0 0 0
cpu0 600 5 120 4350 30 0 20 0 0 0
cpu1 600 5 120 4350 30 0 20 0 0 0
`

const meminfo = `MemTotal:        8000000 kB
MemFree:         1000000 kB
MemAvailable:    5000000 kB
Buffers:          100000 kB
SwapTotal:       2000000 kB
SwapFree:        1500000 kB
`

const netBefore = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 500000   100    0    0    0     0          0         0 500000   100    0    0    0     0       0          0
  eth0: 1000000  200    0    0    0     0          0         0 3000000  300    0    0    0     0       0          0
`

const netAfter = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 900000   100    0    0    0     0          0         0 900000   100    0    0    0     0       0          0
  eth0: 1100000  200    0    0    0     0          0         0 3050000  300    0    0    0     0       0          0
`

// Un faux /proc dans un dossier temporaire ; les fichiers se réécrivent
// entre deux lectures.
type fakeProc struct {
	t    *testing.T
	path string
}

func newFakeProc(t *testing.T) fakeProc {
	t.Helper()
	proc := fakeProc{t: t, path: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(proc.path, "net"), 0o750); err != nil {
		t.Fatal(err)
	}
	return proc
}

func (p fakeProc) write(name, content string) {
	p.t.Helper()
	if err := os.WriteFile(filepath.Join(p.path, name), []byte(content), 0o600); err != nil {
		p.t.Fatal(err)
	}
}

func TestSample_FirstReadingArmsTheCounters_SecondOneMeasures(t *testing.T) {
	proc := newFakeProc(t)
	proc.write("stat", statBefore)
	proc.write("meminfo", meminfo)
	proc.write("loadavg", "0.90 1.20 1.50 2/300 4242\n")
	proc.write("net/dev", netBefore)
	// Deux montages du même volume, et un tmpfs : une seule mesure, faite
	// par statfs sur un dossier qui existe.
	proc.write("mounts", "/dev/fake "+proc.path+"/data ext4 rw 0 0\n/dev/fake "+proc.path+" ext4 rw 0 0\ntmpfs /run tmpfs rw 0 0\n")
	sampler := NewAt(proc.path)
	start := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)

	if _, ok, err := sampler.Sample(start); ok || err != nil {
		t.Fatalf("first reading: ok=%v err=%v", ok, err)
	}

	proc.write("stat", statAfter)
	proc.write("net/dev", netAfter)
	reading, ok, err := sampler.Sample(start.Add(10 * time.Second))
	if !ok || err != nil {
		t.Fatalf("second reading: ok=%v err=%v", ok, err)
	}
	if reading.CPUPercent != 25 || reading.CPUCores != 2 || reading.Load1 != 0.9 {
		t.Errorf("cpu %+v", reading)
	}
	if reading.MemTotal != 8000000*1024 || reading.MemUsed != 3000000*1024 {
		t.Errorf("memory %+v", reading)
	}
	if reading.SwapTotal != 2000000*1024 || reading.SwapUsed != 500000*1024 {
		t.Errorf("swap %+v", reading)
	}
	// La boucle locale a bougé de 400 000 octets et ne compte pas.
	if reading.NetRxPerSecond != 10000 || reading.NetTxPerSecond != 5000 {
		t.Errorf("net %+v", reading)
	}
	if len(reading.Disks) != 1 || reading.Disks[0].MountPoint != proc.path || reading.Disks[0].Device != "/dev/fake" {
		t.Errorf("disks %+v", reading.Disks)
	}
	if reading.DiskTotal == 0 || reading.DiskUsed == 0 || reading.DiskUsed > reading.DiskTotal || reading.DiskTotal != reading.Disks[0].Total {
		t.Errorf("disk %+v", reading)
	}
	if !reading.SampledAt.Equal(start.Add(10 * time.Second)) {
		t.Errorf("sampled at %v", reading.SampledAt)
	}
}

func TestCPUPercent_ACounterThatGoesBackwardsGivesZero(t *testing.T) {
	if got := cpuPercent(cpuCounters{active: 500, total: 1000}, cpuCounters{active: 10, total: 20}); got != 0 {
		t.Fatalf("got %v", got)
	}
	if got := cpuPercent(cpuCounters{active: 500, total: 1000}, cpuCounters{active: 500, total: 1000}); got != 0 {
		t.Fatalf("no elapsed time: %v", got)
	}
}

func TestReadCPU_RefusesAFileWithoutTheCPULine(t *testing.T) {
	proc := newFakeProc(t)
	proc.write("stat", "intr 1\n")
	if _, err := readCPU(filepath.Join(proc.path, "stat")); err == nil {
		t.Fatal("accepted")
	}
	if _, err := readCPU(filepath.Join(proc.path, "absent")); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestReadMeminfo_RefusesAFileWithoutMemTotal(t *testing.T) {
	proc := newFakeProc(t)
	proc.write("meminfo", "MemFree: 12 kB\n")
	if _, err := readMeminfo(filepath.Join(proc.path, "meminfo")); err == nil {
		t.Fatal("accepted")
	}
}

func TestSample_OnThisMachine_ReadsTheRealProc(t *testing.T) {
	if _, err := os.Stat("/proc/stat"); err != nil {
		t.Skip("no /proc here")
	}
	sampler := New()
	now := time.Now()
	if _, _, err := sampler.Sample(now); err != nil {
		t.Fatal(err)
	}
	reading, ok, err := sampler.Sample(now.Add(time.Second))
	if !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if reading.CPUCores == 0 || reading.MemTotal == 0 || reading.DiskTotal == 0 || len(reading.Disks) == 0 {
		t.Fatalf("empty reading %+v", reading)
	}
}
