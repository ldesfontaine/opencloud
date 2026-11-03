package server

import (
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/firewall"
	"github.com/ldesfontaine/opencloud/internal/policy"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
)

// La vue « qui parle à qui » d'un pare-feu : les zones, les objets que les
// règles désignent, et la grille construite sur le modèle des échanges
// (internal/policy), jamais sur le vocabulaire d'un constructeur. Une case
// absente de cells est une case vide : refusé. Lecture seule.
type firewallFlowsResponse struct {
	firewallReadingJSON
	Zones   []flowZoneJSON   `json:"zones"`
	Objects []flowObjectJSON `json:"objects"`
	// rows : les zones de départ ; columns : celles d'arrivée.
	Rows     []string       `json:"rows"`
	Columns  []string       `json:"columns"`
	Cells    []flowCellJSON `json:"cells"`
	Unplaced []flowRuleJSON `json:"unplaced"`
	Disabled int            `json:"disabled"`
}

type flowZoneJSON struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Network string `json:"network"`
	IsUp    bool   `json:"is_up"`
}

type flowObjectJSON struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Zone      string `json:"zone"`
	IsNegated bool   `json:"is_negated"`
}

type flowEndpointJSON struct {
	Zone   string `json:"zone"`
	Object string `json:"object"`
	Except string `json:"except"`
}

type flowRuleJSON struct {
	Ref        string           `json:"ref"`
	From       flowEndpointJSON `json:"from"`
	To         flowEndpointJSON `json:"to"`
	Protocol   string           `json:"protocol"`
	Port       string           `json:"port"`
	SourcePort string           `json:"source_port"`
	Why        string           `json:"why"`
	Effect     string           `json:"effect"`
	IsEnabled  bool             `json:"is_enabled"`
}

type flowCellJSON struct {
	From    string         `json:"from"`
	To      string         `json:"to"`
	Allowed []flowRuleJSON `json:"allowed"`
	Denied  []flowRuleJSON `json:"denied"`
}

func flowRuleToJSON(rule policy.Rule) flowRuleJSON {
	return flowRuleJSON{
		Ref:      rule.Ref,
		From:     flowEndpointJSON{Zone: rule.From.Zone, Object: rule.From.Object, Except: rule.From.Except},
		To:       flowEndpointJSON{Zone: rule.To.Zone, Object: rule.To.Object, Except: rule.To.Except},
		Protocol: rule.What.Protocol, Port: rule.What.Port, SourcePort: rule.What.SourcePort,
		Why: rule.Why, Effect: rule.Effect, IsEnabled: rule.Enabled,
	}
}

func flowsToJSON(view firewall.View, model policy.Model) firewallFlowsResponse {
	grid := model.Grid()
	return firewallFlowsResponse{
		firewallReadingJSON: firewallReadingOf(view),
		Zones: httpx.ConvertAll(model.Zones, func(zone policy.Zone) flowZoneJSON {
			return flowZoneJSON{ID: zone.ID, Label: zone.Label, Kind: zone.Kind, Network: zone.Network, IsUp: zone.IsUp}
		}),
		Objects: httpx.ConvertAll(model.Objects, func(object policy.Object) flowObjectJSON {
			return flowObjectJSON{ID: object.ID, Kind: object.Kind, Label: object.Label, Zone: object.Zone, IsNegated: object.Negated}
		}),
		Rows: nonNil(grid.Rows), Columns: nonNil(grid.Columns),
		Cells: httpx.ConvertAll(grid.Cells, func(cell policy.Cell) flowCellJSON {
			return flowCellJSON{
				From: cell.From, To: cell.To,
				Allowed: httpx.ConvertAll(cell.Allowed, flowRuleToJSON), Denied: httpx.ConvertAll(cell.Denied, flowRuleToJSON),
			}
		}),
		Unplaced: httpx.ConvertAll(grid.Unplaced, flowRuleToJSON),
		Disabled: grid.Disabled,
	}
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func (s *Server) listFirewallFlows(w http.ResponseWriter, r *http.Request) {
	view, ok := s.firewallViewOf(w, r)
	if !ok {
		return
	}
	model, err := s.firewalls.Policy(view)
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, flowsToJSON(view, model))
}
