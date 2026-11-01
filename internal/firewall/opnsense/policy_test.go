package opnsense

import (
	"testing"

	"github.com/ldesfontaine/opencloud/internal/firewall"
	"github.com/ldesfontaine/opencloud/internal/policy"
)

func testSnapshot() firewall.Snapshot {
	return firewall.Snapshot{
		Interfaces: []firewall.Interface{
			{Name: "wan", Role: "WAN", Address: "203.0.113.2/24", Status: "up"},
			{Name: "lan", Role: "LAN", Address: "192.168.1.1/24", Status: "up"},
			{Name: "opt1", Role: "DMZ", Address: "10.0.20.1/24", Status: "down"},
		},
	}
}

func rulesOf(model policy.Model, from string) []policy.Rule {
	var found []policy.Rule
	for _, rule := range model.Rules {
		if rule.From.Zone == from {
			found = append(found, rule)
		}
	}
	return found
}

func TestPolicy_EveryInterfaceIsAZoneAndThePublicOneIsTheInternet(t *testing.T) {
	model := Policy(testSnapshot())
	kinds := map[string]string{}
	for _, zone := range model.Zones {
		kinds[zone.ID] = zone.Kind
	}
	want := map[string]string{"self": policy.ZoneSelf, "wan": policy.ZoneInternet, "lan": policy.ZoneNetwork, "opt1": policy.ZoneNetwork}
	for id, kind := range want {
		if kinds[id] != kind {
			t.Errorf("zone %s: %q, want %q", id, kinds[id], kind)
		}
	}
	if len(model.Zones) != len(want) {
		t.Errorf("zones %+v", model.Zones)
	}
}

func TestPolicy_WithoutAPublicInterfaceInternetIsAddedApart(t *testing.T) {
	model := Policy(firewall.Snapshot{Interfaces: []firewall.Interface{{Name: "lan", Address: "192.168.1.1/24"}}})
	last := model.Zones[len(model.Zones)-1]
	if last.ID != "internet" || last.Kind != policy.ZoneInternet {
		t.Errorf("last zone %+v", last)
	}
}

func TestPolicy_PlacesTheEndsOfARule(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Rules = []firewall.Rule{
		{UUID: "r1", Interface: "lan", Action: "pass", Protocol: "TCP", Source: "192.168.1.0/24", Destination: "any", DestinationPort: "443", Description: "Web", Enabled: true},
		{UUID: "r2", Interface: "lan,opt1", Action: "pass", Protocol: "UDP", Source: "any", Destination: "(self)", DestinationPort: "53", Enabled: true},
		{UUID: "r3", Interface: "lan", Action: "block", Protocol: "any", Source: "any", Destination: "opt1", Enabled: true},
		{UUID: "r4", Interface: "lan", Action: "pass", Protocol: "TCP", Source: "any", Destination: "10.0.20.5", DestinationPort: "8006", Enabled: true},
		{UUID: "r5", Interface: "lan", Action: "pass", Protocol: "TCP", Source: "any", Destination: "198.51.100.7", Enabled: true},
	}
	model := Policy(snapshot)
	byRef := map[string][]policy.Rule{}
	for _, rule := range model.Rules {
		byRef[rule.Ref] = append(byRef[rule.Ref], rule)
	}
	if got := byRef["r1"][0]; got.To.Zone != policy.AllZones || got.What.Port != "443" || got.Why != "Web" || got.Effect != policy.EffectAllow {
		t.Errorf("r1 %+v", got)
	}
	if got := len(byRef["r2"]); got != 2 {
		t.Fatalf("r2 is split per start zone: %d", got)
	}
	if got := byRef["r2"][0]; got.To.Zone != "self" || got.What.Protocol != "UDP" {
		t.Errorf("r2 %+v", got)
	}
	if got := byRef["r3"][0]; got.To.Zone != "opt1" || got.Effect != policy.EffectDeny || got.What.Protocol != "" {
		t.Errorf("r3 %+v", got)
	}
	if got := byRef["r4"][0]; got.To.Zone != "opt1" {
		t.Errorf("an address of a zone belongs to it: %+v", got)
	}
	if got := byRef["r5"][0]; got.To.Zone != "wan" {
		t.Errorf("an unknown public address is the internet: %+v", got)
	}
}

func TestPolicy_WhatItCannotPlaceStaysUnknown(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Rules = []firewall.Rule{
		{UUID: "floating", Interface: "", Action: "pass", Source: "any", Destination: "any", Enabled: true},
		{UUID: "outbound", Interface: "lan", Direction: "out", Action: "pass", Source: "any", Destination: "any", Enabled: true},
		{UUID: "alias", Interface: "lan", Action: "pass", Source: "any", Destination: "Servers", Enabled: true},
	}
	grid := Policy(snapshot).Grid()
	if len(grid.Unplaced) != 3 || len(grid.Cells) != 0 {
		t.Errorf("unplaced %d, cells %d", len(grid.Unplaced), len(grid.Cells))
	}
}

func TestPolicy_ANegatedTargetKeepsItsMeaning(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Rules = []firewall.Rule{{UUID: "n", Interface: "lan", Action: "pass", Source: "any", Destination: "!opt1", Enabled: true}}
	model := Policy(snapshot)
	grid := model.Grid()
	for _, cell := range grid.Cells {
		if cell.To == "opt1" {
			t.Errorf("not opt1 reaches opt1: %+v", cell)
		}
	}
	if len(grid.Cells) == 0 {
		t.Error("the rule passes elsewhere")
	}
	object, _ := model.ObjectByID(model.Rules[0].To.Object)
	if !object.Negated {
		t.Errorf("object %+v", object)
	}
}

func TestPolicy_InterfaceAddressOfTheFirewallIsTheFirewall(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Rules = []firewall.Rule{
		{UUID: "a", Interface: "lan", Action: "pass", Source: "any", Destination: "192.168.1.1", Enabled: true},
		{UUID: "b", Interface: "lan", Action: "pass", Source: "any", Destination: "lanip", Enabled: true},
	}
	for _, rule := range rulesOf(Policy(snapshot), "lan") {
		if rule.To.Zone != "self" {
			t.Errorf("%s → %s", rule.Ref, rule.To.Zone)
		}
	}
}
