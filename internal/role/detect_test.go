package role

import (
	"strings"
	"testing"
	"testing/fstest"
)

const pmxcfsMount = "41 29 0:35 / /etc/pve rw,nosuid,nodev,relatime shared:60 - fuse /dev/fuse rw,user_id=0,group_id=0,default_permissions,allow_other\n"

const plainMounts = "29 1 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw\n" +
	"30 29 0:6 / /dev rw,nosuid shared:2 - devtmpfs udev rw\n"

func dpkgStanza(name, status, version string) string {
	return "Package: " + name + "\nStatus: " + status + "\nPriority: optional\nVersion: " + version +
		"\nDescription: un résumé\n une suite de description\n\n"
}

func machineFiles(status, mounts string) fstest.MapFS {
	return fstest.MapFS{
		"var/lib/dpkg/status": {Data: []byte(status)},
		"proc/self/mountinfo": {Data: []byte(mounts)},
	}
}

func TestDetect_ProxmoxPackageAndPmxcfsMount_FindsTheVersion(t *testing.T) {
	status := dpkgStanza("bash", "install ok installed", "5.2") + dpkgStanza("pve-manager", "install ok installed", "8.2.4")
	report := Detect(machineFiles(status, plainMounts+pmxcfsMount))
	if len(report.Roles) != 1 || report.Roles[0] != (Role{Name: ProxmoxVE, Version: "8.2.4"}) {
		t.Fatalf("rapport : %+v", report)
	}
}

func TestDetect_LastStanzaWithoutTrailingBlankLine_IsRead(t *testing.T) {
	status := strings.TrimSuffix(dpkgStanza("pve-manager", "install ok installed", "8.2.4"), "\n")
	report := Detect(machineFiles(status, pmxcfsMount))
	if len(report.Roles) != 1 {
		t.Fatalf("rapport : %+v", report)
	}
}

func TestDetect_APlainMachine_ReportsNoRoleButAnEmptyList(t *testing.T) {
	report := Detect(machineFiles(dpkgStanza("bash", "install ok installed", "5.2"), plainMounts))
	if report.Roles == nil || len(report.Roles) != 0 {
		t.Fatalf("rapport : %+v", report)
	}
}

func TestDetect_APartialSign_IsNotEnough(t *testing.T) {
	installed := dpkgStanza("pve-manager", "install ok installed", "8.2.4")
	removed := dpkgStanza("pve-manager", "deinstall ok config-files", "8.2.4")
	notInstalled := dpkgStanza("pve-manager", "install ok not-installed", "8.2.4")
	// Un dossier /etc/pve ordinaire, ou monté d'un autre type, n'est pas pmxcfs.
	ordinaryMount := "41 29 8:1 /pve /etc/pve rw,relatime shared:3 - ext4 /dev/sda1 rw\n"
	cases := map[string]fstest.MapFS{
		"paquet sans montage":        machineFiles(installed, plainMounts),
		"montage sans paquet":        machineFiles(dpkgStanza("bash", "install ok installed", "5.2"), pmxcfsMount),
		"paquet retiré":              machineFiles(removed, pmxcfsMount),
		"paquet jamais installé":     machineFiles(notInstalled, pmxcfsMount),
		"montage d'un autre type":    machineFiles(installed, ordinaryMount),
		"un autre paquet pve-":       machineFiles(dpkgStanza("pve-manager-extra", "install ok installed", "1.0"), pmxcfsMount),
		"status illisible":           {"proc/self/mountinfo": {Data: []byte(pmxcfsMount)}},
		"table des montages absente": {"var/lib/dpkg/status": {Data: []byte(installed)}},
	}
	for name, files := range cases {
		if report := Detect(files); len(report.Roles) != 0 {
			t.Errorf("%s : rôle détecté %+v", name, report.Roles)
		}
	}
}

// Un mot dans un champ libre ne fait pas un paquet : « Package: pve-manager »
// dans une description (ligne de suite) n'est pas lu.
func TestDetect_APackageNameInsideADescription_IsNotAPackage(t *testing.T) {
	status := "Package: bash\nStatus: install ok installed\nVersion: 5.2\nDescription: x\n Package: pve-manager\n\n"
	if report := Detect(machineFiles(status, pmxcfsMount)); len(report.Roles) != 0 {
		t.Fatalf("rapport : %+v", report)
	}
}

func TestDetect_AnOddVersion_IsDroppedButTheRoleStays(t *testing.T) {
	status := dpkgStanza("pve-manager", "install ok installed", "<script>")
	report := Detect(machineFiles(status, pmxcfsMount))
	if len(report.Roles) != 1 || report.Roles[0].Version != "" {
		t.Fatalf("rapport : %+v", report)
	}
}

func TestDetect_AMountPointThatOnlyEndsLikeEtcPve_IsNotPmxcfs(t *testing.T) {
	status := dpkgStanza("pve-manager", "install ok installed", "8.2.4")
	lookalike := "41 29 0:35 / /srv/etc/pve rw - fuse /dev/fuse rw\n42 29 0:36 / /etc/pve2 rw - fuse /dev/fuse rw\n"
	if report := Detect(machineFiles(status, lookalike)); len(report.Roles) != 0 {
		t.Fatalf("rapport : %+v", report)
	}
}
