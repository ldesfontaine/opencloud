package probe

import (
	"testing"
	"time"
)

var planNow = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

func watched() Probe {
	return Probe{
		ID: "p1", Name: "site", Kind: KindHTTP, Target: "https://a.fr/", MachineID: "m1",
		Status: StatusNew, Interval: time.Minute, Timeout: 10 * time.Second,
		FailureThreshold: 3, RecoveryThreshold: 2,
	}
}

// apply rejoue une suite d'essais sur une sonde, comme un signal le ferait.
func apply(current Probe, outcomes ...Outcome) Probe {
	for index, outcome := range outcomes {
		known := map[string]Probe{current.ID: current}
		result := Result{ProbeID: current.ID, CheckedAt: planNow.Add(time.Duration(index) * time.Minute), Outcome: outcome}
		changes := planChanges(known, Report{Results: []Result{result}}, planNow)
		if len(changes.Probes) == 0 {
			continue
		}
		current = changes.Probes[0]
	}
	return current
}

// Une sonde qui répond ne reste pas « Nouveau » le temps d'un seuil :
// le premier essai tranche, comme le premier ping d'une tâche.
func TestPlan_FirstResultSetsTheStatusAtOnce(t *testing.T) {
	if got := apply(watched(), OutcomeUp).Status; got != StatusUp {
		t.Fatalf("attendu %q, obtenu %q", StatusUp, got)
	}
	if got := apply(watched(), OutcomeDown).Status; got != StatusDown {
		t.Fatalf("attendu %q, obtenu %q", StatusDown, got)
	}
}

// Le seuil est entre le résultat et l'état : un hoquet ne fait pas
// clignoter la page.
func TestPlan_StatusFallsOnlyAtTheFailureThreshold(t *testing.T) {
	online := apply(watched(), OutcomeUp)
	after := apply(online, OutcomeDown)
	if after.Status != StatusUp || after.ConsecutiveFailures != 1 {
		t.Fatalf("un seul échec a fait basculer: %+v", after)
	}
	after = apply(after, OutcomeDown)
	if after.Status != StatusUp || after.ConsecutiveFailures != 2 {
		t.Fatalf("deux échecs ont fait basculer avec un seuil de trois: %+v", after)
	}
	after = apply(after, OutcomeDown)
	if after.Status != StatusDown || after.ConsecutiveFailures != 3 {
		t.Fatalf("le seuil atteint n'a pas fait basculer: %+v", after)
	}
}

// Et la cible qui revient doit le confirmer.
func TestPlan_StatusRisesOnlyAtTheRecoveryThreshold(t *testing.T) {
	offline := apply(watched(), OutcomeDown, OutcomeDown, OutcomeDown)
	after := apply(offline, OutcomeUp)
	if after.Status != StatusDown || after.ConsecutiveSuccesses != 1 {
		t.Fatalf("un seul succès a ramené en ligne: %+v", after)
	}
	after = apply(after, OutcomeUp)
	if after.Status != StatusUp {
		t.Fatalf("le seuil de retour atteint n'a pas ramené en ligne: %+v", after)
	}
}

// Dégradé et En ligne sont deux nuances d'un même succès : passer de
// l'une à l'autre n'attend aucun seuil.
func TestPlan_DegradedSwitchesWithoutWaitingAThreshold(t *testing.T) {
	online := apply(watched(), OutcomeUp)
	degraded := apply(online, OutcomeDegraded)
	if degraded.Status != StatusDegraded {
		t.Fatalf("attendu %q, obtenu %q", StatusDegraded, degraded.Status)
	}
	if degraded.ConsecutiveFailures != 0 {
		t.Fatalf("un essai dégradé a compté comme un échec: %+v", degraded)
	}
	if back := apply(degraded, OutcomeUp); back.Status != StatusUp {
		t.Fatalf("attendu %q, obtenu %q", StatusUp, back.Status)
	}
}

// Un essai rejoué est de l'histoire : il s'écrit, et rien d'autre.
func TestPlan_ReplayedResultWritesHistoryOnly(t *testing.T) {
	online := apply(watched(), OutcomeUp)
	known := map[string]Probe{online.ID: online}
	replayed := Result{ProbeID: online.ID, CheckedAt: planNow, Outcome: OutcomeDown, Replayed: true}
	changes := planChanges(known, Report{Results: []Result{replayed}}, planNow)
	if len(changes.Results) != 1 {
		t.Fatalf("l'essai rejoué n'est pas écrit: %+v", changes)
	}
	if len(changes.Probes) != 0 {
		t.Fatalf("l'essai rejoué a bougé l'état: %+v", changes.Probes)
	}
}

// Une sonde en pause ne bouge pas, même si un essai parti avant la pause
// arrive après.
func TestPlan_PausedProbeKeepsItsStatus(t *testing.T) {
	paused := watched()
	paused.Status = StatusPaused
	after := apply(paused, OutcomeDown, OutcomeDown, OutcomeDown)
	if after.Status != StatusPaused {
		t.Fatalf("attendu %q, obtenu %q", StatusPaused, after.Status)
	}
}

// Une sonde que la machine ne porte plus est ignorée, ses essais avec.
func TestPlan_UnknownProbeIsDropped(t *testing.T) {
	changes := planChanges(map[string]Probe{}, Report{Results: []Result{{ProbeID: "disparue", CheckedAt: planNow, Outcome: OutcomeUp}}}, planNow)
	if len(changes.Results) != 0 || len(changes.Probes) != 0 {
		t.Fatalf("une sonde inconnue a été écrite: %+v", changes)
	}
}

// Une sonde touchée plusieurs fois par le même signal n'est écrite qu'une
// fois, dans l'état où le dernier essai l'a laissée.
func TestPlan_SameProbeTwiceIsWrittenOnce(t *testing.T) {
	online := apply(watched(), OutcomeUp)
	known := map[string]Probe{online.ID: online}
	report := Report{Results: []Result{
		{ProbeID: online.ID, CheckedAt: planNow, Outcome: OutcomeDown},
		{ProbeID: online.ID, CheckedAt: planNow.Add(time.Minute), Outcome: OutcomeDown},
	}}
	changes := planChanges(known, report, planNow)
	if len(changes.Results) != 2 {
		t.Fatalf("les deux essais devaient s'écrire: %+v", changes.Results)
	}
	if len(changes.Probes) != 1 || changes.Probes[0].ConsecutiveFailures != 2 {
		t.Fatalf("la sonde n'est pas écrite une fois dans son dernier état: %+v", changes.Probes)
	}
}

func TestPlan_ResultCarriesTheCertificateOntoTheProbe(t *testing.T) {
	known := map[string]Probe{"p1": watched()}
	certificate := &Certificate{Subject: "cloud.exemple.fr", Issuer: "openCloud", NotBefore: planNow, NotAfter: planNow.Add(90 * 24 * time.Hour)}
	report := Report{Results: []Result{{ProbeID: "p1", CheckedAt: planNow, Outcome: OutcomeUp, Certificate: certificate}}}
	changes := planChanges(known, report, planNow)
	if changes.Probes[0].Certificate == nil || changes.Probes[0].Certificate.Subject != "cloud.exemple.fr" {
		t.Fatalf("le certificat vu n'est pas porté sur la sonde: %+v", changes.Probes[0].Certificate)
	}
}
