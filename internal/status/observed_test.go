package status_test

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/status"
)

// Un service dont la machine ne signale plus depuis plus de deux minutes est
// une panne qui compte, même si son dernier état connu est « running » : la
// page publique ne dit pas « tout va bien » d'une VM tombée. Un signal frais
// la rétablit, et la borne est celle de machine.LostAfter.
func TestDeclaredServiceState_MachineThatStoppedSignalingIsDown(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	declared := f.service(t, "nextcloud", container.StateRunning, 0)
	component := f.component(t, "Nuage", status.MemberRef{Kind: status.KindService, ID: declared})

	for _, step := range []struct {
		after time.Duration
		want  status.State
	}{
		{0, status.StateOperational},
		{machine.LostAfter, status.StateOperational},
		{time.Second, status.StateDown},
		{5 * time.Hour, status.StateDown},
	} {
		f.now = f.now.Add(step.after)
		got, err := f.status.GetComponent(ctx, component.ID)
		if err != nil || got.Effective != step.want || !got.Members[0].Counts {
			t.Fatalf("à +%s : effective %q, compte %v, %v ; attendu %q qui compte", step.after, got.Effective, got.Members[0].Counts, err, step.want)
		}
	}

	f.seen(t, machine.LocalID)
	back, err := f.status.GetComponent(ctx, component.ID)
	if err != nil || back.Effective != status.StateOperational {
		t.Fatalf("après un signal : %q, %v", back.Effective, err)
	}
}

// Un composant dont une sonde en ligne est membre et dont le service est sur
// une machine muette prend le pire des deux, comme avant.
func TestDeclaredServiceState_ProbeMemberDoesNotHideAMuteMachine(t *testing.T) {
	f := newFixture(t)
	declared := f.service(t, "nextcloud", container.StateRunning, 0)
	up := f.probe(t, "site", probe.OutcomeUp, nil)
	component := f.component(t, "Nuage", status.MemberRef{Kind: status.KindService, ID: declared}, status.MemberRef{Kind: status.KindProbe, ID: up.ID})
	f.now = f.now.Add(10 * time.Minute)
	got, err := f.status.GetComponent(context.Background(), component.ID)
	if err != nil || got.Derived != status.StateDown {
		t.Fatalf("derived %q, %v ; attendu down", got.Derived, err)
	}
	if got.Members[1].State != status.StateOperational {
		t.Fatalf("la sonde, elle, reste %q", got.Members[1].State)
	}
}
