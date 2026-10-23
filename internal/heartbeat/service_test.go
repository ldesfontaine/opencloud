package heartbeat_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/seal"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

var testNow = time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)

// Le service se teste sur la vraie base SQLite, temporaire, avec une horloge
// qu'on avance à la main.
type fixture struct {
	*heartbeat.Service
	db      *store.DB
	clock   time.Time
	alerter *recordingAlerter
}

type recordingAlerter struct {
	events []string
}

func (a *recordingAlerter) Late(_ context.Context, h heartbeat.Heartbeat) {
	a.events = append(a.events, "late:"+h.Name)
}
func (a *recordingAlerter) Recovered(_ context.Context, h heartbeat.Heartbeat) {
	a.events = append(a.events, "recovered:"+h.Name)
}
func (a *recordingAlerter) Failed(_ context.Context, h heartbeat.Heartbeat, code int) {
	a.events = append(a.events, "failed:"+h.Name)
}
func (a *recordingAlerter) Paused(_ context.Context, h heartbeat.Heartbeat) {
	a.events = append(a.events, "paused:"+h.Name)
}
func (a *recordingAlerter) Removed(_ context.Context, id string) {
	a.events = append(a.events, "removed:"+id)
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	db, err := store.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	db.SetSealer(seal.Generate())
	t.Cleanup(func() { db.Close() })
	f := &fixture{db: db, Service: heartbeat.New(db, slog.New(slog.NewTextHandler(os.Stderr, nil))), clock: testNow, alerter: &recordingAlerter{}}
	f.SetClock(func() time.Time { return f.clock })
	f.SetAlerter(f.alerter)
	return f
}

func (f *fixture) addLocalMachine(t *testing.T) {
	t.Helper()
	storetest.SaveLocalMachine(t, f.db, testNow)
}

func (f *fixture) advance(d time.Duration) {
	f.clock = f.clock.Add(d)
}

