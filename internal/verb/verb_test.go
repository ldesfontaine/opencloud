package verb

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func valid() Verb {
	return Verb{ID: "thing.start", Route: "POST /api/things/{id}/actions/start", Params: []string{"id"}, Target: TargetMachine, Label: "l", Summary: "s", Reverse: "thing.stop"}
}

func TestRegister_RefusesAVerbWithoutReverse(t *testing.T) {
	v := valid()
	v.Reverse = ""
	if err := (&Registry{}).Register(v); !errors.Is(err, ErrNoReverse) {
		t.Fatalf("got %v, want ErrNoReverse", err)
	}
}

func confirmable() Verb {
	return Verb{ID: "thing.delete", Route: "DELETE /api/things/{id}", Params: []string{"id"}, Target: TargetMachine, Label: "l", Summary: "s", Confirm: true}
}

// Un verbe à confirmation remplace l'inverse par la confirmation : il n'en a
// pas, et sa route peut être un DELETE ou un PUT.
func TestRegister_AcceptsAVerbToConfirmWithoutReverse(t *testing.T) {
	registry := &Registry{}
	registry.MustRegister(confirmable())
	put := confirmable()
	put.ID, put.Route = "thing.replace", "PUT /api/things/{id}"
	registry.MustRegister(put)
	if err := registry.Check(); err != nil {
		t.Fatalf("a verb to confirm has no reverse to check: %v", err)
	}
}

func TestRegister_RefusesMalformedVerbsToConfirm(t *testing.T) {
	cases := map[string]struct {
		mutate func(*Verb)
		want   error
	}{
		"with a reverse":   {func(v *Verb) { v.Reverse = "thing.create" }, ErrConfirmReverse},
		"a read":           {func(v *Verb) { v.Route = "GET /api/things/{id}" }, ErrRouteInvalid},
		"outside api":      {func(v *Verb) { v.Route = "DELETE /agent/things/{id}" }, ErrRouteInvalid},
		"not on a machine": {func(v *Verb) { v.Target = TargetProbe }, ErrConfirmTarget},
	}
	for name, c := range cases {
		v := confirmable()
		c.mutate(&v)
		if err := (&Registry{}).Register(v); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", name, err, c.want)
		}
	}
}

// Un DELETE ne passe pas pour un verbe ordinaire : seule la confirmation
// l'ouvre.
func TestRegister_RefusesADeleteWithoutConfirmation(t *testing.T) {
	v := valid()
	v.Route = "DELETE /api/things/{id}"
	if err := (&Registry{}).Register(v); !errors.Is(err, ErrRouteInvalid) {
		t.Fatalf("got %v, want ErrRouteInvalid", err)
	}
}

func TestDefault_MachineDeleteAsksForAConfirmation(t *testing.T) {
	found, ok := Default.ByRoute("DELETE /api/machines/{id}")
	if !ok || found.ID != "machine.delete" || !found.Confirm || found.Reverse != "" {
		t.Fatalf("got %+v %v", found, ok)
	}
}

func TestRegister_RefusesMalformedVerbs(t *testing.T) {
	cases := map[string]func(*Verb){
		"id without dot": func(v *Verb) { v.ID = "start" },
		"id uppercase":   func(v *Verb) { v.ID = "Thing.Start" },
		"read route":     func(v *Verb) { v.Route = "GET /api/things" },
		"outside api":    func(v *Verb) { v.Route = "POST /agent/things" },
		"no target":      func(v *Verb) { v.Target = "" },
		"unknown target": func(v *Verb) { v.Target = "disk" },
		"no label":       func(v *Verb) { v.Label = "" },
		"no summary":     func(v *Verb) { v.Summary = "" },
		"missing param":  func(v *Verb) { v.Params = nil },
		"invented param": func(v *Verb) { v.Params = []string{"id", "vmid"} },
	}
	for name, mutate := range cases {
		v := valid()
		mutate(&v)
		if err := (&Registry{}).Register(v); err == nil {
			t.Errorf("%s: registered", name)
		}
	}
}

