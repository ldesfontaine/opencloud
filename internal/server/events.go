package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/live"
)

const (
	eventsPingInterval  = 15 * time.Second
	maxEventSubscribers = 64
	// Ce que le navigateur attend avant de revenir, en millisecondes.
	eventsRetry = "5000"
	codeBusy    = "busy"
)

// Le direct du navigateur : une connexion par onglet, qui reçoit le nom de
// ce qui a changé (machines, jobs) et rien d'autre ; le front relit. Un
// identifiant sur chaque événement fait que le navigateur revient avec
// Last-Event-ID : on lui dit alors « reconnected » et il relit tout, car rien
// n'est rejoué. Un commentaire toutes les 15 s tient la connexion ouverte
// derrière Traefik. Sans authentification, comme le reste de l'interface :
// le plafond d'abonnés empêche seulement d'épuiser le serveur.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	if s.live.Count() >= maxEventSubscribers {
		s.writeAPIError(w, http.StatusServiceUnavailable, codeBusy)
		return
	}
	s.streamTopics(w, r, s.live)
}

// streamTopics tient une connexion SSE sur un bus jusqu'à ce que l'onglet
// parte ; le direct public et celui de l'administration ont chacun leur
// bus, la mécanique est la même.
func (s *Server) streamTopics(w http.ResponseWriter, r *http.Request, bus Live) {
	subscription := bus.Subscribe()
	defer subscription.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	sequence := 0
	first := "connected"
	if r.Header.Get("Last-Event-ID") != "" {
		first = "reconnected"
	}
	if err := writeLiveEvent(w, &sequence, first); err != nil {
		return
	}

	topics := readTopics(r.Context(), subscription)
	ticker := time.NewTicker(eventsPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if err := writeComment(w, "ping"); err != nil {
				return
			}
		case batch, open := <-topics:
			if !open {
				return
			}
			for _, topic := range batch {
				if err := writeLiveEvent(w, &sequence, string(topic)); err != nil {
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
func writeLiveEvent(w http.ResponseWriter, sequence *int, name string) error {
	*sequence++
	retry := ""
	if *sequence == 1 {
		retry = "retry: " + eventsRetry + "\n"
	}
	if _, err := fmt.Fprintf(w, "%sid: %d\nevent: %s\ndata: \n\n", retry, *sequence, name); err != nil { // #nosec G705 -- text/event-stream, noms du serveur, jamais une entrée.
		return err
	}
	return http.NewResponseController(w).Flush()
}

func writeComment(w http.ResponseWriter, text string) error {
	if _, err := fmt.Fprintf(w, ": %s\n\n", text); err != nil { // #nosec G705 -- text/event-stream, constante.
		return err
	}
	return http.NewResponseController(w).Flush()
}
