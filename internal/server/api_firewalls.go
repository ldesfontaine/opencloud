package server

import (
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/firewall"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
)

// Un pare-feu déclaré, tel que la liste et la fiche le lisent : ce que son API
// a dit au dernier relevé réussi. Le secret n'est jamais rendu, ni ici ni dans
// une erreur ; last_error est la clé du refus du dernier relevé qui a échoué,
// vide s'il a réussi. Un jeton d'API ne voit ni la clé ni l'empreinte
// (redactedFirewall).
type firewallJSON struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Kind       string             `json:"kind"`
	APIURL     string             `json:"api_url"`
	APIKey     string             `json:"api_key"`
	Pin        string             `json:"pin"`
	IsPinned   bool               `json:"is_pinned"`
	Version    string             `json:"version"`
	VerifiedAt time.Time          `json:"verified_at"`
	LastReadAt *time.Time         `json:"last_read_at"`
	LastError  string             `json:"last_error"`
	Counts     firewallCountsJSON `json:"counts"`
}

type firewallCountsJSON struct {
	Interfaces int `json:"interfaces"`
	Rules      int `json:"rules"`
	Leases     int `json:"leases"`
	Gateways   int `json:"gateways"`
}

type firewallsResponse struct {
	Firewalls []firewallJSON `json:"firewalls"`
	Limit     int            `json:"limit"`
}

// La fiche : le pare-feu et ses passerelles. Les règles, les interfaces et les
// baux, plus nombreux, ont leur route.
type firewallDetailJSON struct {
	firewallJSON
	Gateways []firewallGatewayJSON `json:"gateways"`
}

type firewallGatewayJSON struct {
	Name        string   `json:"name"`
	Address     string   `json:"address"`
	Status      string   `json:"status"`
	DelayMillis *float64 `json:"delay_ms"`
	LossPercent *float64 `json:"loss_percent"`
}

// Ce que chaque route de relevé rappelle du pare-feu : de quand date ce qu'elle
// montre, et si le dernier relevé a échoué.
type firewallReadingJSON struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	LastReadAt *time.Time `json:"last_read_at"`
	LastError  string     `json:"last_error"`
}

type firewallInterfaceJSON struct {
	Name    string `json:"name"`
	Device  string `json:"device"`
	Role    string `json:"role"`
	Address string `json:"address"`
	Status  string `json:"status"`
}

type firewallInterfacesResponse struct {
	firewallReadingJSON
	Interfaces []firewallInterfaceJSON `json:"interfaces"`
}

type firewallRuleJSON struct {
	Interface       string `json:"interface"`
	Action          string `json:"action"`
	Direction       string `json:"direction"`
	Protocol        string `json:"protocol"`
	Source          string `json:"source"`
	SourcePort      string `json:"source_port"`
	Destination     string `json:"destination"`
	DestinationPort string `json:"destination_port"`
	Description     string `json:"description"`
	IsEnabled       bool   `json:"is_enabled"`
}

type firewallRulesResponse struct {
	firewallReadingJSON
	Rules []firewallRuleJSON `json:"rules"`
}

type firewallLeaseJSON struct {
	Address   string     `json:"address"`
	MAC       string     `json:"mac"`
	Hostname  string     `json:"hostname"`
	Interface string     `json:"interface"`
	Server    string     `json:"server"`
	IsStatic  bool       `json:"is_static"`
	ExpiresAt *time.Time `json:"expires_at"`
}

type firewallLeasesResponse struct {
	firewallReadingJSON
	Leases []firewallLeaseJSON `json:"leases"`
}

// Le secret arrive ici et nulle part ailleurs : le composant le voit le temps
// de joindre l'API, la base le garde scellé, rien ne le journalise.
type firewallRequest struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	APIURL    string `json:"api_url"`
	APIKey    string `json:"api_key"`
	APISecret string `json:"api_secret"`
	// Facultative : l'empreinte SHA-256 du certificat du pare-feu.
	Pin string `json:"pin"`
}

