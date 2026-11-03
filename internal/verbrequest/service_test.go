package verbrequest_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/apitoken"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
	"github.com/ldesfontaine/opencloud/internal/verbrequest"
)

var start = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

type counter struct{ changes atomic.Int32 }

func (c *counter) VerbRequestsChanged() { c.changes.Add(1) }

type bench struct {
	*verbrequest.Service
	now     *time.Time
	changes *counter
	tokenID string
}

func newBench(t *testing.T) bench {
	t.Helper()
	db := storetest.Open(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, token, err := apitoken.New(db, logger).Create(context.Background(), apitoken.Request{Name: "agent", Scope: apitoken.ScopeWrite})
	if err != nil {
		t.Fatal(err)
	}
	now := start
	service := verbrequest.New(db, logger)
	service.SetClock(func() time.Time { return now })
	changes := &counter{}
	service.SetWatcher(changes)
	return bench{Service: service, now: &now, changes: changes, tokenID: token.ID}
}

func (b bench) submission(machineID string) verbrequest.Submission {
	return verbrequest.Submission{
		Verb: "machine.delete", ObjectName: machineID, Params: map[string]string{"id": machineID},
		TokenID: b.tokenID, TokenName: "agent", OnBehalfOf: "alice",
	}
}

// Redemander la même chose pendant qu'elle attend rend la même demande, sans
// rien ajouter ni annoncer de nouveau.
func TestSubmit_AnIdenticalPendingRequestIsReturnedNotDuplicated(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	first, err := b.Submit(ctx, b.submission("m1"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.Submit(ctx, b.submission("m1"))
	if err != nil || second.ID != first.ID {
		t.Fatalf("second : %+v %v, attendu %s", second, err, first.ID)
	}
	pending, _ := b.ListPending(ctx)
	if len(pending) != 1 || b.changes.changes.Load() != 1 {
		t.Fatalf("%d demandes, %d annonces", len(pending), b.changes.changes.Load())
	}
	if first.ExpiresAt.Sub(first.CreatedAt) != verbrequest.TTL {
		t.Fatalf("délai : %v", first.ExpiresAt.Sub(first.CreatedAt))
	}
}

// Un agent qui boucle sur des machines différentes est borné.
func TestSubmit_ATokenCannotFloodTheOperator(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	for i := range verbrequest.MaxPendingPerToken {
		if _, err := b.Submit(ctx, b.submission(fmt.Sprintf("m%d", i))); err != nil {
			t.Fatalf("demande %d : %v", i, err)
		}
	}
	if _, err := b.Submit(ctx, b.submission("une-de-trop")); !errors.Is(err, verbrequest.ErrTooMany) {
		t.Fatalf("got %v, want ErrTooMany", err)
	}
	// Retrouver une demande qui attend déjà ne dépasse rien.
	if _, err := b.Submit(ctx, b.submission("m0")); err != nil {
		t.Fatalf("la même demande : %v", err)
	}
}

// Deux confirmations simultanées : une seule gagne, l'autre voit une demande
// déjà décidée.
func TestConfirm_OnlyOneOfTwoSimultaneousConfirmationsWins(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	request, err := b.Submit(ctx, b.submission("m1"))
	if err != nil {
		t.Fatal(err)
	}
	var wins, refused atomic.Int32
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := b.Confirm(ctx, request.ID, offeredAll)
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, verbrequest.ErrDecided):
				refused.Add(1)
			default:
				t.Errorf("confirm : %v", err)
			}
		}()
	}
	wait.Wait()
	if wins.Load() != 1 || refused.Load() != 7 {
		t.Fatalf("%d gagnent, %d refusées", wins.Load(), refused.Load())
	}
}

func TestConfirm_RefusesAnExpiredAndAnUnknownRequest(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	request, err := b.Submit(ctx, b.submission("m1"))
	if err != nil {
		t.Fatal(err)
	}
	*b.now = start.Add(verbrequest.TTL)
	if _, err := b.Confirm(ctx, request.ID, offeredAll); !errors.Is(err, verbrequest.ErrExpired) {
		t.Fatalf("échue : %v", err)
	}
	if _, err := b.Reject(ctx, request.ID); !errors.Is(err, verbrequest.ErrExpired) {
		t.Fatalf("échue, refus : %v", err)
	}
	if _, err := b.Confirm(ctx, "inconnue", offeredAll); !errors.Is(err, verbrequest.ErrNotFound) {
		t.Fatalf("inconnue : %v", err)
	}
}

// Une demande échue libère la place du jeton.
func TestSubmit_AnExpiredRequestFreesTheCeiling(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	for i := range verbrequest.MaxPendingPerToken {
		if _, err := b.Submit(ctx, b.submission(fmt.Sprintf("m%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	*b.now = start.Add(verbrequest.TTL + time.Second)
	if _, err := b.Submit(ctx, b.submission("neuve")); err != nil {
		t.Fatalf("après échéance : %v", err)
	}
}

func offeredAll(string) bool { return true }

// Un verbe retiré du registre depuis le dépôt de la demande : la confirmation
// la refuse (elle ne reste pas en attente), rien n'est rejoué, et une seconde
// tentative la voit décidée.
func TestConfirm_ARequestOfARemovedVerbIsRefusedNotReplayed(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	request, err := b.Submit(ctx, b.submission("m1"))
	if err != nil {
		t.Fatal(err)
	}
	offeredNothing := func(string) bool { return false }
	if _, err := b.Confirm(ctx, request.ID, offeredNothing); !errors.Is(err, verbrequest.ErrVerbRemoved) {
		t.Fatalf("verbe retiré : %v", err)
	}
	if pending, _ := b.ListPending(ctx); len(pending) != 0 {
		t.Fatalf("la demande reste en attente : %+v", pending)
	}
	if _, err := b.Confirm(ctx, request.ID, offeredAll); !errors.Is(err, verbrequest.ErrDecided) {
		t.Fatalf("seconde confirmation : %v", err)
	}
}