func TestRegister_RefusesDuplicates(t *testing.T) {
	registry := &Registry{}
	registry.MustRegister(valid())
	if err := registry.Register(valid()); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("same id: %v", err)
	}
	other := valid()
	other.ID = "thing.other"
	if err := registry.Register(other); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("same route: %v", err)
	}
}

func TestCheck_CatchesAReverseThatDoesNotExist(t *testing.T) {
	registry := &Registry{}
	registry.MustRegister(valid())
	if err := registry.Check(); !errors.Is(err, ErrUnknownReverse) {
		t.Fatalf("got %v, want ErrUnknownReverse", err)
	}
}

// Le garde-fou de la décision 19 : tout verbe offert déclare un inverse
// qui existe, et l'inverse de l'inverse est le verbe lui-même.
func TestDefault_EveryVerbHasARealReverse(t *testing.T) {
	if err := Default.Check(); err != nil {
		t.Fatal(err)
	}
	for _, v := range Default.All() {
		if v.Confirm {
			continue
		}
		back, _ := Default.Lookup(v.Reverse)
		if back.Reverse != v.ID {
			t.Errorf("%s -> %s -> %s: the reverse does not come back", v.ID, back.ID, back.Reverse)
		}
	}
}

func TestDefault_AcknowledgeStaysWithTheOperator(t *testing.T) {
	if _, found := Default.Lookup("alert.acknowledge"); found {
		t.Fatal("alert.acknowledge has no reverse: it must not be offered")
	}
}

func TestByRoute_FindsTheVerbOfARoute(t *testing.T) {
	found, ok := Default.ByRoute("POST /api/probes/{id}/actions/pause")
	if !ok || found.ID != "probe.pause" {
		t.Fatalf("got %+v %v", found, ok)
	}
	if _, ok := Default.ByRoute("DELETE /api/probes/{id}"); ok {
		t.Fatal("a deletion without confirmation is a verb")
	}
}

// Params et Body sont déclarés dans le registre : chaque verbe offert dit
// les segments de son chemin sans qu'on parse de texte.
func TestDefault_ParamsMatchTheRoutePath(t *testing.T) {
	for _, v := range Default.All() {
		if got := PathParams(v.Route); !slices.Equal(got, v.Params) {
			t.Errorf("%s: params %v, route says %v", v.ID, v.Params, got)
		}
	}
}

// Le cadrage #366 : un jeton lance, vérifie, parcourt et restaure à côté ;
// il ne crée, ne modifie ni ne supprime une cible, une sauvegarde ou une
// rétention. La liste est exacte : un verbe restic de plus fait échouer ce
// test, et c'est à Lucas de le décider.
func TestDefault_ResticVerbsAreTheFourOfTheTicketAndNoWrite(t *testing.T) {
	want := []string{"restic.backup.run", "restic.backup.verify", "restic.backup.restore", "restic.backup.browse"}
	var got []string
	for _, offered := range Default.All() {
		if strings.HasPrefix(offered.ID, "restic.") {
			got = append(got, offered.ID)
		}
		route := strings.ToLower(offered.Route)
		for _, forbidden := range []string{"retain", "forget", "prune", "restic-target", "retention"} {
			if strings.Contains(route, forbidden) {
				t.Errorf("%s : la route %s ouvre %s à un jeton", offered.ID, offered.Route, forbidden)
			}
		}
		if strings.HasPrefix(offered.ID, "restic.") && strings.HasPrefix(offered.Route, "PUT ") {
			t.Errorf("%s : un verbe restic ne modifie rien", offered.ID)
		}
		if strings.HasPrefix(offered.ID, "restic.") && strings.HasPrefix(offered.Route, "DELETE ") {
			t.Errorf("%s : un verbe restic ne supprime rien", offered.ID)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("verbes restic %v, attendus %v", got, want)
	}
}
