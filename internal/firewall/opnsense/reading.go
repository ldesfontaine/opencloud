package opnsense

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/firewall"
)

// Les points d'accès lus, tous en GET, tirés de la documentation d'OPNsense
// (docs.opnsense.org/development/api) et de ses sources
// (github.com/opnsense/core, controllers/OPNsense/*/Api). Le droit qu'il
// demande à l'utilisateur d'API est dit à côté.
const (
	// Accueil : Tableau de bord. `versions[0]` est « OPNsense 25.1-amd64 ».
	versionPath = "/api/diagnostics/system/system_information"
	// Statut : Interfaces.
	interfacesPath = "/api/interfaces/overview/interfacesInfo"
	// Pare-feu : Règles [nouveau]. Les règles que l'opérateur a écrites.
	rulesPath = "/api/firewall/filter/searchRule"
	// Système : Passerelles.
	gatewaysPath = "/api/routes/gateway/status"

	// Les trois serveurs DHCP que OPNsense sait faire tourner ; l'opérateur
	// n'en a en général qu'un. Dnsmasq et Kea sont dans le cœur, ISC est le
	// greffon os-isc-dhcp.
	dnsmasqLeasesPath = "/api/dnsmasq/leases/search"
	keaLeasesPath     = "/api/kea/leases4/search"
	iscLeasesPath     = "/api/dhcpv4/leases/searchLease"

	// Au-delà de ces bornes, le relevé est refusé plutôt que d'écrire des
	// dizaines de milliers de lignes.
	maxInterfaces = 500
	maxRules      = 10000
	maxLeases     = 20000
	maxGateways   = 200
)

// text lit une valeur JSON que OPNsense rend tantôt en chaîne, tantôt en
// nombre ou en booléen, selon la version.
type text string

func (t *text) UnmarshalJSON(raw []byte) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	switch typed := value.(type) {
	case string:
		*t = text(typed)
	case float64:
		*t = text(strconv.FormatFloat(typed, 'f', -1, 64))
	case bool:
		*t = text(strconv.FormatBool(typed))
	default:
		*t = ""
	}
	return nil
}

func (t text) bounded() string { return boundedText(strings.TrimSpace(string(t))) }

// grid est la forme de toute liste « search » d'OPNsense.
type grid[Row any] struct {
	Rows     []Row `json:"rows"`
	RowCount int   `json:"rowCount"`
	Total    int   `json:"total"`
}

// searchRows lit une liste en une page de `limit` lignes au plus. Un pare-feu
// qui en annonce davantage est refusé : un relevé tronqué passerait pour entier.
func searchRows[Row any](ctx context.Context, c *Client, path string, limit int) ([]Row, error) {
	var page grid[Row]
	query := url.Values{"current": {"1"}, "rowCount": {strconv.Itoa(limit)}}
	if err := c.get(ctx, path, query, &page); err != nil {
		return nil, err
	}
	if len(page.Rows) > limit || page.Total > limit {
		return nil, fmt.Errorf("%w: more than %d rows", firewall.ErrBadResponse, limit)
	}
	return page.Rows, nil
}

func (c *Client) Version(ctx context.Context) (string, error) {
	var information struct {
		Versions []text `json:"versions"`
	}
	if err := c.get(ctx, versionPath, nil, &information); err != nil {
		return "", err
	}
	if len(information.Versions) == 0 || information.Versions[0].bounded() == "" {
		return "", fmt.Errorf("%w: no version in the answer", firewall.ErrBadResponse)
	}
	return information.Versions[0].bounded(), nil
}

type interfaceRow struct {
	Identifier  text `json:"identifier"`
	Device      text `json:"device"`
	Description text `json:"description"`
	Status      text `json:"status"`
	Addr4       text `json:"addr4"`
	Addr6       text `json:"addr6"`
}

