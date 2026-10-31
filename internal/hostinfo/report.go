package hostinfo

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Report est ce que l'agent sait de l'endroit où tourne sa machine et de ses
// disques : il change rarement, il part quand il change et à chaque ouverture
// du flux. Lecture seule, aucun droit demandé.
type Report struct {
	// Virt est la réponse brute de systemd-detect-virt (« none », « kvm »,
	// « lxc »…) ; vide quand la commande manque : le type reste inconnu.
	Virt       string      `json:"virt"`
	Interfaces []Interface `json:"interfaces"`
	Storage
}

// Interface est une carte réseau de la machine : de quoi la reconnaître dans
// la configuration de son hôte.
type Interface struct {
	Name string `json:"name"`
	MAC  string `json:"mac"`
}

// Storage est ce que la machine a comme disques physiques et comme pools. Aucun
// relevé SMART : il viendra comme un droit à part de l'agent.
type Storage struct {
	Disks []Disk `json:"disks"`
	Pools []Pool `json:"pools"`
}

// Disk est un disque que le noyau expose : jamais une partition ni un volume.
type Disk struct {
	Name      string `json:"name"`
	Model     string `json:"model"`
	Size      int64  `json:"size"`
	Media     string `json:"media"`
	Removable bool   `json:"removable"`
}

// Pool regroupe des disques en un seul espace : une grappe RAID du noyau, un
// groupe de volumes LVM, un pool ZFS. Size vaut 0 quand le noyau ne le dit
// pas sans commande (ZFS).
type Pool struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Level   string   `json:"level"`
	State   string   `json:"state"`
	Size    int64    `json:"size"`
	Members []string `json:"members"`
}

const (
	MediaNVMe    = "nvme"
	MediaSSD     = "ssd"
	MediaHDD     = "hdd"
	MediaVirtual = "virtual"
	MediaUnknown = "unknown"

	PoolMD  = "md"
	PoolLVM = "lvm"
	PoolZFS = "zfs"

	PoolOK       = "ok"
	PoolDegraded = "degraded"
	PoolFailed   = "failed"
	PoolUnknown  = "unknown"
)

// DiskMedia, PoolKinds et PoolStates sont les listes fermées : chacune a ses
// libellés dans les catalogues, sous storage.media_<nom>, storage.pool_<nom>
// et storage.state_<nom>.
func DiskMedia() []string {
	return []string{MediaNVMe, MediaSSD, MediaHDD, MediaVirtual, MediaUnknown}
}

func PoolKinds() []string { return []string{PoolMD, PoolLVM, PoolZFS} }

func PoolStates() []string {
	return []string{PoolOK, PoolDegraded, PoolFailed, PoolUnknown}
}

// Les bornes d'un rapport : un pair peut mentir, la liste est finie.
const (
	MaxInterfaces = 32
	MaxDisks      = 128
	MaxPools      = 64
	MaxMembers    = 64
	maxModelBytes = 64
	maxLevelBytes = 16
)

// ErrReportInvalid : le rapport sort de ce qu'un agent peut avoir relevé. Il
// est refusé en entier.
var ErrReportInvalid = errors.New("host report out of range")

var (
	virtPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)
	macPattern  = regexp.MustCompile(`^([0-9a-f]{2}:){5}[0-9a-f]{2}$`)
	// Un nom de noyau (eth0, sda, md0, nvme0n1) ou de volume (vg-data, tank).
	namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@:+-]{0,63}$`)
)

// IsMAC dit si un texte est une adresse MAC en minuscules, deux-points compris.
func IsMAC(text string) bool {
	return macPattern.MatchString(text)
}

func (r Report) Validate() error {
	if r.Virt != "" && !virtPattern.MatchString(r.Virt) {
		return ErrReportInvalid
	}
	if len(r.Interfaces) > MaxInterfaces {
		return ErrReportInvalid
	}
	seen := map[string]bool{}
	for _, iface := range r.Interfaces {
		if !namePattern.MatchString(iface.Name) || !IsMAC(iface.MAC) || seen[iface.Name] {
			return ErrReportInvalid
		}
		seen[iface.Name] = true
	}
	return r.Storage.validate()
}

func (s Storage) validate() error {
	if len(s.Disks) > MaxDisks || len(s.Pools) > MaxPools {
		return ErrReportInvalid
	}
	diskNames := map[string]bool{}
	for _, disk := range s.Disks {
		if !namePattern.MatchString(disk.Name) || diskNames[disk.Name] || disk.Size < 0 ||
			!slices.Contains(DiskMedia(), disk.Media) || !isShortText(disk.Model, maxModelBytes) {
			return ErrReportInvalid
		}
		diskNames[disk.Name] = true
	}
	poolNames := map[string]bool{}
	for _, pool := range s.Pools {
		if !namePattern.MatchString(pool.Name) || poolNames[pool.Name] || pool.Size < 0 ||
			!slices.Contains(PoolKinds(), pool.Kind) || !slices.Contains(PoolStates(), pool.State) ||
			!isShortText(pool.Level, maxLevelBytes) || len(pool.Members) > MaxMembers {
			return ErrReportInvalid
		}
		poolNames[pool.Name] = true
		for _, member := range pool.Members {
			if !namePattern.MatchString(member) {
				return ErrReportInvalid
			}
		}
	}
	return nil
}

// isShortText : un texte venu d'un pair, qui finit dans l'interface.
func isShortText(text string, limit int) bool {
	return len(text) <= limit && !strings.ContainsFunc(text, unicode.IsControl)
}
