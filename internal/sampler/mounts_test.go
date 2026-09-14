package sampler

import (
	"path/filepath"
	"testing"
)

func mountPoints(mounts []mount) []string {
	points := make([]string, 0, len(mounts))
	for _, entry := range mounts {
		points = append(points, entry.mountPoint)
	}
	return points
}

func assertMounts(t *testing.T, fixture string, want ...string) []mount {
	t.Helper()
	mounts, err := readMounts(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	if got := mountPoints(mounts); len(got) != len(want) || !equalStrings(got, want) {
		t.Fatalf("%s: got %v, want %v", fixture, got, want)
	}
	return mounts
}

func equalStrings(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Un btrfs monté trois fois, en `/`, `/home` et un sous-volume remonté,
// fait un seul volume, nommé `/`. Les snaps, l'amorçage, les pseudo-systèmes
// et le montage du portail ne comptent pas.
func TestReadMounts_ABtrfsRootAndHome_MakeOneVolume(t *testing.T) {
	mounts := assertMounts(t, "mounts_btrfs.txt", "/")
	if mounts[0].device != "/dev/nvme0n1p2" || mounts[0].fstype != "btrfs" {
		t.Fatalf("volume %+v", mounts[0])
	}
}

// Un VPS classique : le système et les données, sans la partition EFI.
func TestReadMounts_AVPSWithSystemAndData_MakesTwoVolumes(t *testing.T) {
	mounts := assertMounts(t, "mounts_vps.txt", "/", "/data")
	if mounts[1].device != "/dev/vdb" || mounts[1].fstype != "xfs" {
		t.Fatalf("data volume %+v", mounts[1])
	}
}

// Les snaps et les images en boucle, les couches et les disques dédiés aux
// conteneurs, les montages réseau et /boot sont exclus ; un point de
// montage avec une espace se lit tel quel.
func TestReadMounts_SnapsContainersAndNetwork_AreLeftOut(t *testing.T) {
	assertMounts(t, "mounts_containers.txt", "/", "/mnt/disque externe")
}

func TestReadMounts_MissingFile_IsAnError(t *testing.T) {
	if _, err := readMounts(filepath.Join(t.TempDir(), "mounts")); err == nil {
		t.Fatal("accepted")
	}
}

func TestSumDisks_AddsEveryVolume(t *testing.T) {
	used, total := sumDisks([]Disk{{Used: 1, Total: 10}, {Used: 2, Total: 20}})
	if used != 3 || total != 30 {
		t.Fatalf("got %d/%d", used, total)
	}
	if used, total := sumDisks(nil); used != 0 || total != 0 {
		t.Fatalf("empty: %d/%d", used, total)
	}
}
