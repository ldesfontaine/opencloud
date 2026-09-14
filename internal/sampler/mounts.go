package sampler

import (
	"bufio"
	"bytes"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// Disk est un volume réel de la machine : un système de fichiers de
// disque, présenté par son point de montage le plus court.
type Disk struct {
	MountPoint string `json:"mount_point"`
	Device     string `json:"device"`
	Used       int64  `json:"used"`
	Total      int64  `json:"total"`
}

// Les systèmes de fichiers qui vivent sur un disque de la machine. Tout le
// reste, pseudo-systèmes du noyau, tmpfs, overlay des conteneurs,
// squashfs des snaps, montages réseau, n'est pas un volume à surveiller.
var diskFilesystems = map[string]bool{
	"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true, "zfs": true,
	"f2fs": true, "jfs": true, "vfat": true, "exfat": true, "ntfs": true, "ntfs3": true,
	"fuseblk": true,
}

// Les montages qu'on ne montre pas même sur un vrai disque : l'amorçage,
// les snaps, les couches des conteneurs.
var hiddenMountPrefixes = []string{"/boot", "/snap", "/var/lib/docker", "/var/lib/containers"}

// Un périphérique en boucle porte une image, jamais un disque.
const loopDevicePrefix = "/dev/loop"

type mount struct {
	device     string
	mountPoint string
	fstype     string
}

// readMounts lit /proc/mounts et garde un montage par périphérique : `/` et
// `/home` sur le même btrfs font un seul volume, nommé `/`. Le résultat
// est trié par point de montage.
func readMounts(path string) ([]mount, error) {
	content, err := readFile(path)
	if err != nil {
		return nil, err
	}
	byDevice := map[string]mount{}
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		entry, ok := parseMount(scanner.Text())
		if !ok || !isDiskMount(entry) {
			continue
		}
		known, seen := byDevice[entry.device]
		if !seen || shorterMountPoint(entry.mountPoint, known.mountPoint) {
			byDevice[entry.device] = entry
		}
	}
	mounts := make([]mount, 0, len(byDevice))
	for _, entry := range byDevice {
		mounts = append(mounts, entry)
	}
	sort.Slice(mounts, func(i, j int) bool { return mounts[i].mountPoint < mounts[j].mountPoint })
	return mounts, nil
}

// parseMount lit une ligne de /proc/mounts : périphérique, point de
// montage, type, options, puis deux nombres. Les espaces des chemins y
// sont écrits `\040`.
func parseMount(line string) (mount, bool) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return mount{}, false
	}
	return mount{
		device:     unescapeMount(fields[0]),
		mountPoint: unescapeMount(fields[1]),
		fstype:     fields[2],
	}, true
}

func unescapeMount(field string) string {
	if !strings.Contains(field, `\`) {
		return field
	}
	var out strings.Builder
	for i := 0; i < len(field); i++ {
		if field[i] == '\\' && i+3 < len(field) {
			if code, err := strconv.ParseUint(field[i+1:i+4], 8, 8); err == nil {
				out.WriteByte(byte(code))
				i += 3
				continue
			}
		}
		out.WriteByte(field[i])
	}
	return out.String()
}

func isDiskMount(entry mount) bool {
	if !diskFilesystems[entry.fstype] || strings.HasPrefix(entry.device, loopDevicePrefix) {
		return false
	}
	for _, prefix := range hiddenMountPrefixes {
		if entry.mountPoint == prefix || strings.HasPrefix(entry.mountPoint, prefix+"/") {
			return false
		}
	}
	return true
}

func shorterMountPoint(candidate, current string) bool {
	if len(candidate) != len(current) {
		return len(candidate) < len(current)
	}
	return candidate < current
}

// measureDisks fait un statfs par volume, comme df : les blocs réservés à
// root comptent dans l'utilisé. Un volume qui ne se mesure pas est laissé
// de côté, jamais montré à zéro.
func measureDisks(mounts []mount) []Disk {
	disks := make([]Disk, 0, len(mounts))
	for _, entry := range mounts {
		used, total, ok := diskUsage(entry.mountPoint)
		if !ok {
			continue
		}
		disks = append(disks, Disk{MountPoint: entry.mountPoint, Device: entry.device, Used: used, Total: total})
	}
	return disks
}

func diskUsage(path string) (used, total int64, ok bool) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return 0, 0, false
	}
	blockSize := uint64(fs.Bsize)        // #nosec G115 -- une taille de bloc, toujours positive.
	total = int64(fs.Blocks * blockSize) // #nosec G115 -- des octets, loin de MaxInt64.
	free := int64(fs.Bavail * blockSize) // #nosec G115 -- idem.
	if total >= free {
		used = total - free
	}
	return used, total, total > 0
}

// sumDisks fait le total de la machine : ce que la vue d'ensemble et
// l'historique montrent.
func sumDisks(disks []Disk) (used, total int64) {
	for _, disk := range disks {
		used += disk.Used
		total += disk.Total
	}
	return used, total
}
