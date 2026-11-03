package hostconn

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/link"
)

type countingWatcher struct{ changes int }

func (w *countingWatcher) DirectoryChanged() { w.changes++ }

func TestService_SeenAdvancesAndNeverForgets(t *testing.T) {
	service := New()
	watcher := &countingWatcher{}
	service.SetWatcher(watcher)
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	service.SetClock(func() time.Time { return now })
	report := Report{Available: true, Connections: []Connection{
		{Port: 3000, Origin: OriginAccount, Name: "forgejo"},
		{Port: 3000, Origin: OriginInternet},
	}}
	if err := service.Record(context.Background(), "m1", report); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	// Un second rapport, sans le compte : il n'efface rien, et n'avance que l'autre.
	if err := service.Record(context.Background(), "m1", Report{Available: true, Connections: report.Connections[1:]}); err != nil {
		t.Fatal(err)
	}
	seen := service.Seen("m1")
	if len(seen) != 2 {
		t.Fatalf("seen = %+v", seen)
	}
	byKey := map[string]link.Sighting{}
	for _, sighting := range seen {
		byKey[sighting.Source] = sighting
	}
	account, internet := byKey[link.AccountKey("forgejo")], byKey[link.InternetKey()]
	if !account.LastSeen.Equal(now.Add(-time.Hour)) || !internet.LastSeen.Equal(now) || !internet.FirstSeen.Equal(now.Add(-time.Hour)) {
		t.Errorf("account = %+v, internet = %+v", account, internet)
	}
	if watcher.changes != 1 {
		t.Errorf("the live stream is told when a source appears, not at each report: %d", watcher.changes)
	}
	if got := service.Seen("another"); len(got) != 0 {
		t.Errorf("another machine = %+v", got)
	}
}

func TestService_RefusesOutOfRangeAndBoundsMemory(t *testing.T) {
	service := New()
	if err := service.Record(context.Background(), "m1", Report{Available: true, Connections: []Connection{{Port: 0, Origin: OriginInternet}}}); err == nil {
		t.Fatal("out of range report accepted")
	}
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	service.SetClock(func() time.Time { return now })
	for batch := 0; batch < 8; batch++ {
		var connections []Connection
		for port := batch*400 + 1; port <= batch*400+400 && port <= 65535; port++ {
			connections = append(connections, Connection{Port: port, Origin: OriginInternet})
		}
		connections = connections[:min(len(connections), MaxConnections)]
		now = now.Add(time.Minute)
		if err := service.Record(context.Background(), "m1", Report{Available: true, Connections: connections}); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(service.Seen("m1")); got != MaxSightingsPerMachine {
		t.Errorf("%d sightings, want the bound %d", got, MaxSightingsPerMachine)
	}
}
