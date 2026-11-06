package agent

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/hostinfo"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/wait/waittest"
)

func aHostReport(virt string) hostinfo.Report {
	return hostinfo.Report{
		Virt:       virt,
		Interfaces: []hostinfo.Interface{{Name: "eth0", MAC: "bc:24:11:aa:bb:cc"}},
		Storage:    hostinfo.Storage{Disks: []hostinfo.Disk{{Name: "sda", Size: 1 << 30, Media: hostinfo.MediaSSD}}, Pools: []hostinfo.Pool{}},
	}
}

func TestHostReports_AnUnchangedReportIsSentOnceAndAgainOnANewStream(t *testing.T) {
	reports := &hostReports{}
	if !reports.update(aHostReport("kvm")) || reports.take() == nil {
		t.Fatal("le premier rapport doit partir")
	}
	if reports.update(aHostReport("kvm")) || reports.take() != nil {
		t.Fatal("un rapport identique ne repart pas")
	}
	reports.markReplayed()
	if reports.take() == nil {
		t.Fatal("un flux neuf redit le rapport")
	}
	if !reports.update(aHostReport("none")) || reports.take() == nil {
		t.Fatal("un type changé repart")
	}
}

func TestHostReports_WhatASignalCouldNotCarryIsSentAgain(t *testing.T) {
	reports := &hostReports{}
	reports.update(aHostReport("kvm"))
	taken := reports.take()
	reports.restore(taken)
	if reports.take() == nil {
		t.Fatal("un rapport non livré doit repartir")
	}
}

func TestHostLoop_ReadsAtStartAndEveryPeriodAndStopsWithTheAgent(t *testing.T) {
	reports := &hostReports{}
	var reads atomic.Int32
	read := func(context.Context) hostinfo.Report {
		reads.Add(1)
		return aHostReport("kvm")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		hostLoop(ctx, read, reports, 5*time.Millisecond, slog.New(slog.DiscardHandler))
		close(done)
	}()
	waittest.Until(t, "plusieurs lectures", func() bool { return reads.Load() >= 3 })
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("la boucle survit à l'agent")
	}
	if reports.take() == nil || reports.take() != nil {
		t.Fatal("des lectures identiques ne repartent pas")
	}
}

// De bout en bout : l'agent dit où il tourne et ce qu'il a comme disques, le
// serveur le garde. Aucun droit n'est demandé pour cela.
func TestRun_ReportsItsHostToTheServer(t *testing.T) {
	b := newBench(t)
	token, _, err := b.machines.CreateToken(context.Background(), "web-1", machine.RoleGeneric, nil)
	if err != nil {
		t.Fatal(err)
	}
	options := b.options(token)
	options.readHost = func(context.Context) hostinfo.Report { return aHostReport("kvm") }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, options) }()
	online := b.waitFor(t, "web-1", func(s machine.Status) bool { return s.Online })

	waittest.Until(t, "le type arrive au serveur", func() bool {
		stored, err := b.machines.Get(context.Background(), online.ID)
		return err == nil && stored.Kind == machine.KindVM && len(stored.MACs) == 1
	})
	storage, err := b.db.MachineStorage(context.Background(), online.ID)
	if err != nil || len(storage.Disks) != 1 || storage.Disks[0].Name != "sda" {
		t.Fatalf("stockage : %+v %v", storage, err)
	}
	cancel()
	<-done
}
