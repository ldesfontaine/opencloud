package dockerwatch

import (
	"context"
	"errors"
	"io"
	"regexp"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/dockerapi"
)

// Les codes que l'agent rend quand il ne peut pas servir un journal ; le
// serveur les fait suivre, le front les traduit. Clés figées : les agents
// installés les envoient sous « service. », le catalogue les garde.
const (
	LogsNotFound    = "service.logs_not_found"
	LogsBusy        = "service.logs_busy"
	LogsUnavailable = "service.logs_failed"
)

// containerIDPattern est un identifiant Docker complet : 64 hexadécimaux
// minuscules. Rien d'autre ne part vers l'API (un chemin, un nom, un
// identifiant tronqué ne passent pas).
var containerIDPattern = regexp.MustCompile("^[0-9a-f]{64}$")

// inventoryWait est combien une demande de journal attend le premier
// inventaire, juste après le démarrage de l'agent. Variable pour les tests.
var inventoryWait = 10 * time.Second

// logIDPrefix est ce que le journal de l'agent garde d'un identifiant de
// conteneur : de quoi le reconnaître, pas de quoi le forger.
const logIDPrefix = 12

// OpenLogs sert un journal par lots : toutes les secondes ou dès 64 lignes.
// Le canal se ferme après le lot final, Done ou Error. Au-delà de quatre
// suivis, refusé tout de suite. La commande n'est pas signée : seul le
// journal d'un conteneur que l'agent a lui-même rapporté s'ouvre, et le
// refus tombe avant tout appel à Docker.
func (w *Watcher) OpenLogs(ctx context.Context, request container.LogRequest) (<-chan container.LogBatch, error) {
	batches := make(chan container.LogBatch, batchBacklog)
	refuse := func(code, reason string) (<-chan container.LogBatch, error) {
		w.logger.Warn("logs refused", "request", clip(request.ID, logIDPrefix*3), "container", clip(request.ContainerID, logIDPrefix),
			"follow", request.Follow, "reason", reason)
		batches <- container.LogBatch{Error: code}
		close(batches)
		return batches, nil
	}
	if !containerIDPattern.MatchString(request.ContainerID) {
		return refuse(LogsNotFound, "not_a_docker_id")
	}
	if !w.awaitInventory(ctx) {
		return refuse(LogsUnavailable, "inventory_pending")
	}
	if !w.isKnown(request.ContainerID) {
		return refuse(LogsNotFound, "not_inventoried")
	}
	if !w.takeFollow() {
		return refuse(LogsBusy, "busy")
	}
	reader, err := w.client.Logs(ctx, request.ContainerID, dockerapi.LogsOptions{Tail: container.ClampLogTail(request.Tail), Follow: request.Follow})
	if err != nil {
		w.releaseFollow()
		batches <- container.LogBatch{Error: logsError(err)}
		close(batches)
		return batches, nil
	}
	w.logger.Info("logs opened", "request", clip(request.ID, logIDPrefix*3), "container", clip(request.ContainerID, logIDPrefix), "follow", request.Follow)
	go w.pump(ctx, reader, batches)
	return batches, nil
}

// awaitInventory attend le premier inventaire : avant lui, l'agent n'a rien
// rapporté, et un conteneur bien réel serait refusé. Faux si Docker ne l'a
// pas donné à temps.
func (w *Watcher) awaitInventory(ctx context.Context) bool {
	select {
	case <-w.firstInventory:
		return true
	default:
	}
	timer := time.NewTimer(inventoryWait)
	defer timer.Stop()
	select {
	case <-w.firstInventory:
		return true
	case <-timer.C:
		return false
	case <-ctx.Done():
		return false
	}
}

// clip garde le début d'une valeur venue du serveur, pour le journal.
func clip(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func (w *Watcher) takeFollow() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.follows >= container.MaxFollowsPerMachine {
		return false
	}
	w.follows++
	return true
}

func (w *Watcher) releaseFollow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.follows--
}

// pump lit les lignes et les groupe ; la lecture se fait à part pour que
// la cadence tienne même quand le journal se tait.
func (w *Watcher) pump(ctx context.Context, reader *dockerapi.LogReader, batches chan<- container.LogBatch) {
	defer close(batches)
	defer w.releaseFollow()
	defer reader.Close()
	lines := make(chan container.LogLine, container.MaxLinesPerBatch)
	ended := make(chan error, 1)
	go func() {
		defer close(lines)
		for {
			line, err := reader.Next()
			if err != nil {
				ended <- err
				return
			}
			select {
			case lines <- container.LogLine{At: line.At, Stream: line.Stream, Text: line.Text}:
			case <-ctx.Done():
				return
			}
		}
	}()

	ticker := time.NewTicker(batchInterval)
	defer ticker.Stop()
	var pending []container.LogLine
	flush := func(last container.LogBatch) bool {
		if len(pending) == 0 && !last.Done && last.Error == "" {
			return true
		}
		last.Lines = pending
		pending = nil
		select {
		case batches <- last:
			return true
		case <-ctx.Done():
			return false
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !flush(container.LogBatch{}) {
				return
			}
		case line, open := <-lines:
			if !open {
				var err error
				select {
				case err = <-ended:
				case <-ctx.Done():
					return
				}
				last := container.LogBatch{Done: true}
				if !errors.Is(err, io.EOF) && ctx.Err() == nil {
					last = container.LogBatch{Error: logsError(err)}
				}
				flush(last)
				return
			}
			pending = append(pending, line)
			if len(pending) >= container.MaxLinesPerBatch && !flush(container.LogBatch{}) {
				return
			}
		}
	}
}

func logsError(err error) string {
	if errors.Is(err, dockerapi.ErrNotFound) {
		return LogsNotFound
	}
	return LogsUnavailable
}
