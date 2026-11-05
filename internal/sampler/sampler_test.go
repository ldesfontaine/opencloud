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
	if reading.MemTotal != 8000000*1024 || reading.MemUsed != 3000000*1024 || reading.ZFSCache != nil {
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

// Un arcstats de Pi de sauvegarde en ZFS : l'ARC pèse 9,9 Go, son plancher
// est de 100 Mo.
const arcstats = `13 1 0x01 98 26656 7195180000 123456789
name                            type data
hits                            4    123456
size                            4    9900000000
c                               4    14000000000
c_min                           4    100000000
c_max                           4    14800000000
`

func TestMemoryUsed_SubtractsTheArcThatZFSGivesBack(t *testing.T) {
	proc := newFakeProc(t)
	proc.write("meminfo", meminfo) // utilisé : 3 072 000 000 octets
	arcPath := filepath.Join(proc.path, "arcstats")
	memory, err := readMeminfo(filepath.Join(proc.path, "meminfo"))
	if err != nil {
		t.Fatal(err)
	}
	// Machine sans ZFS : le fichier n'existe pas, rien ne change.
	used, cache := memoryUsed(memory, arcPath)
	if used != 3000000*1024 || cache != nil {
		t.Fatalf("without zfs: used=%d cache=%v", used, cache)
	}
	// 3 072 000 000 − (9 900 000 000 − 100 000 000) serait négatif : borné à 0.
	proc.write("arcstats", arcstats)
	used, cache = memoryUsed(memory, arcPath)
	if used != 0 || cache == nil || *cache != 9900000000 {
		t.Fatalf("arc larger than used: used=%d cache=%v", used, cache)
	}
	// Le cas du ticket : 11 Gio « utilisés », dont 9,8 Go d'ARC rendables.
	memory.available = memory.total - 11*1024*1024*1024
	used, _ = memoryUsed(memory, arcPath)
	if want := 11*1024*1024*1024 - 9800000000; used != int64(want) {
		t.Fatalf("used=%d want %d", used, want)
	}
}

func TestReadArcstats_AFileWithoutSizeIsIgnored(t *testing.T) {
	proc := newFakeProc(t)
	proc.write("arcstats", "name type data\nc_min 4 100\n")
	if _, ok := readArcstats(filepath.Join(proc.path, "arcstats")); ok {
		t.Fatal("accepted a file without size")
	}
}

func TestSample_WithZFS_ReportsTheCorrectedMemoryAndTheCache(t *testing.T) {
	proc := newFakeProc(t)
	proc.write("stat", statBefore)
	proc.write("meminfo", "MemTotal: 16000000 kB\nMemAvailable: 5000000 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n")
	proc.write("loadavg", "0.10 0.10 0.10 1/1 1\n")
	proc.write("net/dev", netBefore)
	proc.write("mounts", "tmpfs /run tmpfs rw 0 0\n")
	if err := os.MkdirAll(filepath.Join(proc.path, "spl", "kstat", "zfs"), 0o750); err != nil {
		t.Fatal(err)
	}
	proc.write("spl/kstat/zfs/arcstats", arcstats)
	sampler := NewAt(proc.path)
	start := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	sampler.Sample(start)
	reading, ok, err := sampler.Sample(start.Add(10 * time.Second))
	if !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	// 11 000 000 kB utilisés − 9 800 000 000 octets rendables.
	if want := int64(11000000*1024 - 9800000000); reading.MemUsed != want {
		t.Errorf("mem used %d want %d", reading.MemUsed, want)
	}
	if reading.ZFSCache == nil || *reading.ZFSCache != 9900000000 {
		t.Errorf("zfs cache %v", reading.ZFSCache)
	}
}
