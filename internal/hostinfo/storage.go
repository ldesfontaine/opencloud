package hostinfo

import (
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strings"
)

const (
	blockDir = "sys/block"
	zfsDir   = "proc/spl/kstat/zfs"
	// sysfs compte toujours en secteurs de 512 octets, quelle que soit la
	// taille de secteur du disque.
	sectorBytes = 512
)

// Les noms de disques physiques ou virtuels du noyau. Les boucles, la RAM, les
// lecteurs optiques et les périphériques de volume (dm-*, md*) n'en sont pas.
var diskPrefixes = []string{"sd", "hd", "vd", "xvd", "nvme", "mmcblk"}

var (
	mdPattern = regexp.MustCompile(`^md[0-9]+$`)
	// nvme0n1p2 et mmcblk0p1 portent un « p » avant le numéro de partition.
	partitionSuffix = regexp.MustCompile(`^(.*[0-9])p[0-9]+$|^(.*[a-z])[0-9]+$`)
)

func readStorage(root fs.FS) Storage {
	names := sortedNames(root, blockDir)
	storage := Storage{Disks: []Disk{}, Pools: []Pool{}}
	for _, name := range names {
		if disk, ok := readDisk(root, name); ok && len(storage.Disks) < MaxDisks {
			storage.Disks = append(storage.Disks, disk)
		}
	}
	pools := append(readMDPools(root, names), readLVMPools(root, names)...)
	pools = append(pools, readZFSPools(root)...)
	storage.Pools = pools[:min(len(pools), MaxPools)]
	return storage
}

func readDisk(root fs.FS, name string) (Disk, bool) {
	if !isDiskName(name) {
		return Disk{}, false
	}
	sectors, ok := readNumber(root, blockDir+"/"+name+"/size")
	// Un lecteur de cartes vide expose un disque de taille nulle.
	if !ok || sectors <= 0 {
		return Disk{}, false
	}
	model := readText(root, blockDir+"/"+name+"/device/model")
	disk := Disk{
		Name:      name,
		Model:     truncateBytes(model, maxModelBytes),
		Size:      sectors * sectorBytes,
		Media:     mediaOf(root, name, model),
		Removable: readText(root, blockDir+"/"+name+"/removable") == "1",
	}
	return disk, true
}

func isDiskName(name string) bool {
	for _, prefix := range diskPrefixes {
		if strings.HasPrefix(name, prefix) {
			return namePattern.MatchString(name)
		}
	}
	return false
}

// mediaOf devine la nature du disque. Un disque virtuel annonce souvent
// « rotationnel » sans l'être : le dire serait mentir, il est donc « virtuel ».
func mediaOf(root fs.FS, name, model string) string {
	switch {
	case strings.HasPrefix(name, "vd"), strings.HasPrefix(name, "xvd"), strings.HasPrefix(model, "QEMU"), strings.HasPrefix(model, "VBOX"), strings.HasPrefix(model, "VMware"):
		return MediaVirtual
	case strings.HasPrefix(name, "nvme"):
		return MediaNVMe
	}
	switch readText(root, blockDir+"/"+name+"/queue/rotational") {
	case "1":
		return MediaHDD
	case "0":
		return MediaSSD
	}
	return MediaUnknown
}

// readMDPools relit les grappes RAID du noyau (md0, md1…).
func readMDPools(root fs.FS, blockNames []string) []Pool {
	pools := []Pool{}
	for _, name := range blockNames {
		if !mdPattern.MatchString(name) {
			continue
		}
		sectors, _ := readNumber(root, blockDir+"/"+name+"/size")
		degraded, _ := readNumber(root, blockDir+"/"+name+"/md/degraded")
		pools = append(pools, Pool{
			Name:    name,
			Kind:    PoolMD,
			Level:   truncateBytes(readText(root, blockDir+"/"+name+"/md/level"), maxLevelBytes),
			State:   mdState(readText(root, blockDir+"/"+name+"/md/array_state"), degraded),
			Size:    sectors * sectorBytes,
			Members: memberDisks(sortedNames(root, blockDir+"/"+name+"/slaves")),
		})
	}
	return pools
}

// mdState : une grappe arrêtée est en panne, une grappe qui a perdu un disque
// tourne dégradée. Un état que le noyau ne dit pas reste inconnu.
func mdState(arrayState string, degraded int64) string {
	switch {
	case arrayState == "":
		return PoolUnknown
	case arrayState == "inactive":
		return PoolFailed
	case degraded > 0:
		return PoolDegraded
	}
	return PoolOK
}

