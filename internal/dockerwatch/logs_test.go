package dockerwatch

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
)

// loggedWatcher monte un veilleur sur un faux démon, avec un journal qu'on
// peut relire. Le veilleur ne tourne pas : rien d'autre n'écrit dans le
// tampon pendant un test.
func loggedWatcher(t *testing.T) (*Watcher, *daemon, *bytes.Buffer) {
	t.Helper()
	fake := newDaemon(t, true)
	var journal bytes.Buffer
	watcher := New(serve(t, fake.mux), newSink(), Options{Logger: slog.New(slog.NewTextHandler(&journal, nil))})
	return watcher, fake, &journal
}

func firstBatch(t *testing.T, watcher *Watcher, request container.LogRequest) container.LogBatch {
	t.Helper()
	batches, err := watcher.OpenLogs(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case batch := <-batches:
		return batch
	case <-time.After(waitLimit):
		t.Fatal("no batch")
		return container.LogBatch{}
	}
}

// Un identifiant bien formé que l'agent n'a jamais rapporté : refusé sans
// que Docker en entende parler. Le serveur n'est pas cru sur un identifiant.
func TestOpenLogs_RefusesAContainerItNeverReported(t *testing.T) {
	watcher, fake, journal := loggedWatcher(t)
	watcher.rememberAll([]string{fixtureID})

	for _, follow := range []bool{false, true} {
		batch := firstBatch(t, watcher, container.LogRequest{ID: "req-1", ContainerID: networkedID, Tail: 50, Follow: follow})
		if batch.Error != LogsNotFound || len(batch.Lines) != 0 {
			t.Fatalf("follow=%v: lot = %+v", follow, batch)
		}
	}
	if fake.logs.Load() != 0 {
		t.Fatalf("%d demandes de journal sont arrivées jusqu'à Docker", fake.logs.Load())
	}
	if watcher.follows != 0 {
		t.Fatal("un suivi refusé ne prend pas de place")
	}
	text := journal.String()
	if !strings.Contains(text, "logs refused") || !strings.Contains(text, "not_inventoried") || !strings.Contains(text, "container="+networkedID[:12]) {
		t.Fatalf("journal:\n%s", text)
	}
	if strings.Contains(text, networkedID) {
		t.Fatalf("le journal garde l'identifiant entier:\n%s", text)
	}
}

// Même inscrit dans l'ensemble, ce qui n'a pas la forme d'un identifiant
// Docker n'atteint jamais l'API : « ../../info » y serait un chemin.
func TestOpenLogs_RefusesAnIDThatIsNotADockerID(t *testing.T) {
	forged := map[string]string{
		"chemin":                     "../../info",
		"chemin après l'identifiant": fixtureID + "/../../info",
		"court":                      "abc",
		"nom de conteneur":           "nginx",
		"63 hexadécimaux":            fixtureID[:63],
		"65 hexadécimaux":            fixtureID + "0",
		"majuscules":                 strings.ToUpper(fixtureID),
		"saut de ligne":              fixtureID[:63] + "\n",
		"vide":                       "",
		"requête":                    fixtureID + "?follow=1",
	}
	watcher, fake, journal := loggedWatcher(t)
	ids := []string{fixtureID}
	for _, id := range forged {
		ids = append(ids, id)
	}
	watcher.rememberAll(ids)

	for name, id := range forged {
		t.Run(name, func(t *testing.T) {
			batch := firstBatch(t, watcher, container.LogRequest{ID: "req-2", ContainerID: id})
			if batch.Error != LogsNotFound {
				t.Fatalf("lot = %+v", batch)
			}
		})
	}
	if fake.logs.Load() != 0 {
		t.Fatalf("%d demandes de journal sont arrivées jusqu'à Docker", fake.logs.Load())
	}
	if !strings.Contains(journal.String(), "not_a_docker_id") {
		t.Fatalf("journal:\n%s", journal.String())
	}
}

