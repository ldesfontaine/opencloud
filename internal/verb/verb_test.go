package verb

import (
	"errors"
	"slices"
	"testing"
)

func valid() Verb {
	return Verb{ID: "thing.start", Route: "POST /api/things/{id}/actions/start", Params: []string{"id"}, Label: "l", Summary: "s", Reverse: "thing.stop"}
}

func TestRegister_RefusesAVerbWithoutReverse(t *testing.T) {
	v := valid()
	v.Reverse = ""
	if err := (&Registry{}).Register(v); !errors.Is(err, ErrNoReverse) {
		t.Fatalf("got %v, want ErrNoReverse", err)
	}
}

func TestRegister_RefusesMalformedVerbs(t *testing.T) {
	cases := map[string]func(*Verb){
		"id without dot": func(v *Verb) { v.ID = "start" },
		"id uppercase":   func(v *Verb) { v.ID = "Thing.Start" },
		"read route":     func(v *Verb) { v.Route = "GET /api/things" },
		"outside api":    func(v *Verb) { v.Route = "POST /agent/things" },
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
		t.Fatal("a deletion is a verb")
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
