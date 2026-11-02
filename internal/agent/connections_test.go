package agent

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/hostconn"
)

func connectionReport(port int) hostconn.Report {
	return hostconn.Report{Available: true, Connections: []hostconn.Connection{{Port: port, Origin: hostconn.OriginInternet}}}
}

func TestConnectionReports_SendOnChangeAndAgainOnANewStream(t *testing.T) {
	reports := &connectionReports{}
	if !reports.update(connectionReport(80)) || reports.take() == nil {
		t.Fatal("the first reading must be delivered")
	}
	if reports.update(connectionReport(80)) || reports.take() != nil {
		t.Fatal("an unchanged reading is not delivered again")
	}
	reports.markReplayed()
	if reports.take() == nil {
		t.Fatal("a new stream hears it again")
	}
	if !reports.update(connectionReport(443)) || reports.take() == nil {
		t.Fatal("a changed reading is delivered")
	}
	// Un signal que le réseau a perdu : le rapport repart.
	reports.restore(&hostconn.Report{})
	if reports.take() == nil {
		t.Fatal("a lost signal puts the report back")
	}
}

func TestConnectionsLoop_HeartbeatRepeatsAnUnchangedReport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reports := &connectionReports{}
	reads := make(chan struct{}, 64)
	go connectionsLoop(ctx, func() hostconn.Report {
		reads <- struct{}{}
		return connectionReport(80)
	}, reports, 5*time.Millisecond, 20*time.Millisecond, slog.New(slog.DiscardHandler))

	deadline := time.After(2 * time.Second)
	delivered := 0
	for delivered < 3 {
		select {
		case <-deadline:
			t.Fatalf("only %d deliveries, the heartbeat must repeat the unchanged report", delivered)
		case <-time.After(2 * time.Millisecond):
			if reports.take() != nil {
				delivered++
			}
		}
	}
}
