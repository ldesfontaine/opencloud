package proxmox

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/hostinfo"
)

// maxMACReads borne les lectures de configuration d'un relevé : une par invité,
// chacune sur une connexion neuve. Au-delà, les invités restent rapprochés par
// leur nom.
const maxMACReads = 500

var nicKeyPattern = regexp.MustCompile(`^net[0-9]+$`)

// GuestMACs lit les adresses MAC d'un invité dans sa configuration chez l'hôte :
// « net0: virtio=BC:24:…,bridge=vmbr0 » pour une VM, « net0: name=eth0,hwaddr=BC:24:… »
// pour un conteneur. Le nœud vient de l'API : il est échappé avant d'entrer
// dans le chemin.
func (c *Client) GuestMACs(ctx context.Context, node, kind string, vmid int) ([]string, error) {
	path := "/nodes/" + url.PathEscape(node) + "/" + url.PathEscape(kind) + "/" + strconv.Itoa(vmid) + "/config"
	var config map[string]json.RawMessage
	if err := c.get(ctx, path, nil, &config); err != nil {
		return nil, err
	}
	macs := []string{}
	for key, raw := range config {
		var value string
		if !nicKeyPattern.MatchString(key) || json.Unmarshal(raw, &value) != nil {
			continue
		}
		macs = append(macs, macsOfNIC(value)...)
	}
	return macs, nil
}

// macsOfNIC cherche, parmi les champs « clé=valeur » d'une carte, celui dont la
// valeur est une adresse MAC : le nom du modèle (virtio, e1000…) change, la
// forme de l'adresse non.
func macsOfNIC(nic string) []string {
	macs := []string{}
	for _, field := range strings.Split(nic, ",") {
		_, value, found := strings.Cut(field, "=")
		if mac := strings.ToLower(strings.TrimSpace(value)); found && hostinfo.IsMAC(mac) {
			macs = append(macs, mac)
		}
	}
	return macs
}

// readGuestMACs complète les invités de leurs adresses MAC. Un invité dont la
// configuration ne se lit pas garde ses adresses vides ; un jeton qui n'a pas
// le droit de lire les configurations (VM.Audit) arrête les lectures, il
// refuserait les suivantes pareil. Seule l'annulation du relevé est une erreur.
func (c *Client) readGuestMACs(ctx context.Context, guests []Guest) error {
	for index := range guests {
		if index == maxMACReads {
			return nil
		}
		macs, err := c.GuestMACs(ctx, guests[index].Node, guests[index].Kind, guests[index].VMID)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, ErrUnauthorized) {
			return nil
		}
		if err == nil {
			guests[index].MACs = macs
		}
	}
	return nil
}

// ClusterName lit le nom de la grappe ; vide pour un nœud seul, qui n'en a pas.
func (c *Client) ClusterName(ctx context.Context) (string, error) {
	var members []struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := c.get(ctx, "/cluster/status", nil, &members); err != nil {
		return "", err
	}
	for _, member := range members {
		if member.Type == "cluster" {
			return boundedText(member.Name), nil
		}
	}
	return "", nil
}

// readClusterName tolère un jeton qui ne peut pas lire l'état de la grappe : le
// relevé garde alors ses VM et ses stockages, sans niveau « grappe ».
func (c *Client) readClusterName(ctx context.Context) (string, error) {
	name, err := c.ClusterName(ctx)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", nil
	}
	return name, nil
}