func (request firewallRequest) definition() firewall.Definition {
	return firewall.Definition{
		Name: request.Name, Kind: request.Kind, APIURL: request.APIURL,
		APIKey: request.APIKey, APISecret: request.APISecret, Pin: request.Pin,
	}
}

func firewallToJSON(declared firewall.Firewall, counts firewallCountsJSON) firewallJSON {
	return firewallJSON{
		ID: declared.ID, Name: declared.Name, Kind: declared.Kind, APIURL: declared.APIURL, APIKey: declared.APIKey,
		Pin: declared.Pin, IsPinned: declared.Pin != "", Version: declared.Version, VerifiedAt: declared.VerifiedAt.UTC(),
		LastReadAt: httpx.TimeOrNil(declared.LastReadAt), LastError: declared.LastError, Counts: counts,
	}
}

func firewallSummaryToJSON(summary firewall.Summary) firewallJSON {
	return firewallToJSON(summary.Firewall, firewallCountsJSON{
		Interfaces: summary.Interfaces, Rules: summary.Rules, Leases: summary.Leases, Gateways: summary.Gateways,
	})
}

func firewallViewToJSON(view firewall.View) firewallDetailJSON {
	return firewallDetailJSON{
		firewallJSON: firewallToJSON(view.Firewall, firewallCountsJSON{
			Interfaces: len(view.Interfaces), Rules: len(view.Rules), Leases: len(view.Leases), Gateways: len(view.Gateways),
		}),
		Gateways: httpx.ConvertAll(view.Gateways, firewallGatewayToJSON),
	}
}

func firewallGatewayToJSON(gateway firewall.Gateway) firewallGatewayJSON {
	converted := firewallGatewayJSON{Name: gateway.Name, Address: gateway.Address, Status: gateway.Status}
	if gateway.IsMonitored {
		converted.DelayMillis, converted.LossPercent = &gateway.DelayMillis, &gateway.LossPercent
	}
	return converted
}

func firewallReadingOf(view firewall.View) firewallReadingJSON {
	return firewallReadingJSON{
		ID: view.ID, Name: view.Name, LastReadAt: httpx.TimeOrNil(view.LastReadAt), LastError: view.LastError,
	}
}

// redactedFirewall est la seule règle « un jeton masque la paire de clés » :
// la clé d'API et l'empreinte du certificat ne sortent que vers l'opérateur.
func redactedFirewall(converted firewallJSON) firewallJSON {
	converted.APIKey, converted.Pin = "", ""
	return converted
}

