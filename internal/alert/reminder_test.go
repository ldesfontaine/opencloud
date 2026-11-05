package alert_test

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// openDanger ouvre une alerte de danger sur un volume et rend son identifiant.
func (f *fixture) openDanger(t *testing.T) int64 {
	t.Helper()
	f.Open(context.Background(), volumeFact(alert.SeverityDanger, 96))
	return f.only(t, alert.StatusOpen).ID
}

func TestReminder_ADangerLeftUnreadIsRemindedOnce(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "ops", alert.SeverityAttention, true)
	ctx := context.Background()
	f.openDanger(t)

	f.advance(11*time.Hour + 59*time.Minute)
	f.Sweep(ctx)
	if f.sender.events() != "opened:ops" {
		t.Fatalf("before 12 h: %q", f.sender.events())
	}
	f.advance(2 * time.Minute)
	f.Sweep(ctx)
	if f.sender.events() != "opened:ops,reminded:ops" {
		t.Fatalf("after 12 h: %q", f.sender.events())
	}
	reminded := f.only(t, alert.StatusOpen).RemindedAt
	if reminded.IsZero() {
		t.Fatal("the alert must remember its reminder")
	}
	changes := f.watcher.changes.Load()
	f.advance(48 * time.Hour)
	f.Sweep(ctx)
	if f.sender.events() != "opened:ops,reminded:ops" {
		t.Fatalf("a reminder goes once: %q", f.sender.events())
	}
	// Ni réécrite, ni annoncée au direct : le rappel parti n'est plus à faire.
	if again := f.only(t, alert.StatusOpen).RemindedAt; !again.Equal(reminded) || f.watcher.changes.Load() != changes {
		t.Fatalf("a reminded alert must be left alone: %v then %v, %d changes then %d", reminded, again, changes, f.watcher.changes.Load())
	}
}

func TestReminder_NeverForAnAttentionNorAReadNorAMutedAlert(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "ops", alert.SeverityAttention, true)
	ctx := context.Background()

	f.Open(ctx, volumeFact(alert.SeverityAttention, 86))
	attention := f.only(t, alert.StatusOpen)
	f.advance(24 * time.Hour)
	f.Sweep(ctx)
	if f.sender.events() != "opened:ops" {
		t.Fatalf("an attention is not reminded: %q", f.sender.events())
	}
	if _, err := f.Acknowledge(ctx, attention.ID); err != nil {
		t.Fatal(err)
	}

	f.Resolve(ctx, alert.KindDiskFull, attention.Object)
	f.Open(ctx, volumeFact(alert.SeverityDanger, 96))
	read := f.only(t, alert.StatusOpen)
	if _, err := f.Acknowledge(ctx, read.ID); err != nil {
		t.Fatal(err)
	}
	f.advance(24 * time.Hour)
	f.Sweep(ctx)
	if f.sender.events() != "opened:ops,resolved:ops,opened:ops" {
		t.Fatalf("a read alert is not reminded: %q", f.sender.events())
	}
}

func TestReminder_AMutedDangerWaitsForTheEndOfTheMute(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "ops", alert.SeverityAttention, true)
	ctx := context.Background()
	f.openDanger(t)
	f.mute(t, alert.MuteDefinition{Target: alert.Target{Kind: alert.TargetMachine, ID: machine.LocalID}, Duration: 24 * time.Hour})

	f.advance(13 * time.Hour)
	f.Sweep(ctx)
	if f.sender.events() != "opened:ops" {
		t.Fatalf("muted: %q", f.sender.events())
	}
	if !f.only(t, alert.StatusOpen).RemindedAt.IsZero() {
		t.Fatal("a muted reminder is not spent: it goes when the mute ends")
	}
	f.advance(12 * time.Hour)
	f.Sweep(ctx)
	if f.sender.events() != "opened:ops,reminded:ops" {
		t.Fatalf("after the mute: %q", f.sender.events())
	}
}

func TestReminder_AnAggravationRestartsTheDelay(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "ops", alert.SeverityAttention, true)
	ctx := context.Background()
	f.Open(ctx, volumeFact(alert.SeverityAttention, 86))
	f.advance(20 * time.Hour)
	f.Open(ctx, volumeFact(alert.SeverityDanger, 96))
	f.advance(11 * time.Hour)
	f.Sweep(ctx)
	if f.sender.events() != "opened:ops,aggravated:ops" {
		t.Fatalf("11 h after the aggravation: %q", f.sender.events())
	}
	f.advance(time.Hour)
	f.Sweep(ctx)
	if f.sender.events() != "opened:ops,aggravated:ops,reminded:ops" {
		t.Fatalf("12 h after the aggravation: %q", f.sender.events())
	}
}

func TestReminder_ZeroTurnsItOff_AndTheDelayIsReadAtEachSweep(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "ops", alert.SeverityAttention, true)
	ctx := context.Background()
	delay := time.Duration(0)
	f.SetReminderDelay(func() time.Duration { return delay })
	f.openDanger(t)
	f.advance(72 * time.Hour)
	f.Sweep(ctx)
	if f.sender.events() != "opened:ops" {
		t.Fatalf("off: %q", f.sender.events())
	}
	delay = 24 * time.Hour
	f.Sweep(ctx)
	if f.sender.events() != "opened:ops,reminded:ops" {
		t.Fatalf("turned on: %q", f.sender.events())
	}
}
