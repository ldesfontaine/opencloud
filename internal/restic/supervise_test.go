package restic

import (
	"slices"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/backup"
)

var now = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

func hoursAgo(hours int) time.Time { return now.Add(-time.Duration(hours) * time.Hour) }

func resticProtectionOf(id string, lastSuccess time.Time, verify *VerifyView) Protection {
	return Protection{
		Origin: OriginRestic, ID: id, Name: "nextcloud", TargetMachineID: "nas", State: ProtectionOK,
		LastSuccessAt: lastSuccess, CreatedAt: hoursAgo(24 * 90), Verify: verify,
	}
}

func kindsOf(findings []Finding) []FindingKind {
	kinds := []FindingKind{}
	for _, finding := range findings {
		kinds = append(kinds, finding.Kind)
	}
	return kinds
}

func serviceSource(protections ...Protection) Source {
	return Source{Key: "service:s1", Kind: agentwire.ResticScopeService, Name: "nextcloud", MachineID: "vps", ServiceID: "s1", Protections: protections}
}

func TestFindingsOf_NothingToReportWhenEverythingIsFresh(t *testing.T) {
	verify := &VerifyView{State: StateSucceeded, SuccessAt: hoursAgo(24), Right: RightFit{State: RightOK}}
	overview := Overview{Sources: []Source{serviceSource(resticProtectionOf("b1", hoursAgo(10), verify))}}
	if findings := FindingsOf(overview, now); len(findings) != 0 {
		t.Fatalf("findings %+v", findings)
	}
}

func TestFindingsOf_NoSuccessSinceThirtySixHours(t *testing.T) {
	overview := Overview{Sources: []Source{serviceSource(resticProtectionOf("b1", hoursAgo(40), nil))}}
	findings := FindingsOf(overview, now)
	if len(findings) != 1 || findings[0].Kind != FindingStale || findings[0].Danger || findings[0].ObjectID != "restic:b1" ||
		findings[0].MachineID != "vps" || findings[0].ServiceID != "s1" || !findings[0].Since.Equal(hoursAgo(40)) {
		t.Fatalf("findings %+v", findings)
	}
	overview.Sources[0].Protections[0].LastSuccessAt = hoursAgo(24 * 8)
	if findings := FindingsOf(overview, now); !findings[0].Danger {
		t.Error("a week without success is a danger")
	}
}

func TestFindingsOf_AFailedLastAttemptIsSaidByItsState(t *testing.T) {
	failed := resticProtectionOf("b1", hoursAgo(40), nil)
	failed.State, failed.Reason = ProtectionFailed, "restic_failed"
	findings := FindingsOf(Overview{Sources: []Source{serviceSource(failed)}}, now)
	if findings[0].Reason != ProtectionFailed {
		t.Errorf("reason %q: the alert says failed, the card says why", findings[0].Reason)
	}
}

func TestFindingsOf_ABackupNeverSucceededWaitsFromItsCreation(t *testing.T) {
	young := resticProtectionOf("b1", time.Time{}, nil)
	young.CreatedAt = hoursAgo(3)
	if findings := FindingsOf(Overview{Sources: []Source{serviceSource(young)}}, now); len(findings) != 0 {
		t.Fatalf("a backup created three hours ago: %+v", findings)
	}
	old := resticProtectionOf("b1", time.Time{}, nil)
	old.CreatedAt = hoursAgo(50)
	if findings := FindingsOf(Overview{Sources: []Source{serviceSource(old)}}, now); len(findings) != 1 || findings[0].Kind != FindingStale {
		t.Fatalf("findings %+v", findings)
	}
}

func TestFindingsOf_ObservedProxmoxJobsAreNotJudged(t *testing.T) {
	observed := Protection{Origin: OriginObserved, ID: "nuit-pi", LastSuccessAt: hoursAgo(24 * 20)}
	source := Source{Key: "vm:1", Kind: backup.KindVM, Name: "pi", MachineID: "pve", Protections: []Protection{observed}}
	if findings := FindingsOf(Overview{Sources: []Source{source}}, now); len(findings) != 0 {
		t.Fatalf("an observed job is not ours to judge: %+v", findings)
	}
}

