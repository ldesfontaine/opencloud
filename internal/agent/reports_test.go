package agent

import (
	"testing"

	"github.com/ldesfontaine/opencloud/internal/container"
)

func TestReports_MergeTakeRestoreAndReplay(t *testing.T) {
	var pending reports
	if pending.take() != nil {
		t.Fatal("empty at start")
	}
	pending.Deliver(container.Report{Engine: &container.EngineReport{Present: true}})
	pending.Deliver(container.Report{Complete: true, Inventory: []container.Observed{{Name: "a"}}})
	pending.Deliver(container.Report{Complete: true, Inventory: []container.Observed{{Name: "b"}}})
	pending.Deliver(container.Report{NetworksComplete: true, Networks: []container.NetworkReport{{Name: "old"}}})
	pending.Deliver(container.Report{NetworksComplete: true, Networks: []container.NetworkReport{{Name: "new"}}})
	pending.Deliver(container.Report{VolumesComplete: true, Volumes: []container.VolumeReport{{Name: "old", Driver: "local"}}})
	pending.Deliver(container.Report{VolumesComplete: true, Volumes: []container.VolumeReport{{Name: "new", Driver: "local"}}})
	pending.Deliver(container.Report{Events: []container.Event{{Action: "start"}}})
	pending.Deliver(container.Report{Stats: []container.Stat{{CPUPercent: 1}}})

	taken := pending.take()
	if taken == nil || !taken.Engine.Present || len(taken.Inventory) != 1 || taken.Inventory[0].Name != "b" || len(taken.Events) != 1 || len(taken.Stats) != 1 {
		t.Fatalf("taken = %+v", taken)
	}
	if !taken.NetworksComplete || len(taken.Networks) != 1 || taken.Networks[0].Name != "new" {
		t.Fatalf("networks = %+v", taken.Networks)
	}
	if !taken.VolumesComplete || len(taken.Volumes) != 1 || taken.Volumes[0].Name != "new" {
		t.Fatalf("volumes = %+v", taken.Volumes)
	}
	if pending.take() != nil {
		t.Fatal("take empties the buffer")
	}
	pending.Deliver(container.Report{Events: []container.Event{{Action: "die"}}})
	pending.restore(taken)
	pending.markReplayed()
	again := pending.take()
	if len(again.Events) != 2 || again.Events[0].Action != "start" || !again.Events[0].Replayed || !again.Events[1].Replayed || again.Inventory[0].Name != "b" || !again.NetworksComplete || !again.VolumesComplete {
		t.Fatalf("again = %+v", again)
	}
}

func TestReports_ForgetsTheOldestWhenFull(t *testing.T) {
	var pending reports
	for i := 0; i < container.MaxEventsPerReport+5; i++ {
		pending.Deliver(container.Report{Events: []container.Event{{Action: "start"}}})
	}
	if taken := pending.take(); len(taken.Events) != container.MaxEventsPerReport {
		t.Fatalf("len = %d", len(taken.Events))
	}
}