// readLVMPools regroupe les volumes logiques LVM par groupe de volumes. Le
// noyau ne dit pas la santé d'un groupe sans la commande lvm : son état reste
// inconnu. Sa taille est celle des disques ou partitions qui le portent.
func readLVMPools(root fs.FS, blockNames []string) []Pool {
	type group struct {
		sizes   map[string]int64
		members []string
	}
	groups := map[string]*group{}
	for _, name := range blockNames {
		if !strings.HasPrefix(name, "dm-") || !strings.HasPrefix(readText(root, blockDir+"/"+name+"/dm/uuid"), "LVM-") {
			continue
		}
		volumeGroup := volumeGroupOf(readText(root, blockDir+"/"+name+"/dm/name"))
		if volumeGroup == "" || !namePattern.MatchString(volumeGroup) {
			continue
		}
		found := groups[volumeGroup]
		if found == nil {
			found = &group{sizes: map[string]int64{}}
			groups[volumeGroup] = found
		}
		for _, slave := range sortedNames(root, blockDir+"/"+name+"/slaves") {
			if strings.HasPrefix(slave, "dm-") {
				continue
			}
			found.sizes[slave] = blockSize(root, slave)
			found.members = append(found.members, slave)
		}
	}
	pools := []Pool{}
	for _, volumeGroup := range slices.Sorted(maps.Keys(groups)) {
		found := groups[volumeGroup]
		var size int64
		for _, bytes := range found.sizes {
			size += bytes
		}
		pools = append(pools, Pool{
			Name: volumeGroup, Kind: PoolLVM, State: PoolUnknown, Size: size, Members: memberDisks(found.members),
		})
	}
	return pools
}

// volumeGroupOf lit le groupe dans le nom « groupe-volume » de device-mapper,
// où un tiret du nom est doublé.
func volumeGroupOf(mapperName string) string {
	var group strings.Builder
	for index := 0; index < len(mapperName); index++ {
		if mapperName[index] != '-' {
			group.WriteByte(mapperName[index])
			continue
		}
		if index+1 < len(mapperName) && mapperName[index+1] == '-' {
			group.WriteByte('-')
			index++
			continue
		}
		return group.String()
	}
	return ""
}

// blockSize rend la taille d'un disque, ou d'une partition rangée sous son disque.
func blockSize(root fs.FS, name string) int64 {
	if sectors, ok := readNumber(root, blockDir+"/"+name+"/size"); ok {
		return sectors * sectorBytes
	}
	if sectors, ok := readNumber(root, blockDir+"/"+parentDisk(name)+"/"+name+"/size"); ok {
		return sectors * sectorBytes
	}
	return 0
}

// readZFSPools lit l'état des pools ZFS que le module du noyau expose. Taille
// et disques demandent la commande zpool : ils restent vides.
func readZFSPools(root fs.FS) []Pool {
	pools := []Pool{}
	for _, name := range sortedNames(root, zfsDir) {
		state := readText(root, zfsDir+"/"+name+"/state")
		if state == "" || !namePattern.MatchString(name) {
			continue
		}
		pools = append(pools, Pool{Name: name, Kind: PoolZFS, State: zfsState(state), Members: []string{}})
	}
	return pools
}

func zfsState(state string) string {
	switch state {
	case "ONLINE":
		return PoolOK
	case "DEGRADED":
		return PoolDegraded
	case "FAULTED", "UNAVAIL", "SUSPENDED", "REMOVED":
		return PoolFailed
	}
	return PoolUnknown
}

// memberDisks ramène des partitions à leur disque, sans doublon.
func memberDisks(names []string) []string {
	disks := []string{}
	for _, name := range names {
		disk := parentDisk(name)
		if !slices.Contains(disks, disk) {
			disks = append(disks, disk)
		}
	}
	slices.Sort(disks)
	return disks
}

// parentDisk rend le disque d'une partition (sda1 → sda, nvme0n1p2 → nvme0n1) ;
// un disque entier, ou un nom hors des disques du noyau, se rend tel quel.
func parentDisk(name string) string {
	if !isDiskName(name) {
		return name
	}
	match := partitionSuffix.FindStringSubmatch(name)
	switch {
	case match == nil:
		return name
	case match[1] != "":
		return match[1]
	}
	// « nvme0n1 » finit par un chiffre mais n'est pas une partition : seul un
	// nom de disque à lettres (sda1, vdb2) perd son numéro.
	if strings.HasPrefix(name, "nvme") || strings.HasPrefix(name, "mmcblk") {
		return name
	}
	return match[2]
}

func truncateBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && text[cut]&0xC0 == 0x80 {
		cut--
	}
	return text[:cut]
}
