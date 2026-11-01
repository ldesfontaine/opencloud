package proxmox

import (
	"context"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Les privilèges que la liste des archives d'un stockage exige. Source :
// pve-storage, src/PVE/API2/Storage/Content.pm (route « index ») ne demande que
// Datastore.Audit pour répondre, puis retire en silence chaque archive dont
// PVE::Storage::check_volume_access (src/PVE/Storage.pm) refuse l'accès :
//
//   - Datastore.Allocate sur /storage/<id> : toutes les archives passent ;
//   - sinon, pour une archive de sauvegarde d'une VM : Datastore.AllocateSpace
//     sur /storage/<id> ET VM.Backup sur /vms/<vmid>.
//
// PVEAuditor n'a ni l'un ni l'autre : l'API répond 200 avec une liste vide.
// Une absence de droit se lit donc « aucune sauvegarde », d'où la vérification
// des droits du jeton avant de croire une liste.
const (
	PrivilegeAudit         = "Datastore.Audit"
	PrivilegeAllocate      = "Datastore.Allocate"
	privilegeAllocateSpace = "Datastore.AllocateSpace"
	privilegeVMBackup      = "VM.Backup"
)

// BackupRole est le rôle que la commande de l'écran crée : le plus petit qui
// fasse répondre la liste des archives sur un seul stockage.
const BackupRole = "LectureSauvegardes"

// storageIDPattern est la forme d'un identifiant de stockage de Proxmox
// (pve-storage, PVE::JSONSchema « pve-storage-id ») : seule une valeur de cette
// forme entre dans un chemin d'ACL ou dans une commande à coller.
var storageIDPattern = regexp.MustCompile(`^[a-z][a-z0-9\-_.]*[a-z0-9]$`)

// Permissions lit les privilèges que le jeton a sur un chemin d'ACL. La route
// est ouverte à tout jeton pour ses propres droits (pve-access-control,
// AccessControl.pm, « permissions »).
func (c *Client) Permissions(ctx context.Context, path string) (map[string]bool, error) {
	var listed map[string]map[string]any
	if err := c.get(ctx, "/access/permissions", url.Values{"path": {path}}, &listed); err != nil {
		return nil, err
	}
	held := map[string]bool{}
	for privilege := range listed[path] {
		held[privilege] = true
	}
	return held, nil
}

// backupGate dit, pour un relevé, si le jeton voit les archives des stockages.
// Les droits sur /vms ne se lisent qu'une fois, et seulement si un stockage ne
// les rend pas inutiles.
type backupGate struct {
	client     *Client
	vms        map[string]bool
	vmsRead    bool
	blind      []string
	checkedFor map[string]bool
}

func newBackupGate(client *Client) *backupGate {
	return &backupGate{client: client, checkedFor: map[string]bool{}}
}

// sees dit si la liste des archives du stockage est complète pour ce jeton. Un
// stockage dit « aveugle » est retenu par son identifiant, une fois.
func (g *backupGate) sees(ctx context.Context, storage string) (bool, error) {
	if sees, checked := g.checkedFor[storage]; checked {
		return sees, nil
	}
	sees, err := g.check(ctx, storage)
	if err != nil {
		return false, err
	}
	g.checkedFor[storage] = sees
	if !sees {
		g.blind = append(g.blind, storage)
	}
	return sees, nil
}

func (g *backupGate) check(ctx context.Context, storage string) (bool, error) {
	onStorage, err := g.client.Permissions(ctx, "/storage/"+storage)
	if err != nil {
		return false, err
	}
	if onStorage[PrivilegeAllocate] {
		return true, nil
	}
	if !onStorage[privilegeAllocateSpace] {
		return false, nil
	}
	if !g.vmsRead {
		if g.vms, err = g.client.Permissions(ctx, "/vms"); err != nil {
			return false, err
		}
		g.vmsRead = true
	}
	return g.vms[privilegeVMBackup], nil
}

// BackupGrant est ce que l'opérateur colle dans un terminal de l'hôte pour que
// le jeton voie les archives : un rôle, puis une ligne par stockage.
type BackupGrant struct {
	Privileges []string
	Role       string
	Commands   []string
}

// GrantFor écrit les commandes pveum pour le jeton et les stockages donnés. Le
// jeton et les stockages sont des textes de Proxmox, bornés, mais c'est une
// commande qui sera collée : tout ce qui n'a pas la forme d'un identifiant
// Proxmox est écarté. Sans stockage valable, pas de commande (nil).
func GrantFor(tokenID string, storages []string) *BackupGrant {
	if !tokenIDPattern.MatchString(tokenID) {
		return nil
	}
	privileges := []string{PrivilegeAudit, PrivilegeAllocate}
	grant := &BackupGrant{
		Privileges: privileges, Role: BackupRole,
		Commands: []string{"pveum role add " + BackupRole + " --privs \"" + strings.Join(privileges, " ") + "\""},
	}
	valid := slices.DeleteFunc(slices.Clone(storages), func(name string) bool { return !storageIDPattern.MatchString(name) })
	if len(valid) == 0 {
		return nil
	}
	for _, name := range valid {
		grant.Commands = append(grant.Commands, "pveum acl modify /storage/"+name+" --roles "+BackupRole+" --tokens '"+tokenID+"'")
	}
	return grant
}
