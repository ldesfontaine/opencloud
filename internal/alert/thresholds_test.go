package alert_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/resource"
	"github.com/ldesfontaine/opencloud/internal/sampler"
)

// feed écrit une lecture toutes les 10 s sur les `span` qui finissent à
// l'horloge, puis les annonce au moteur : le moteur juge une fois, sur la
// dernière, avec ce que la base garde.
func (f *fixture) feed(t *testing.T, machineID string, span time.Duration, cpuPercent float64, memoryPercent int64) {
	t.Helper()
	resources := resource.New(f.db, discardLogger())
	resources.SetClock(func() time.Time { return f.clock })
	resources.SetAlerter(f.Engine)
	var readings []sampler.Reading
	for at := f.clock.Add(-span + resource.SampleInterval); !at.After(f.clock); at = at.Add(resource.SampleInterval) {
		readings = append(readings, sampler.Reading{
			SampledAt: at, CPUPercent: cpuPercent, CPUCores: 2, MemUsed: memoryPercent * 10, MemTotal: 1000, DiskUsed: 1, DiskTotal: 2,
		})
	}
	if err := resources.Record(context.Background(), machineID, readings); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) rule(t *testing.T, machineID string, resource alert.Resource) alert.Applied {
	t.Helper()
	rules, err := f.Rules(context.Background(), machineID)
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range rules {
		if rule.Resource == resource {
			return rule
		}
	}
	t.Fatalf("no rule for %s", resource)
	return alert.Applied{}
}

func TestRules_MachineBeforeGlobalBeforeDefault(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	enrolled := f.enrollRemote(t)

	got := f.rule(t, enrolled.ID, alert.ResourceCPU)
	if got.Origin != alert.OriginDefault || got.Rule != alert.DefaultRule(alert.ResourceCPU) {
		t.Fatalf("nothing set: %+v", got)
	}
	global := alert.Rule{Resource: alert.ResourceCPU, Attention: 80, Danger: 90, Window: 10 * time.Minute}
	if err := f.SetRule(ctx, "", global); err != nil {
		t.Fatal(err)
	}
	if got := f.rule(t, enrolled.ID, alert.ResourceCPU); got.Origin != alert.OriginGlobal || got.Rule != global {
		t.Fatalf("global set: %+v", got)
	}
	override := alert.Rule{Resource: alert.ResourceCPU, Attention: 98, Danger: 100, Window: 30 * time.Minute}
	if err := f.SetRule(ctx, enrolled.ID, override); err != nil {
		t.Fatal(err)
	}
	if got := f.rule(t, enrolled.ID, alert.ResourceCPU); got.Origin != alert.OriginMachine || got.Rule != override {
		t.Fatalf("override set: %+v", got)
	}
	// Une autre machine, et l'autre ressource, suivent leur propre chemin.
	if got := f.rule(t, machine.LocalID, alert.ResourceCPU); got.Origin != alert.OriginGlobal || got.Rule != global {
		t.Fatalf("another machine: %+v", got)
	}
	if got := f.rule(t, enrolled.ID, alert.ResourceMemory); got.Origin != alert.OriginDefault {
		t.Fatalf("another resource: %+v", got)
	}
	if err := f.ClearRule(ctx, enrolled.ID, alert.ResourceCPU); err != nil {
		t.Fatal(err)
	}
	if got := f.rule(t, enrolled.ID, alert.ResourceCPU); got.Origin != alert.OriginGlobal || got.Rule != global {
		t.Fatalf("override cleared: %+v", got)
	}
}

