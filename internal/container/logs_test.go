package container

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

// La machine openCloud n'a plus de source en processus : son journal se
// demande à son agent, comme pour toute machine.
func TestRelay_TheOpenCloudMachineAsksItsAgentLikeAnyOther(t *testing.T) {
	relay := NewRelay()
	commander := &fakeCommander{}
	relay.SetCommander(commander)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := relay.Open(ctx, "local", LogRequest{ContainerID: "c", Tail: 10}); err != nil {
		t.Fatal(err)
	}
	if commander.count() != 1 || commander.commands[0] != "local:logs" {
		t.Fatalf("commands = %v", commander.commands)
	}
}

func TestRelay_RemoteCommandsTheAgentAndRelaysItsBatches(t *testing.T) {
	relay := NewRelay()
	relay.newID = func() string { return "req-1" }
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
	if err := relay.Deliver(context.Background(), "vps", "req-1", LogBatch{Lines: []LogLine{{Text: "a"}}}); err != nil {
		t.Fatalf("delivery refused: %v", err)
	}
	if batch := <-batches; batch.Lines[0].Text != "a" {
		t.Fatalf("batch = %+v", batch)
	}
	_ = relay.Deliver(context.Background(), "vps", "req-1", LogBatch{Done: true})
	if batch := <-batches; !batch.Done {
		t.Fatalf("batch = %+v", batch)
	}
	if _, open := <-batches; open {
		t.Fatal("channel should close after Done")
	}
	if err := relay.Deliver(context.Background(), "vps", "req-1", LogBatch{}); !errors.Is(err, ErrLogRequestUnknown) {
		t.Fatalf("a finished request accepts nothing: %v", err)
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

func TestRelay_WithoutCommander_SaysLogsAreUnavailableHere(t *testing.T) {
	relay := NewRelay()
	if _, err := relay.Open(context.Background(), "vps", LogRequest{}); !errors.Is(err, ErrLogsUnavailableLocally) {
		t.Fatalf("err = %v", err)
	}
}

func TestRelay_BatchForAnotherMachine_IsRefused(t *testing.T) {
	relay := NewRelay()
	relay.newID = func() string { return "req-1" }
	relay.SetCommander(&fakeCommander{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := relay.Open(ctx, "vps", LogRequest{Follow: true}); err != nil {
		t.Fatal(err)
	}
	if err := relay.Deliver(context.Background(), "other", "req-1", LogBatch{Lines: []LogLine{{Text: "intrus"}}}); !errors.Is(err, ErrLogRequestUnknown) {
		t.Fatalf("another machine delivered into this request: %v", err)
	}
	if err := relay.Deliver(context.Background(), "vps", "req-1", LogBatch{}); err != nil {
		t.Fatalf("the machine that was asked is refused: %v", err)
	}
}

// Des livraisons en rafale pendant que le lecteur s'en va : aucune ne doit
// envoyer sur le canal fermé. Sous -race, ou par la panique sans.
func TestRelay_DeliveriesWhileTheReaderLeaves_NeverSendOnAClosedChannel(t *testing.T) {
	for round := 0; round < 200; round++ {
		relay := NewRelay()
		relay.SetCommander(&fakeCommander{})
		var requestID string
		relay.newID = func() string { requestID = newRequestID(); return requestID }
		ctx, cancel := context.WithCancel(context.Background())
		batches, err := relay.Open(ctx, "vps", LogRequest{Follow: true})
		if err != nil {
			t.Fatal(err)
		}
		var senders sync.WaitGroup
		for sender := 0; sender < 8; sender++ {
			senders.Add(1)
			go func() {
				defer senders.Done()
				for range batchBacklog * 2 {
					_ = relay.Deliver(context.Background(), "vps", requestID, LogBatch{Lines: []LogLine{{Text: "x"}}})
				}
			}()
		}
		<-batches
		cancel()
		for range batches {
		}
		senders.Wait()
	}
}
