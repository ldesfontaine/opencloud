package link_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/link"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

var testNow = time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)

type bench struct {
	db      *store.DB
	service *link.Service
	now     time.Time
	changes int
}

func (b *bench) DirectoryChanged() { b.changes++ }

// Le composant se teste sur la vraie base SQLite, avec deux machines et une
// horloge qu'on avance à la main.
func newBench(t *testing.T) *bench {
	t.Helper()
	db := storetest.Open(t)
	machines := machine.New(db, machine.NewSessions(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	storetest.EnrollMachine(t, machines, "11111111-2222-4333-8444-000000000001", "m1")
	storetest.EnrollMachine(t, machines, "11111111-2222-4333-8444-000000000002", "m2")
	b := &bench{db: db, now: testNow}
	b.service = link.New(db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	b.service.SetClock(func() time.Time { return b.now })
	b.service.SetWatcher(b)
	return b
}

const (
	m1 = "11111111-2222-4333-8444-000000000001"
	m2 = "11111111-2222-4333-8444-000000000002"
)

func endpoint(machineID, name string) link.Endpoint {
	return link.Endpoint{MachineID: machineID, Key: link.ContainerKey(name)}
}

func TestDeclare_WritesADeclaredOperatorLinkAndRefusesTheRest(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	written, err := b.service.Declare(ctx, link.Definition{From: endpoint(m1, "web"), To: endpoint(m2, "db"), Port: 5432})
	if err != nil {
		t.Fatal(err)
	}
	if written.Proof != link.ProofDeclared || written.Signal != link.SignalOperator || written.Nature != link.NatureConfig || written.Port != 5432 || !written.FirstSeen.Equal(testNow) {
		t.Fatalf("lien = %+v", written)
	}
	if b.changes != 1 {
		t.Errorf("%d changement(s) annoncé(s), 1 attendu", b.changes)
	}
	for name, tc := range map[string]struct {
		definition link.Definition
		want       error
	}{
		"doublon":          {link.Definition{From: endpoint(m1, "web"), To: endpoint(m2, "db")}, link.ErrDuplicate},
		"machine inconnue": {link.Definition{From: endpoint(m1, "web"), To: endpoint("ghost", "db")}, link.ErrMachine},
		"soi-même":         {link.Definition{From: endpoint(m1, "web"), To: endpoint(m1, "web")}, link.ErrSelf},
	} {
		if _, err := b.service.Declare(ctx, tc.definition); !errors.Is(err, tc.want) {
			t.Errorf("%s : %v, attendu %v", name, err, tc.want)
		}
	}
	if err := b.service.Remove(ctx, written.ID); err != nil {
		t.Fatal(err)
	}
	if err := b.service.Remove(ctx, written.ID); !errors.Is(err, link.ErrNotFound) {
		t.Errorf("retrait d'un lien retiré : %v", err)
	}
}

// Un lien tiré d'une configuration se revoit au rapport suivant : le retirer
// ici n'aurait aucun sens, et le dire vaut mieux qu'un lien qui revient.
func TestRecordOrders_RefreshesWhatIsKnownKeepsWhatIsGoneAndIsNotRemovable(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	orders := []link.Order{{From: link.ContainerKey("web"), To: link.ContainerKey("db"), Signal: link.SignalDependsOn}}
	if err := b.service.RecordOrders(ctx, m1, orders); err != nil {
		t.Fatal(err)
	}
	first, err := b.service.List(ctx, m1)
	if err != nil || len(first) != 1 {
		t.Fatalf("liens = %+v, %v", first, err)
	}
	if first[0].Nature != link.NatureOrder || first[0].Proof != link.ProofDeclared || first[0].Signal != link.SignalDependsOn {
		t.Fatalf("lien = %+v", first[0])
	}

	// Revu plus tard : même ligne, dernière vue avancée, première vue gardée.
	b.now = testNow.Add(48 * time.Hour)
	if err := b.service.RecordOrders(ctx, m1, orders); err != nil {
		t.Fatal(err)
	}
	again, _ := b.service.List(ctx, m1)
	if len(again) != 1 || again[0].ID != first[0].ID || !again[0].FirstSeen.Equal(testNow) || !again[0].LastSeen.Equal(b.now) {
		t.Fatalf("après revue : %+v", again)
	}

	// Le conteneur disparaît du rapport : le lien vieillit, il ne part pas.
	other := []link.Order{{From: link.ContainerKey("job"), To: link.ContainerKey("db"), Signal: link.SignalContainerLink}}
	if err := b.service.RecordOrders(ctx, m1, other); err != nil {
		t.Fatal(err)
	}
	if err := b.service.RecordOrders(ctx, m1, nil); err != nil {
		t.Fatal(err)
	}
	all, _ := b.service.List(ctx, m1)
	if len(all) != 2 {
		t.Fatalf("un lien non revu doit rester : %+v", all)
	}
	if err := b.service.Remove(ctx, all[0].ID); !errors.Is(err, link.ErrNotRemovable) {
		t.Errorf("retrait d'un lien d'outil : %v", err)
	}
	later := b.now.Add(link.StaleAfter + time.Hour)
	if !all[0].Stale(later) || all[1].Stale(b.now) {
		t.Errorf("fraîcheur : %v %v", all[0].LastSeen, all[1].LastSeen)
	}
}

func TestList_FiltersByMachine(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	if _, err := b.service.Declare(ctx, link.Definition{From: endpoint(m1, "web"), To: endpoint(m2, "db")}); err != nil {
		t.Fatal(err)
	}
	if err := b.service.RecordOrders(ctx, m2, []link.Order{{From: link.ContainerKey("a"), To: link.ContainerKey("b"), Signal: link.SignalDependsOn}}); err != nil {
		t.Fatal(err)
	}
	for machineID, want := range map[string]int{m1: 1, m2: 2, "": 2, "ghost": 0} {
		if got, _ := b.service.List(ctx, machineID); len(got) != want {
			t.Errorf("liens de %q : %d, attendu %d", machineID, len(got), want)
		}
	}
}
