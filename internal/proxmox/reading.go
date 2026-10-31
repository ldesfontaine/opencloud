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
	seen := map[int]archives{}
	for _, storage := range storages {
		// Un stockage hors ligne répond par une erreur : il n'a rien à dire.
		if !storage.Active || !storage.AcceptsBackups() {
			continue
		}
		entries, err := c.Backups(ctx, storage.Node, storage.Name)
		if err != nil {
			return Reading{}, err
		}
		for index := range entries {
			entries[index].Storage = storage.Name
		}
		addArchives(seen, entries)
	}
	for index := range guests {
		if found, ok := seen[guests[index].VMID]; ok {
			guests[index].LastBackupAt = found.newest.CreatedAt
			guests[index].LastBackupBytes = found.newest.Size
			guests[index].LastBackupStorage = found.newest.Storage
			guests[index].BackupCount = found.count
			guests[index].BackupTotalBytes = found.totalBytes
		}
	}
	return Reading{Version: version, Guests: guests, Storages: storages}, nil
}

// archives est ce que les stockages gardent d'une VM : la plus récente, le
// nombre d'archives et leur taille totale.
type archives struct {
	newest     BackupEntry
	count      int
	totalBytes int64
}

// addArchives rattache chaque archive à sa VM (le numéro du nom du fichier) et
// retient, par VM, la plus récente de toutes celles vues.
func addArchives(seen map[int]archives, entries []BackupEntry) {
	for _, entry := range entries {
		known := seen[entry.VMID]
		if known.count == 0 || entry.CreatedAt.After(known.newest.CreatedAt) {
			known.newest = entry
		}
		known.count++
		known.totalBytes += entry.Size
		seen[entry.VMID] = known
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
