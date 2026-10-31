package dockerwatch

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/dockerapi"
)

// follow tient le flux d'événements et les deux cadences tant que le démon
// répond ; il rend la main sur la première coupure.
func (w *Watcher) follow(ctx context.Context) error {
	w.mu.Lock()
	since := w.since
	w.mu.Unlock()
	stream, err := w.client.Events(ctx, since)
	if err != nil {
		return err
	}
	defer stream.Close()

	events := make(chan dockerapi.Event)
	failed := make(chan error, 1)
	go func() {
		defer close(events)
		for {
			event, err := stream.Next()
			if errors.Is(err, dockerapi.ErrEventLineTooLong) {
				// Sans le contenu : ce sont des labels, que l'auteur d'une image écrit.
				w.logger.Warn("docker event skipped", "reason", "line too long")
				continue
			}
			if err != nil {
				failed <- err
				return
			}
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
	}()

	inventories := time.NewTicker(w.opts.InventoryInterval)
	defer inventories.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-failed:
			return streamError(err)
		case event, open := <-events:
			if !open {
				// Le lecteur dépose son erreur dans failed avant de fermer
				// events, mais si la boucle était occupée les deux cas sont
				// prêts et Go choisit au hasard : lire ici un événement vide
				// ramènerait since à « 0 », et le démon rejouerait tout son
				// tampon à la reconnexion.
				return streamEnd(ctx, failed)
			}
			w.handleEvent(ctx, event)
		case <-inventories.C:
			w.deliverInventory(ctx)
		}
	}
}

// streamEnd rend l'erreur qui a fermé le flux, ou nil si c'est le contexte.
func streamEnd(ctx context.Context, failed <-chan error) error {
	select {
	case err := <-failed:
		return streamError(err)
	default:
	}
	if ctx.Err() != nil {
		return nil
	}
	return errors.New("event stream closed")
}

func streamError(err error) error {
	if errors.Is(err, io.EOF) {
		return errors.New("event stream closed")
	}
	return err
}

// handleEvent traduit une action Docker en événement de conteneur, avec le
// conteneur réinspecté ; les actions qui ne changent ni l'état ni la
// santé ni le nom ni les réseaux sont ignorées, le prochain inventaire
// couvre le reste. Un réseau créé ou détruit fait relire la liste.
func (w *Watcher) handleEvent(ctx context.Context, raw dockerapi.Event) {
	// Un événement sans horodatage ne dit pas d'où reprendre : « 0 » ferait
	// rejouer au démon tout son tampon.
	if raw.TimeNano > 0 || raw.Time > 0 {
		w.mu.Lock()
		w.since = raw.Since()
		w.mu.Unlock()
	}
	if raw.Type == "network" && (raw.Action == "create" || raw.Action == "destroy") {
		w.deliverNetworks(ctx)
		return
	}
	// Qui monte quoi ne se lit que dans l'inventaire des conteneurs : un volume
	// créé ou détruit le fait relire en entier.
	if raw.Type == "volume" && (raw.Action == "create" || raw.Action == "destroy") {
		w.deliverInventory(ctx)
		return
	}
	w.noteStopRequest(raw)
	event, wanted := translate(raw)
	if !wanted {
		return
	}
	if event.Action == container.ActionDestroy {
		w.forgetCounters(event.ContainerID)
		w.forgetStopRequest(event.ContainerID)
		w.forget(event.ContainerID)
		w.sink.Deliver(container.Report{Events: []container.Event{event}})
		return
	}
	inspected, err := w.client.Inspect(ctx, event.ContainerID)
	isGone := errors.Is(err, dockerapi.ErrNotFound)
	var observed container.Observed
	if err == nil {
		observed = toContainer(inspected)
		event.Container = &observed
		w.remember(event.ContainerID)
	} else if isGone {
		w.forget(event.ContainerID)
	} else {
		w.logger.Warn("inspect after event", "action", raw.Action, "error", err)
	}
	if event.Action == "die" {
		// Un conteneur qu'on retire (rm -f, compose down) est tué en
		// SIGKILL sans « stop » : c'est demandé aussi.
		isRemoved := isGone || inspected.State.Status == string(container.StateRemoving)
		isRequested := w.takeStopRequest(event.ContainerID, event.At, stopTimeout(inspected))
		event.Killed = event.ExitCode != nil && *event.ExitCode == exitCodeKilled &&
			!observed.OOMKilled && !isRemoved && !isRequested
		// Le manque de mémoire passe avant un arrêt demandé.
		event.StopRequested = isRequested && !observed.OOMKilled
	}
	// Le manque de mémoire ne se lit qu'à l'inspection : elle passe avant.
	if event.Action == "die" && event.ExitCode != nil && !container.IsCleanStop(*event.ExitCode, observed.OOMKilled, event.Killed, event.StopRequested) {
		event.Snippet = w.snippet(ctx, event.ContainerID)
	}
	w.sink.Deliver(container.Report{Events: []container.Event{event}})
}

