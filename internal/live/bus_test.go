package live

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPublish_ReachesEverySubscriber(t *testing.T) {
	bus := New()
	first := bus.Subscribe()
	second := bus.Subscribe()
	defer first.Close()
	defer second.Close()
	bus.Publish(TopicMachines)
	for _, subscription := range []*Subscription{first, second} {
		topics, err := subscription.Next(context.Background())
		if err != nil || len(topics) != 1 || topics[0] != TopicMachines {
			t.Fatalf("topics %v, err %v", topics, err)
		}
	}
}

// Un abonné qui ne lit pas voit ses sujets fusionnés : cent signaux et une
// tâche donnent deux sujets, jamais une file qui déborde.
func TestSubscription_CoalescesWhileTheSubscriberIsSlow(t *testing.T) {
	bus := New()
	subscription := bus.Subscribe()
	defer subscription.Close()
	for range 100 {
		bus.MachineChanged("m1")
	}
	bus.HeartbeatChanged("h1")
	topics, err := subscription.Next(context.Background())
	if err != nil || len(topics) != 2 || topics[0] != TopicJobs || topics[1] != TopicMachines {
		t.Fatalf("topics %v, err %v", topics, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := subscription.Next(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a second read must wait, got %v", err)
	}
}

func TestNext_WakesUpOnALaterPublish(t *testing.T) {
	bus := New()
	subscription := bus.Subscribe()
	defer subscription.Close()
	go func() {
		time.Sleep(20 * time.Millisecond)
		bus.Publish(TopicJobs)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	topics, err := subscription.Next(ctx)
	if err != nil || len(topics) != 1 || topics[0] != TopicJobs {
		t.Fatalf("topics %v, err %v", topics, err)
	}
}

func TestClose_StopsDeliveryAndTheCount(t *testing.T) {
	bus := New()
	subscription := bus.Subscribe()
	if bus.Count() != 1 {
		t.Fatalf("count %d", bus.Count())
	}
	subscription.Close()
	bus.Publish(TopicMachines)
	if bus.Count() != 0 || len(subscription.take()) != 0 {
		t.Fatalf("closed subscription still served: count %d", bus.Count())
	}
}

// Cent lots de mesures dans la fenêtre ne font qu'un « resources » tout de
// suite ; le suivant attend la fin de la fenêtre.
func TestResourcesChanged_PublishesAtMostOncePerWindow(t *testing.T) {
	bus := New()
	subscription := bus.Subscribe()
	defer subscription.Close()
	for range 100 {
		bus.ResourcesChanged("m1")
	}
	topics, err := subscription.Next(context.Background())
	if err != nil || len(topics) != 1 || topics[0] != TopicResources {
		t.Fatalf("topics %v, err %v", topics, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if topics, err := subscription.Next(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a second resources within the window: %v, err %v", topics, err)
	}
}

// Ce qui arrive dans la fenêtre est reporté à sa fin, une seule fois, jamais
// perdu ; les autres sujets ne sont pas retenus.
func TestPublishAtMost_DefersTheLastChangeToTheEndOfTheWindow(t *testing.T) {
	const window = 100 * time.Millisecond
	bus := New()
	subscription := bus.Subscribe()
	defer subscription.Close()
	started := time.Now()
	bus.PublishAtMost(TopicResources, window)
	if topics, _ := subscription.Next(context.Background()); len(topics) != 1 {
		t.Fatalf("first publication: %v", topics)
	}
	bus.PublishAtMost(TopicResources, window)
	bus.PublishAtMost(TopicResources, window)
	bus.MachineChanged("m1")
	if topics, _ := subscription.Next(context.Background()); len(topics) != 1 || topics[0] != TopicMachines {
		t.Fatalf("machines must not wait: %v", topics)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	topics, err := subscription.Next(ctx)
	if err != nil || len(topics) != 1 || topics[0] != TopicResources {
		t.Fatalf("deferred publication: %v, err %v", topics, err)
	}
	if elapsed := time.Since(started); elapsed < window {
		t.Fatalf("deferred publication after %v, before the window", elapsed)
	}
	short, cancelShort := context.WithTimeout(context.Background(), 2*window)
	defer cancelShort()
	if topics, err := subscription.Next(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the deferred publication went out twice: %v, err %v", topics, err)
	}
}
