package alert_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Un type d'alerte sans catégorie ne passerait que par les canaux sans
// filtre : ce test oblige chaque nouveau type à choisir sa famille.
func TestCategoryOf_EveryKindHasACategoryOfTheCatalog(t *testing.T) {
	for _, kind := range alert.Kinds() {
		category, found := alert.CategoryOf(kind)
		if !found || !slices.Contains(alert.Categories(), category) {
			t.Errorf("kind %q has no category in the catalog (got %q)", kind, category)
		}
	}
}

// Une catégorie que plus aucun type n'utilise est une case morte à l'écran.
func TestCategories_EveryCategoryHasAKind(t *testing.T) {
	for _, category := range alert.Categories() {
		used := slices.ContainsFunc(alert.Kinds(), func(kind alert.Kind) bool {
			got, _ := alert.CategoryOf(kind)
			return got == category
		})
		if !used {
			t.Errorf("category %q has no kind", category)
		}
	}
}

func updateFact() alert.Fact {
	return alert.Fact{
		Kind: alert.KindAgentRolledBack, Severity: alert.SeverityAttention,
		Object:    alert.Object{Kind: alert.ObjectMachine, ID: machine.LocalID, Name: "opencloud"},
		MachineID: machine.LocalID,
	}
}

func (f *fixture) channelOf(t *testing.T, name string, categories ...alert.Category) alert.Channel {
	t.Helper()
	channel, err := f.CreateChannel(context.Background(), alert.ChannelDefinition{
		Name: name, URL: "http://127.0.0.1:9/hook", Format: alert.FormatJSON, MinSeverity: alert.SeverityAttention, NotifyResolve: true,
		Categories: categories, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return channel
}

func TestChannel_Categories_ReceiveOnlyTheirFamily(t *testing.T) {
	f := newFixture(t)
	f.channelOf(t, "tout")
	f.channelOf(t, "maj", alert.CategoryUpdates)
	f.channelOf(t, "machines", alert.CategoryMachines)
	ctx := context.Background()

	f.Open(ctx, updateFact())
	if got := f.sender.events(); got != "opened:maj,opened:tout" {
		t.Fatalf("an update alert goes to the open channel and the updates channel: %s", got)
	}
	f.Open(ctx, volumeFact(alert.SeverityAttention, 86))
	if got := f.sender.events(); got != "opened:maj,opened:tout,opened:machines,opened:tout" {
		t.Fatalf("a disk alert must not reach the updates channel: %s", got)
	}
}

// Une résolution prévient les canaux déjà avertis, même si leurs catégories
// ont changé depuis : l'alerte annoncée ne reste pas ouverte chez eux.
func TestChannel_Categories_ResolveStillReachesAnAlreadyToldChannel(t *testing.T) {
	f := newFixture(t)
	channel := f.channelOf(t, "maj", alert.CategoryUpdates)
	ctx := context.Background()
	f.Open(ctx, updateFact())
	_, err := f.UpdateChannel(ctx, channel.ID, alert.ChannelDefinition{
		Name: "maj", URL: channel.URL, Format: channel.Format, MinSeverity: channel.MinSeverity, NotifyResolve: true,
		Categories: []alert.Category{alert.CategoryBackups}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.Gone(ctx, alert.Object{Kind: alert.ObjectMachine, ID: machine.LocalID})
	if got := f.sender.events(); got != "opened:maj,resolved:maj" {
		t.Fatalf("events %s", got)
	}
}

// Le plafond se compte par canal : un canal dédié a son propre budget, une
// rafale de disques pleins sur l'autre ne retient pas la mise à jour.
func TestChannel_Categories_HaveTheirOwnBudget(t *testing.T) {
	f := newFixture(t)
	f.channelOf(t, "tout")
	f.channelOf(t, "maj", alert.CategoryUpdates)
	ctx := context.Background()
	remote := f.enrollRemote(t)
	for _, machineID := range []string{machine.LocalID, remote.ID} {
		for i := range alert.MachineRateLimit {
			f.Open(ctx, f.volume(machineID, fmt.Sprintf("/m%d", i), alert.SeverityAttention))
		}
	}
	f.Open(ctx, updateFact())
	f.sender.mu.Lock()
	defer f.sender.mu.Unlock()
	last := f.sender.jobs[len(f.sender.jobs)-1]
	if last.Channel.Name != "maj" || last.Delivery.Event != alert.EventOpened {
		t.Fatalf("the updates channel must hear the update: %+v", last.Delivery)
	}
}

func TestChannelDefinition_Categories_AreValidatedAndNormalized(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	definition := alert.ChannelDefinition{Name: "x", URL: "http://127.0.0.1:9/hook", Format: alert.FormatJSON, MinSeverity: alert.SeverityAttention, Enabled: true}

	definition.Categories = []alert.Category{alert.CategoryUpdates, "inconnue"}
	if _, err := f.CreateChannel(ctx, definition); err == nil || err.Error() != alert.ErrChannelCategoryInvalid.Error() {
		t.Fatalf("an unknown category must be refused, not dropped: %v", err)
	}
	definition.Categories = []alert.Category{alert.CategoryUpdates, alert.CategoryMachines, alert.CategoryUpdates}
	created, err := f.CreateChannel(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	if want := []alert.Category{alert.CategoryMachines, alert.CategoryUpdates}; !slices.Equal(created.Categories, want) {
		t.Fatalf("categories %v, want %v (deduplicated, in catalog order)", created.Categories, want)
	}
}
