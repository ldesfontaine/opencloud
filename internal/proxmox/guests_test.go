package proxmox

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

type fakeOrders struct {
	mu     sync.Mutex
	sent   []agentwire.Kind
	params []json.RawMessage
	err    error
	waited chan string
}

func (o *fakeOrders) Send(_ context.Context, _ string, kind agentwire.Kind, params json.RawMessage) (action.Run, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.err != nil {
		return action.Run{}, o.err
	}
	o.sent = append(o.sent, kind)
	o.params = append(o.params, params)
	return action.Run{ID: "run1", Kind: kind, Params: params, State: action.StateSent}, nil
}

func (o *fakeOrders) Wait(_ context.Context, runID string) (action.Run, error) {
	o.waited <- runID
	return action.Run{ID: runID, State: action.StateSucceeded}, nil
}

type fakeViewer struct{ guests []Guest }

func (v fakeViewer) GetProxmoxView(context.Context, string) (View, error) {
	return View{Guests: v.guests}, nil
}

func guestsRig(t *testing.T) (*Service, *fakeOrders) {
	t.Helper()
	service := New(newMemoryStore(), nil, nil)
	orders := &fakeOrders{waited: make(chan string, 4)}
	service.SetOrders(orders)
	service.SetViewer(fakeViewer{guests: []Guest{
		{VMID: 105, Kind: KindQEMU, Name: "web", Status: "stopped"},
		{VMID: 106, Kind: KindQEMU, Name: "db", Status: "running"},
		{VMID: 107, Kind: KindQEMU, Name: "model", Status: "stopped", Template: true},
		{VMID: 108, Kind: KindQEMU, Name: "server", Status: "running", LinkedMachineID: machine.LocalID},
	}})
	return service, orders
}

func TestAct_SendsTheOrderOfTheGestureWithTheGuestKind(t *testing.T) {
	service, orders := guestsRig(t)
	cases := []struct {
		vmid     int
		verb     Verb
		snapshot string
		kind     agentwire.Kind
	}{
		{105, VerbStart, "", agentwire.KindProxmoxStart},
		{106, VerbShutdown, "", agentwire.KindProxmoxShutdown},
		{106, VerbSnapshot, "", agentwire.KindProxmoxSnapshot},
		{105, VerbSnapshotDelete, "oc-20261021-120000", agentwire.KindProxmoxSnapshotDelete},
	}
	for _, c := range cases {
		if _, err := service.Act(context.Background(), "host", c.vmid, c.verb, c.snapshot); err != nil {
			t.Fatalf("%s: %v", c.verb, err)
		}
	}
	got := orders.sent
	if len(got) != 4 || got[0] != cases[0].kind || got[3] != cases[3].kind {
		t.Fatalf("sent %v", got)
	}
	if string(orders.params[0]) != `{"vmid":105,"type":"qemu"}` {
		t.Fatalf("params %s", orders.params[0])
	}
	for range cases {
		<-orders.waited
	}
	service.WaitBackground()
}

func TestAct_RefusesBeforeSendingAnythingTheServerKnowsImpossible(t *testing.T) {
	cases := map[string]struct {
		vmid     int
		verb     Verb
		snapshot string
		want     error
	}{
		"unknown guest":          {999, VerbStart, "", ErrGuestUnknown},
		"template, start":        {107, VerbStart, "", ErrTemplate},
		"template, snapshot":     {107, VerbSnapshot, "", ErrTemplate},
		"start a running guest":  {106, VerbStart, "", ErrStateWrong},
		"shut down a stopped":    {105, VerbShutdown, "", ErrStateWrong},
		"the server's own guest": {108, VerbShutdown, "", ErrSelfTarget},
		"a snapshot not ours":    {105, VerbSnapshotDelete, "avant-migration", ErrSnapshotName},
		"no snapshot to delete":  {105, VerbSnapshotDelete, "", ErrSnapshotName},
		"a name on a start":      {105, VerbStart, "oc-20261021-120000", ErrGuestInvalid},
	}
	for name, c := range cases {
		service, orders := guestsRig(t)
		if _, err := service.Act(context.Background(), "host", c.vmid, c.verb, c.snapshot); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
		if len(orders.sent) != 0 {
			t.Errorf("%s: an order left: %v", name, orders.sent)
		}
	}
}

func TestAct_ASnapshotOfTheServersOwnGuestIsAllowed(t *testing.T) {
	service, orders := guestsRig(t)
	if _, err := service.Act(context.Background(), "host", 108, VerbSnapshot, ""); err != nil {
		t.Fatal(err)
	}
	<-orders.waited
	service.WaitBackground()
}

func TestAct_PassesTheAgentRefusalsThrough(t *testing.T) {
	service, orders := guestsRig(t)
	orders.err = action.ErrRightMissing
	if _, err := service.Act(context.Background(), "host", 105, VerbStart, ""); !errors.Is(err, action.ErrRightMissing) {
		t.Fatalf("%v", err)
	}
}

func TestOwnSnapshots_AreCreatedMinusDeleted(t *testing.T) {
	params := func(vmid int, snapshot string) json.RawMessage {
		encoded, _ := json.Marshal(agentwire.ProxmoxGuestParams{VMID: vmid, Type: "qemu", Snapshot: snapshot})
		return encoded
	}
	line := func(text string) []action.LogLine { return []action.LogLine{{Line: "task UPID"}, {Line: text}} }
	// Du plus récent au plus ancien, comme la liste des runs.
	runs := []action.Run{
		{Kind: agentwire.KindProxmoxSnapshotDelete, State: action.StateSucceeded, Params: params(105, "oc-20261021-100000"), Output: line("snapshot_deleted qemu/105 oc-20261021-100000"), RequestedAt: time.Now()},
		{Kind: agentwire.KindProxmoxSnapshot, State: action.StateFailed, Params: params(105, ""), Output: line("snapshot qemu/105 oc-20261021-110000")},
		{Kind: agentwire.KindProxmoxSnapshot, State: action.StateSucceeded, Params: params(105, ""), Output: line("snapshot qemu/105 oc-20261021-100000")},
		{Kind: agentwire.KindProxmoxSnapshot, State: action.StateSucceeded, Params: params(105, ""), Output: line("snapshot qemu/105 oc-20261021-090000")},
		{Kind: agentwire.KindProxmoxSnapshot, State: action.StateSucceeded, Params: params(106, ""), Output: line("snapshot qemu/106 oc-20261021-090000")},
		{Kind: agentwire.KindProxmoxSnapshot, State: action.StateSucceeded, Params: params(105, ""), Output: line("snapshot qemu/105 manuel")},
	}
	if got := OwnSnapshots(runs, 105); !slices.Equal(got, []string{"oc-20261021-090000"}) {
		t.Fatalf("%v", got)
	}
	if got := OwnSnapshots(nil, 105); got == nil || len(got) != 0 {
		t.Fatalf("%v", got)
	}
}
