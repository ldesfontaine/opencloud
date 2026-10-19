package dockerwatch

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/dockerapi"
)

const (
	pauseAt = int64(1789802640_000000000)
	dieAt   = int64(1789802649_736266200)
	// dieSince est ce que le veilleur doit retenir après ce « die ».
	dieSince = "1789802649.736266200"
)

func eventLine(action string, nano int64, attributes string) string {
	return `{"Action":"` + action + `","Actor":{"Attributes":{` + attributes + `},"ID":"` + fixtureID + `"},"Type":"container","time":` +
		strconv.FormatInt(nano/1e9, 10) + `,"timeNano":` + strconv.FormatInt(nano, 10) + "}\n"
}

// slowSink occupe la boucle de follow le temps d'une livraison : c'est
// quand elle est occupée que la fin du flux et son erreur sont prêtes
// ensemble.
type slowSink struct{ *recordingSink }

func (s slowSink) Deliver(report container.Report) {
	time.Sleep(time.Millisecond)
	s.recordingSink.Deliver(report)
}

// Un flux qui se termine pendant que la boucle est occupée ne ramène jamais
// since à « 0 » : le démon rejouerait tout son tampon, et ses vieux die et
// start ouvriraient de fausses alertes.
func TestFollow_AStreamThatEndsNeverResetsSince(t *testing.T) {
	daemon := newDaemon(t, false)
	daemon.onEvents = func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(eventLine("pause", pauseAt, "")))
	}
	watcher := newWatcher(serve(t, daemon.mux), slowSink{newSink()})
	for i := 0; i < 200; i++ {
		watcher.since = "1789802630.000000000"
		err := watcher.follow(context.Background())
		if err == nil || err.Error() != "event stream closed" {
			t.Fatalf("round %d: follow = %v, want the stream closed", i, err)
		}
		if watcher.since != "1789802640.000000000" {
			t.Fatalf("round %d: since = %q, want the last event handled", i, watcher.since)
		}
	}
}

func TestHandleEvent_WithoutATimestampKeepsSince(t *testing.T) {
	watcher := newWatcher(serve(t, newDaemon(t, false).mux), newSink())
	watcher.since = "1789802630.000000000"
	watcher.handleEvent(context.Background(), dockerapi.Event{})
	if watcher.since != "1789802630.000000000" {
		t.Fatalf("since = %q", watcher.since)
	}
}

// lockedBuffer reçoit le journal de plusieurs goroutines.
type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// Une ligne d'événement de 300 Kio (les labels d'une image) est sautée, une
// fois dans le journal et sans son contenu ; le « die » qui la suit est livré
// et since avance au-delà.
func TestFollow_SkipsAnOversizedEventLineAndGoesOn(t *testing.T) {
	daemon := newDaemon(t, false)
	huge := eventLine("start", pauseAt, `"blob":"`+strings.Repeat("secret-label-", 300<<10/13)+`"`)
	if len(huge) <= 256<<10 {
		t.Fatalf("the line is only %d bytes", len(huge))
	}
	daemon.onEvents = func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(huge + eventLine("die", dieAt, `"exitCode":"1"`)))
	}
	journal := &lockedBuffer{}
	sink := newSink()
	watcher := New(serve(t, daemon.mux), sink, Options{
		Logger: slog.New(slog.NewTextHandler(journal, nil)), SampleInterval: time.Hour, InventoryInterval: time.Hour,
	})
	_ = watcher.follow(context.Background())
	die := sink.waitFor(t, "die", func(r container.Report) bool { return len(r.Events) == 1 && r.Events[0].Action == "die" })
	if *die.Events[0].ExitCode != 1 {
		t.Fatalf("die = %+v", die.Events[0])
	}
	if watcher.since != dieSince {
		t.Fatalf("since = %q, want %q", watcher.since, dieSince)
	}
	text := journal.String()
	if strings.Count(text, "docker event skipped") != 1 || strings.Contains(text, "secret-label") {
		t.Fatalf("journal:\n%.500s", text)
	}
}

// Les mesures ne tiennent pas à la vie du flux d'événements : un flux qui ne
// s'ouvre pas, ou qui retombe, n'en prive pas la machine.
func TestRun_StatsContinueWhileTheEventStreamFails(t *testing.T) {
	daemon := newDaemon(t, false)
	daemon.onEvents = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }
	sink := newSink()
	watcher := newWatcher(serve(t, daemon.mux), sink)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watcher.Run(ctx)
	stats := sink.waitFor(t, "stats", func(r container.Report) bool { return len(r.Stats) > 0 })
	if stats.Stats[0].ContainerID != fixtureID {
		t.Fatalf("stats = %+v", stats.Stats)
	}
}

// blockingStatsSink retient la livraison des mesures jusqu'à release.
type blockingStatsSink struct {
	*recordingSink
	delivering chan struct{}
	release    chan struct{}
}

func (s blockingStatsSink) Deliver(report container.Report) {
	if len(report.Stats) > 0 {
		select {
		case s.delivering <- struct{}{}:
		default:
		}
		<-s.release
	}
	s.recordingSink.Deliver(report)
}

// Run ne rend la main qu'une fois la mesure en cours finie : la machine
// openCloud ferme sa base après lui.
func TestRun_WaitsForTheStatsInFlight(t *testing.T) {
	daemon := newDaemon(t, true)
	sink := blockingStatsSink{recordingSink: newSink(), delivering: make(chan struct{}, 1), release: make(chan struct{})}
	watcher := newWatcher(serve(t, daemon.mux), sink)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		watcher.Run(ctx)
		close(done)
	}()
	<-sink.delivering
	cancel()
	select {
	case <-done:
		t.Fatal("Run returned while stats were being delivered")
	case <-time.After(50 * time.Millisecond):
	}
	close(sink.release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return")
	}
}
