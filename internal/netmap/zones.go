package netmap

import (
	"fmt"
	"hash/fnv"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/machine"
)

// ToneCount est le nombre de teintes de zone ; la feuille de style en dessine
// autant.
const ToneCount = 8

// Les appareils de tunnel que les pare-feu nomment d'après leur système.
var tunnelDevices = []string{"wg", "tun", "ovpn", "ipsec", "gif"}

// vlanDevice reconnaît le tag VLAN que le nom d'un appareil porte : vlan0.50,
// igc1_vlan50, igc1.50.
var vlanDevice = regexp.MustCompile(`(?:vlan0*|[._])([0-9]{1,4})$`)

// loopbackDevice reconnaît l'appareil de la boucle locale : lo, lo0.
var loopbackDevice = regexp.MustCompile(`(?i)^lo[0-9]*$`)

// subject est ce qui a une zone : une machine ou un invité, par son adresse
// et son tag VLAN, quand les données les disent.
type subject struct {
	address netip.Addr
	vlan    int
}

// zoner déduit les zones des données : l'interface du pare-feu qui contient
// l'adresse, sinon le tag VLAN, sinon le sous-réseau. Rien n'est écrit en dur.
type zoner struct {
	zones map[string]*Zone
	order []string
	// Les zones des interfaces des pare-feu, dans l'ordre où ils les lisent.
	fromFirewall []firewallZone
	byTag        map[int]string
}

type firewallZone struct {
	id     string
	prefix netip.Prefix
	vlan   int
}

func newZoner(firewalls []Firewall) *zoner {
	z := &zoner{zones: map[string]*Zone{}, byTag: map[int]string{}}
	for _, firewall := range firewalls {
		for _, iface := range firewall.Interfaces {
			if isLoopback(iface) || isTunnelDevice(iface.Device) || isPublic(iface.Address) {
				continue
			}
			prefix, err := netip.ParsePrefix(iface.Address)
			if err != nil {
				continue
			}
			id := "fw:" + firewall.ID + ":" + iface.Name
			name := iface.Role
			if name == "" {
				name = iface.Name
			}
			zone := &Zone{ID: id, Name: name, Source: SourceFirewall, CIDR: prefix.Masked().String()}
			if match := vlanDevice.FindStringSubmatch(iface.Device); match != nil {
				zone.VLAN, _ = strconv.Atoi(match[1])
			}
			z.add(zone)
			z.fromFirewall = append(z.fromFirewall, firewallZone{id: id, prefix: prefix, vlan: zone.VLAN})
		}
	}
	return z
}

func (z *zoner) add(zone *Zone) {
	if _, known := z.zones[zone.ID]; !known {
		z.zones[zone.ID] = zone
		z.order = append(z.order, zone.ID)
	}
}