func TestSetRule_RefusesWhatMakesNoSense_AndKeepsThePrevious(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	refused := []alert.Rule{
		{Resource: alert.ResourceDisk, Attention: 49, Danger: 95},
		{Resource: alert.ResourceDisk, Attention: 85, Danger: 100},
		{Resource: alert.ResourceDisk, Attention: 90, Danger: 90},
		{Resource: alert.ResourceDisk, Attention: 95, Danger: 85},
		{Resource: alert.ResourceDisk, Attention: 85, Danger: 95, Window: time.Minute},
		{Resource: alert.ResourceCPU, Attention: 95, Danger: 101, Window: 15 * time.Minute},
		{Resource: alert.ResourceCPU, Attention: 95, Danger: 99},
		{Resource: alert.ResourceMemory, Attention: 90, Danger: 95, Window: 30 * time.Second},
		{Resource: alert.ResourceMemory, Attention: 90, Danger: 95, Window: 3 * time.Hour},
		{Resource: alert.ResourceMemory, Attention: 90, Danger: 95, Window: 90 * time.Second},
		{Resource: "network", Attention: 80, Danger: 90},
		{},
	}
	for _, rule := range refused {
		if err := f.SetRule(ctx, "", rule); !errors.Is(err, alert.ErrThresholdInvalid) {
			t.Errorf("%+v accepted: %v", rule, err)
		}
	}
	if got := f.rule(t, "", alert.ResourceDisk); got.Origin != alert.OriginDefault {
		t.Fatalf("a refusal must not write: %+v", got)
	}
	if err := f.SetRule(ctx, "", alert.Rule{Resource: alert.ResourceDisk, Attention: 50, Danger: 99}); err != nil {
		t.Fatalf("50/99 refused: %v", err)
	}
	if err := f.SetRule(ctx, "no-such-machine", alert.DefaultRule(alert.ResourceDisk)); !errors.Is(err, alert.ErrNotFound) {
		t.Fatalf("an unknown machine: %v", err)
	}
	if err := f.ClearRule(ctx, "", alert.ResourceDisk); !errors.Is(err, alert.ErrThresholdInvalid) {
		t.Fatalf("the global rule cannot be cleared: %v", err)
	}
}

// Les seuils réglés valent dès la lecture suivante, sans redémarrer le
// moteur : la même lecture à 72 % n'alerte pas à 85, alerte à 70, et le
// retour suit l'attention cinq points dessous.
func TestResource_DiskRuleIsReadAtEachReading(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	resources := resource.New(f.db, discardLogger())
	resources.SetClock(func() time.Time { return f.clock })
	resources.SetAlerter(f.Engine)
	record := func(percent int) {
		f.advance(10 * time.Second)
		disks := []sampler.Disk{{MountPoint: "/data", Device: "/dev/vdb", Used: int64(percent) * 10, Total: 1000}}
		reading := sampler.Reading{SampledAt: f.clock, CPUCores: 2, MemTotal: 1, DiskUsed: 1, DiskTotal: 2, Disks: disks}
		if err := resources.Record(ctx, machine.LocalID, []sampler.Reading{reading}); err != nil {
			t.Fatal(err)
		}
	}
	record(72)
	if f.kinds(t) != "" {
		t.Fatalf("72 %% under the default threshold: %q", f.kinds(t))
	}
	if err := f.SetRule(ctx, "", alert.Rule{Resource: alert.ResourceDisk, Attention: 70, Danger: 80}); err != nil {
		t.Fatal(err)
	}
	record(72)
	if f.kinds(t) != "disk_full:attention" || f.only(t, alert.StatusOpen).Details.Threshold != 70 {
		t.Fatalf("72 %% over a 70 %% threshold: %q", f.kinds(t))
	}
	record(81)
	if f.kinds(t) != "disk_full:danger" {
		t.Fatalf("81 %% over a 80 %% danger: %q", f.kinds(t))
	}
	record(66)
	if f.kinds(t) != "disk_full:danger" {
		t.Fatal("66 % is above the 65 % recovery: nothing moves")
	}
	record(64)
	if f.kinds(t) != "" {
		t.Fatalf("64 %% under the recovery: %q", f.kinds(t))
	}
}

