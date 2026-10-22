package agent

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
)

// probeOnLoopback enrôle un agent avec ces droits, lui donne une sonde TCP
// vers un écouteur de la boucle locale, le relance (le jeu de sondes arrive à
// l'ouverture du flux) et rend la sonde une fois son premier essai rapporté.
// narrowed relance l'agent sur un état jugé mal gardé : il n'a plus qu'observe.
func probeOnLoopback(t *testing.T, rights string, narrowed bool) probe.Probe {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	b := newBench(t)
	granted, err := machine.ParseCapabilities(rights)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := b.machines.CreateToken(context.Background(), "vps-lan-1", granted)
	if err != nil {
		t.Fatal(err)
	}
	enrolling := b.options(token)
	enrolling.Rights = granted
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, enrolling) }()
	enrolled := b.waitFor(t, "vps-lan-1", func(s machine.Status) bool { return s.Online })
	cancel()
	<-done
	b.waitFor(t, "vps-lan-1", func(s machine.Status) bool { return !s.Online })

	created, err := b.probes.Create(context.Background(), probe.Definition{
		Name: "service local", Kind: probe.KindTCP, Target: listener.Addr().String(), MachineID: enrolled.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	restarted := b.options("")
	if narrowed {
		restarted.UnsafeState = &UnsafeStateError{Path: "/var/lib/opencloud", UID: 999, Mode: dir0755}
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer func() {
		cancel()
		<-done
	}()
	go func() { done <- Run(ctx, restarted) }()
	deadline := time.Now().Add(benchTimeout)
	for time.Now().Before(deadline) {
		found, err := b.db.GetProbe(context.Background(), created.ID)
		if err == nil && !found.LastCheckedAt.IsZero() {
			return found
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the probe never reported a result")
	return probe.Probe{}
}

// Le réseau local ne s'ouvre aux sondes que par probe.lan : sans lui, la
// sonde vers la boucle locale tombe avec sa raison ; avec lui, elle répond.
// Un agent enrôlé avant le droit (observe seul) est dans le premier cas, et
// un état mal gardé (observe seul aussi) ferme le réseau local même quand le
// fichier porte le droit.
func TestRun_ProbesFollowTheLANRight(t *testing.T) {
	cases := []struct {
		name     string
		rights   string
		narrowed bool
		status   probe.Status
		reason   probe.Reason
	}{
		{"agent enrolled before the right", "observe", false, probe.StatusDown, probe.ReasonLAN},
		{"another right is not this one", "observe,agent.update", false, probe.StatusDown, probe.ReasonLAN},
		{"with probe.lan", "observe,probe.lan", false, probe.StatusUp, probe.ReasonNone},
		{"right on disk but state unsafe", "observe,probe.lan", true, probe.StatusDown, probe.ReasonLAN},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := probeOnLoopback(t, tc.rights, tc.narrowed)
			if got.Status != tc.status || got.LastReason != tc.reason {
				t.Fatalf("status %s reason %q, want %s %q", got.Status, got.LastReason, tc.status, tc.reason)
			}
		})
	}
}