func TestFindingsOf_RestoreTestOutOfDate(t *testing.T) {
	stale := func(view VerifyView) []Finding {
		view.IsStale, view.Since = true, hoursAgo(24*90)
		return FindingsOf(Overview{Sources: []Source{serviceSource(resticProtectionOf("b1", hoursAgo(1), &view))}}, now)
	}
	cases := map[string]struct {
		view   VerifyView
		reason string
		danger bool
	}{
		"never":         {VerifyView{}, ReasonNeverVerified, true},
		"old success":   {VerifyView{State: StateSucceeded, SuccessAt: hoursAgo(24 * 20), Right: RightFit{State: RightOK}}, "", false},
		"right missing": {VerifyView{SuccessAt: hoursAgo(24 * 20), Right: RightFit{State: RightMissing}}, ReasonVerifyRightMissing, false},
		"unavailable":   {VerifyView{SuccessAt: hoursAgo(24 * 20), Right: RightFit{State: RightUnavailable}}, ReasonVerifyRightUnavailable, false},
		"last failed":   {VerifyView{State: StateFailed, Reason: "check_failed", SuccessAt: hoursAgo(24 * 20), Right: RightFit{State: RightOK}}, "check_failed", false},
		"a month":       {VerifyView{State: StateSucceeded, SuccessAt: hoursAgo(24 * 31), Right: RightFit{State: RightOK}}, "", true},
	}
	for name, want := range cases {
		findings := stale(want.view)
		if len(findings) != 1 || findings[0].Kind != FindingRestoreStale || findings[0].Reason != want.reason {
			t.Errorf("%s: %+v", name, findings)
			continue
		}
		// « never » est jugé depuis la création, 90 jours : danger ; les autres, selon l'âge.
		if findings[0].Danger != want.danger {
			t.Errorf("%s: danger %v", name, findings[0].Danger)
		}
	}
}

// Le test est celui du dépôt : deux sauvegardes d'une même source vers une même
// cible n'ouvrent qu'une alerte, nommée par les deux machines.
func TestFindingsOf_TheRestoreTestIsOneFindingPerRepository(t *testing.T) {
	view := &VerifyView{IsStale: true, Since: hoursAgo(24 * 20), Right: RightFit{State: RightOK}}
	source := Source{
		Key: "service:s1", Kind: agentwire.ResticScopeService, Name: "nextcloud", MachineID: "vps", MachineName: "vps", ServiceID: "s1",
		Protections: []Protection{
			resticProtectionOf("b1", hoursAgo(1), view),
			resticProtectionOf("b2", hoursAgo(1), view),
		},
	}
	source.Protections[0].TargetMachineName, source.Protections[1].TargetMachineName = "nas", "nas"
	findings := FindingsOf(Overview{Sources: []Source{source}}, now)
	if len(findings) != 1 || findings[0].ObjectID != "repo:vps>nas" || findings[0].ObjectName != "vps → nas" || !findings[0].Since.Equal(hoursAgo(24*20)) {
		t.Fatalf("findings %+v", findings)
	}
}

func TestFindingsOf_SourceWithoutAPlan(t *testing.T) {
	protected := serviceSource(resticProtectionOf("b1", hoursAgo(1), nil))
	bare := []Source{
		{Key: "database", Kind: backup.KindDatabase, Name: "opencloud", MachineID: "local"},
		{Key: "firewall:f1", Kind: backup.KindFirewall, Name: "opnsense", MachineID: "local"},
		{Key: "vm:100", Kind: backup.KindVM, Name: "docker1", MachineID: "pve"},
		{Key: "service:s2", Kind: agentwire.ResticScopeService, Name: "paperless", MachineID: "vps", ServiceID: "s2"},
		{Key: "vm:101", Kind: backup.KindVM, Name: "inconnue", MachineID: "pve2", ObservedUnknown: true},
		{Key: "volume:x", Kind: backup.KindVolume, Name: "cache", MachineID: "vps"},
	}
	findings := FindingsOf(Overview{Sources: append([]Source{protected}, bare...)}, now)
	var got []string
	for _, finding := range findings {
		if finding.Kind != FindingSourceUnprotected {
			t.Fatalf("finding %+v", finding)
		}
		got = append(got, finding.ObjectID)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"source:database", "source:firewall:f1", "source:service:s2", "source:vm:100"}) {
		t.Errorf("sources without a plan: %v (a volume belongs to its service, an unknown host is not « none »)", got)
	}
}

