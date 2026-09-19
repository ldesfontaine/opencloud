package agent

import (
	"testing"

	"github.com/ldesfontaine/opencloud/internal/service"
)

func TestReports_MergeTakeRestoreAndReplay(t *testing.T) {
	var pending reports
	if pending.take() != nil {
		t.Fatal("empty at start")
	}
	pending.Deliver(service.Report{Engine: &service.EngineReport{Present: true}})
	pending.Deliver(service.Report{Complete: true, Inventory: []service.Container{{Name: "a"}}})
	pending.Deliver(service.Report{Complete: true, Inventory: []service.Container{{Name: "b"}}})
	pending.Deliver(service.Report{Events: []service.Event{{Action: "start"}}})
	pending.Deliver(service.Report{Stats: []service.Stat{{CPUPercent: 1}}})

	taken := pending.take()
	if taken == nil || !taken.Engine.Present || len(taken.Inventory) != 1 || taken.Inventory[0].Name != "b" || len(taken.Events) != 1 || len(taken.Stats) != 1 {
		t.Fatalf("taken = %+v", taken)
	}
	if pending.take() != nil {
		t.Fatal("take empties the buffer")
	}
	pending.Deliver(service.Report{Events: []service.Event{{Action: "die"}}})
	pending.restore(taken)
	pending.markReplayed()
	again := pending.take()
	if len(again.Events) != 2 || again.Events[0].Action != "start" || !again.Events[0].Replayed || !again.Events[1].Replayed || again.Inventory[0].Name != "b" {
		t.Fatalf("again = %+v", again)
	}
}

func TestReports_ForgetsTheOldestWhenFull(t *testing.T) {
	var pending reports
	for i := 0; i < service.MaxEventsPerReport+5; i++ {
		pending.Deliver(service.Report{Events: []service.Event{{Action: "start"}}})
	}
	if taken := pending.take(); len(taken.Events) != service.MaxEventsPerReport {
		t.Fatalf("len = %d", len(taken.Events))
	}
}
