package proxmox

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/hostinfo"
)

// maxMACReads borne les lectures de configuration d'un relevé : une par invité,
// chacune sur une connexion neuve. Au-delà, les invités restent rapprochés par
// leur nom.
const maxMACReads = 500

var nicKeyPattern = regexp.MustCompile(`^net[0-9]+$`)

// NIC est une carte réseau d'un invité, réduite à ce que la carte du réseau et
// le rapprochement en lisent : son adresse MAC, le pont de l'hôte où elle est
// branchée, et le tag VLAN de la carte (0 : sans tag). Le reste de la ligne
// (pare-feu, débit, adresse IP) n'est jamais gardé.
type NIC struct {
	MAC    string
	Bridge string
	VLAN   int
}

// GuestNICs lit les cartes réseau d'un invité dans sa configuration chez l'hôte :
// « net0: virtio=BC:24:…,bridge=vmbr0,tag=50 » pour une VM, « net0: name=eth0,
// hwaddr=BC:24:…,bridge=vmbr0,tag=50 » pour un conteneur. Le nœud vient de
// l'API : il est échappé avant d'entrer dans le chemin.
func (c *Client) GuestNICs(ctx context.Context, node, kind string, vmid int) ([]NIC, error) {
	path := "/nodes/" + url.PathEscape(node) + "/" + url.PathEscape(kind) + "/" + strconv.Itoa(vmid) + "/config"
	var config map[string]json.RawMessage
	if err := c.get(ctx, path, nil, &config); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(config))
	for key := range config {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	nics := []NIC{}
	for _, key := range keys {
		var value string
		if !nicKeyPattern.MatchString(key) || json.Unmarshal(config[key], &value) != nil {
			continue
		}
		nics = append(nics, nicOf(value))
	}
	return nics, nil
}

// nicOf cherche, parmi les champs « clé=valeur » d'une carte, celui dont la
// valeur est une adresse MAC : le nom du modèle (virtio, e1000…) change, la
// forme de l'adresse non. « bridge » et « tag » sont lus par leur nom.
func nicOf(nic string) NIC {
	var found NIC
	for _, field := range strings.Split(nic, ",") {
		name, value, hasValue := strings.Cut(field, "=")
		value = strings.TrimSpace(value)
		switch {
		case !hasValue:
		case name == "bridge":
			found.Bridge = boundedText(value)
		case name == "tag":
			if tag, err := strconv.Atoi(value); err == nil && tag > 0 && tag < 4095 {
				found.VLAN = tag
			}
		case hostinfo.IsMAC(strings.ToLower(value)):
			found.MAC = strings.ToLower(value)
		}
	}
	return found
}

// readGuestNICs complète les invités de leurs cartes réseau. Un invité dont la
// configuration ne se lit pas garde ses cartes vides ; un jeton qui n'a pas
// le droit de lire les configurations (VM.Audit) arrête les lectures, il
// refuserait les suivantes pareil. Seule l'annulation du relevé est une erreur.
func (c *Client) readGuestNICs(ctx context.Context, guests []Guest) error {
	for index := range guests {
		if index == maxMACReads {
			return nil
		}
		nics, err := c.GuestNICs(ctx, guests[index].Node, guests[index].Kind, guests[index].VMID)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, ErrUnauthorized) {
			return nil
		}
		if err == nil {
			guests[index].NICs = nics
			guests[index].MACs = macsOf(nics)
		}
	}
	return nil
}

func macsOf(nics []NIC) []string {
	macs := []string{}
	for _, nic := range nics {
		if nic.MAC != "" {
			macs = append(macs, nic.MAC)
		}
	}
	return macs
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
