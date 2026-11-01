package opnsense

import (
	"net/netip"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/firewall"
	"github.com/ldesfontaine/opencloud/internal/policy"
)

// Les identifiants des zones que le connecteur ajoute aux interfaces.
const (
	selfZoneID     = "self"
	internetZoneID = "internet"
)

// negationMark précède une cible niée (« tout sauf ») dans une règle lue ;
// Rules l'a posé, Policy le lit. Il évite une colonne de plus en base.
const negationMark = "!"

// sharedAddressSpace est le bloc des opérateurs (100.64.0.0/10) : ni privé au
// sens de la RFC 1918, ni joignable depuis Internet.
var sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")

// Policy traduit un relevé OPNsense vers le modèle des échanges : chaque
// interface est une zone, chaque règle une ou plusieurs règles
// « de → vers : quoi, pourquoi ». Ce que le relevé ne permet pas de placer
// (règle flottante, règle sortante, alias) garde une zone inconnue.
func Policy(snapshot firewall.Snapshot) policy.Model {
	translation := newTranslation(snapshot.Interfaces)
	for _, rule := range snapshot.Rules {
		translation.add(rule)
	}
	return policy.Model{Zones: translation.zones, Objects: translation.objects, Rules: translation.rules}
}

type zoneNetwork struct {
	zone    string
	network netip.Prefix
	// self : l'adresse du pare-feu sur cette interface.
	self netip.Addr
}

type translation struct {
	zones    []policy.Zone
	networks []zoneNetwork
	objects  []policy.Object
	rules    []policy.Rule
	known    map[string]bool
	seen     map[string]bool
	internet string
}

func newTranslation(interfaces []firewall.Interface) *translation {
	t := &translation{known: map[string]bool{}, seen: map[string]bool{}}
	t.zones = append(t.zones, policy.Zone{ID: selfZoneID, Kind: policy.ZoneSelf, IsUp: true})
	t.known[selfZoneID] = true
	for _, item := range interfaces {
		t.addInterface(item)
	}
	if t.internet == "" {
		t.zones = append(t.zones, policy.Zone{ID: internetZoneID, Kind: policy.ZoneInternet, IsUp: true})
		t.known[internetZoneID] = true
		t.internet = internetZoneID
	}
	return t
}

// addInterface pose la zone d'une interface. Une interface qui porte une
// adresse publique est la sortie vers Internet : c'est la zone « internet ».
func (t *translation) addInterface(item firewall.Interface) {
	zone := policy.Zone{ID: item.Name, Label: zoneLabel(item), Kind: policy.ZoneNetwork, IsUp: item.Status == "up"}
	if address, err := netip.ParsePrefix(item.Address); err == nil {
		zone.Network = address.Masked().String()
		t.networks = append(t.networks, zoneNetwork{zone: item.Name, network: address.Masked(), self: address.Addr()})
		if isPublic(address.Addr()) && t.internet == "" {
			zone.Kind, t.internet = policy.ZoneInternet, item.Name
		}
	}
	t.zones = append(t.zones, zone)
	t.known[item.Name] = true
}

func zoneLabel(item firewall.Interface) string {
	if item.Role != "" {
		return item.Role
	}
	return item.Name
}

func isPublic(address netip.Addr) bool {
	return address.IsValid() && !address.IsPrivate() && !address.IsLoopback() && !address.IsLinkLocalUnicast() &&
		!address.IsUnspecified() && !sharedAddressSpace.Contains(address)
}

// add traduit une règle du pare-feu : une règle par zone de départ, source et
// destination.
func (t *translation) add(rule firewall.Rule) {
	effect := policy.EffectDeny
	if rule.Action == "pass" {
		effect = policy.EffectAllow
	}
	what := policy.Service{Protocol: protocolOf(rule.Protocol), Port: portOf(rule.DestinationPort), SourcePort: portOf(rule.SourcePort)}
	zones := t.startZones(rule)
	for _, zone := range zones {
		for _, source := range splitTargets(rule.Source) {
			for _, destination := range splitTargets(rule.Destination) {
				from := t.endpoint(source)
				from.Zone, from.Except = zone, ""
				to := t.endpoint(destination)
				if zone == policy.UnknownZone {
					to.Zone = policy.UnknownZone
				}
				t.rules = append(t.rules, policy.Rule{
					Ref: rule.UUID, From: from, To: to, What: what,
					Why: rule.Description, Effect: effect, Enabled: rule.Enabled,
				})
			}
		}
	}
}

// startZones : les interfaces où la règle s'applique. Une règle flottante
// (sans interface) ou sortante n'a pas de zone de départ qu'on puisse dire.
func (t *translation) startZones(rule firewall.Rule) []string {
	if rule.Direction == "out" {
		return []string{policy.UnknownZone}
	}
	var zones []string
	for _, name := range strings.Split(rule.Interface, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !t.known[name] {
			t.zones = append(t.zones, policy.Zone{ID: name, Label: name, Kind: policy.ZoneNetwork})
			t.known[name] = true
		}
		zones = append(zones, name)
	}
	if len(zones) == 0 {
		return []string{policy.UnknownZone}
	}
	return zones
}