func (f *fixture) create(t *testing.T, name string) heartbeat.Heartbeat {
	t.Helper()
	h, err := f.Create(context.Background(), heartbeat.Definition{Name: name, Interval: 5 * time.Minute, Grace: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (f *fixture) ping(t *testing.T, token string, kind heartbeat.Kind, exitCode *int) heartbeat.Heartbeat {
	t.Helper()
	h, err := f.Receive(context.Background(), token, heartbeat.Ping{Kind: kind, ExitCode: exitCode, Source: "192.0.2.1", Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func code(value int) *int {
	return &value
}

func TestCreate_StartsNewWithATokenAndAFirstDeadline(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "sauvegarde nextcloud")
	if h.Status != heartbeat.StatusNew || !h.NextDeadlineAt.Equal(testNow.Add(6*time.Minute)) || !strings.HasPrefix(h.Token, "hb_") || len(h.ID) != 16 {
		t.Fatalf("created %+v", h)
	}
	f.advance(6*time.Minute - time.Second)
	if err := f.CheckDeadlines(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.db.GetHeartbeat(context.Background(), h.ID); got.Status != heartbeat.StatusNew {
		t.Fatalf("before its first deadline a new heartbeat stays new: %s", got.Status)
	}
}

func TestCheckDeadlines_NeverPinged_BecomesLateAndAlertsOnce(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "sauvegarde mal branchée")
	f.advance(6*time.Minute + time.Second)
	for range 2 {
		if err := f.CheckDeadlines(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := f.db.GetHeartbeat(context.Background(), h.ID)
	if got.Status != heartbeat.StatusLate || !got.NeverPinged() || !got.NextDeadlineAt.IsZero() {
		t.Fatalf("a never-pinged heartbeat must become late: %+v", got)
	}
	if total, attention, _ := f.db.CountHeartbeats(context.Background()); total != 1 || attention != 1 {
		t.Fatalf("count %d/%d", total, attention)
	}
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	if want := "late:sauvegarde mal branchée,recovered:sauvegarde mal branchée"; strings.Join(f.alerter.events, ",") != want {
		t.Fatalf("events %v", f.alerter.events)
	}
}

func TestResume_NeverPinged_StaysNewWithAFreshDeadline(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	if err := f.Pause(context.Background(), h.ID); err != nil {
		t.Fatal(err)
	}
	f.advance(time.Hour)
	if err := f.Resume(context.Background(), h.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := f.db.GetHeartbeat(context.Background(), h.ID)
	if got.Status != heartbeat.StatusNew || !got.NextDeadlineAt.Equal(f.clock.Add(6*time.Minute)) {
		t.Fatalf("after resume %+v", got)
	}
}

func TestCreate_RejectsBadDefinitions(t *testing.T) {
	f := newFixture(t)
	cases := map[string]struct {
		definition heartbeat.Definition
		want       error
	}{
		"empty name":     {heartbeat.Definition{Name: " ", Interval: time.Hour}, heartbeat.ErrNameInvalid},
		"short interval": {heartbeat.Definition{Name: "x", Interval: 30 * time.Second}, heartbeat.ErrIntervalInvalid},
		"long interval":  {heartbeat.Definition{Name: "x", Interval: 8 * 24 * time.Hour}, heartbeat.ErrIntervalInvalid},
		"grace too long": {heartbeat.Definition{Name: "x", Interval: time.Hour, Grace: 2 * time.Hour}, heartbeat.ErrGraceInvalid},
	}
	for name, tc := range cases {
		if _, err := f.Create(context.Background(), tc.definition); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Le contrôle vit dans le composant : le fichier, qui crée une tâche sans
// passer par l'API, est tenu de même.
func TestCreate_UnknownMachineRefused(t *testing.T) {
	f := newFixture(t)
	definition := heartbeat.Definition{Name: "job", MachineID: "inconnue", Interval: time.Hour}
	if _, err := f.Create(context.Background(), definition); !errors.Is(err, heartbeat.ErrMachineInvalid) {
		t.Fatalf("got %v, want %v", err, heartbeat.ErrMachineInvalid)
	}
	if listed, _ := f.db.ListHeartbeats(context.Background()); len(listed) != 0 {
		t.Fatalf("a refused job was written: %+v", listed)
	}
}

func TestCreate_WithoutMachineAccepted(t *testing.T) {
	f := newFixture(t)
	created, err := f.Create(context.Background(), heartbeat.Definition{Name: "job", Interval: time.Hour})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.MachineID != "" {
		t.Fatalf("machine invented: %+v", created)
	}
}

func TestCreate_KnownMachineAccepted(t *testing.T) {
	f := newFixture(t)
	f.addLocalMachine(t)
	definition := heartbeat.Definition{Name: "job", MachineID: machine.LocalID, Interval: time.Hour}
	created, err := f.Create(context.Background(), definition)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.MachineID != machine.LocalID {
		t.Fatalf("machine lost: %+v", created)
	}
}

func TestReceive_FinishPing_MovesToOnTimeAndRecordsAClosedRun(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	got := f.ping(t, h.Token, heartbeat.KindFinish, nil)
	if got.Status != heartbeat.StatusOnTime || !got.LastPingAt.Equal(testNow) || !got.NextDeadlineAt.Equal(testNow.Add(6*time.Minute)) {
		t.Fatalf("after ping %+v", got)
	}
	runs, _ := f.db.ListRuns(context.Background(), h.ID, 10)
	pings, _ := f.db.ListPings(context.Background(), h.ID, 10)
	if len(runs) != 1 || runs[0].Outcome != heartbeat.OutcomeSuccess || !runs[0].StartedAt.IsZero() || len(pings) != 1 || pings[0].Source != "192.0.2.1" {
		t.Fatalf("runs %+v pings %+v", runs, pings)
	}
}

func TestReceive_StartThenFinish_MeasuresTheDuration(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	started := f.ping(t, h.Token, heartbeat.KindStart, nil)
	if started.Status != heartbeat.StatusStarted || !started.RunStartedAt.Equal(testNow) {
		t.Fatalf("after start %+v", started)
	}
	f.advance(90 * time.Second)
	done := f.ping(t, h.Token, heartbeat.KindExitCode, code(0))
	if done.Status != heartbeat.StatusOnTime || done.LastDuration == nil || *done.LastDuration != 90*time.Second || !done.RunStartedAt.IsZero() {
		t.Fatalf("after finish %+v", done)
	}
	runs, _ := f.db.ListRuns(context.Background(), h.ID, 10)
	if len(runs) != 1 || runs[0].Outcome != heartbeat.OutcomeSuccess || *runs[0].Duration != 90*time.Second || *runs[0].ExitCode != 0 {
		t.Fatalf("runs %+v", runs)
	}
}

func TestReceive_SecondStart_TimesOutThePreviousRun(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindStart, nil)
	f.advance(time.Minute)
	f.ping(t, h.Token, heartbeat.KindStart, nil)
	runs, _ := f.db.ListRuns(context.Background(), h.ID, 10)
	if len(runs) != 2 || runs[0].Outcome != heartbeat.OutcomeInProgress || runs[1].Outcome != heartbeat.OutcomeTimeout {
		t.Fatalf("runs %+v", runs)
	}
}

func TestReceive_NonZeroExitCode_FailsAndNotifies(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	got := f.ping(t, h.Token, heartbeat.KindExitCode, code(2))
	if got.Status != heartbeat.StatusFailed || *got.LastExitCode != 2 {
		t.Fatalf("after failure %+v", got)
	}
	f.ping(t, h.Token, heartbeat.KindExitCode, code(0))
	if want := []string{"failed:job", "recovered:job"}; strings.Join(f.alerter.events, ",") != strings.Join(want, ",") {
		t.Fatalf("events %v", f.alerter.events)
	}
}

// Le chemin de l'extrait « script » : la tâche repart par un start. L'état
// d'avant la fin est « Démarré », et la reprise doit partir quand même.
func TestReceive_StartThenFinish_AfterLate_Recovers(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	f.advance(7 * time.Minute)
	if err := f.CheckDeadlines(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.ping(t, h.Token, heartbeat.KindStart, nil)
	if want := "late:job"; strings.Join(f.alerter.events, ",") != want {
		t.Fatalf("a start alone proves nothing yet: %v", f.alerter.events)
	}
	done := f.ping(t, h.Token, heartbeat.KindExitCode, code(0))
	if want := "late:job,recovered:job"; done.Status != heartbeat.StatusOnTime || strings.Join(f.alerter.events, ",") != want {
		t.Fatalf("status %s, events %v", done.Status, f.alerter.events)
	}
}

func TestReceive_StartThenFinish_AfterFailure_Recovers(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindExitCode, code(2))
	f.ping(t, h.Token, heartbeat.KindStart, nil)
	f.ping(t, h.Token, heartbeat.KindExitCode, code(0))
	if want := "failed:job,recovered:job"; strings.Join(f.alerter.events, ",") != want {
		t.Fatalf("events %v", f.alerter.events)
	}
}

// Une fin en échec après un start ne lève rien : elle ouvre l'échec.
func TestReceive_StartThenFailure_AfterLate_FailsWithoutRecovering(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	f.advance(7 * time.Minute)
	if err := f.CheckDeadlines(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.ping(t, h.Token, heartbeat.KindStart, nil)
	f.ping(t, h.Token, heartbeat.KindExitCode, code(1))
	if want := "late:job,failed:job"; strings.Join(f.alerter.events, ",") != want {
		t.Fatalf("events %v", f.alerter.events)
	}
}

func TestReceive_RejectsUnknownTokenAndBadExitCode(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	if _, err := f.Receive(context.Background(), "hb_nope", heartbeat.Ping{Kind: heartbeat.KindFinish}); !errors.Is(err, heartbeat.ErrNotFound) {
		t.Errorf("unknown token: %v", err)
	}
	if _, err := f.Receive(context.Background(), h.Token, heartbeat.Ping{Kind: heartbeat.KindExitCode, ExitCode: code(256)}); !errors.Is(err, heartbeat.ErrExitCodeInvalid) {
		t.Errorf("bad exit code: %v", err)
	}
}

func TestReceive_TruncatesThePayload(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	long := strings.Repeat("x", heartbeat.MaxPayloadBytes+100)
	if _, err := f.Receive(context.Background(), h.Token, heartbeat.Ping{Kind: heartbeat.KindFinish, Method: "POST", Payload: long}); err != nil {
		t.Fatal(err)
	}
	pings, _ := f.db.ListPings(context.Background(), h.ID, 1)
	runs, _ := f.db.ListRuns(context.Background(), h.ID, 1)
	if len(pings[0].Payload) != heartbeat.MaxPayloadBytes || len(runs[0].Payload) != heartbeat.MaxPayloadBytes {
		t.Fatalf("payload kept %d and %d bytes", len(pings[0].Payload), len(runs[0].Payload))
	}
}

func TestCheckDeadlines_OverdueBecomesLateOnceAndRecoversOnPing(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	f.advance(6*time.Minute - time.Second)
	if err := f.CheckDeadlines(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.db.GetHeartbeat(context.Background(), h.ID); got.Status != heartbeat.StatusOnTime {
		t.Fatalf("before the deadline: %s", got.Status)
	}
	f.advance(2 * time.Second)
	for range 2 {
		if err := f.CheckDeadlines(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := f.db.GetHeartbeat(context.Background(), h.ID)
	if got.Status != heartbeat.StatusLate || !got.NextDeadlineAt.IsZero() {
		t.Fatalf("after the deadline: %+v", got)
	}
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	if want := "late:job,recovered:job"; strings.Join(f.alerter.events, ",") != want {
		t.Fatalf("events %v", f.alerter.events)
	}
}

func TestCheckDeadlines_StartedRun_IsClosedAsTimeout(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindStart, nil)
	f.advance(7 * time.Minute)
	if err := f.CheckDeadlines(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := f.db.GetHeartbeat(context.Background(), h.ID)
	runs, _ := f.db.ListRuns(context.Background(), h.ID, 10)
	if got.Status != heartbeat.StatusLate || !got.RunStartedAt.IsZero() || len(runs) != 1 || runs[0].Outcome != heartbeat.OutcomeTimeout {
		t.Fatalf("heartbeat %+v runs %+v", got, runs)
	}
}

func TestPauseAndResume_PausedNeverGetsLateAndResumeOpensAFullInterval(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	if err := f.Pause(context.Background(), h.ID); err != nil {
		t.Fatal(err)
	}
	f.advance(time.Hour)
	if err := f.CheckDeadlines(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.db.GetHeartbeat(context.Background(), h.ID); got.Status != heartbeat.StatusPaused {
		t.Fatalf("paused heartbeat became %s", got.Status)
	}
	if err := f.Resume(context.Background(), h.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.Resume(context.Background(), h.ID); !errors.Is(err, heartbeat.ErrNotPaused) {
		t.Fatalf("second resume: %v", err)
	}
	got, _ := f.db.GetHeartbeat(context.Background(), h.ID)
	if got.Status != heartbeat.StatusOnTime || !got.NextDeadlineAt.Equal(f.clock.Add(6*time.Minute)) {
		t.Fatalf("after resume %+v", got)
	}
}

// Un cron oublié continue de pinger : le ping est noté, pour qu'on voie qu'il
// tourne encore, mais rien ne bouge avant la reprise.
func TestPause_PingsAreRecordedButChangeNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	if err := f.Pause(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	paused, _ := f.db.GetHeartbeat(ctx, h.ID)
	f.alerter.events = nil
	f.advance(time.Hour)

	f.ping(t, h.Token, heartbeat.KindStart, nil)
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	got := f.ping(t, h.Token, heartbeat.KindExitCode, code(3))

	if got.Status != heartbeat.StatusPaused {
		t.Fatalf("a ping must not resume a paused heartbeat, got %s", got.Status)
	}
	after, _ := f.db.GetHeartbeat(ctx, h.ID)
	if after.Status != heartbeat.StatusPaused || !after.LastPingAt.Equal(paused.LastPingAt) || !after.NextDeadlineAt.IsZero() ||
		!after.RunStartedAt.IsZero() || after.LastExitCode != paused.LastExitCode {
		t.Fatalf("a paused heartbeat moved: before %+v, after %+v", paused, after)
	}
	if runs, _ := f.db.ListRuns(ctx, h.ID, 10); len(runs) != 1 {
		t.Fatalf("a ping while paused must not open or close a run: %d runs", len(runs))
	}
	if pings, _ := f.db.ListPings(ctx, h.ID, 10); len(pings) != 4 {
		t.Fatalf("the raw pings must be kept: %d pings", len(pings))
	}
	if len(f.alerter.events) != 0 {
		t.Fatalf("a ping while paused must raise nothing: %v", f.alerter.events)
	}
	if err := f.CheckDeadlines(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.Resume(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	if resumed, _ := f.db.GetHeartbeat(ctx, h.ID); resumed.Status != heartbeat.StatusOnTime {
		t.Fatalf("only Resume leaves the pause: %s", resumed.Status)
	}
}

func TestReceive_PausedStillRefusesABadExitCode(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	if err := f.Pause(context.Background(), h.ID); err != nil {
		t.Fatal(err)
	}
	_, err := f.Receive(context.Background(), h.Token, heartbeat.Ping{Kind: heartbeat.KindExitCode, ExitCode: code(256)})
	if !errors.Is(err, heartbeat.ErrExitCodeInvalid) {
		t.Fatalf("got %v", err)
	}
}

// Une tâche créée avant le correctif n'a pas d'échéance ; le rattrapage
// du démarrage lui donne celle qu'elle aurait eue, une seule fois.
func TestBackfillFirstDeadlines_GivesAnOldNeverPingedJobItsDeadline(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	h := f.create(t, "ancienne")
	if err := f.db.Transact(ctx, h.ID, func(before heartbeat.Heartbeat) *heartbeat.Transition {
		after := before
		after.NextDeadlineAt = time.Time{}
		return &heartbeat.Transition{Heartbeat: after}
	}); err != nil {
		t.Fatal(err)
	}
	f.advance(time.Hour)
	for range 2 {
		if err := f.BackfillFirstDeadlines(ctx); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := f.db.GetHeartbeat(ctx, h.ID)
	if got.Status != heartbeat.StatusNew || !got.NextDeadlineAt.Equal(h.CreatedAt.Add(6*time.Minute)) {
		t.Fatalf("after backfill %+v", got)
	}
	if err := f.CheckDeadlines(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.db.GetHeartbeat(ctx, h.ID); got.Status != heartbeat.StatusLate {
		t.Fatalf("an old deadline already passed must make it late: %s", got.Status)
	}
}

func TestDelete_RemovesTheHeartbeatAndItsHistory(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	if err := f.Delete(context.Background(), h.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.GetHeartbeat(context.Background(), h.ID); !errors.Is(err, heartbeat.ErrNotFound) {
		t.Fatalf("still there: %v", err)
	}
	if pings, _ := f.db.ListPings(context.Background(), h.ID, 10); len(pings) != 0 {
		t.Fatalf("pings survived: %+v", pings)
	}
	if err := f.Delete(context.Background(), h.ID); !errors.Is(err, heartbeat.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestPurge_DropsOldPingsAndRunsOnly(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	f.advance(8 * 24 * time.Hour)
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	if err := f.Purge(context.Background()); err != nil {
		t.Fatal(err)
	}
	pings, _ := f.db.ListPings(context.Background(), h.ID, 10)
	runs, _ := f.db.ListRuns(context.Background(), h.ID, 10)
	if len(pings) != 1 || len(runs) != 2 {
		t.Fatalf("pings %d runs %d", len(pings), len(runs))
	}
	f.advance(91 * 24 * time.Hour)
	if err := f.Purge(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runs, _ := f.db.ListRuns(context.Background(), h.ID, 10); len(runs) != 0 {
		t.Fatalf("runs %d", len(runs))
	}
}

func TestCount_CountsLateAndFailedAsAttention(t *testing.T) {
	f := newFixture(t)
	a := f.create(t, "a")
	f.create(t, "b")
	f.ping(t, a.Token, heartbeat.KindExitCode, code(1))
	total, attention, err := f.db.CountHeartbeats(context.Background())
	if err != nil || total != 2 || attention != 1 {
		t.Fatalf("total %d attention %d err %v", total, attention, err)
	}
}

func TestPause_DuringAStartedRun_ClosesItAsTimeout(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindStart, nil)
	if err := f.Pause(context.Background(), h.ID); err != nil {
		t.Fatal(err)
	}
	runs, _ := f.db.ListRuns(context.Background(), h.ID, 10)
	got, _ := f.db.GetHeartbeat(context.Background(), h.ID)
	if len(runs) != 1 || runs[0].Outcome != heartbeat.OutcomeTimeout || !got.RunStartedAt.IsZero() {
		t.Fatalf("runs %+v heartbeat %+v", runs, got)
	}
}

func TestReceive_ConcurrentStarts_LeaveASingleOpenRun(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.Receive(context.Background(), h.Token, heartbeat.Ping{Kind: heartbeat.KindStart})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	runs, _ := f.db.ListRuns(context.Background(), h.ID, 20)
	open := 0
	for _, run := range runs {
		if run.Outcome == heartbeat.OutcomeInProgress {
			open++
		}
	}
	if len(runs) != 8 || open != 1 {
		t.Fatalf("%d runs, %d open", len(runs), open)
	}
}

func TestCheckDeadlines_PingBetweenListAndWrite_IsNotLost(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	f.advance(10 * time.Minute)
	// La présélection voit le moniteur en retard ; un ping arrive avant que
	// la boucle n'écrive : la transaction relit l'échéance et n'écrit rien.
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	if err := f.CheckDeadlines(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.db.GetHeartbeat(context.Background(), h.ID); got.Status != heartbeat.StatusOnTime {
		t.Fatalf("status %s", got.Status)
	}
	if len(f.alerter.events) != 0 {
		t.Fatalf("events %v", f.alerter.events)
	}
}

func TestReceive_TruncatesOnARuneBoundary(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	payload := strings.Repeat("x", heartbeat.MaxPayloadBytes-1) + "é"
	if _, err := f.Receive(context.Background(), h.Token, heartbeat.Ping{Kind: heartbeat.KindFinish, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	pings, _ := f.db.ListPings(context.Background(), h.ID, 1)
	if len(pings[0].Payload) != heartbeat.MaxPayloadBytes-1 {
		t.Fatalf("payload kept %d bytes", len(pings[0].Payload))
	}
}

func (f *fixture) update(t *testing.T, id string, definition heartbeat.Definition) heartbeat.Heartbeat {
	t.Helper()
	updated, err := f.Update(context.Background(), id, definition)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	return updated
}

func TestUpdate_KeepsIDTokenAndHistory(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindStart, nil)
	f.ping(t, h.Token, heartbeat.KindExitCode, code(0))

	updated := f.update(t, h.ID, heartbeat.Definition{Name: "  sauvegarde  ", Interval: time.Hour, Grace: 5 * time.Minute})

	if updated.ID != h.ID || updated.Token != h.Token || !updated.CreatedAt.Equal(h.CreatedAt) {
		t.Fatalf("identity changed: %+v", updated)
	}
	if updated.Name != "sauvegarde" || updated.Interval != time.Hour || updated.Grace != 5*time.Minute || updated.MachineID != "" {
		t.Fatalf("definition not replaced: %+v", updated)
	}
	if runs, _ := f.db.ListRuns(ctx, h.ID, 10); len(runs) != 1 {
		t.Fatalf("runs lost: %d", len(runs))
	}
	if pings, _ := f.db.ListPings(ctx, h.ID, 10); len(pings) != 2 {
		t.Fatalf("pings lost: %d", len(pings))
	}
	if got := f.ping(t, h.Token, heartbeat.KindFinish, nil); got.ID != h.ID {
		t.Fatalf("the old URL must keep working: %+v", got)
	}
}

// L'échéance suit le rythme : plus d'intervalle ou de grâce, c'est plus de
// temps, à partir du même dernier ping.
func TestUpdate_ShiftsTheRunningDeadline(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	pinged := f.ping(t, h.Token, heartbeat.KindFinish, nil)
	f.advance(2 * time.Minute)

	updated := f.update(t, h.ID, heartbeat.Definition{Name: "job", Interval: 10 * time.Minute, Grace: 2 * time.Minute})
	if want := pinged.NextDeadlineAt.Add(6 * time.Minute); !updated.NextDeadlineAt.Equal(want) {
		t.Fatalf("deadline %s, want %s", updated.NextDeadlineAt, want)
	}
	if updated.Status != heartbeat.StatusOnTime || !updated.LastPingAt.Equal(pinged.LastPingAt) {
		t.Fatalf("state moved: %+v", updated)
	}

	renamed := f.update(t, h.ID, heartbeat.Definition{Name: "autre", Interval: 10 * time.Minute, Grace: 2 * time.Minute})
	if !renamed.NextDeadlineAt.Equal(updated.NextDeadlineAt) {
		t.Fatalf("a rename must not move the deadline: %s", renamed.NextDeadlineAt)
	}
}

func TestUpdate_Paused_ChangesNoDeadlineNorState(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	if err := f.Pause(context.Background(), h.ID); err != nil {
		t.Fatal(err)
	}
	updated := f.update(t, h.ID, heartbeat.Definition{Name: "job", Interval: time.Hour, Grace: time.Minute})
	if updated.Status != heartbeat.StatusPaused || !updated.NextDeadlineAt.IsZero() || updated.Interval != time.Hour {
		t.Fatalf("paused update: %+v", updated)
	}
}

func TestUpdate_LateWithinNewInterval_BackOnTimeAndRecovered(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	pinged := f.ping(t, h.Token, heartbeat.KindFinish, nil)
	f.advance(7 * time.Minute)
	if err := f.CheckDeadlines(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.alerter.events = nil

	updated := f.update(t, h.ID, heartbeat.Definition{Name: "job", Interval: 10 * time.Minute, Grace: time.Minute})

	if updated.Status != heartbeat.StatusOnTime || !updated.NextDeadlineAt.Equal(pinged.LastPingAt.Add(11*time.Minute)) {
		t.Fatalf("a late job with time left must be back on time: %+v", updated)
	}
	if want := []string{"recovered:job"}; !slices.Equal(f.alerter.events, want) {
		t.Fatalf("events %v, want %v", f.alerter.events, want)
	}
}

func TestUpdate_LateWithinNewInterval_AfterAFailureGoesBackToFailed(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindExitCode, code(2))
	f.advance(7 * time.Minute)
	if err := f.CheckDeadlines(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.alerter.events = nil

	updated := f.update(t, h.ID, heartbeat.Definition{Name: "job", Interval: 10 * time.Minute, Grace: time.Minute})

	if updated.Status != heartbeat.StatusFailed || updated.NextDeadlineAt.IsZero() {
		t.Fatalf("got %+v", updated)
	}
	if want := []string{"failed:job"}; !slices.Equal(f.alerter.events, want) {
		t.Fatalf("events %v, want %v", f.alerter.events, want)
	}
}

func TestUpdate_LateNeverPinged_WithinNewInterval_BackToNew(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	f.advance(7 * time.Minute)
	if err := f.CheckDeadlines(context.Background()); err != nil {
		t.Fatal(err)
	}

	updated := f.update(t, h.ID, heartbeat.Definition{Name: "job", Interval: 10 * time.Minute, Grace: time.Minute})
	if updated.Status != heartbeat.StatusNew || !updated.NextDeadlineAt.Equal(h.CreatedAt.Add(11*time.Minute)) {
		t.Fatalf("got %+v", updated)
	}
}

func TestUpdate_LateBeyondNewInterval_StaysLate(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	f.advance(20 * time.Minute)
	if err := f.CheckDeadlines(context.Background()); err != nil {
		t.Fatal(err)
	}

	updated := f.update(t, h.ID, heartbeat.Definition{Name: "job", Interval: 10 * time.Minute, Grace: time.Minute})
	if updated.Status != heartbeat.StatusLate || !updated.NextDeadlineAt.IsZero() {
		t.Fatalf("got %+v", updated)
	}
}

func TestUpdate_ShorterInterval_LateAtNextCheck(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	f.advance(2 * time.Minute)

	updated := f.update(t, h.ID, heartbeat.Definition{Name: "job", Interval: time.Minute, Grace: 0})
	if updated.Status != heartbeat.StatusOnTime {
		t.Fatalf("the edit itself judges nothing: %+v", updated)
	}
	if err := f.CheckDeadlines(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.db.GetHeartbeat(ctx, h.ID); got.Status != heartbeat.StatusLate {
		t.Fatalf("the next check must find it late, got %s", got.Status)
	}
}

func TestUpdate_RejectsBadDefinitions(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	cases := []struct {
		name       string
		definition heartbeat.Definition
		want       error
	}{
		{"empty name", heartbeat.Definition{Name: " ", Interval: time.Hour}, heartbeat.ErrNameInvalid},
		{"interval too short", heartbeat.Definition{Name: "job", Interval: time.Second}, heartbeat.ErrIntervalInvalid},
		{"grace longer than the interval", heartbeat.Definition{Name: "job", Interval: time.Hour, Grace: 2 * time.Hour}, heartbeat.ErrGraceInvalid},
	}
	for _, c := range cases {
		if _, err := f.Update(context.Background(), h.ID, c.definition); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
	if got, _ := f.db.GetHeartbeat(context.Background(), h.ID); got.Interval != 5*time.Minute {
		t.Fatalf("a refused edit was written: %+v", got)
	}
	if _, err := f.Update(context.Background(), "inconnu", heartbeat.Definition{Name: "job", Interval: time.Hour}); !errors.Is(err, heartbeat.ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

// La tâche inconnue d'abord : introuvable, quoi que dise la définition.
func TestUpdate_UnknownMachineRefused(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	definition := heartbeat.Definition{Name: "job", MachineID: "inconnue", Interval: time.Hour}
	if _, err := f.Update(context.Background(), h.ID, definition); !errors.Is(err, heartbeat.ErrMachineInvalid) {
		t.Fatalf("got %v, want %v", err, heartbeat.ErrMachineInvalid)
	}
	if got, _ := f.db.GetHeartbeat(context.Background(), h.ID); got.MachineID != "" || got.Interval != 5*time.Minute {
		t.Fatalf("a refused edit was written: %+v", got)
	}
	if _, err := f.Update(context.Background(), "inconnu", definition); !errors.Is(err, heartbeat.ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

// Une tâche encore en retard est renommée : le moteur des alertes reçoit le
// nouveau nom, il n'en ouvre pas une seconde.
func TestUpdate_RenamesTheOpenAlert(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "ancien")
	f.advance(7 * time.Minute)
	if err := f.CheckDeadlines(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.alerter.events = nil

	f.update(t, h.ID, heartbeat.Definition{Name: "nouveau", Interval: 5 * time.Minute, Grace: time.Minute})
	if want := []string{"late:nouveau"}; !slices.Equal(f.alerter.events, want) {
		t.Fatalf("events %v, want %v", f.alerter.events, want)
	}
}

func TestRegenerateToken_OldRefusedNewAccepted(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	h := f.create(t, "job")
	pinged := f.ping(t, h.Token, heartbeat.KindFinish, nil)
	if err := f.Pause(ctx, h.ID); err != nil {
		t.Fatal(err)
	}

	if err := f.RegenerateToken(ctx, h.ID); err != nil {
		t.Fatal(err)
	}

	regenerated, _ := f.db.GetHeartbeat(ctx, h.ID)
	if regenerated.Token == h.Token || !strings.HasPrefix(regenerated.Token, "hb_") {
		t.Fatalf("token not renewed: %q", regenerated.Token)
	}
	if _, err := f.Receive(ctx, h.Token, heartbeat.Ping{Kind: heartbeat.KindFinish}); !errors.Is(err, heartbeat.ErrNotFound) {
		t.Fatalf("the old token must be refused: %v", err)
	}
	if _, err := f.Receive(ctx, regenerated.Token, heartbeat.Ping{Kind: heartbeat.KindFinish}); err != nil {
		t.Fatalf("the new token must work: %v", err)
	}
	if regenerated.Status != heartbeat.StatusPaused || !regenerated.LastPingAt.Equal(pinged.LastPingAt) {
		t.Fatalf("regenerating must not touch the state: %+v", regenerated)
	}
	if pings, _ := f.db.ListPings(ctx, h.ID, 10); len(pings) != 2 {
		t.Fatalf("history lost: %d pings", len(pings))
	}
	if err := f.RegenerateToken(ctx, "inconnu"); !errors.Is(err, heartbeat.ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

// Le retard s'inscrit dans l'historique : une ligne « missed » close à
// l'échéance dépassée (pas à l'instant du contrôle), sans début ni code.
func TestCheckDeadlines_Overdue_WritesAMissedRunClosedAtTheDeadline(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	h := f.create(t, "job")
	pinged := f.ping(t, h.Token, heartbeat.KindFinish, nil)
	f.advance(2 * time.Hour)
	if err := f.CheckDeadlines(ctx); err != nil {
		t.Fatal(err)
	}
	runs, _ := f.db.ListRuns(ctx, h.ID, 10)
	if len(runs) != 2 || runs[0].Outcome != heartbeat.OutcomeMissed {
		t.Fatalf("runs %+v", runs)
	}
	missed := runs[0]
	if !missed.CompletedAt.Equal(pinged.NextDeadlineAt) || !missed.StartedAt.IsZero() || missed.Duration != nil || missed.ExitCode != nil || missed.Payload != "" {
		t.Fatalf("missed run %+v, deadline %v", missed, pinged.NextDeadlineAt)
	}
}

// Une tâche muette pendant des heures passe en retard une fois : une ligne
// par épisode, pas une par échéance ni une par contrôle.
func TestCheckDeadlines_AlreadyLate_DoesNotWriteASecondMissedRun(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	f.advance(10 * time.Minute)
	for range 3 {
		if err := f.CheckDeadlines(ctx); err != nil {
			t.Fatal(err)
		}
		f.advance(24 * time.Hour)
	}
	if missed := countOutcome(t, f, h.ID, heartbeat.OutcomeMissed); missed != 1 {
		t.Fatalf("%d missed runs, 1 expected", missed)
	}
	// L'alerte suit le passage en retard, une fois : la ligne n'y change rien.
	if want := "late:job"; strings.Join(f.alerter.events, ",") != want {
		t.Fatalf("alerts %v, want %s", f.alerter.events, want)
	}
}

// Deux contrôles qui se croisent (deux boucles, ou une boucle et un
// redémarrage) : tous ont présélectionné la tâche avant qu'un seul ne la
// passe en retard. L'échéance relue dans la transaction n'en laisse passer
// qu'un : une seule ligne.
func TestCheckDeadlines_CrossedChecks_WriteOneMissedRun(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	const checks = 4
	listed := &listedTogether{DB: f.db}
	listed.wg.Add(checks)
	var wg sync.WaitGroup
	errs := make([]error, checks)
	for i := range errs {
		service := heartbeat.New(listed, slog.New(slog.NewTextHandler(os.Stderr, nil)))
		service.SetClock(func() time.Time { return testNow.Add(10 * time.Minute) })
		wg.Go(func() { errs[i] = service.CheckDeadlines(ctx) })
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	if missed := countOutcome(t, f, h.ID, heartbeat.OutcomeMissed); missed != 1 {
		t.Fatalf("%d missed runs, 1 expected", missed)
	}
}

// listedTogether retient chaque présélection jusqu'à ce que toutes soient
// faites : les contrôles se croisent à coup sûr.
type listedTogether struct {
	*store.DB
	wg sync.WaitGroup
}

func (l *listedTogether) ListOverdueHeartbeats(ctx context.Context, now time.Time) ([]heartbeat.Heartbeat, error) {
	found, err := l.DB.ListOverdueHeartbeats(ctx, now)
	l.wg.Done()
	l.wg.Wait()
	return found, err
}

// Le serveur redémarre pendant le retard : l'état est en base, le nouveau
// service (rattrapage des premières échéances puis contrôle, comme Watch au
// départ) ne réécrit rien, pour une tâche pingée comme pour une jamais pingée.
func TestCheckDeadlines_AfterARestartDuringTheLateness_WritesNoSecondMissedRun(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	pinged := f.create(t, "job")
	never := f.create(t, "jamais pingée")
	f.ping(t, pinged.Token, heartbeat.KindFinish, nil)
	f.advance(time.Hour)
	if err := f.CheckDeadlines(ctx); err != nil {
		t.Fatal(err)
	}
	restarted := heartbeat.New(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	restarted.SetClock(func() time.Time { return testNow.Add(3 * 24 * time.Hour) })
	if err := restarted.BackfillFirstDeadlines(ctx); err != nil {
		t.Fatal(err)
	}
	if err := restarted.CheckDeadlines(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{pinged.ID, never.ID} {
		if missed := countOutcome(t, f, id, heartbeat.OutcomeMissed); missed != 1 {
			t.Fatalf("%s: %d missed runs, 1 expected", id, missed)
		}
	}
}

// En pause pendant le retard puis reprise : la ligne du retard reste, la
// pause et la reprise n'en écrivent pas. La reprise ouvre une échéance
// neuve : dépassée à son tour sans ping, c'est un nouvel épisode.
func TestPauseWhileLate_ThenResume_WritesOnlyForANewlyMissedDeadline(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	f.advance(10 * time.Minute)
	if err := f.CheckDeadlines(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.Pause(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	f.advance(48 * time.Hour)
	if err := f.CheckDeadlines(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.Resume(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	f.advance(5 * time.Minute)
	if err := f.CheckDeadlines(ctx); err != nil {
		t.Fatal(err)
	}
	if missed := countOutcome(t, f, h.ID, heartbeat.OutcomeMissed); missed != 1 {
		t.Fatalf("%d missed runs before the new deadline, 1 expected", missed)
	}
	f.advance(2 * time.Minute)
	if err := f.CheckDeadlines(ctx); err != nil {
		t.Fatal(err)
	}
	if missed := countOutcome(t, f, h.ID, heartbeat.OutcomeMissed); missed != 2 {
		t.Fatalf("%d missed runs after the new deadline, 2 expected", missed)
	}
}

// Un nouvel épisode, après une reprise, fait une nouvelle ligne.
func TestCheckDeadlines_LateAgainAfterARecovery_WritesASecondMissedRun(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	for range 2 {
		f.advance(10 * time.Minute)
		if err := f.CheckDeadlines(ctx); err != nil {
			t.Fatal(err)
		}
		f.ping(t, h.Token, heartbeat.KindFinish, nil)
	}
	if missed := countOutcome(t, f, h.ID, heartbeat.OutcomeMissed); missed != 2 {
		t.Fatalf("%d missed runs, 2 expected", missed)
	}
}

// Une exécution ouverte par un start est déjà close en dépassement, qui
// compte comme un échec : « missed » en plus compterait l'échéance deux fois.
func TestCheckDeadlines_OpenRun_IsClosedAsTimeoutWithoutAMissedRun(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindStart, nil)
	f.advance(time.Hour)
	if err := f.CheckDeadlines(ctx); err != nil {
		t.Fatal(err)
	}
	runs, _ := f.db.ListRuns(ctx, h.ID, 10)
	if len(runs) != 1 || runs[0].Outcome != heartbeat.OutcomeTimeout {
		t.Fatalf("runs %+v", runs)
	}
}

// Une tâche en pause ne peut pas être en retard, et la reprise n'écrit rien.
func TestPauseAndResume_WriteNoMissedRun(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	if err := f.Pause(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	f.advance(48 * time.Hour)
	if err := f.CheckDeadlines(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.Resume(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	if missed := countOutcome(t, f, h.ID, heartbeat.OutcomeMissed); missed != 0 {
		t.Fatalf("%d missed runs, none expected", missed)
	}
}

// Jamais pingée, une tâche passe en retard : c'est un vrai retard, il s'inscrit.
func TestCheckDeadlines_NeverPinged_WritesAMissedRun(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	h := f.create(t, "mal branchée")
	f.advance(time.Hour)
	if err := f.CheckDeadlines(ctx); err != nil {
		t.Fatal(err)
	}
	runs, _ := f.db.ListRuns(ctx, h.ID, 10)
	if len(runs) != 1 || runs[0].Outcome != heartbeat.OutcomeMissed || !runs[0].CompletedAt.Equal(h.NextDeadlineAt) {
		t.Fatalf("runs %+v, deadline %v", runs, h.NextDeadlineAt)
	}
}

func countOutcome(t *testing.T, f *fixture, id string, outcome heartbeat.Outcome) int {
	t.Helper()
	runs, err := f.db.ListRuns(context.Background(), id, 100)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, run := range runs {
		if run.Outcome == outcome {
			total++
		}
	}
	return total
}