func TestFindingsOf_NothingIsMissingBeforeTheModuleIsInUse(t *testing.T) {
	overview := Overview{Sources: []Source{{Key: "database", Kind: backup.KindDatabase, Name: "opencloud", MachineID: "local"}}}
	if findings := FindingsOf(overview, now); len(findings) != 0 {
		t.Fatalf("a fresh install must not ring: %+v", findings)
	}
}

func TestFindingsOf_TargetUnreachableOrFull(t *testing.T) {
	added := int64(2 << 30)
	kept := serviceSource(Protection{Origin: OriginRestic, ID: "b1", Name: "nextcloud", TargetMachineID: "nas", LastSuccessAt: hoursAgo(1), CreatedAt: hoursAgo(99), AddedBytes: &added})
	target := func(change func(*TargetView)) []Finding {
		view := TargetView{Kind: TargetKindRestic, Name: "nas", MachineID: "nas", Online: true, State: agentwire.ResticTargetRunning, DiskTotal: 1 << 40, DiskFree: 500 << 30}
		change(&view)
		return FindingsOf(Overview{Sources: []Source{kept}, Targets: []TargetView{view}}, now)
	}
	if got := target(func(*TargetView) {}); len(got) != 0 {
		t.Fatalf("a healthy target: %+v", got)
	}
	for reason, change := range map[string]func(*TargetView){
		ReasonTargetOffline: func(v *TargetView) { v.Online = false },
		ReasonTargetStopped: func(v *TargetView) { v.State = agentwire.ResticTargetStopped },
		ReasonTargetError:   func(v *TargetView) { v.State = agentwire.ResticTargetError },
	} {
		got := target(change)
		if len(got) != 1 || got[0].Kind != FindingTargetUnreachable || got[0].Reason != reason || !got[0].Danger || got[0].Count != 1 || got[0].MachineID != "nas" {
			t.Errorf("%s: %+v", reason, got)
		}
	}
	// Pas la place du prochain ajout (deux fois le plus gros : 4 Gio).
	got := target(func(v *TargetView) { v.DiskFree = 3 << 30 })
	if len(got) != 1 || got[0].Kind != FindingTargetFull || got[0].Danger || got[0].Bytes != 3<<30 {
		t.Errorf("tight target: %+v", got)
	}
	got = target(func(v *TargetView) { v.DiskFree = 1 << 30 })
	if len(got) != 1 || got[0].Kind != FindingTargetFull || !got[0].Danger {
		t.Errorf("target that cannot take the next backup: %+v", got)
	}
}

func TestFindingsOf_ATargetThatKeepsNothingIsNotWatched(t *testing.T) {
	protected := serviceSource(resticProtectionOf("b1", hoursAgo(1), nil))
	empty := TargetView{Kind: TargetKindRestic, Name: "vide", MachineID: "vide", Online: false, State: agentwire.ResticTargetStopped}
	if findings := FindingsOf(Overview{Sources: []Source{protected}, Targets: []TargetView{empty}}, now); len(findings) != 0 {
		t.Fatalf("findings %v", kindsOf(findings))
	}
}

func TestVerifyDue(t *testing.T) {
	cases := map[string]struct {
		verify VerifyOutcome
		due    bool
	}{
		"never":                 {VerifyOutcome{}, true},
		"succeeded yesterday":   {VerifyOutcome{State: StateSucceeded, At: hoursAgo(24)}, false},
		"succeeded a week ago":  {VerifyOutcome{State: StateSucceeded, At: hoursAgo(24*7 + 1)}, true},
		"failed this morning":   {VerifyOutcome{State: StateFailed, At: hoursAgo(10)}, false},
		"failed yesterday":      {VerifyOutcome{State: StateFailed, At: hoursAgo(21)}, true},
		"sent a moment ago":     {VerifyOutcome{State: StateSent, At: hoursAgo(0)}, false},
		"sent and never closed": {VerifyOutcome{State: StateSent, At: hoursAgo(5)}, true},
	}
	for name, want := range cases {
		if got := verifyDue(now, want.verify); got != want.due {
			t.Errorf("%s: due %v, want %v", name, got, want.due)
		}
	}
}