func protocolOf(protocol string) string {
	if strings.EqualFold(protocol, "any") {
		return ""
	}
	return protocol
}

func portOf(port string) string {
	if strings.EqualFold(port, "any") {
		return ""
	}
	return port
}

// splitTargets sépare « a,b » ; une cible vide est « any ».
func splitTargets(raw string) []string {
	negated := strings.HasPrefix(raw, negationMark)
	raw = strings.TrimPrefix(raw, negationMark)
	var targets []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if negated {
			part = negationMark + part
		}
		targets = append(targets, part)
	}
	if len(targets) == 0 {
		return []string{"any"}
	}
	return targets
}

// endpoint traduit une cible d'OPNsense : « any », « (self) », le nom d'une
// interface (son réseau), « <interface>ip » (l'adresse du pare-feu dessus), une
// adresse, un bloc, ou un alias qu'on ne déplie pas.
func (t *translation) endpoint(target string) policy.Endpoint {
	negated := strings.HasPrefix(target, negationMark)
	target = strings.TrimPrefix(target, negationMark)
	object, zone := t.resolve(target)
	object.Negated = negated
	if negated {
		object.ID = negationMark + object.ID
		object.Label = target
	}
	t.addObject(object)
	endpoint := policy.Endpoint{Zone: zone, Object: object.ID}
	if negated {
		endpoint.Zone, endpoint.Except = policy.AllZones, zone
		if zone == policy.UnknownZone || zone == policy.AllZones {
			endpoint.Zone, endpoint.Except = policy.UnknownZone, ""
		}
	}
	return endpoint
}

func (t *translation) resolve(target string) (policy.Object, string) {
	switch {
	case target == "any":
		return policy.Object{ID: "any", Kind: policy.ObjectAny, Label: "any", Zone: policy.AllZones}, policy.AllZones
	case target == "(self)":
		return policy.Object{ID: "self", Kind: policy.ObjectSelf, Label: "(self)", Zone: selfZoneID}, selfZoneID
	case t.known[target] && target != selfZoneID && target != internetZoneID:
		return policy.Object{ID: "zone:" + target, Kind: policy.ObjectZone, Label: target, Zone: target}, target
	}
	if name, found := strings.CutSuffix(target, "ip"); found && t.known[name] && name != selfZoneID {
		return policy.Object{ID: "self", Kind: policy.ObjectSelf, Label: "(self)", Zone: selfZoneID}, selfZoneID
	}
	if prefix, err := netip.ParsePrefix(target); err == nil {
		return policy.Object{ID: "network:" + prefix.String(), Kind: policy.ObjectNetwork, Label: prefix.String(), Zone: t.zoneOfPrefix(prefix)}, t.zoneOfPrefix(prefix)
	}
	if address, err := netip.ParseAddr(target); err == nil {
		zone := t.zoneOfAddress(address)
		return policy.Object{ID: "host:" + address.String(), Kind: policy.ObjectHost, Label: address.String(), Zone: zone}, zone
	}
	return policy.Object{ID: "named:" + target, Kind: policy.ObjectNamed, Label: target, Zone: policy.UnknownZone}, policy.UnknownZone
}

// zoneOfAddress : la zone dont le réseau contient l'adresse ; le pare-feu
// lui-même si c'est la sienne ; sinon Internet.
func (t *translation) zoneOfAddress(address netip.Addr) string {
	for _, candidate := range t.networks {
		if candidate.self == address {
			return selfZoneID
		}
	}
	best, bestBits := "", -1
	for _, candidate := range t.networks {
		if candidate.network.Contains(address) && candidate.network.Bits() > bestBits {
			best, bestBits = candidate.zone, candidate.network.Bits()
		}
	}
	if best == "" {
		return t.internet
	}
	return best
}

// zoneOfPrefix : la zone dont le réseau contient tout le bloc ; sinon
// Internet si le bloc est public, sinon une zone qu'on ne sait pas dire.
func (t *translation) zoneOfPrefix(prefix netip.Prefix) string {
	best, bestBits := "", -1
	for _, candidate := range t.networks {
		if candidate.network.Bits() <= prefix.Bits() && candidate.network.Contains(prefix.Addr()) && candidate.network.Bits() > bestBits {
			best, bestBits = candidate.zone, candidate.network.Bits()
		}
	}
	switch {
	case best != "":
		return best
	case isPublic(prefix.Addr()):
		return t.internet
	default:
		return policy.UnknownZone
	}
}

func (t *translation) addObject(object policy.Object) {
	if t.seen[object.ID] {
		return
	}
	t.seen[object.ID] = true
	t.objects = append(t.objects, object)
}
