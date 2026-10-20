package sse

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/live"
)

const (
	// Un commentaire toutes les 15 s tient la connexion ouverte derrière un
	// mandataire.
	PingInterval = 15 * time.Second
	// Ce que le navigateur attend avant de revenir, en millisecondes.
	retry = "5000"
)

// Stream tient une connexion SSE sur un abonnement jusqu'à ce que le client
// parte, et le ferme à la fin. Un identifiant sur chaque événement fait que le
// navigateur revient avec Last-Event-ID : on lui dit alors « reconnected » et
// il relit tout, car rien n'est rejoué. lapsed, s'il est donné, est jugé à
// chaque ping : vrai, le flux se ferme et le navigateur revient se faire
// juger (un flux n'est jugé qu'à l'ouverture, sinon).
func Stream(w http.ResponseWriter, r *http.Request, subscription *live.Subscription, lapsed func() bool) {
	stream(w, r, subscription, lapsed, PingInterval)
}

func stream(w http.ResponseWriter, r *http.Request, subscription *live.Subscription, lapsed func() bool, pingEvery time.Duration) {
	defer subscription.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	sequence := 0
	first := "connected"
	if r.Header.Get("Last-Event-ID") != "" {
		first = "reconnected"
	}
	if err := writeEvent(w, &sequence, first); err != nil {
		return
	}

	topics := readTopics(r.Context(), subscription)
	ticker := time.NewTicker(pingEvery)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if lapsed != nil && lapsed() {
				return
			}
			if err := WriteComment(w, "ping"); err != nil {
				return
			}
		case batch, open := <-topics:
			if !open {
				return
			}
			for _, topic := range batch {
				if err := writeEvent(w, &sequence, string(topic)); err != nil {
					return
				}
			}
		}
	}
}

// readTopics lit l'abonnement dans sa propre goroutine et s'arrête avec le
// contexte de la requête, en fermant le canal.
func readTopics(ctx context.Context, subscription *live.Subscription) <-chan []live.Topic {
	topics := make(chan []live.Topic)
	go func() {
		defer close(topics)
		for {
			batch, err := subscription.Next(ctx)
			if err != nil {
				return
			}
			select {
			case topics <- batch:
			case <-ctx.Done():
				return
			}
		}
	}()
	return topics
}

// Le premier événement porte retry : le navigateur attend cinq secondes
// avant de revenir, plutôt que ses trois par défaut.
func writeEvent(w http.ResponseWriter, sequence *int, name string) error {
	*sequence++
	prefix := ""
	if *sequence == 1 {
		prefix = "retry: " + retry + "\n"
	}
	if _, err := fmt.Fprintf(w, "%sid: %d\nevent: %s\ndata: \n\n", prefix, *sequence, name); err != nil { // #nosec G705 -- text/event-stream, noms du serveur, jamais une entrée.
		return err
	}
	return http.NewResponseController(w).Flush()
}

// WriteComment écrit un commentaire SSE, que le navigateur ignore.
func WriteComment(w http.ResponseWriter, text string) error {
	if _, err := fmt.Fprintf(w, ": %s\n\n", text); err != nil { // #nosec G705 -- text/event-stream, constante.
		return err
	}
	return http.NewResponseController(w).Flush()
}
