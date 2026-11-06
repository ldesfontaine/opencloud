package agent

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/systemdunits"
	"github.com/ldesfontaine/opencloud/internal/wait/waittest"
)

func aUnitReport(active systemdunits.Active) systemdunits.Report {
	return systemdunits.Report{Available: true, Units: []systemdunits.Unit{{Name: "forgejo.service", Load: systemdunits.LoadLoaded, Active: active, Sub: systemdunits.SubRunning}}}
}

func TestUnitReports_AnUnchangedReportIsSentOnceAndAgainOnANewStream(t *testing.T) {
	reports := &unitReports{}
	if !reports.update(aUnitReport(systemdunits.ActiveActive)) || reports.take() == nil {
		t.Fatal("le premier relevé doit partir")
	}
	if reports.update(aUnitReport(systemdunits.ActiveActive)) || reports.take() != nil {
		t.Fatal("un relevé identique ne repart pas")
	}
	reports.markReplayed()
	if reports.take() == nil {
		t.Fatal("un flux neuf redit le relevé")
	}
	if !reports.update(aUnitReport(systemdunits.ActiveFailed)) || reports.take() == nil {
		t.Fatal("une unité tombée repart")
	}
}

func TestUnitReports_WhatASignalCouldNotCarryIsSentAgain(t *testing.T) {
	reports := &unitReports{}
	reports.update(aUnitReport(systemdunits.ActiveActive))
	reports.restore(reports.take())
	if reports.take() == nil {
		t.Fatal("un relevé non livré doit repartir")
	}
}

// Le journal de l'agent ne dit que des comptes : ni nom d'unité ni état.
func TestUnitsLoop_ReadsEveryPeriodAndLogsOnlyCounts(t *testing.T) {
	reports := &unitReports{}
	var reads atomic.Int32
	read := func(context.Context) systemdunits.Report {
		reads.Add(1)
		return aUnitReport(systemdunits.ActiveActive)
	}
	var journal bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		unitsLoop(ctx, read, reports, 5*time.Millisecond, slog.New(slog.NewTextHandler(&journal, nil)))
		close(done)
	}()
	waittest.Until(t, "plusieurs lectures", func() bool { return reads.Load() >= 3 })
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("la boucle survit à l'agent")
	}
	if strings.Contains(journal.String(), "forgejo") {
		t.Errorf("le journal nomme une unité : %s", journal.String())
	}
	if reports.take() == nil || reports.take() != nil {
		t.Fatal("des lectures identiques ne repartent pas")
	}
}