// resolve rend la zone de chaque sujet, par clé. Le tag VLAN d'un sujet sans
// adresse se lit dans ce que les autres sujets du même tag disent : le vote
// va à la zone la plus citée.
func (z *zoner) resolve(subjects map[string]subject) map[string]string {
	keys := make([]string, 0, len(subjects))
	for key := range subjects {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	resolved := map[string]string{}
	votes := map[int]map[string]int{}
	for _, key := range keys {
		found := subjects[key]
		if !found.address.IsValid() {
			continue
		}
		id := z.zoneOfAddress(found.address)
		resolved[key] = id
		if found.vlan > 0 {
			if votes[found.vlan] == nil {
				votes[found.vlan] = map[string]int{}
			}
			votes[found.vlan][id]++
		}
	}
	for tag, byZone := range votes {
		z.byTag[tag] = mostVoted(byZone)
	}
	for _, key := range keys {
		found := subjects[key]
		if _, done := resolved[key]; done || found.vlan == 0 {
			continue
		}
		resolved[key] = z.zoneOfTag(found.vlan)
	}
	for tag, id := range z.byTag {
		z.noteTag(id, tag)
	}
	return resolved
}

func (z *zoner) zoneOfAddress(address netip.Addr) string {
	best, bestBits := "", -1
	for _, candidate := range z.fromFirewall {
		if candidate.prefix.Contains(address) && candidate.prefix.Bits() > bestBits {
			best, bestBits = candidate.id, candidate.prefix.Bits()
		}
	}
	if best != "" {
		return best
	}
	subnet := subnetOf(address)
	id := "net:" + subnet.String()
	z.add(&Zone{ID: id, Name: subnet.String(), Source: SourceSubnet, CIDR: subnet.String()})
	return id
}

func (z *zoner) zoneOfTag(tag int) string {
	if id, known := z.byTag[tag]; known {
		return id
	}
	for _, candidate := range z.fromFirewall {
		if candidate.vlan == tag {
			z.byTag[tag] = candidate.id
			return candidate.id
		}
	}
	id := "vlan:" + strconv.Itoa(tag)
	z.add(&Zone{ID: id, Name: vlanName(tag), Source: SourceVLAN, VLAN: tag})
	z.byTag[tag] = id
	return id
}

// noteTag dit à une zone son tag : une zone de sous-réseau qu'un tag désigne
// prend son nom ; celle d'un pare-feu garde le sien.
func (z *zoner) noteTag(id string, tag int) {
	zone := z.zones[id]
	if zone == nil || zone.VLAN != 0 && zone.VLAN != tag {
		return
	}
	zone.VLAN = tag
	if zone.Source == SourceSubnet {
		zone.Source = SourceVLAN
		zone.Name = vlanName(tag)
	}
}

func vlanName(tag int) string {
	return fmt.Sprintf("VLAN %d", tag)
}

// list rend les zones utiles : celles des pare-feu d'abord, dans leur ordre,
// puis les autres par nom ; chacune a sa teinte.
func (z *zoner) list(used map[string]bool) []Zone {
	ids := make([]string, 0, len(z.order))
	for _, id := range z.order {
		if used[id] || z.zones[id].Source == SourceFirewall {
			ids = append(ids, id)
		}
	}
	rank := func(id string) int {
		if z.zones[id].Source == SourceFirewall {
			return 0
		}
		return 1
	}
	slices.SortStableFunc(ids, func(a, b string) int {
		if rank(a) != rank(b) {
			return rank(a) - rank(b)
		}
		if rank(a) == 0 {
			return 0
		}
		return strings.Compare(z.zones[a].Name+a, z.zones[b].Name+b)
	})
	tones := assignTones(ids)
	zones := make([]Zone, 0, len(ids))
	for _, id := range ids {
		zone := *z.zones[id]
		zone.Tone = tones[id]
		zones = append(zones, zone)
	}
	return zones
}

// assignTones donne à chaque zone la teinte que son identifiant désigne ; une
// teinte déjà prise passe à la suivante. Une zone gardée, une teinte gardée,
// tant que les autres ne changent pas de rang.
func assignTones(ids []string) map[string]int {
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	taken := map[int]bool{}
	tones := map[string]int{}
	for _, id := range sorted {
		hash := fnv.New32a()
		_, _ = hash.Write([]byte(id))
		tone := int(hash.Sum32() % ToneCount)
		for attempt := 0; attempt < ToneCount && taken[tone]; attempt++ {
			tone = (tone + 1) % ToneCount
		}
		taken[tone] = true
		tones[id] = tone
	}
	return tones
}

func mostVoted(byZone map[string]int) string {
	best, bestVotes := "", 0
	for id, count := range byZone {
		if count > bestVotes || count == bestVotes && id < best {
			best, bestVotes = id, count
		}
	}
	return best
}

// subnetOf rend le sous-réseau que l'adresse seule laisse supposer : /24 en
// IPv4, /64 en IPv6.
func subnetOf(address netip.Addr) netip.Prefix {
	address = address.Unmap()
	bits := 64
	if address.Is4() {
		bits = 24
	}
	return netip.PrefixFrom(address, bits).Masked()
}

// isLoopback : la boucle locale d'un pare-feu (lo0, lo, 127.0.0.1/8) n'est ni
// une zone ni une interface qu'on dessine.
func isLoopback(iface Interface) bool {
	if loopbackDevice.MatchString(iface.Device) {
		return true
	}
	prefix, err := netip.ParsePrefix(iface.Address)
	return err == nil && prefix.Addr().IsLoopback()
}

func isTunnelDevice(device string) bool {
	device = strings.ToLower(device)
	for _, prefix := range tunnelDevices {
		if strings.HasPrefix(device, prefix) {
			return true
		}
	}
	return false
}

// isPublic dit si l'adresse (avec ou sans masque) est routable sur Internet.
func isPublic(address string) bool {
	if prefix, err := netip.ParsePrefix(address); err == nil {
		address = prefix.Addr().String()
	}
	return machine.IsPublicAddress(address)
}

// addressOf lit l'adresse d'une machine, qui peut porter un port.
func addressOf(address string) netip.Addr {
	host := address
	if withoutPort, _, err := net.SplitHostPort(address); err == nil {
		host = withoutPort
	}
	parsed, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return netip.Addr{}
	}
	return parsed
}