func (s *Server) listFirewalls(w http.ResponseWriter, r *http.Request) {
	summaries, err := s.reader.ListFirewalls(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	_, byToken := apiTokenFrom(r.Context())
	listed := httpx.ConvertAll(summaries, func(summary firewall.Summary) firewallJSON {
		converted := firewallSummaryToJSON(summary)
		if byToken {
			return redactedFirewall(converted)
		}
		return converted
	})
	s.writeAPI(w, http.StatusOK, firewallsResponse{Firewalls: listed, Limit: firewall.MaxFirewalls})
}

// firewallViewOf lit la fiche demandée par l'adresse ; faux quand la réponse
// d'échec est déjà partie.
func (s *Server) firewallViewOf(w http.ResponseWriter, r *http.Request) (firewall.View, bool) {
	view, err := s.reader.GetFirewallView(r.Context(), r.PathValue("id"))
	if s.failed(w, r, err, firewall.ErrNotFound) {
		return firewall.View{}, false
	}
	return view, true
}

func (s *Server) getFirewall(w http.ResponseWriter, r *http.Request) {
	view, ok := s.firewallViewOf(w, r)
	if !ok {
		return
	}
	detail := firewallViewToJSON(view)
	if _, byToken := apiTokenFrom(r.Context()); byToken {
		detail.firewallJSON = redactedFirewall(detail.firewallJSON)
	}
	s.writeAPI(w, http.StatusOK, detail)
}

func (s *Server) listFirewallRules(w http.ResponseWriter, r *http.Request) {
	view, ok := s.firewallViewOf(w, r)
	if !ok {
		return
	}
	s.writeAPI(w, http.StatusOK, firewallRulesResponse{
		firewallReadingJSON: firewallReadingOf(view),
		Rules: httpx.ConvertAll(view.Rules, func(rule firewall.Rule) firewallRuleJSON {
			return firewallRuleJSON{
				Interface: rule.Interface, Action: rule.Action, Direction: rule.Direction, Protocol: rule.Protocol,
				Source: rule.Source, SourcePort: rule.SourcePort, Destination: rule.Destination,
				DestinationPort: rule.DestinationPort, Description: rule.Description, IsEnabled: rule.Enabled,
			}
		}),
	})
}

func (s *Server) listFirewallInterfaces(w http.ResponseWriter, r *http.Request) {
	view, ok := s.firewallViewOf(w, r)
	if !ok {
		return
	}
	s.writeAPI(w, http.StatusOK, firewallInterfacesResponse{
		firewallReadingJSON: firewallReadingOf(view),
		Interfaces: httpx.ConvertAll(view.Interfaces, func(item firewall.Interface) firewallInterfaceJSON {
			return firewallInterfaceJSON{Name: item.Name, Device: item.Device, Role: item.Role, Address: item.Address, Status: item.Status}
		}),
	})
}

func (s *Server) listFirewallLeases(w http.ResponseWriter, r *http.Request) {
	view, ok := s.firewallViewOf(w, r)
	if !ok {
		return
	}
	s.writeAPI(w, http.StatusOK, firewallLeasesResponse{
		firewallReadingJSON: firewallReadingOf(view),
		Leases: httpx.ConvertAll(view.Leases, func(lease firewall.Lease) firewallLeaseJSON {
			return firewallLeaseJSON{
				Address: lease.Address, MAC: lease.MAC, Hostname: lease.Hostname, Interface: lease.Interface,
				Server: lease.Server, IsStatic: lease.IsStatic, ExpiresAt: httpx.TimeOrNil(lease.ExpiresAt),
			}
		}),
	})
}

// Déclarer un pare-feu : la paire de clés répond à l'API avant que rien ne
// s'écrive, puis un premier relevé part. La réponse est la fiche, avec sa
// dernière erreur si ce relevé a échoué. Réservé à l'opérateur : la route n'est
// pas de celles qu'un jeton d'API ouvre.
func (s *Server) createFirewall(w http.ResponseWriter, r *http.Request) {
	var request firewallRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	id, err := s.firewalls.Declare(r.Context(), request.definition())
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeFirewallDetail(w, r, id, http.StatusCreated)
}

// Redéclarer : le secret se ressaisit à chaque fois.
func (s *Server) updateFirewall(w http.ResponseWriter, r *http.Request) {
	var request firewallRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	id := r.PathValue("id")
	err := s.firewalls.Redeclare(r.Context(), id, request.definition())
	if s.failed(w, r, err, firewall.ErrNotFound) {
		return
	}
	s.writeFirewallDetail(w, r, id, http.StatusOK)
}

func (s *Server) writeFirewallDetail(w http.ResponseWriter, r *http.Request, id string, status int) {
	view, err := s.reader.GetFirewallView(r.Context(), id)
	if s.failed(w, r, err, firewall.ErrNotFound) {
		return
	}
	s.writeAPI(w, status, firewallViewToJSON(view))
}

func (s *Server) deleteFirewall(w http.ResponseWriter, r *http.Request) {
	s.finishAPIAction(w, r, s.firewalls.Remove(r.Context(), r.PathValue("id")), firewall.ErrNotFound)
}
