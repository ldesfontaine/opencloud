package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeCommander struct {
	mu       sync.Mutex
	commands []string
	offline  bool
}

func (c *fakeCommander) Command(machineID, name string, payload any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.offline {
		return ErrMachineOffline
	}
	c.commands = append(c.commands, machineID+":"+name)
	return nil
}

func (c *fakeCommander) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.commands)
}

type fakeLocal struct{ opened []LogRequest }

func (l *fakeLocal) OpenLogs(_ context.Context, request LogRequest) (<-chan LogBatch, error) {
	l.opened = append(l.opened, request)
	batches := make(chan LogBatch, 1)
	batches <- LogBatch{Lines: []LogLine{{Text: "local"}}, Done: true}
	close(batches)
	return batches, nil
}

func TestRelay_LocalMachineGoesToTheInProcessSource(t *testing.T) {
	relay := NewRelay()
	local := &fakeLocal{}
	relay.SetLocal("local", local)
	relay.SetCommander(&fakeCommander{})
	batches, err := relay.Open(context.Background(), "local", LogRequest{ContainerID: "c", Tail: 10})
	if err != nil {
		t.Fatal(err)
	}
	if batch := <-batches; batch.Lines[0].Text != "local" {
		t.Fatalf("batch = %+v", batch)
	}
	if len(local.opened) != 1 || local.opened[0].ID == "" || local.opened[0].Tail != 10 {
		t.Fatalf("opened = %+v", local.opened)
	}
}

func TestRelay_RemoteCommandsTheAgentAndRelaysItsBatches(t *testing.T) {
	relay := NewRelay()
	relay.setIDs(func() string { return "req-1" })
	commander := &fakeCommander{}
	relay.SetCommander(commander)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	batches, err := relay.Open(ctx, "vps", LogRequest{ContainerID: "c", Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	if commander.commands[0] != "vps:logs" {
		t.Fatalf("commands = %v", commander.commands)
	}
	if !relay.Deliver(context.Background(), "req-1", LogBatch{Lines: []LogLine{{Text: "a"}}}) {
		t.Fatal("delivery refused")
	}
	if batch := <-batches; batch.Lines[0].Text != "a" {
		t.Fatalf("batch = %+v", batch)
	}
	relay.Deliver(context.Background(), "req-1", LogBatch{Done: true})
	if batch := <-batches; !batch.Done {
		t.Fatalf("batch = %+v", batch)
	}
	if _, open := <-batches; open {
		t.Fatal("channel should close after Done")
	}
	if relay.Deliver(context.Background(), "req-1", LogBatch{}) {
		t.Fatal("a finished request accepts nothing")
	}
}

func TestRelay_ReaderLeaving_StopsTheAgent(t *testing.T) {
	relay := NewRelay()
	commander := &fakeCommander{}
	relay.SetCommander(commander)
	ctx, cancel := context.WithCancel(context.Background())
	batches, err := relay.Open(ctx, "vps", LogRequest{ContainerID: "c", Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, open := <-batches; open {
		t.Fatal("channel should close with the context")
	}
	deadline := time.Now().Add(time.Second)
	for commander.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if commander.count() != 2 || commander.commands[1] != "vps:logs_stop" {
		t.Fatalf("commands = %v", commander.commands)
	}
}

func TestRelay_OfflineMachineAndBusyMachine(t *testing.T) {
	relay := NewRelay()
	commander := &fakeCommander{offline: true}
	relay.SetCommander(commander)
	if _, err := relay.Open(context.Background(), "vps", LogRequest{}); !errors.Is(err, ErrMachineOffline) {
		t.Fatalf("err = %v", err)
	}
	commander.offline = false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := 0; i < MaxFollowsPerMachine; i++ {
		if _, err := relay.Open(ctx, "vps", LogRequest{Follow: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := relay.Open(ctx, "vps", LogRequest{Follow: true}); !errors.Is(err, ErrLogsBusy) {
		t.Fatalf("err = %v", err)
	}
	if _, err := relay.Open(ctx, "other", LogRequest{Follow: true}); err != nil {
		t.Fatalf("another machine is not busy: %v", err)
	}
}

func TestRelay_WithoutCommander_IsOffline(t *testing.T) {
	relay := NewRelay()
	if _, err := relay.Open(context.Background(), "vps", LogRequest{}); !errors.Is(err, ErrMachineOffline) {
		t.Fatalf("err = %v", err)
	}
}
