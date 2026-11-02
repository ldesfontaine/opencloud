package container

import (
	"fmt"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/link"
)

func observedNamed(id, group, name string, targets ...EnvTarget) Observed {
	service := ""
	if group != "" {
		service = name
	}
	return Observed{ContainerID: id, Name: name, Group: group, ComposeService: service, EnvTargets: targets}
}

func TestEnvironmentLinks_ResolveInTheInventoryOnlyAndKeepOnePerPair(t *testing.T) {
	inventory := []Observed{
		observedNamed("a", "shop", "web",
			EnvTarget{Name: "db", Group: "shop", Port: 0}, EnvTarget{Name: "db", Group: "shop", Port: 5432},
			EnvTarget{Name: "web", Group: "shop"}, EnvTarget{Name: "ghost", Group: "shop"}, EnvTarget{Name: "db", Group: "other"}),
		observedNamed("b", "shop", "db"),
		observedNamed("c", "", "legacy", EnvTarget{Name: "db", Group: "shop"}),
	}
	got := EnvironmentLinks(inventory)
	want := []link.Seen{
		{From: link.ComposeKey("shop", "web"), To: link.ComposeKey("shop", "db"), Signal: link.SignalEnvironment, Port: 5432},
		{From: link.ContainerKey("legacy"), To: link.ComposeKey("shop", "db"), Signal: link.SignalEnvironment},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("links %+v, want %+v (itself, an unknown name and another project give nothing; the first port wins)", got, want)
	}
	if len(EnvironmentLinks(nil)) != 0 {
		t.Error("an empty inventory gives no link")
	}
}

func TestReport_WithoutEnvironmentDropsEveryTargetAndLeavesTheOriginalAlone(t *testing.T) {
	target := EnvTarget{Name: "db", Group: "shop"}
	original := Report{
		Inventory: []Observed{observedNamed("a", "shop", "web", target)},
		Events:    []Event{{ContainerID: "a", Container: &Observed{ContainerID: "a", EnvTargets: []EnvTarget{target}}}, {ContainerID: "b"}},
	}
	if !original.HasEnvironment() {
		t.Fatal("the report carries targets")
	}
	bound := original.WithoutEnvironment()
	if bound.HasEnvironment() || bound.Inventory[0].Name != "web" || len(bound.Events) != 2 {
		t.Errorf("bound report = %+v", bound)
	}
	if !original.HasEnvironment() {
		t.Error("the received report must not be modified")
	}
}

func TestKeepEnvTargets_DropsWhatIsUnreadableAndWhatExceedsTheBound(t *testing.T) {
	var targets []EnvTarget
	for index := range MaxEnvTargets + 3 {
		targets = append(targets, EnvTarget{Name: fmt.Sprintf("svc%02d", index)})
	}
	targets = append(targets, EnvTarget{Name: ""}, EnvTarget{Name: "x", Port: 70000}, EnvTarget{Name: "line\nbreak"})
	kept, discards := keepEnvTargets(Observed{Name: "web", EnvTargets: targets})
	if len(kept) != MaxEnvTargets || len(discards) != 6 {
		t.Errorf("kept %d, discarded %d", len(kept), len(discards))
	}
	if _, _, err := validateContainer(Observed{ContainerID: fmt.Sprintf("%064d", 1), Name: "web", State: "running", CreatedAt: time.Now().Add(-time.Hour), EnvTargets: targets}, time.Now()); err != nil {
		t.Errorf("a bad target must not refuse the container: %v", err)
	}
}
