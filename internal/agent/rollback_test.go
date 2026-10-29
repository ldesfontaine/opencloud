package agent

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/rollback"
)

var rollbackNow = time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)

func (b *updateBench) installPrevious(t *testing.T, version string) {
	t.Helper()
	if err := b.stateDir.WriteFile("bin/opencloud-"+version, []byte("binary "+version), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (b *updateBench) link(t *testing.T, name string) string {
	t.Helper()
	target, err := b.stateDir.Readlink("bin/" + name)
	if err != nil {
		t.Fatalf("link %s: %v", name, err)
	}
	return target
}

func TestAgentUpdate_WritesTheMarkerAndThePreviousLinkWithTheGracePeriod(t *testing.T) {
	b := newUpdateBench(t, "v0.0.1")
	b.updater.now = func() time.Time { return rollbackNow }
	published := b.publish(t, "v0.0.2", "v0.0.2", b.private)
	if err := b.updater.update(context.Background(), sourceFor(t, published.server), published.params, "run42"); err != nil {
		t.Fatal(err)
	}
	marker, err := rollback.Load(b.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	want := rollback.Marker{
		State: rollback.StatePending, Kind: rollback.KindUpdate, RunID: "run42", Target: "v0.0.2", Previous: "v0.0.1",
		StartedAt: rollbackNow, Deadline: rollbackNow.Add(rollback.GracePeriod),
	}
	if marker != want {
		t.Fatalf("marker %+v, want %+v", marker, want)
	}
	if b.link(t, "previous") != "opencloud-v0.0.1" || b.link(t, "current") != "opencloud-v0.0.2" {
		t.Fatalf("previous %s, current %s", b.link(t, "previous"), b.link(t, "current"))
	}
	// L'ancien binaire reste intact, c'est ce que le lanceur rebasculera.
	if content, _ := b.stateDir.ReadFile("bin/opencloud-v0.0.1"); string(content) != "the running binary" {
		t.Fatalf("the previous binary became %q", content)
	}
	if rollback.GracePeriod != 3*time.Minute {
		t.Fatalf("the grace period is %s", rollback.GracePeriod)
	}
}

func TestAgentUpdate_WithoutAVersionToReturnTo_IsRefusedBeforeAnySwitch(t *testing.T) {
	b := newUpdateBench(t, "v0.0.1")
	// current n'est pas un lien vers un binaire nommé comme une version.
	if err := b.stateDir.Remove("bin/current"); err != nil {
		t.Fatal(err)
	}
	if err := b.stateDir.Symlink("opencloud-dev", "bin/current"); err != nil {
		t.Fatal(err)
	}
	err := b.update(t, b.publish(t, "v0.0.2", "v0.0.2", b.private))
	if reason := reasonOf(t, err); reason != agentwire.ReasonNoPrevious {
		t.Fatalf("reason %s", reason)
	}
	if _, err := rollback.Load(b.stateDir); !errors.Is(err, rollback.ErrNoMarker) {
		t.Fatalf("a marker was written: %v", err)
	}
	if _, err := b.stateDir.Lstat("bin/opencloud-v0.0.2"); err == nil {
		t.Fatal("the new binary was installed")
	}
}

// Sans lanceur stable, aucune bascule : rien ne ramènerait l'agent en arrière.
func TestOrderRunner_WithoutTheGuard_NeitherUpdateNorRollbackIsAccepted(t *testing.T) {
	b := newOrderBenchWithRights(t, []string{"observe", "agent.update"})
	update := newUpdateBench(t, "v0.0.1")
	update.updater.guarded = false
	b.runner.updater = update.updater
	for _, kind := range []agentwire.Kind{agentwire.KindAgentUpdate, agentwire.KindAgentRollback} {
		body := order(int64(len(kind)))
		body.Kind = kind
		result, job := b.runner.execute(body)
		if result.State != agentwire.ActionRefused || result.Reason != agentwire.ReasonGuardMissing || job != nil {
			t.Errorf("%s: %+v", kind, result)
		}
	}
}

func TestAgentRollback_SwitchesToPreviousAndWritesTheSameMarker(t *testing.T) {
	b := newUpdateBench(t, "v0.0.2")
	b.updater.now = func() time.Time { return rollbackNow }
	// L'état après une mise à jour : current sur v0.0.2, previous sur v0.0.1.
	b.installPrevious(t, "v0.0.2")
	if err := b.stateDir.Remove("bin/current"); err != nil {
		t.Fatal(err)
	}
	if err := b.stateDir.Symlink("opencloud-v0.0.2", "bin/current"); err != nil {
		t.Fatal(err)
	}
	if err := b.stateDir.Symlink("opencloud-v0.0.1", "bin/previous"); err != nil {
		t.Fatal(err)
	}
	if err := b.updater.rollback(context.Background(), "run9"); err != nil {
		t.Fatal(err)
	}
	if b.link(t, "current") != "opencloud-v0.0.1" || b.link(t, "previous") != "opencloud-v0.0.2" {
		t.Fatalf("current %s, previous %s", b.link(t, "current"), b.link(t, "previous"))
	}
	marker, err := rollback.Load(b.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	want := rollback.Marker{
		State: rollback.StatePending, Kind: rollback.KindRollback, RunID: "run9", Target: "v0.0.1", Previous: "v0.0.2",
		StartedAt: rollbackNow, Deadline: rollbackNow.Add(rollback.GracePeriod),
	}
	if marker != want {
		t.Fatalf("marker %+v, want %+v", marker, want)
	}
	// La version d'avant est éprouvée comme une version neuve.
	if len(b.checked) != 1 || !strings.HasSuffix(b.checked[0], "opencloud-v0.0.1 v0.0.1") {
		t.Fatalf("checked %v", b.checked)
	}
}

func TestAgentRollback_RefusesWhenThereIsNothingToReturnTo(t *testing.T) {
	b := newUpdateBench(t, "v0.0.1")
	// Aucun lien previous.
	err := b.updater.rollback(context.Background(), "run1")
	if reason := reasonOf(t, err); reason != agentwire.ReasonNoPrevious {
		t.Fatalf("reason %s", reason)
	}
	// Un précédent identique à ce qui tourne.
	if err := b.stateDir.Symlink("opencloud-v0.0.1", "bin/previous"); err != nil {
		t.Fatal(err)
	}
	err = b.updater.rollback(context.Background(), "run2")
	if reason := reasonOf(t, err); reason != agentwire.ReasonPreviousIdentical {
		t.Fatalf("reason %s", reason)
	}
	if _, err := rollback.Load(b.stateDir); !errors.Is(err, rollback.ErrNoMarker) {
		t.Fatalf("a refused rollback wrote a marker: %v", err)
	}
	if b.link(t, "current") != "opencloud-v0.0.1" || b.restarted != 0 {
		t.Fatal("a refused rollback moved something")
	}
}

func TestAgentRollback_ABrokenPreviousKeepsTheLink(t *testing.T) {
	b := newUpdateBench(t, "v0.0.2")
	b.installPrevious(t, "v0.0.2")
	if err := b.stateDir.Remove("bin/current"); err != nil {
		t.Fatal(err)
	}
	if err := b.stateDir.Symlink("opencloud-v0.0.2", "bin/current"); err != nil {
		t.Fatal(err)
	}
	if err := b.stateDir.Symlink("opencloud-v0.0.1", "bin/previous"); err != nil {
		t.Fatal(err)
	}
	b.checkErr = errors.New("exec format error")
	err := b.updater.rollback(context.Background(), "run1")
	if reason := reasonOf(t, err); reason != agentwire.ReasonSelfCheckFailed {
		t.Fatalf("reason %s", reason)
	}
	if b.link(t, "current") != "opencloud-v0.0.2" {
		t.Fatal("a previous that does not start was switched to")
	}
	if _, err := rollback.Load(b.stateDir); !errors.Is(err, rollback.ErrNoMarker) {
		t.Fatalf("marker: %v", err)
	}
}

type outcomeBench struct {
	*updateBench
	outcome *updateOutcome
	results *actionResults
	logs    *bytes.Buffer
}

func newOutcomeBench(t *testing.T, version string) *outcomeBench {
	t.Helper()
	b := newUpdateBench(t, version)
	logs := &bytes.Buffer{}
	results := newActionResults()
	return &outcomeBench{
		updateBench: b, results: results, logs: logs,
		outcome: newUpdateOutcome(b.stateDir, version, results, slog.New(slog.NewTextHandler(logs, nil))),
	}
}

func (o *outcomeBench) save(t *testing.T, marker rollback.Marker) {
	t.Helper()
	if marker.RunID == "" {
		marker.RunID = "run7"
	}
	if marker.Target == "" {
		marker.Target, marker.Previous = "v0.0.2", "v0.0.1"
	}
	if marker.Kind == "" {
		marker.Kind = rollback.KindUpdate
	}
	marker.StartedAt, marker.Deadline = rollbackNow, rollbackNow.Add(rollback.GracePeriod)
	if err := rollback.Save(o.stateDir, marker); err != nil {
		t.Fatal(err)
	}
}

func TestOutcome_AnUpdateIsConfirmedWhenTheTargetVersionRunsAndTheServerAcknowledged(t *testing.T) {
	o := newOutcomeBench(t, "v0.0.2")
	o.save(t, rollback.Marker{State: rollback.StatePending})
	o.outcome.announce()
	if report := o.results.take(); report != nil {
		t.Fatalf("a pending update reported %+v", report)
	}
	o.outcome.confirm()
	if _, err := rollback.Load(o.stateDir); !errors.Is(err, rollback.ErrNoMarker) {
		t.Fatalf("the confirmed update left its marker: %v", err)
	}
	// La fin de l'ordre est constatée par le serveur au retour : l'agent ne
	// répond rien de plus.
	if report := o.results.take(); report != nil {
		t.Fatalf("a confirmed update reported %+v", report)
	}
}

func TestOutcome_AnotherVersionThanTheTargetConfirmsNothing(t *testing.T) {
	o := newOutcomeBench(t, "v0.0.1")
	o.save(t, rollback.Marker{State: rollback.StatePending})
	o.outcome.confirm()
	if marker, err := rollback.Load(o.stateDir); err != nil || marker.State != rollback.StatePending {
		t.Fatalf("marker %+v %v", marker, err)
	}
}

func TestOutcome_ARollbackIsConfirmedThenReportedAsDoneAndRemovedOnceDelivered(t *testing.T) {
	o := newOutcomeBench(t, "v0.0.1")
	o.save(t, rollback.Marker{State: rollback.StatePending, Kind: rollback.KindRollback, Target: "v0.0.1", Previous: "v0.0.2"})
	o.outcome.confirm()
	report := o.results.take()
	if report == nil || len(report.Results) != 1 || report.Results[0].State != agentwire.ActionSucceeded || report.Results[0].RunID != "run7" {
		t.Fatalf("report %+v", report)
	}
	// Tant que le serveur n'a pas écrit le signal, le marqueur reste : un
	// redémarrage rapporterait encore.
	if marker, err := rollback.Load(o.stateDir); err != nil || marker.State != rollback.StateConfirmed {
		t.Fatalf("marker %+v %v", marker, err)
	}
	o.outcome.delivered(&agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: "other"}}})
	if _, err := rollback.Load(o.stateDir); err != nil {
		t.Fatal("another run's delivery removed the marker")
	}
	o.outcome.delivered(report)
	if _, err := rollback.Load(o.stateDir); !errors.Is(err, rollback.ErrNoMarker) {
		t.Fatalf("delivered, the marker stayed: %v", err)
	}
}

// Le lanceur a ramené l'agent : au retour de l'ancienne version, il dit au
// journal et au serveur ce qui s'est passé.
func TestOutcome_AnAutomaticReturnIsReportedAsFailedWithItsCause(t *testing.T) {
	cases := map[rollback.Cause]string{
		rollback.CauseTimeout: agentwire.ReasonRolledBackTimeout,
		rollback.CauseCrash:   agentwire.ReasonRolledBackCrash,
	}
	for cause, reason := range cases {
		o := newOutcomeBench(t, "v0.0.1")
		o.save(t, rollback.Marker{State: rollback.StateRolledBack, Cause: cause})
		o.outcome.announce()
		report := o.results.take()
		if report == nil || len(report.Results) != 1 {
			t.Fatalf("%s: report %+v", cause, report)
		}
		result := report.Results[0]
		if result.RunID != "run7" || result.State != agentwire.ActionFailed || result.Reason != reason {
			t.Errorf("%s: %+v", cause, result)
		}
		lines := make([]string, 0, len(result.Output))
		for _, line := range result.Output {
			lines = append(lines, line.Line)
		}
		if got := strings.Join(lines, "|"); got != "target v0.0.2|previous v0.0.1|running v0.0.1" {
			t.Errorf("%s: output %q", cause, got)
		}
		if !strings.Contains(o.logs.String(), "level=ERROR") || !strings.Contains(o.logs.String(), "back on the previous version") {
			t.Errorf("%s: the return is not loud in the log: %s", cause, o.logs.String())
		}
		// Rien n'est retiré avant la livraison.
		if _, err := rollback.Load(o.stateDir); err != nil {
			t.Errorf("%s: %v", cause, err)
		}
		o.outcome.delivered(report)
		if _, err := rollback.Load(o.stateDir); !errors.Is(err, rollback.ErrNoMarker) {
			t.Errorf("%s: %v", cause, err)
		}
	}
}

func TestOutcome_AFailedRecoveryIsSaidLoudAndReportedAsRollbackFailed(t *testing.T) {
	o := newOutcomeBench(t, "v0.0.1")
	o.save(t, rollback.Marker{State: rollback.StateFailed, Cause: rollback.CausePreviousCrash})
	o.outcome.announce()
	report := o.results.take()
	if report == nil || report.Results[0].Reason != agentwire.ReasonRollbackFailed || report.Results[0].State != agentwire.ActionFailed {
		t.Fatalf("report %+v", report)
	}
	if !strings.Contains(o.logs.String(), "level=ERROR") || !strings.Contains(o.logs.String(), "needs a hand") {
		t.Fatalf("log: %s", o.logs.String())
	}
}

func TestOutcome_AnUnreadableMarkerIsRemovedAndNeverStopsTheAgent(t *testing.T) {
	o := newOutcomeBench(t, "v0.0.2")
	if err := o.stateDir.WriteFile(brand.AgentUpdateMarker, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	o.outcome.announce()
	o.outcome.confirm()
	if report := o.results.take(); report != nil {
		t.Fatalf("report %+v", report)
	}
	if _, err := rollback.Load(o.stateDir); !errors.Is(err, rollback.ErrNoMarker) {
		t.Fatalf("marker: %v", err)
	}
	// Absent : rien à dire non plus.
	o.outcome.announce()
	o.outcome.confirm()
}

func TestGuardVersion_OnlyAKnownPositiveVersionCounts(t *testing.T) {
	for text, want := range map[string]int{"": 0, "0": 0, "-1": 0, "x": 0, "1": 1, "2": agentwire.RollbackVersion, "99": agentwire.RollbackVersion} {
		if got := guardVersion(text); got != want {
			t.Errorf("%q: %d, want %d", text, got, want)
		}
	}
}