// Un identifiant géant ne remplit pas le journal.
func TestOpenLogs_LogsOnlyAPrefixOfAForgedID(t *testing.T) {
	watcher, _, journal := loggedWatcher(t)
	watcher.rememberAll([]string{fixtureID})
	firstBatch(t, watcher, container.LogRequest{ID: strings.Repeat("r", 5000), ContainerID: strings.Repeat("c", 5000)})
	if journal.Len() > 1000 {
		t.Fatalf("journal de %d octets", journal.Len())
	}
}

// Un conteneur que l'inventaire liste s'ouvre ; la longueur demandée est
// bornée par la règle du serveur, pas laissée à l'ordre reçu.
func TestOpenLogs_ServesAnInventoriedContainer(t *testing.T) {
	watcher, fake, journal := loggedWatcher(t)
	watcher.deliverInventory(context.Background())

	batches, err := watcher.OpenLogs(context.Background(), container.LogRequest{ID: "req-3", ContainerID: fixtureID, Tail: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	var lines []container.LogLine
	for batch := range batches {
		if batch.Error != "" {
			t.Fatalf("erreur %s", batch.Error)
		}
		lines = append(lines, batch.Lines...)
	}
	if len(lines) == 0 || fake.logs.Load() != 1 {
		t.Fatalf("%d lignes, %d demandes", len(lines), fake.logs.Load())
	}
	fake.logsMu.Lock()
	defer fake.logsMu.Unlock()
	if want := "500"; fake.logTails[0] != want {
		t.Fatalf("tail transmis = %q, attendu %q", fake.logTails[0], want)
	}
	if !strings.Contains(journal.String(), "logs opened") || !strings.Contains(journal.String(), "container="+fixtureID[:12]) {
		t.Fatalf("journal:\n%s", journal.String())
	}
}

// Sans « tail », Docker rendrait tout le journal : l'agent pose la valeur
// par défaut, comme le serveur.
func TestOpenLogs_AMissingTailGetsTheDefault(t *testing.T) {
	watcher, fake, _ := loggedWatcher(t)
	watcher.rememberAll([]string{fixtureID})
	batches, _ := watcher.OpenLogs(context.Background(), container.LogRequest{ContainerID: fixtureID})
	for range batches {
	}
	fake.logsMu.Lock()
	defer fake.logsMu.Unlock()
	if len(fake.logTails) != 1 || fake.logTails[0] != "100" {
		t.Fatalf("tail transmis = %v", fake.logTails)
	}
}

// Un événement qui inspecte un conteneur l'ajoute ; sa destruction le
// retire ; un nouvel inventaire remplace tout.
func TestOpenLogs_ForgetsADestroyedContainer(t *testing.T) {
	watcher, fake, _ := loggedWatcher(t)
	ctx := context.Background()
	watcher.deliverInventory(ctx)
	if !watcher.isKnown(fixtureID) {
		t.Fatal("l'inventaire livré doit inscrire son conteneur")
	}

	start := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	watcher.handleEvent(ctx, dockerEvent(networkedID, "start", start, nil))
	if !watcher.isKnown(networkedID) {
		t.Fatal("un conteneur inspecté après un événement doit être inscrit")
	}

	watcher.handleEvent(ctx, dockerEvent(fixtureID, "destroy", start, nil))
	batch := firstBatch(t, watcher, container.LogRequest{ID: "req-4", ContainerID: fixtureID})
	if batch.Error != LogsNotFound || fake.logs.Load() != 0 {
		t.Fatalf("lot = %+v, %d demandes", batch, fake.logs.Load())
	}

	// Un conteneur disparu entre l'événement et l'inspection n'est plus à lui.
	watcher.handleEvent(ctx, dockerEvent(goneID, "die", start, nil))
	watcher.remember(goneID)
	watcher.handleEvent(ctx, dockerEvent(goneID, "die", start, nil))
	if watcher.isKnown(goneID) {
		t.Fatal("un conteneur introuvable à l'inspection doit être oublié")
	}

	watcher.rememberAll([]string{fixtureID})
	if watcher.isKnown(networkedID) || !watcher.isKnown(fixtureID) {
		t.Fatal("un inventaire complet remplace l'ensemble")
	}
}

// Juste après le démarrage, l'agent n'a encore rien rapporté : la demande
// attend le premier inventaire au lieu de refuser un conteneur bien réel.
func TestOpenLogs_WaitsForTheFirstInventory(t *testing.T) {
	watcher, fake, _ := loggedWatcher(t)
	go func() {
		time.Sleep(50 * time.Millisecond)
		watcher.rememberAll([]string{fixtureID})
	}()
	batches, err := watcher.OpenLogs(context.Background(), container.LogRequest{ID: "req-5", ContainerID: fixtureID, Tail: 5})
	if err != nil {
		t.Fatal(err)
	}
	for batch := range batches {
		if batch.Error != "" {
			t.Fatalf("erreur %s", batch.Error)
		}
	}
	if fake.logs.Load() != 1 {
		t.Fatalf("%d demandes", fake.logs.Load())
	}
}

// Docker muet : pas d'inventaire, donc pas de journal, et une réponse qui
// dit « indisponible », pas « introuvable ».
func TestOpenLogs_WithoutAnInventoryIsUnavailable(t *testing.T) {
	previous := inventoryWait
	inventoryWait = 30 * time.Millisecond
	t.Cleanup(func() { inventoryWait = previous })
	watcher, fake, journal := loggedWatcher(t)

	batch := firstBatch(t, watcher, container.LogRequest{ID: "req-6", ContainerID: fixtureID})
	if batch.Error != LogsUnavailable || fake.logs.Load() != 0 {
		t.Fatalf("lot = %+v, %d demandes", batch, fake.logs.Load())
	}
	if !strings.Contains(journal.String(), "inventory_pending") {
		t.Fatalf("journal:\n%s", journal.String())
	}
}

// knowingSink vérifie, à chaque rapport livré, que le veilleur connaît déjà
// chaque conteneur qu'il décrit : le serveur ne peut demander que le journal
// d'un conteneur qu'il a reçu, il ne doit jamais l'apprendre avant l'agent.
type knowingSink struct {
	*recordingSink
	watcher *Watcher
	mu      sync.Mutex
	unknown []string
}

func (s *knowingSink) Deliver(report container.Report) {
	var described []string
	for _, observed := range report.Inventory {
		described = append(described, observed.ContainerID)
	}
	for _, event := range report.Events {
		if event.Container != nil {
			described = append(described, event.Container.ContainerID)
		}
	}
	for _, id := range described {
		if !s.watcher.isKnown(id) {
			s.mu.Lock()
			s.unknown = append(s.unknown, id)
			s.mu.Unlock()
		}
	}
	s.recordingSink.Deliver(report)
}

// Aucune fenêtre où le serveur connaît un conteneur que l'agent refuserait :
// l'inventaire et l'événement inscrivent avant de livrer.
func TestRun_KnowsEveryContainerBeforeReportingIt(t *testing.T) {
	sink := &knowingSink{recordingSink: newSink()}
	watcher := newWatcher(serve(t, newDaemon(t, false).mux), sink)
	sink.watcher = watcher
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watcher.Run(ctx)

	sink.waitFor(t, "inventory", func(r container.Report) bool { return r.Complete && len(r.Inventory) > 0 })
	sink.waitFor(t, "network connect", func(r container.Report) bool {
		return len(r.Events) == 1 && r.Events[0].ContainerID == networkedID && r.Events[0].Container != nil
	})
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.unknown) != 0 {
		t.Fatalf("rapportés avant d'être connus: %v", sink.unknown)
	}
}
