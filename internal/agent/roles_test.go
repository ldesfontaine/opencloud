package agent

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/role"
)

func proxmoxReport(version string) role.Report {
	return role.Report{Roles: []role.Role{{Name: role.ProxmoxVE, Version: version}}}
}

func TestRoleReports_AnUnchangedReportIsSentOnceAndAgainOnANewStream(t *testing.T) {
	reports := &roleReports{}
	if !reports.update(proxmoxReport("8.2.4")) || reports.take() == nil {
		t.Fatal("le premier rapport doit partir")
	}
	if reports.update(proxmoxReport("8.2.4")) || reports.take() != nil {
		t.Fatal("un rapport identique ne repart pas")
	}
	reports.markReplayed()
	if reports.take() == nil {
		t.Fatal("un flux neuf redit le rapport")
	}
	if !reports.update(proxmoxReport("8.3.0")) || reports.take() == nil {
		t.Fatal("une version changée repart")
	}
}

// Un rapport vide part aussi : c'est lui qui efface un rôle perdu.
func TestRoleReports_AnEmptyReportAfterARoleIsSent(t *testing.T) {
	reports := &roleReports{}
	reports.update(proxmoxReport("8.2.4"))
	reports.take()
	if !reports.update(role.Report{Roles: []role.Role{}}) {
		t.Fatal("la perte du rôle est un changement")
	}
	if taken := reports.take(); taken == nil || len(taken.Roles) != 0 {
		t.Fatalf("rapport : %+v", taken)
	}
}

func TestRoleReports_WhatASignalCouldNotCarryIsSentAgain(t *testing.T) {
	reports := &roleReports{}
	reports.update(proxmoxReport("8.2.4"))
	taken := reports.take()
	reports.restore(taken)
	if reports.take() == nil {
		t.Fatal("un rapport non livré doit repartir")
	}
}

func TestRolesLoop_ReadsAtStartAndEveryPeriodAndStopsWithTheAgent(t *testing.T) {
	reports := &roleReports{}
	var reads atomic.Int32
	detect := func() role.Report {
		reads.Add(1)
		return proxmoxReport("8.2.4")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		rolesLoop(ctx, detect, reports, 5*time.Millisecond, slog.New(slog.DiscardHandler))
		close(done)
	}()
	waitUntil(t, "plusieurs lectures", func() bool { return reads.Load() >= 3 })
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

// De bout en bout : l'agent reconnaît Proxmox et le serveur le garde. Aucun
// droit n'est demandé pour cela.
func TestRun_ReportsItsRolesToTheServer(t *testing.T) {
	b := newBench(t)
	token, _, err := b.machines.CreateToken(context.Background(), "pve-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	options := b.options(token)
	options.detectRoles = func() role.Report { return proxmoxReport("8.2.4") }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, options) }()
	online := b.waitFor(t, "pve-1", func(s machine.Status) bool { return s.Online })

	storedRoles := func() []role.Role {
		byMachine, err := b.db.MachineRoles(context.Background())
		if err != nil {
			t.Error(err)
		}
		return byMachine[online.ID]
	}
	waitUntil(t, "le rôle arrive au serveur", func() bool { return len(storedRoles()) == 1 })
	if got := storedRoles()[0]; got.Name != role.ProxmoxVE || got.Version != "8.2.4" {
		t.Fatalf("rôle : %+v", got)
	}
	cancel()
	<-done
}
