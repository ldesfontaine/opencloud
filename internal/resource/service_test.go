package resource_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/resource"
	"github.com/ldesfontaine/opencloud/internal/sampler"
	"github.com/ldesfontaine/opencloud/internal/store"
)

var testNow = time.Date(2026, 9, 14, 12, 30, 0, 0, time.UTC)

type bench struct {
	service *resource.Service
	db      *store.DB
	now     time.Time
	changed []string
	mu      sync.Mutex
}

func (b *bench) ResourcesChanged(machineID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.changed = append(b.changed, machineID)
}

// Le service se teste sur la vraie base SQLite, temporaire, avec la machine
// openCloud en place et une horloge qu'on avance à la main.
func newBench(t *testing.T) *bench {
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
	err = db.SaveLocalMachine(context.Background(), machine.Machine{
		ID: machine.LocalID, Name: "opencloud", Kind: machine.KindLocal, EnrolledAt: testNow, CreatedAt: testNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	b := &bench{db: db, now: testNow}
	b.service = resource.New(db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	b.service.SetClock(func() time.Time { return b.now })
	b.service.SetWatcher(b)
	return b
}

// Deux volumes, le système et les données ; les totaux en font la somme.
func reading(at time.Time, cpu float64) sampler.Reading {
	return sampler.Reading{
		SampledAt: at, CPUPercent: cpu, CPUCores: 4, Load1: 0.9,
		MemUsed: 3 << 30, MemTotal: 8 << 30, SwapUsed: 0, SwapTotal: 2 << 30,
		DiskUsed: 341 << 30, DiskTotal: 580 << 30, NetRxPerSecond: 1_000_000, NetTxPerSecond: 200_000,
		Disks: []sampler.Disk{
			{MountPoint: "/", Device: "/dev/vda1", Used: 41 << 30, Total: 80 << 30},
			{MountPoint: "/data", Device: "/dev/vdb", Used: 300 << 30, Total: 500 << 30},
		},
	}
}

func TestRecord_KeepsTheLatestAsCurrent_AndTellsTheWatcher(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	err := b.service.Record(ctx, machine.LocalID, []sampler.Reading{
		reading(testNow.Add(-20*time.Second), 10),
		reading(testNow.Add(-10*time.Second), 30),
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := b.service.Current(ctx, machine.LocalID)
	if err != nil || !current.Available || current.Sample == nil || current.Sample.CPUPercent != 30 {
		t.Fatalf("current %+v %v", current, err)
	}
	if len(b.changed) != 1 || b.changed[0] != machine.LocalID {
		t.Fatalf("watcher %v", b.changed)
	}
	// Un échantillon plus ancien, rejoué, s'historise sans devenir le courant.
	if err := b.service.Record(ctx, machine.LocalID, []sampler.Reading{reading(testNow.Add(-30*time.Second), 99)}); err != nil {
		t.Fatal(err)
	}
	current, _ = b.service.Current(ctx, machine.LocalID)
	if current.Sample.CPUPercent != 30 {
		t.Fatalf("replayed sample took over: %+v", current.Sample)
	}
	// Le même échantillon deux fois n'est écrit qu'une fois.
	if err := b.service.Record(ctx, machine.LocalID, []sampler.Reading{reading(testNow.Add(-10*time.Second), 30)}); err != nil {
		t.Fatal(err)
	}
	_, points, err := b.service.History(ctx, machine.LocalID, "1h")
	if err != nil || len(points) != 3 {
		t.Fatalf("history %d points %v", len(points), err)
	}
}

// Les volumes suivent leur échantillon : le courant les rend, un
// échantillon rejoué ne les double pas, l'historique n'en garde que la
// somme.
func TestRecord_KeepsEachVolumeWithItsSample(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	first := reading(testNow.Add(-10*time.Second), 10)
	replayed := reading(testNow.Add(-10*time.Second), 10)
	replayed.Disks = replayed.Disks[:1]
	if err := b.service.Record(ctx, machine.LocalID, []sampler.Reading{first, replayed}); err != nil {
		t.Fatal(err)
	}
	current, err := b.service.Current(ctx, machine.LocalID)
	if err != nil || current.Sample == nil || len(current.Sample.Disks) != 2 {
		t.Fatalf("current %+v %v", current, err)
	}
	if got := current.Sample.Disks[1]; got.MountPoint != "/data" || got.Device != "/dev/vdb" || got.Used != 300<<30 || got.Total != 500<<30 {
		t.Fatalf("data volume %+v", got)
	}
	all, err := b.service.CurrentAll(ctx)
	if err != nil || len(all) != 1 || len(all[0].Sample.Disks) != 2 || all[0].Sample.Disks[0].MountPoint != "/" {
		t.Fatalf("all %+v %v", all, err)
	}
	// L'historique ne porte que la somme.
	_, points, err := b.service.History(ctx, machine.LocalID, "1h")
	if err != nil || len(points) != 1 || points[0].Disks != nil || points[0].DiskTotal != 580<<30 {
		t.Fatalf("history %+v %v", points, err)
	}
}

func TestCurrent_IsUnavailableWhenStaleOrAbsent_NeverZero(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	current, err := b.service.Current(ctx, machine.LocalID)
	if err != nil || current.Available || current.Sample != nil {
		t.Fatalf("without sample: %+v %v", current, err)
	}
	if err := b.service.Record(ctx, machine.LocalID, []sampler.Reading{reading(testNow, 50)}); err != nil {
		t.Fatal(err)
	}
	b.now = testNow.Add(resource.StaleAfter + time.Second)
	current, _ = b.service.Current(ctx, machine.LocalID)
	if current.Available || current.Sample == nil || current.Sample.CPUPercent != 50 {
		t.Fatalf("stale: %+v", current)
	}
	all, err := b.service.CurrentAll(ctx)
	if err != nil || len(all) != 1 || all[0].Available {
		t.Fatalf("all: %+v %v", all, err)
	}
}

func TestRecord_RefusesWhatAnAgentCannotHaveMeasured(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	tooManyDisks := make([]sampler.Disk, resource.MaxDisksPerReading+1)
	for i := range tooManyDisks {
		tooManyDisks[i] = sampler.Disk{MountPoint: "/" + strconv.Itoa(i), Total: 1}
	}
	cases := map[string]sampler.Reading{
		"cpu over 100":         {SampledAt: testNow, CPUPercent: 101},
		"negative":             {SampledAt: testNow, MemUsed: -1},
		"far future":           {SampledAt: testNow.Add(time.Hour)},
		"older than raw":       {SampledAt: testNow.Add(-resource.RawRetention - time.Hour)},
		"no date":              {},
		"relative mount point": {SampledAt: testNow, Disks: []sampler.Disk{{MountPoint: "data", Total: 1}}},
		"negative volume":      {SampledAt: testNow, Disks: []sampler.Disk{{MountPoint: "/", Used: -1, Total: 1}}},
		"same volume twice":    {SampledAt: testNow, Disks: []sampler.Disk{{MountPoint: "/", Total: 1}, {MountPoint: "/", Total: 2}}},
		"too many volumes":     {SampledAt: testNow, Disks: tooManyDisks},
	}
	for name, bad := range cases {
		if err := b.service.Record(ctx, machine.LocalID, []sampler.Reading{reading(testNow, 1), bad}); !errors.Is(err, resource.ErrReadingInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if current, _ := b.service.Current(ctx, machine.LocalID); current.Sample != nil {
		t.Fatal("a refused batch was partly written")
	}
	tooMany := make([]sampler.Reading, resource.MaxReadingsPerSignal+1)
	for i := range tooMany {
		tooMany[i] = reading(testNow.Add(-time.Duration(i)*time.Second), 1)
	}
	if err := b.service.Record(ctx, machine.LocalID, tooMany); !errors.Is(err, resource.ErrTooManyReadings) {
		t.Fatalf("too many: %v", err)
	}
}

// Deux heures de brut : une heure pleine à 60 % et une heure à trois
// échantillons à 0 %. Le rollup horaire moyenne chaque heure, le journalier
// pondère par le nombre d'échantillons, et tout se rejoue à l'identique.
func TestRollup_IsIdempotentAndWeightsTheDailyByCount(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	fullHour := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	var readings []sampler.Reading
	for i := range 360 {
		readings = append(readings, reading(fullHour.Add(time.Duration(i)*resource.SampleInterval), 60))
	}
	for i := range 3 {
		readings = append(readings, reading(fullHour.Add(time.Hour+time.Duration(i)*resource.SampleInterval), 0))
	}
	for start := 0; start < len(readings); start += resource.MaxReadingsPerSignal {
		end := min(start+resource.MaxReadingsPerSignal, len(readings))
		if err := b.service.Record(ctx, machine.LocalID, readings[start:end]); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := b.service.Rollup(ctx); err != nil {
			t.Fatal(err)
		}
	}
	_, hourly, err := b.service.History(ctx, machine.LocalID, "7d")
	if err != nil || len(hourly) != 2 {
		t.Fatalf("hourly %d %v", len(hourly), err)
	}
	if hourly[0].CPUPercent != 60 || hourly[1].CPUPercent != 0 || !hourly[0].SampledAt.Equal(fullHour) {
		t.Fatalf("hourly buckets %+v", hourly)
	}
	_, daily, err := b.service.History(ctx, machine.LocalID, "90d")
	if err != nil || len(daily) != 1 {
		t.Fatalf("daily %d %v", len(daily), err)
	}
	// 360 × 60 + 3 × 0 sur 363 : 59,5 %, pas la moyenne des moyennes à 30 %.
	if got := daily[0].CPUPercent; got < 59.4 || got > 59.6 {
		t.Fatalf("daily cpu %v", got)
	}
	if daily[0].MemTotal != 8<<30 || daily[0].CPUCores != 4 {
		t.Fatalf("daily bucket %+v", daily[0])
	}
}

func TestHistory_GroupsTheRawByStep_AndRefusesAnUnknownWindow(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	start := testNow.Add(-10 * time.Minute).Truncate(5 * time.Minute)
	var readings []sampler.Reading
	for i := range 60 {
		readings = append(readings, reading(start.Add(time.Duration(i)*resource.SampleInterval), float64(i%2*100)))
	}
	if err := b.service.Record(ctx, machine.LocalID, readings); err != nil {
		t.Fatal(err)
	}
	window, points, err := b.service.History(ctx, machine.LocalID, "24h")
	if err != nil || window.Step != 5*time.Minute || len(points) != 2 {
		t.Fatalf("24h: %d points, step %v, %v", len(points), window.Step, err)
	}
	if points[0].CPUPercent != 50 || !points[0].SampledAt.Equal(start) {
		t.Fatalf("bucket %+v", points[0])
	}
	if _, _, err := b.service.History(ctx, machine.LocalID, "2h"); !errors.Is(err, resource.ErrWindowUnknown) {
		t.Fatalf("unknown window: %v", err)
	}
}

func TestPurge_DropsEachTierPastItsRetention(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	old := testNow.Add(-resource.RawRetention + time.Hour)
	if err := b.service.Record(ctx, machine.LocalID, []sampler.Reading{reading(old, 1), reading(testNow, 2)}); err != nil {
		t.Fatal(err)
	}
	if err := b.service.Rollup(ctx); err != nil {
		t.Fatal(err)
	}
	b.now = testNow.Add(2 * time.Hour)
	if err := b.service.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	_, raw, _ := b.service.History(ctx, machine.LocalID, "24h")
	if len(raw) != 1 || raw[0].CPUPercent != 2 {
		t.Fatalf("raw after purge %+v", raw)
	}
	b.now = testNow.Add(resource.DailyRetention + 48*time.Hour)
	if err := b.service.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	if _, daily, _ := b.service.History(ctx, machine.LocalID, "90d"); len(daily) != 0 {
		t.Fatalf("daily after purge %+v", daily)
	}
	if _, err := b.db.LatestSample(ctx, machine.LocalID); !errors.Is(err, resource.ErrNoSample) {
		t.Fatalf("raw after purge: %v", err)
	}
}

func TestRemoveMachine_TakesItsSamplesAlong(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	id, _ := machine.NewID()
	cleartext, token, err := machine.NewToken("vps-1", "")
	if err != nil {
		t.Fatal(err)
	}
	token.CreatedAt, token.ExpiresAt = testNow, testNow.Add(time.Hour)
	if err := b.db.InsertToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	_ = cleartext
	enrolled, err := b.db.Enroll(ctx, token.Hash, machine.Machine{ID: id, Kind: machine.KindRemote, PublicKey: make([]byte, 32), EnrolledAt: testNow, CreatedAt: testNow}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.service.Record(ctx, enrolled.ID, []sampler.Reading{reading(testNow, 5)}); err != nil {
		t.Fatal(err)
	}
	if err := b.service.Rollup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.db.DeleteMachine(ctx, enrolled.ID); err != nil {
		t.Fatal(err)
	}
	if all, _ := b.service.CurrentAll(ctx); len(all) != 0 {
		t.Fatalf("samples survived: %+v", all)
	}
	if _, hourly, _ := b.service.History(ctx, enrolled.ID, "7d"); len(hourly) != 0 {
		t.Fatalf("hourly survived: %+v", hourly)
	}
}
