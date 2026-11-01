package hostinfo

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

func file(content string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(content + "\n")} }

// aMachine est une machine à deux disques, une grappe RAID1 sur leurs
// partitions, un groupe LVM sur un troisième, et un pool ZFS.
func aMachine() fstest.MapFS {
	return fstest.MapFS{
		"sys/class/net/lo/address":      file("00:00:00:00:00:00"),
		"sys/class/net/eth0/address":    file("BC:24:11:AA:BB:CC"),
		"sys/class/net/docker0/address": file("02:42:ac:11:00:01"),
		"sys/class/net/veth12/address":  file("aa:aa:aa:aa:aa:aa"),
		"sys/class/net/wg0/address":     file("00:00:00:00:00:00"),

		"sys/block/loop0/size":               file("2048"),
		"sys/block/sda/size":                 file("1953525168"),
		"sys/block/sda/removable":            file("0"),
		"sys/block/sda/queue/rotational":     file("1"),
		"sys/block/sda/device/model":         file("WDC WD10EZEX-00BN"),
		"sys/block/sda/sda1/size":            file("976000000"),
		"sys/block/sdb/size":                 file("1953525168"),
		"sys/block/sdb/queue/rotational":     file("1"),
		"sys/block/sdb/device/model":         file("WDC WD10EZEX-00BN"),
		"sys/block/nvme0n1/size":             file("1000215216"),
		"sys/block/nvme0n1/queue/rotational": file("0"),
		"sys/block/nvme0n1/device/model":     file("Samsung SSD 980"),
		"sys/block/nvme0n1/nvme0n1p3/size":   file("900000000"),
		"sys/block/mmcblk0/size":             file("0"),
		"sys/block/md0/size":                 file("1952000000"),
		"sys/block/md0/md/level":             file("raid1"),
		"sys/block/md0/md/array_state":       file("clean"),
		"sys/block/md0/md/degraded":          file("0"),
		"sys/block/md0/slaves/sda1":          file(""),
		"sys/block/md0/slaves/sdb1":          file(""),
		"sys/block/dm-0/dm/uuid":             file("LVM-abcdef"),
		"sys/block/dm-0/dm/name":             file("vg--data-root"),
		"sys/block/dm-0/slaves/nvme0n1p3":    file(""),
		"sys/block/dm-1/dm/uuid":             file("CRYPT-LUKS2-xyz"),
		"sys/block/dm-1/dm/name":             file("secret"),
		"proc/spl/kstat/zfs/arcstats":        file("12 3"),
		"proc/spl/kstat/zfs/tank/state":      file("DEGRADED"),
		"proc/spl/kstat/zfs/tank/io":         file("x"),
		"proc/spl/kstat/zfs/rpool/state":     file("ONLINE"),
	}
}

func TestDetect_Interfaces_KeepsOnlyRealCards(t *testing.T) {
	report := Detect(aMachine(), "kvm")
	want := []Interface{{Name: "eth0", MAC: "bc:24:11:aa:bb:cc"}}
	if !reflect.DeepEqual(report.Interfaces, want) {
		t.Fatalf("got %+v", report.Interfaces)
	}
	if report.Virt != "kvm" {
		t.Fatalf("virt %q", report.Virt)
	}
}

func TestDetect_Disks_PhysicalOnlyWithSizeAndMedia(t *testing.T) {
	report := Detect(aMachine(), "none")
	got := map[string]Disk{}
	for _, disk := range report.Disks {
		got[disk.Name] = disk
	}
	if len(got) != 3 {
		t.Fatalf("disks: %+v", report.Disks)
	}
	if got["sda"].Size != 1953525168*512 || got["sda"].Media != MediaHDD || got["sda"].Model != "WDC WD10EZEX-00BN" {
		t.Errorf("sda: %+v", got["sda"])
	}
	if got["nvme0n1"].Media != MediaNVMe {
		t.Errorf("nvme: %+v", got["nvme0n1"])
	}
}

func TestDetect_Disks_AVirtualDiskIsNeverCalledRotational(t *testing.T) {
	root := fstest.MapFS{
		"sys/block/vda/size":             file("2097152"),
		"sys/block/vda/queue/rotational": file("1"),
	}
	disks := Detect(root, "kvm").Disks
	if len(disks) != 1 || disks[0].Media != MediaVirtual {
		t.Fatalf("got %+v", disks)
	}
}