func (c *Client) Interfaces(ctx context.Context) ([]firewall.Interface, error) {
	rows, err := searchRows[interfaceRow](ctx, c, interfacesPath, maxInterfaces)
	if err != nil {
		return nil, err
	}
	interfaces := make([]firewall.Interface, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		name := row.Identifier.bounded()
		// Une interface sans identifiant n'est pas assignée : elle ne sert à
		// aucune règle.
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		address := row.Addr4.bounded()
		if address == "" {
			address = row.Addr6.bounded()
		}
		interfaces = append(interfaces, firewall.Interface{
			Name: name, Device: row.Device.bounded(), Role: row.Description.bounded(),
			Address: address, Status: row.Status.bounded(),
		})
	}
	return interfaces, nil
}

type ruleRow struct {
	UUID            text `json:"uuid"`
	Enabled         text `json:"enabled"`
	Interface       text `json:"interface"`
	Action          text `json:"action"`
	Direction       text `json:"direction"`
	Protocol        text `json:"protocol"`
	SourceNet       text `json:"source_net"`
	SourcePort      text `json:"source_port"`
	DestinationNet  text `json:"destination_net"`
	DestinationPort text `json:"destination_port"`
	Description     text `json:"description"`
}

func (c *Client) Rules(ctx context.Context) ([]firewall.Rule, error) {
	rows, err := searchRows[ruleRow](ctx, c, rulesPath, maxRules)
	if err != nil {
		return nil, err
	}
	rules := make([]firewall.Rule, 0, len(rows))
	for _, row := range rows {
		rules = append(rules, firewall.Rule{
			UUID: row.UUID.bounded(), Interface: row.Interface.bounded(), Action: row.Action.bounded(),
			Direction: row.Direction.bounded(), Protocol: row.Protocol.bounded(),
			Source: row.SourceNet.bounded(), SourcePort: row.SourcePort.bounded(),
			Destination: row.DestinationNet.bounded(), DestinationPort: row.DestinationPort.bounded(),
			Description: row.Description.bounded(),
			// Une règle sans le champ est une règle de la liste de base : active.
			Enabled: row.Enabled != "0" && row.Enabled != "false",
		})
	}
	return rules, nil
}

// leaseRow réunit les champs des trois serveurs DHCP : chacun en remplit une
// partie (hwaddr chez dnsmasq et Kea, mac chez ISC ; expire contre ends).
type leaseRow struct {
	Address   text `json:"address"`
	HWAddr    text `json:"hwaddr"`
	MAC       text `json:"mac"`
	Hostname  text `json:"hostname"`
	IfDescr   text `json:"if_descr"`
	IfName    text `json:"if_name"`
	If        text `json:"if"`
	Expire    text `json:"expire"`
	Ends      text `json:"ends"`
	LeaseType text `json:"lease_type"`
	Type      text `json:"type"`
}

// leaseSource est un serveur DHCP et le point d'accès qui liste ses baux.
type leaseSource struct {
	server string
	path   string
}

var leaseSources = []leaseSource{
	{"dnsmasq", dnsmasqLeasesPath},
	{"kea", keaLeasesPath},
	{"isc", iscLeasesPath},
}

// Leases réunit les baux des serveurs DHCP que le pare-feu fait tourner. Un
// serveur absent (404) ou que le droit de l'utilisateur ne couvre pas (403)
// n'a rien à dire ; si aucun ne répond et qu'un a refusé, c'est un refus.
func (c *Client) Leases(ctx context.Context) ([]firewall.Lease, error) {
	leases := []firewall.Lease{}
	answered, forbidden := 0, 0
	var forbiddenErr error
	for _, source := range leaseSources {
		rows, err := searchRows[leaseRow](ctx, c, source.path, maxLeases)
		if isStatus(err, http.StatusNotFound) {
			continue
		}
		if isStatus(err, http.StatusForbidden) || isStatus(err, http.StatusUnauthorized) {
			forbidden++
			forbiddenErr = err
			continue
		}
		if err != nil {
			return nil, err
		}
		answered++
		for _, row := range rows {
			if lease, ok := leaseOf(source.server, row); ok {
				leases = append(leases, lease)
			}
		}
		if len(leases) > maxLeases {
			return nil, fmt.Errorf("%w: more than %d leases", firewall.ErrBadResponse, maxLeases)
		}
	}
	if answered == 0 && forbidden > 0 {
		return nil, forbiddenErr
	}
	return leases, nil
}

