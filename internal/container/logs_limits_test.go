package container

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// containerStore ne sait que rendre la fiche : FetchLogs n'en lit pas plus.
type containerStore struct {
	Store
}

func (containerStore) GetContainer(_ context.Context, id string) (Container, error) {
	return Container{ID: id, MachineID: "vps", ContainerID: "c"}, nil
}

// fetching ouvre un tirage unique sur un faux agent et rend ce qu'il faut
// pour lui répondre.
type fetching struct {
	relay     *Relay
	commander *fakeCommander
	result    chan fetchResult
}

type fetchResult struct {
	lines []LogLine
	err   error
}

func startFetch(t *testing.T, tail int) *fetching {
	t.Helper()
	tracker := New(containerStore{}, slog.New(slog.DiscardHandler))
	tracker.relay.newID = func() string { return "req-1" }
	commander := &fakeCommander{}
	tracker.SetCommander(commander)
	fetch := &fetching{relay: tracker.relay, commander: commander, result: make(chan fetchResult, 1)}
	go func() {
		lines, err := tracker.FetchLogs(context.Background(), "vps:c", tail)
		fetch.result <- fetchResult{lines, err}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for commander.count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("la demande n'est jamais partie à l'agent")
		}
		time.Sleep(time.Millisecond)
	}
	return fetch
}

func (f *fetching) deliver(batch LogBatch) error {
	return f.relay.Deliver(context.Background(), "vps", "req-1", batch)
}

func (f *fetching) wait(t *testing.T) fetchResult {
	t.Helper()
	select {
	case result := <-f.result:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("le tirage ne se termine pas")
		return fetchResult{}
	}
}

func numberedLines(from, count int) []LogLine {
	lines := make([]LogLine, count)
	for i := range lines {
		lines[i] = LogLine{Stream: "stdout", Text: fmt.Sprintf("ligne %d", from+i)}
	}
	return lines
}

func TestDeliver_BatchOverTheLimitOfLines_IsRefusedAndTheLimitPasses(t *testing.T) {
	fetch := startFetch(t, 100)
	if err := fetch.deliver(LogBatch{Lines: numberedLines(0, MaxLinesPerBatch+1)}); !errors.Is(err, ErrLogBatchTooLarge) {
		t.Fatalf("65 lignes : err = %v", err)
	}
	if err := fetch.deliver(LogBatch{Lines: numberedLines(0, MaxLinesPerBatch), Done: true}); err != nil {
		t.Fatalf("64 lignes : err = %v", err)
	}
	if result := fetch.wait(t); result.err != nil || len(result.lines) != MaxLinesPerBatch {
		t.Fatalf("result = %d lignes, %v", len(result.lines), result.err)
	}
}

func TestDeliver_StreamNameOverTheLimit_IsRefused(t *testing.T) {
	fetch := startFetch(t, 100)
	if err := fetch.deliver(LogBatch{Lines: []LogLine{{Stream: strings.Repeat("s", maxLogStreamBytes+1), Text: "a"}}}); !errors.Is(err, ErrLogBatchTooLarge) {
		t.Fatalf("flux trop long : err = %v", err)
	}
	if err := fetch.deliver(LogBatch{Done: true}); err != nil {
		t.Fatal(err)
	}
	fetch.wait(t)
}

// Une ligne trop longue, celle d'un terminal sous un agent installé, est
// coupée sur une limite de caractère et la demande continue.
func TestDeliver_LineOverTheLimitOfBytes_IsClippedOnACharacter(t *testing.T) {
	fetch := startFetch(t, 100)
	// « é » fait deux octets : la borne tombe au milieu d'un caractère.
	long := "a" + strings.Repeat("é", MaxLogLineBytes)
	if err := fetch.deliver(LogBatch{Lines: []LogLine{{Stream: "stdout", Text: long}, {Stream: "stderr", Text: "court"}}, Done: true}); err != nil {
		t.Fatalf("ligne trop longue : err = %v", err)
	}
	result := fetch.wait(t)
	if result.err != nil || len(result.lines) != 2 {
		t.Fatalf("result = %d lignes, %v", len(result.lines), result.err)
	}
	if got := result.lines[0].Text; len(got) != MaxLogLineBytes-1 || !utf8.ValidString(got) {
		t.Fatalf("ligne coupée : %d octets, valide = %v", len(got), utf8.ValidString(got))
	}
	if result.lines[1].Text != "court" {
		t.Fatalf("une ligne courte ne change pas : %q", result.lines[1].Text)
	}
}

// L'agent envoie plus que la fin demandée : le serveur ne retient que la
// fin demandée.
func TestFetchLogs_KeepsOnlyTheRequestedTail(t *testing.T) {
	fetch := startFetch(t, 1)
	for batch := range 3 {
		if err := fetch.deliver(LogBatch{Lines: numberedLines(batch*MaxLinesPerBatch, MaxLinesPerBatch)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := fetch.deliver(LogBatch{Done: true}); err != nil {
		t.Fatal(err)
	}
	result := fetch.wait(t)
	if result.err != nil || len(result.lines) != 1 || result.lines[0].Text != "ligne 191" {
		t.Fatalf("result = %+v, %v", result.lines, result.err)
	}
}

func TestFetchLogs_TailIsClampedToTheMaximum(t *testing.T) {
	fetch := startFetch(t, 100_000)
	for batch := range 10 {
		if err := fetch.deliver(LogBatch{Lines: numberedLines(batch*MaxLinesPerBatch, MaxLinesPerBatch)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := fetch.deliver(LogBatch{Done: true}); err != nil {
		t.Fatal(err)
	}
	result := fetch.wait(t)
	if result.err != nil || len(result.lines) != MaxLogTail || result.lines[MaxLogTail-1].Text != "ligne 639" {
		t.Fatalf("result = %d lignes, %v", len(result.lines), result.err)
	}
}

// Passé le total d'octets, la demande est coupée proprement : ce qui est
// gardé est rendu, l'agent reçoit l'ordre d'arrêter, et ses lots suivants
// sont refusés comme ceux d'une requête finie.
func TestFetchLogs_PastTheTotalOfBytes_CutsTheRequestCleanly(t *testing.T) {
	fetch := startFetch(t, 10)
	line := LogLine{Stream: "stdout", Text: strings.Repeat("x", MaxLogLineBytes)}
	flood := LogBatch{Lines: make([]LogLine, MaxLinesPerBatch)}
	for i := range flood.Lines {
		flood.Lines[i] = line
	}
	// 64 lignes de 64 Kio : exactement le total, les noms de flux le dépassent.
	if err := fetch.deliver(flood); err != nil {
		t.Fatal(err)
	}
	result := fetch.wait(t)
	if result.err != nil || len(result.lines) != 10 {
		t.Fatalf("result = %d lignes, %v", len(result.lines), result.err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for fetch.commander.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if fetch.commander.count() != 2 || fetch.commander.commands[1] != "vps:logs_stop" {
		t.Fatalf("commandes = %v", fetch.commander.commands)
	}
	// Le lot suivant trouve la requête fermée (le temps que l'oubli passe).
	deadline = time.Now().Add(2 * time.Second)
	for {
		err := fetch.deliver(LogBatch{Lines: numberedLines(0, 1)})
		if errors.Is(err, ErrLogRequestUnknown) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("la requête coupée accepte encore des lots : %v", err)
		}
		time.Sleep(time.Millisecond)
	}
}
