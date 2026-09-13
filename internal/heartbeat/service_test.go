package heartbeat_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/store"
)

var testNow = time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)

// Le service se teste sur la vraie base SQLite, temporaire, avec une horloge
// qu'on avance à la main.
type fixture struct {
	*heartbeat.Service
	clock    time.Time
	listener *recordingListener
}

type recordingListener struct {
	events []string
}

func (l *recordingListener) Late(h heartbeat.Heartbeat) { l.events = append(l.events, "late:"+h.Name) }
func (l *recordingListener) Recovered(h heartbeat.Heartbeat) {
	l.events = append(l.events, "recovered:"+h.Name)
}
func (l *recordingListener) Failed(h heartbeat.Heartbeat, code int) {
	l.events = append(l.events, "failed:"+h.Name)
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
	t.Cleanup(func() { db.Close() })
	f := &fixture{Service: heartbeat.New(db, slog.New(slog.NewTextHandler(os.Stderr, nil))), clock: testNow, listener: &recordingListener{}}
	f.SetClock(func() time.Time { return f.clock })
	f.SetListener(f.listener)
	return f
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

func TestCreate_StartsNewWithATokenAndNoDeadline(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "sauvegarde nextcloud")
	if h.Status != heartbeat.StatusNew || !h.NextDeadlineAt.IsZero() || !strings.HasPrefix(h.Token, "hb_") || len(h.ID) != 16 {
		t.Fatalf("created %+v", h)
	}
	if err := f.CheckDeadlines(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.Get(context.Background(), h.ID); got.Status != heartbeat.StatusNew {
		t.Fatalf("a never-pinged heartbeat must not become late: %s", got.Status)
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

func TestReceive_FinishPing_MovesToOnTimeAndRecordsAClosedRun(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	got := f.ping(t, h.Token, heartbeat.KindFinish, nil)
	if got.Status != heartbeat.StatusOnTime || !got.LastPingAt.Equal(testNow) || !got.NextDeadlineAt.Equal(testNow.Add(6*time.Minute)) {
		t.Fatalf("after ping %+v", got)
	}
	runs, _ := f.Runs(context.Background(), h.ID, 10)
	pings, _ := f.Pings(context.Background(), h.ID, 10)
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
	runs, _ := f.Runs(context.Background(), h.ID, 10)
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
	runs, _ := f.Runs(context.Background(), h.ID, 10)
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
	if want := []string{"failed:job", "recovered:job"}; strings.Join(f.listener.events, ",") != strings.Join(want, ",") {
		t.Fatalf("events %v", f.listener.events)
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
	pings, _ := f.Pings(context.Background(), h.ID, 1)
	runs, _ := f.Runs(context.Background(), h.ID, 1)
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
	if got, _ := f.Get(context.Background(), h.ID); got.Status != heartbeat.StatusOnTime {
		t.Fatalf("before the deadline: %s", got.Status)
	}
	f.advance(2 * time.Second)
	for range 2 {
		if err := f.CheckDeadlines(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := f.Get(context.Background(), h.ID)
	if got.Status != heartbeat.StatusLate || !got.NextDeadlineAt.IsZero() {
		t.Fatalf("after the deadline: %+v", got)
	}
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	if want := "late:job,recovered:job"; strings.Join(f.listener.events, ",") != want {
		t.Fatalf("events %v", f.listener.events)
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
	got, _ := f.Get(context.Background(), h.ID)
	runs, _ := f.Runs(context.Background(), h.ID, 10)
	if got.Status != heartbeat.StatusLate || !got.RunStartedAt.IsZero() || len(runs) != 1 || runs[0].Outcome != heartbeat.OutcomeTimeout {
		t.Fatalf("heartbeat %+v runs %+v", got, runs)
	}
}

func TestPauseAndResume_PausedNeverGetsLateAndAPingResumesIt(t *testing.T) {
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
	if got, _ := f.Get(context.Background(), h.ID); got.Status != heartbeat.StatusPaused {
		t.Fatalf("paused heartbeat became %s", got.Status)
	}
	if err := f.Resume(context.Background(), h.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.Resume(context.Background(), h.ID); !errors.Is(err, heartbeat.ErrNotPaused) {
		t.Fatalf("second resume: %v", err)
	}
	got, _ := f.Get(context.Background(), h.ID)
	if got.Status != heartbeat.StatusOnTime || !got.NextDeadlineAt.Equal(f.clock.Add(6*time.Minute)) {
		t.Fatalf("after resume %+v", got)
	}
	if err := f.Pause(context.Background(), h.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.ping(t, h.Token, heartbeat.KindFinish, nil); got.Status != heartbeat.StatusOnTime {
		t.Fatalf("a ping must resume a paused heartbeat, got %s", got.Status)
	}
}

func TestDelete_RemovesTheHeartbeatAndItsHistory(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	f.ping(t, h.Token, heartbeat.KindFinish, nil)
	if err := f.Delete(context.Background(), h.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get(context.Background(), h.ID); !errors.Is(err, heartbeat.ErrNotFound) {
		t.Fatalf("still there: %v", err)
	}
	if pings, _ := f.Pings(context.Background(), h.ID, 10); len(pings) != 0 {
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
	pings, _ := f.Pings(context.Background(), h.ID, 10)
	runs, _ := f.Runs(context.Background(), h.ID, 10)
	if len(pings) != 1 || len(runs) != 2 {
		t.Fatalf("pings %d runs %d", len(pings), len(runs))
	}
	f.advance(91 * 24 * time.Hour)
	if err := f.Purge(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runs, _ := f.Runs(context.Background(), h.ID, 10); len(runs) != 0 {
		t.Fatalf("runs %d", len(runs))
	}
}

func TestCount_CountsLateAndFailedAsAttention(t *testing.T) {
	f := newFixture(t)
	a := f.create(t, "a")
	f.create(t, "b")
	f.ping(t, a.Token, heartbeat.KindExitCode, code(1))
	total, attention, err := f.Count(context.Background())
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
	runs, _ := f.Runs(context.Background(), h.ID, 10)
	got, _ := f.Get(context.Background(), h.ID)
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
	runs, _ := f.Runs(context.Background(), h.ID, 20)
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
	if got, _ := f.Get(context.Background(), h.ID); got.Status != heartbeat.StatusOnTime {
		t.Fatalf("status %s", got.Status)
	}
	if len(f.listener.events) != 0 {
		t.Fatalf("events %v", f.listener.events)
	}
}

func TestReceive_TruncatesOnARuneBoundary(t *testing.T) {
	f := newFixture(t)
	h := f.create(t, "job")
	payload := strings.Repeat("x", heartbeat.MaxPayloadBytes-1) + "é"
	if _, err := f.Receive(context.Background(), h.Token, heartbeat.Ping{Kind: heartbeat.KindFinish, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	pings, _ := f.Pings(context.Background(), h.ID, 1)
	if len(pings[0].Payload) != heartbeat.MaxPayloadBytes-1 {
		t.Fatalf("payload kept %d bytes", len(pings[0].Payload))
	}
}