func leaseOf(server string, row leaseRow) (firewall.Lease, bool) {
	address := row.Address.bounded()
	if address == "" {
		return firewall.Lease{}, false
	}
	mac := row.HWAddr.bounded()
	if mac == "" {
		mac = row.MAC.bounded()
	}
	interfaceName := row.IfDescr.bounded()
	if interfaceName == "" {
		interfaceName = firstFilled(row.IfName, row.If)
	}
	kind := firstFilled(row.LeaseType, row.Type)
	return firewall.Lease{
		Address: address, MAC: mac, Hostname: row.Hostname.bounded(), Interface: interfaceName,
		Server: server, IsStatic: kind == "static",
		ExpiresAt: parseExpiry(firstFilled(row.Expire, row.Ends)),
	}, true
}

func firstFilled(values ...text) string {
	for _, value := range values {
		if bounded := value.bounded(); bounded != "" {
			return bounded
		}
	}
	return ""
}

// parseExpiry lit une échéance en secondes depuis 1970 (dnsmasq, Kea) ou à la
// date d'ISC (« 2026/10/28 12:00:00 », UTC). Zéro quand la valeur est vide,
// nulle ou d'une forme inconnue : le bail n'a alors pas d'échéance à montrer.
func parseExpiry(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if seconds <= 0 {
			return time.Time{}
		}
		return time.Unix(seconds, 0).UTC()
	}
	if parsed, err := time.Parse("2006/01/02 15:04:05", raw); err == nil {
		return parsed.UTC()
	}
	return time.Time{}
}

type gatewayRow struct {
	Name    text `json:"name"`
	Address text `json:"address"`
	Status  text `json:"status"`
	Delay   text `json:"delay"`
	Loss    text `json:"loss"`
}

func (c *Client) Gateways(ctx context.Context) ([]firewall.Gateway, error) {
	var answer struct {
		Items []gatewayRow `json:"items"`
	}
	if err := c.get(ctx, gatewaysPath, nil, &answer); err != nil {
		return nil, err
	}
	if len(answer.Items) > maxGateways {
		return nil, fmt.Errorf("%w: %d gateways, over %d", firewall.ErrBadResponse, len(answer.Items), maxGateways)
	}
	gateways := make([]firewall.Gateway, 0, len(answer.Items))
	seen := map[string]bool{}
	for _, row := range answer.Items {
		name := row.Name.bounded()
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		delay, hasDelay := parseMeasure(string(row.Delay), "ms")
		loss, hasLoss := parseMeasure(string(row.Loss), "%")
		gateways = append(gateways, firewall.Gateway{
			Name: name, Address: row.Address.bounded(), Status: gatewayState(row.Status.bounded()),
			IsMonitored: hasDelay || hasLoss, DelayMillis: delay, LossPercent: loss,
		})
	}
	return gateways, nil
}

func gatewayState(status string) string {
	switch status {
	case "none":
		return firewall.GatewayOnline
	case "down", "force_down":
		return firewall.GatewayOffline
	case "delay", "loss", "delay+loss":
		return firewall.GatewayDegraded
	default:
		return firewall.GatewayUnknown
	}
}

// parseMeasure lit « 1.2 ms » ou « 0.0 % » ; OPNsense écrit « ~ » ou rien pour
// une passerelle qu'il ne sonde pas.
func parseMeasure(raw, unit string) (float64, bool) {
	number := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw), unit))
	value, err := strconv.ParseFloat(number, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}
