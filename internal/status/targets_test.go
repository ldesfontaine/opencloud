package status

import (
	"testing"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
)

// ruleFacts : deux machines, un service et une tâche sur chacune, et deux
// sondes. p1 regarde s1 (sur m1) depuis m2 ; p2 ne regarde aucun service et
// part de m1.
func ruleFacts() facts {
	return facts{
		machines: map[string]machine.Status{"m1": {}, "m2": {}},
		declared: map[string]directory.Service{
			"s1": {ID: "s1", MachineID: "m1"},
			"s2": {ID: "s2", MachineID: "m2"},
		},
		heartbeats: map[string]heartbeat.Heartbeat{
			"h1": {ID: "h1", MachineID: "m1"},
			"h2": {ID: "h2", MachineID: "m2"},
		},
		probes: map[string]probe.Probe{
			"p1": {ID: "p1", MachineID: "m2", ServiceID: "s1"},
			"p2": {ID: "p2", MachineID: "m1"},
		},
	}
}

// Le tableau des seize cases : cible en ligne, membre en colonne, avec les
// cas où la réponse est non.
func TestTouches_FollowsTheRuleTable(t *testing.T) {
	cases := []struct {
		name   string
		target MemberRef
		member Member
		want   bool
	}{
		{"machine on itself", MemberRef{KindMachine, "m1"}, Member{Kind: KindMachine, ID: "m1"}, true},
		{"machine on another machine", MemberRef{KindMachine, "m1"}, Member{Kind: KindMachine, ID: "m2"}, false},
		{"machine on its service", MemberRef{KindMachine, "m1"}, Member{Kind: KindService, ID: "s1"}, true},
		{"machine on another machine's service", MemberRef{KindMachine, "m1"}, Member{Kind: KindService, ID: "s2"}, false},
		{"machine on its job", MemberRef{KindMachine, "m1"}, Member{Kind: KindHeartbeat, ID: "h1"}, true},
		{"machine on another machine's job", MemberRef{KindMachine, "m1"}, Member{Kind: KindHeartbeat, ID: "h2"}, false},
		{"machine on the probe of its service", MemberRef{KindMachine, "m1"}, Member{Kind: KindProbe, ID: "p1"}, true},
		{"machine on a probe without service run from it", MemberRef{KindMachine, "m1"}, Member{Kind: KindProbe, ID: "p2"}, false},
		{"machine does not touch the probe it runs", MemberRef{KindMachine, "m2"}, Member{Kind: KindProbe, ID: "p1"}, false},

		{"service on itself", MemberRef{KindService, "s1"}, Member{Kind: KindService, ID: "s1"}, true},
		{"service on another service", MemberRef{KindService, "s1"}, Member{Kind: KindService, ID: "s2"}, false},
		{"service on its probe", MemberRef{KindService, "s1"}, Member{Kind: KindProbe, ID: "p1"}, true},
		{"service on a probe of nobody", MemberRef{KindService, "s1"}, Member{Kind: KindProbe, ID: "p2"}, false},
		{"service does not touch its machine", MemberRef{KindService, "s1"}, Member{Kind: KindMachine, ID: "m1"}, false},
		{"service does not touch a job", MemberRef{KindService, "s1"}, Member{Kind: KindHeartbeat, ID: "h1"}, false},

		{"job on itself", MemberRef{KindHeartbeat, "h1"}, Member{Kind: KindHeartbeat, ID: "h1"}, true},
		{"job on another job", MemberRef{KindHeartbeat, "h1"}, Member{Kind: KindHeartbeat, ID: "h2"}, false},
		{"job does not touch its machine", MemberRef{KindHeartbeat, "h1"}, Member{Kind: KindMachine, ID: "m1"}, false},
		{"job does not touch a service", MemberRef{KindHeartbeat, "h1"}, Member{Kind: KindService, ID: "s1"}, false},
		{"job does not touch a probe", MemberRef{KindHeartbeat, "h1"}, Member{Kind: KindProbe, ID: "p1"}, false},

		{"probe on itself", MemberRef{KindProbe, "p1"}, Member{Kind: KindProbe, ID: "p1"}, true},
		{"probe on another probe", MemberRef{KindProbe, "p1"}, Member{Kind: KindProbe, ID: "p2"}, false},
		{"probe does not touch the machine it runs from", MemberRef{KindProbe, "p1"}, Member{Kind: KindMachine, ID: "m2"}, false},
		{"probe does not touch the service it watches", MemberRef{KindProbe, "p1"}, Member{Kind: KindService, ID: "s1"}, false},

		{"vanished target touches nothing", MemberRef{KindMachine, "gone"}, Member{Kind: KindMachine, ID: "gone"}, false},
		{"vanished member is touched by nothing", MemberRef{KindMachine, "m1"}, Member{Kind: KindService, ID: "gone"}, false},
		{"unknown kind touches nothing", MemberRef{"container", "m1"}, Member{Kind: KindMachine, ID: "m1"}, false},
	}
	known := ruleFacts()
	for _, tc := range cases {
		if got := known.touches(tc.target, tc.member); got != tc.want {
			t.Errorf("%s: touches = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Un composant est touché si l'un de ses membres l'est, et ne paraît qu'une
// fois quel que soit le nombre de cibles qui le touchent.
func TestTouchedComponents_KeepsTheOrderOfTheComponentsAndEachOnce(t *testing.T) {
	known := ruleFacts()
	components := []Component{
		{ID: "c-job", Name: "Tâche", Members: []Member{{Kind: KindHeartbeat, ID: "h2"}}},
		{ID: "c-web", Name: "Web", Members: []Member{{Kind: KindService, ID: "s1"}, {Kind: KindProbe, ID: "p1"}}},
		{ID: "c-empty", Name: "Vide"},
	}
	got := known.touchedComponents([]MemberRef{{KindMachine, "m1"}, {KindService, "s1"}}, components)
	if len(got) != 1 || got[0] != (ComponentRef{ID: "c-web", Name: "Web"}) {
		t.Fatalf("touched = %+v, want the web component once", got)
	}
	if got := known.touchedComponents(nil, components); got == nil || len(got) != 0 {
		t.Fatalf("no target must give an empty list, not nil: %#v", got)
	}
}