// Une surcharge de machine juge sa machine seule : la même lecture alerte
// sur la machine qui suit le global, pas sur celle qui a plus de tolérance.
func TestResource_MachineOverrideJudgesItsMachineOnly(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	enrolled := f.enrollRemote(t)
	tolerant := alert.Rule{Resource: alert.ResourceDisk, Attention: 95, Danger: 99}
	if err := f.SetRule(ctx, enrolled.ID, tolerant); err != nil {
		t.Fatal(err)
	}
	resources := resource.New(f.db, discardLogger())
	resources.SetClock(func() time.Time { return f.clock })
	resources.SetAlerter(f.Engine)
	f.advance(10 * time.Second)
	disks := []sampler.Disk{{MountPoint: "/data", Device: "/dev/vdb", Used: 900, Total: 1000}}
	reading := sampler.Reading{SampledAt: f.clock, CPUCores: 2, MemTotal: 1, DiskUsed: 1, DiskTotal: 2, Disks: disks}
	for _, id := range []string{machine.LocalID, enrolled.ID} {
		if err := resources.Record(ctx, id, []sampler.Reading{reading}); err != nil {
			t.Fatal(err)
		}
	}
	opened := f.only(t, alert.StatusOpen)
	if opened.MachineID != machine.LocalID {
		t.Fatalf("90 %% alerts on the machine that follows the global rule only: %+v", opened)
	}
}

func TestResource_CPUAlertsOnTheAverageOfItsWindow_NotOnASpike(t *testing.T) {
	f := newFixture(t)
	f.feed(t, machine.LocalID, 15*time.Minute, 50, 10)
	f.advance(10 * time.Second)
	f.feed(t, machine.LocalID, 10*time.Second, 100, 10)
	if f.kinds(t) != "" {
		t.Fatalf("a spike on top of a quiet quarter hour: %q", f.kinds(t))
	}
}

func TestResource_CPUAverageOpensAggravatesAndResolvesWithHysteresis(t *testing.T) {
	f := newFixture(t)
	f.feed(t, machine.LocalID, 16*time.Minute, 97, 10)
	if f.kinds(t) != "cpu_high:attention" {
		t.Fatalf("97 %% over the whole window: %q", f.kinds(t))
	}
	opened := f.only(t, alert.StatusOpen)
	if opened.Object.Kind != alert.ObjectMachine || opened.Object.Name != "opencloud" || opened.Details.Percent != 97 ||
		opened.Details.WindowMinutes != 15 || opened.Details.Threshold != 95 {
		t.Fatalf("alert %+v", opened)
	}
	f.advance(16 * time.Minute)
	f.feed(t, machine.LocalID, 16*time.Minute, 100, 10)
	if f.kinds(t) != "cpu_high:danger" {
		t.Fatalf("100 %% over the window: %q", f.kinds(t))
	}
	f.advance(16 * time.Minute)
	f.feed(t, machine.LocalID, 16*time.Minute, 92, 10)
	if f.kinds(t) != "cpu_high:danger" {
		t.Fatalf("92 %% is above the 90 %% recovery: nothing moves, got %q", f.kinds(t))
	}
	f.advance(16 * time.Minute)
	f.feed(t, machine.LocalID, 16*time.Minute, 80, 10)
	if f.kinds(t) != "" {
		t.Fatalf("80 %% under the recovery: %q", f.kinds(t))
	}
}

// Un agent qui vient d'arriver, ou qui a été coupé, n'a pas de moyenne :
// cinq minutes à 100 % ne suffisent pas à une fenêtre de quinze.
func TestResource_AverageWaitsUntilTheReadingsCoverTheWindow(t *testing.T) {
	f := newFixture(t)
	f.feed(t, machine.LocalID, 5*time.Minute, 100, 10)
	if f.kinds(t) != "" {
		t.Fatalf("five minutes cover a third of the window: %q", f.kinds(t))
	}
	f.advance(5 * time.Minute)
	f.feed(t, machine.LocalID, 6*time.Minute, 100, 10)
	if f.kinds(t) != "" {
		t.Fatalf("eleven minutes: %q", f.kinds(t))
	}
	f.advance(6 * time.Minute)
	f.feed(t, machine.LocalID, 6*time.Minute, 100, 10)
	if f.kinds(t) != "cpu_high:danger" {
		t.Fatalf("seventeen minutes: %q", f.kinds(t))
	}
}