func TestDetect_Pools_RaidLvmZfs(t *testing.T) {
	report := Detect(aMachine(), "none")
	got := map[string]Pool{}
	for _, pool := range report.Pools {
		got[pool.Name] = pool
	}
	if len(got) != 4 {
		t.Fatalf("pools: %+v", report.Pools)
	}
	md := got["md0"]
	if md.Kind != PoolMD || md.Level != "raid1" || md.State != PoolOK || !reflect.DeepEqual(md.Members, []string{"sda", "sdb"}) {
		t.Errorf("md0: %+v", md)
	}
	lvm := got["vg-data"]
	if lvm.Kind != PoolLVM || lvm.Size != 900000000*512 || !reflect.DeepEqual(lvm.Members, []string{"nvme0n1"}) {
		t.Errorf("vg-data: %+v", lvm)
	}
	if got["tank"].State != PoolDegraded || got["rpool"].State != PoolOK {
		t.Errorf("zfs: %+v %+v", got["tank"], got["rpool"])
	}
}

func TestMDState(t *testing.T) {
	cases := []struct {
		state    string
		degraded int64
		want     string
	}{{"clean", 0, PoolOK}, {"clean", 1, PoolDegraded}, {"inactive", 0, PoolFailed}, {"", 0, PoolUnknown}}
	for _, test := range cases {
		if got := mdState(test.state, test.degraded); got != test.want {
			t.Errorf("%+v: %q", test, got)
		}
	}
}

func TestParentDisk(t *testing.T) {
	cases := map[string]string{
		"sda1": "sda", "sda": "sda", "vdb2": "vdb", "xvda1": "xvda", "nvme0n1p2": "nvme0n1", "nvme0n1": "nvme0n1",
		"mmcblk0p1": "mmcblk0", "mmcblk0": "mmcblk0", "dm-3": "dm-3",
	}
	for name, want := range cases {
		if got := parentDisk(name); got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}

func TestVolumeGroupOf_DoubledDashIsPartOfTheName(t *testing.T) {
	cases := map[string]string{"vg--data-root": "vg-data", "pve-root": "pve", "pve": ""}
	for name, want := range cases {
		if got := volumeGroupOf(name); got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}

func TestDetect_NothingReadable_IsAnEmptyReportNotAnError(t *testing.T) {
	report := Detect(fstest.MapFS{}, "")
	if len(report.Interfaces) != 0 || len(report.Disks) != 0 || len(report.Pools) != 0 {
		t.Fatalf("got %+v", report)
	}
	if err := report.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestReportValidate_ClosedLists(t *testing.T) {
	valid := Detect(aMachine(), "kvm")
	if err := valid.Validate(); err != nil {
		t.Fatalf("a real report must validate: %v", err)
	}
	cases := map[string]func(*Report){
		"virt en HTML":      func(r *Report) { r.Virt = "<b>" },
		"MAC en majuscules": func(r *Report) { r.Interfaces[0].MAC = "BC:24:11:AA:BB:CC" },
		"MAC courte":        func(r *Report) { r.Interfaces[0].MAC = "bc:24" },
		"interface en double": func(r *Report) {
			r.Interfaces = append(r.Interfaces, r.Interfaces[0])
		},
		"média inconnu":     func(r *Report) { r.Disks[0].Media = "floppy" },
		"taille négative":   func(r *Report) { r.Disks[0].Size = -1 },
		"modèle à rallonge": func(r *Report) { r.Disks[0].Model = strings.Repeat("x", 80) },
		"genre de pool":     func(r *Report) { r.Pools[0].Kind = "ceph" },
		"état de pool":      func(r *Report) { r.Pools[0].State = "on fire" },
		"membre invalide":   func(r *Report) { r.Pools[0].Members = []string{"../etc"} },
	}
	for name, mutate := range cases {
		report := Detect(aMachine(), "kvm")
		mutate(&report)
		if err := report.Validate(); !errors.Is(err, ErrReportInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	tooMany := Report{}
	for index := range MaxDisks + 1 {
		tooMany.Disks = append(tooMany.Disks, Disk{Name: "sd" + strconv.Itoa(index), Media: MediaSSD})
	}
	if err := tooMany.Validate(); !errors.Is(err, ErrReportInvalid) {
		t.Errorf("trop de disques: %v", err)
	}
}

// La vraie machine qui joue le test : ce que l'agent enverrait doit passer le
// contrôle du serveur, quel que soit le matériel.
func TestRead_TheRealMachineProducesAValidReport(t *testing.T) {
	report := Read(t.Context())
	if err := report.Validate(); err != nil {
		t.Fatalf("%+v: %v", report, err)
	}
}
