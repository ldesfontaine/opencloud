package proxmox

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Read fait un relevé complet : la version, les VM, les stockages, puis, pour
// chaque stockage actif qui reçoit des sauvegardes, la dernière archive de
// chaque VM. Au premier échec rien n'est rendu : un relevé à moitié lu
// remplacerait du vrai par du faux.
func (c *Client) Read(ctx context.Context) (Reading, error) {
	version, err := c.Version(ctx)
	if err != nil {
		return Reading{}, err
	}
	guests, err := c.Guests(ctx)
	if err != nil {
		return Reading{}, err
	}
	storages, err := c.Storages(ctx)
	if err != nil {
		return Reading{}, err
	}
	latest := map[int]BackupEntry{}
	for _, storage := range storages {
		// Un stockage hors ligne répond par une erreur : il n'a rien à dire.
		if !storage.Active || !storage.AcceptsBackups() {
			continue
		}
		entries, err := c.Backups(ctx, storage.Node, storage.Name)
		if err != nil {
			return Reading{}, err
		}
		keepNewest(latest, entries)
	}
	for index := range guests {
		if entry, found := latest[guests[index].VMID]; found {
			guests[index].LastBackupAt = entry.CreatedAt
			guests[index].LastBackupBytes = entry.Size
		}
	}
	return Reading{Version: version, Guests: guests, Storages: storages}, nil
}

// keepNewest retient, par VM, l'archive la plus récente de toutes celles vues.
func keepNewest(latest map[int]BackupEntry, entries []BackupEntry) {
	for _, entry := range entries {
		if known, found := latest[entry.VMID]; !found || entry.CreatedAt.After(known.CreatedAt) {
			latest[entry.VMID] = entry
		}
	}
}

// hostNode dit le nom du nœud de la machine qui porte l'hôte : l'unique nœud
// que l'API montre, sinon celui qui porte le nom d'hôte de la machine, sinon
// rien (l'API ne dit pas lequel répond).
func hostNode(reading Reading, hostname string) string {
	var nodes []string
	for _, guest := range reading.Guests {
		nodes = append(nodes, guest.Node)
	}
	for _, storage := range reading.Storages {
		nodes = append(nodes, storage.Node)
	}
	slices.Sort(nodes)
	nodes = slices.Compact(nodes)
	if len(nodes) == 1 {
		return nodes[0]
	}
	for _, node := range nodes {
		if strings.EqualFold(node, hostname) {
			return node
		}
	}
	return ""
}

// link rapproche chaque VM de la machine enrôlée qui porte son nom d'hôte :
// le nom exact d'abord, puis, faute de mieux, une casse différente si une
// seule machine convient. La machine de l'hôte n'est jamais sa propre VM.
func link(guests []Guest, machines []machine.Status, hostID string) {
	candidates := make([]machine.Status, 0, len(machines))
	for _, candidate := range machines {
		if candidate.ID != hostID && candidate.Hostname != "" {
			candidates = append(candidates, candidate)
		}
	}
	for index := range guests {
		guests[index].LinkedMachineID = linkedMachine(guests[index].Name, candidates)
	}
}

func linkedMachine(name string, candidates []machine.Status) string {
	if name == "" {
		return ""
	}
	var folded []string
	for _, candidate := range candidates {
		if candidate.Hostname == name {
			return candidate.ID
		}
		if strings.EqualFold(candidate.Hostname, name) {
			folded = append(folded, candidate.ID)
		}
	}
	if len(folded) == 1 {
		return folded[0]
	}
	return ""
}

// readTimeout borne tout le relevé d'un hôte : plusieurs appels de 20 s au
// plus, dont le nombre dépend du nombre de stockages.
const readTimeout = 2 * time.Minute