// Une fenêtre trouée (agent coupé la plupart du temps) n'est pas une
// moyenne : la plus ancienne lecture date bien du début, mais trop peu de
// lectures la portent.
func TestResource_AverageIgnoresAWindowFullOfHoles(t *testing.T) {
	f := newFixture(t)
	f.feed(t, machine.LocalID, 10*time.Second, 100, 10)
	f.advance(15 * time.Minute)
	f.feed(t, machine.LocalID, 10*time.Second, 100, 10)
	if f.kinds(t) != "" {
		t.Fatalf("two readings in a quarter hour: %q", f.kinds(t))
	}
}

func TestResource_MemoryAlertsOnItsAverage_AndTheRuleIsPerMachine(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	enrolled := f.enrollRemote(t)
	if err := f.SetRule(ctx, enrolled.ID, alert.Rule{Resource: alert.ResourceMemory, Attention: 96, Danger: 99, Window: 15 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	f.feed(t, machine.LocalID, 16*time.Minute, 10, 92)
	f.feed(t, enrolled.ID, 16*time.Minute, 10, 92)
	if f.kinds(t) != "memory_high:attention" {
		t.Fatalf("92 %% alerts on the global rule only: %q", f.kinds(t))
	}
	if opened := f.only(t, alert.StatusOpen); opened.MachineID != machine.LocalID {
		t.Fatalf("the tolerant machine must not alert: %+v", opened)
	}
}

func TestResource_AlertsFollowARuleChangedBetweenTwoReadings(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.feed(t, machine.LocalID, 16*time.Minute, 93, 10)
	if f.kinds(t) != "" {
		t.Fatalf("93 %% under the 95 %% default: %q", f.kinds(t))
	}
	if err := f.SetRule(ctx, "", alert.Rule{Resource: alert.ResourceCPU, Attention: 90, Danger: 99, Window: 5 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	f.advance(10 * time.Second)
	f.feed(t, machine.LocalID, 10*time.Second, 93, 10)
	if f.kinds(t) != "cpu_high:attention" {
		t.Fatalf("93 %% over a 90 %% rule: %q", f.kinds(t))
	}
	if f.only(t, alert.StatusOpen).Details.WindowMinutes != 5 {
		t.Fatalf("the alert must carry the window that judged it: %+v", f.only(t, alert.StatusOpen).Details)
	}
}

// Une règle écrite à la main hors des bornes n'est jamais lue : elle ne
// coupe pas l'alerte, la règle suivante (ici celle du produit) s'applique.
func TestRules_AnOutOfBoundsRowInTheDatabaseIsIgnored(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.db.UpsertThreshold(ctx, alert.Threshold{Rule: alert.Rule{Resource: alert.ResourceDisk, Attention: 40, Danger: 95}}); err != nil {
		t.Fatal(err)
	}
	if got := f.rule(t, machine.LocalID, alert.ResourceDisk); got.Origin != alert.OriginDefault || got.Rule != alert.DefaultRule(alert.ResourceDisk) {
		t.Fatalf("a bad row must fall back to the defaults: %+v", got)
	}
	override := alert.Rule{Resource: alert.ResourceDisk, Attention: 90, Danger: 95}
	if err := f.SetRule(ctx, machine.LocalID, override); err != nil {
		t.Fatal(err)
	}
	if got := f.rule(t, machine.LocalID, alert.ResourceDisk); got.Origin != alert.OriginMachine || got.Rule != override {
		t.Fatalf("a good override stays above a bad global row: %+v", got)
	}
}
