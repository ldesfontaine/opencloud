package agentwire

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

// Les deux sortes d'invité que Proxmox gère.
const (
	GuestQEMU = "qemu"
	GuestLXC  = "lxc"
)

// Les numéros d'invité de Proxmox VE vont de 100 à 999999999.
const (
	minGuestVMID = 100
	maxGuestVMID = 999999999
)

// ProxmoxGuestParams sont les paramètres des ordres proxmox.vm.* : l'invité
// (son numéro et sa sorte) et, pour la suppression seule, l'instantané. Le
// nom d'un instantané créé n'est pas ici : c'est l'agent qui le tire.
type ProxmoxGuestParams struct {
	VMID     int    `json:"vmid"`
	Type     string `json:"type"`
	Snapshot string `json:"snapshot,omitempty"`
}

var (
	errGuestInvalid    = errors.New("proxmox guest: vmid or type out of range")
	errSnapshotAllowed = errors.New("proxmox guest: the snapshot name belongs to proxmox.vm.snapshot_delete only")
	errSnapshotNotOurs = errors.New("proxmox guest: not a snapshot created by this product")
)

// snapshotNamePattern est exactement ce que SnapshotName écrit : le préfixe,
// la date, l'heure. Plus étroit qu'un simple préfixe, pour qu'aucun nom de
// l'opérateur (« oc-avant-migration ») ni aucun caractère de chemin ne passe.
var snapshotNamePattern = regexp.MustCompile("^" + regexp.QuoteMeta(brand.SnapshotPrefix) + `[0-9]{8}-[0-9]{6}$`)

// SnapshotName rend le nom d'un instantané créé à cet instant, en UTC.
func SnapshotName(now time.Time) string {
	return brand.SnapshotPrefix + now.UTC().Format("20060102-150405")
}

// IsOwnSnapshot dit si un nom est de ceux que SnapshotName écrit.
func IsOwnSnapshot(name string) bool {
	return snapshotNamePattern.MatchString(name)
}

// IsGuestOrder dit si le genre est l'un des ordres sur un invité Proxmox.
func (k Kind) IsGuestOrder() bool {
	return strings.HasPrefix(string(k), "proxmox.vm.")
}

// Validate juge des paramètres pour un genre : le serveur avant d'envoyer,
// l'agent avant d'agir. Rien ne passe qui ne soit un numéro, une sorte connue
// et, pour la suppression, un nom d'instantané de ce produit.
func (p ProxmoxGuestParams) Validate(kind Kind) error {
	if p.VMID < minGuestVMID || p.VMID > maxGuestVMID || (p.Type != GuestQEMU && p.Type != GuestLXC) {
		return errGuestInvalid
	}
	if kind == KindProxmoxSnapshotDelete {
		if !IsOwnSnapshot(p.Snapshot) {
			return errSnapshotNotOurs
		}
		return nil
	}
	if p.Snapshot != "" {
		return errSnapshotAllowed
	}
	return nil
}

// IsSnapshotNotOurs dit si Validate a refusé un nom d'instantané qui n'est pas
// de ce produit : l'agent en fait un refus à part.
func IsSnapshotNotOurs(err error) bool {
	return errors.Is(err, errSnapshotNotOurs)
}
