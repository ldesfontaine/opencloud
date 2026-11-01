package policy_test

import (
	"testing"

	"github.com/ldesfontaine/opencloud/internal/policy"
)

func model() policy.Model {
	return policy.Model{
		Zones: []policy.Zone{
			{ID: "self", Kind: policy.ZoneSelf},
			{ID: "lan", Label: "LAN", Kind: policy.ZoneNetwork},
			{ID: "dmz", Label: "DMZ", Kind: policy.ZoneNetwork},
			{ID: "internet", Kind: policy.ZoneInternet},
		},
	}
}

func rule(from, to, effect string, enabled bool) policy.Rule {
	return policy.Rule{Ref: from + to, From: policy.Endpoint{Zone: from}, To: policy.Endpoint{Zone: to}, Effect: effect, Enabled: enabled}
}

func cellOf(grid policy.Grid, from, to string) (policy.Cell, bool) {
	for _, cell := range grid.Cells {
		if cell.From == from && cell.To == to {
			return cell, true
		}
	}
	return policy.Cell{}, false
}

func TestGrid_RowsLeaveTheFirewallOutButColumnsKeepIt(t *testing.T) {
	grid := model().Grid()
	if got := len(grid.Rows); got != 3 {
		t.Errorf("rows %v", grid.Rows)
	}
	if got := len(grid.Columns); got != 4 {
		t.Errorf("columns %v", grid.Columns)
	}
}

func TestGrid_AnEmptyCellIsRefused(t *testing.T) {
	m := model()
	m.Rules = []policy.Rule{rule("lan", "dmz", policy.EffectAllow, true)}
	grid := m.Grid()
	if _, found := cellOf(grid, "dmz", "lan"); found {
		t.Error("nothing allows dmz → lan: the cell must be absent")
	}
	cell, found := cellOf(grid, "lan", "dmz")
	if !found || len(cell.Allowed) != 1 {
		t.Fatalf("lan → dmz: %+v", cell)
	}
}

func TestGrid_ToAnywhereSpreadsOverEveryOtherColumn(t *testing.T) {
	m := model()
	m.Rules = []policy.Rule{rule("lan", policy.AllZones, policy.EffectAllow, true)}
	grid := m.Grid()
	for _, to := range []string{"self", "dmz", "internet"} {
		if _, found := cellOf(grid, "lan", to); !found {
			t.Errorf("lan → %s missing", to)
		}
	}
	if _, found := cellOf(grid, "lan", "lan"); found {
		t.Error("traffic inside a zone does not cross the firewall")
	}
}

func TestGrid_ANegatedDestinationLeavesItsZoneOut(t *testing.T) {
	m := model()
	blocked := rule("lan", policy.AllZones, policy.EffectAllow, true)
	blocked.To.Except = "dmz"
	m.Rules = []policy.Rule{blocked}
	if _, found := cellOf(m.Grid(), "lan", "dmz"); found {
		t.Error("« anything but dmz » reaches dmz")
	}
}

func TestGrid_DeniedAndAllowedStaySeparate(t *testing.T) {
	m := model()
	m.Rules = []policy.Rule{rule("lan", "dmz", policy.EffectDeny, true), rule("lan", "dmz", policy.EffectAllow, true)}
	cell, _ := cellOf(m.Grid(), "lan", "dmz")
	if len(cell.Allowed) != 1 || len(cell.Denied) != 1 {
		t.Errorf("%+v", cell)
	}
}

func TestGrid_DisabledRulesDoNotCountAndUnknownZonesAreSetAside(t *testing.T) {
	m := model()
	m.Rules = []policy.Rule{
		rule("lan", "dmz", policy.EffectAllow, false),
		rule(policy.UnknownZone, "dmz", policy.EffectAllow, true),
		rule("lan", policy.UnknownZone, policy.EffectAllow, true),
		rule("ghost", "dmz", policy.EffectAllow, true),
	}
	grid := m.Grid()
	if len(grid.Cells) != 0 {
		t.Errorf("cells %+v", grid.Cells)
	}
	if grid.Disabled != 1 || len(grid.Unplaced) != 3 {
		t.Errorf("disabled %d, unplaced %d", grid.Disabled, len(grid.Unplaced))
	}
}