// exitCodeKilled est le code d'un processus tué par SIGKILL (128 + 9).
const exitCodeKilled = 137

// noteStopRequest retient une demande d'arrêt : docker stop, restart et
// compose down envoient d'abord le signal d'arrêt du conteneur, puis
// SIGKILL au bout du délai, et Docker émet « stop ». docker kill n'envoie
// que SIGKILL. Un conteneur qui repart n'a plus d'arrêt en attente.
func (w *Watcher) noteStopRequest(raw dockerapi.Event) {
	if raw.Type != "container" && raw.Type != "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	switch {
	case raw.IsStopSignal(), raw.Action == "stop":
		w.stopRequests[raw.Actor.ID] = raw.At()
	case raw.Action == "start":
		delete(w.stopRequests, raw.Actor.ID)
	}
}

// takeStopRequest dit si un arrêt a été demandé assez peu de temps avant
// ce « die », et l'efface : il ne vaut que pour un arrêt.
func (w *Watcher) takeStopRequest(id string, diedAt time.Time, timeout time.Duration) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	requestedAt, ok := w.stopRequests[id]
	delete(w.stopRequests, id)
	return ok && diedAt.Sub(requestedAt) <= min(timeout, maxStopTimeout)+stopRequestMargin
}

func (w *Watcher) forgetStopRequest(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.stopRequests, id)
}

// stopTimeout rend le délai de docker stop du conteneur.
func stopTimeout(inspected dockerapi.Container) time.Duration {
	if inspected.Config.StopTimeout == nil || *inspected.Config.StopTimeout < 0 {
		return defaultStopTimeout
	}
	return time.Duration(*inspected.Config.StopTimeout) * time.Second
}

// translate dit quel état ou quelle santé une action implique. Un
// « stop » ou un « kill » ne changent rien par eux-mêmes : le « die » qui
// suit porte l'état et le code de sortie. Sur un réseau, l'acteur est le
// réseau : le conteneur est dans les attributs.
func translate(raw dockerapi.Event) (container.Event, bool) {
	if raw.Type == "network" {
		return translateNetwork(raw)
	}
	event := container.Event{At: raw.At(), Action: raw.Action, ContainerID: raw.Actor.ID}
	if strings.HasPrefix(raw.Action, "health_status") {
		_, status, _ := strings.Cut(raw.Action, ": ")
		event.Action = "health_status"
		event.Health = container.Health(status)
		return event, event.Health != ""
	}
	switch raw.Action {
	case "create":
		event.State = container.StateCreated
	case "start", "unpause":
		event.State = container.StateRunning
	case "pause":
		event.State = container.StatePaused
	case "die":
		event.State = container.StateExited
		code := raw.ExitCode()
		if code >= 0 {
			event.ExitCode = &code
		}
	case "rename", container.ActionDestroy:
	default:
		return container.Event{}, false
	}
	return event, true
}

func translateNetwork(raw dockerapi.Event) (container.Event, bool) {
	event := container.Event{At: raw.At(), ContainerID: raw.Actor.Attributes["container"]}
	switch raw.Action {
	case "connect":
		event.Action = container.ActionNetworkConnect
	case "disconnect":
		event.Action = container.ActionNetworkDisconnect
	default:
		return container.Event{}, false
	}
	return event, event.ContainerID != ""
}

// snippet lit les dernières lignes du journal d'un conteneur qui vient de
// s'arrêter mal ; bornées en lignes et en octets.
func (w *Watcher) snippet(ctx context.Context, id string) string {
	reader, err := w.client.Logs(ctx, id, dockerapi.LogsOptions{Tail: container.SnippetLines})
	if err != nil {
		return ""
	}
	defer reader.Close()
	var lines []string
	for {
		line, err := reader.Next()
		if err != nil {
			break
		}
		lines = append(lines, line.Text)
	}
	snippet := strings.Join(lines, "\n")
	if len(snippet) > container.MaxSnippetBytes {
		snippet = snippet[len(snippet)-container.MaxSnippetBytes:]
	}
	return snippet
}
