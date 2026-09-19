package dockerwatch

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/ldesfontaine/opencloud/internal/dockerapi"
	"github.com/ldesfontaine/opencloud/internal/service"
)

// Les codes que l'agent rend quand il ne peut pas servir un journal ; le
// serveur les fait suivre, le front les traduit.
const (
	LogsNotFound    = "service.logs_not_found"
	LogsBusy        = "service.logs_busy"
	LogsUnavailable = "service.logs_failed"
)

// OpenLogs sert un journal par lots : toutes les secondes ou dès 64 lignes.
// Le canal se ferme après le lot final, Done ou Error. Au-delà de quatre
// suivis, refusé tout de suite.
func (w *Watcher) OpenLogs(ctx context.Context, request service.LogRequest) (<-chan service.LogBatch, error) {
	batches := make(chan service.LogBatch, batchBacklog)
	if !w.takeFollow() {
		batches <- service.LogBatch{Error: LogsBusy}
		close(batches)
		return batches, nil
	}
	reader, err := w.client.Logs(ctx, request.ContainerID, dockerapi.LogsOptions{Tail: request.Tail, Follow: request.Follow})
	if err != nil {
		w.releaseFollow()
		batches <- service.LogBatch{Error: logsError(err)}
		close(batches)
		return batches, nil
	}
	go w.pump(ctx, reader, batches)
	return batches, nil
}

func (w *Watcher) takeFollow() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.follows >= service.MaxFollowsPerMachine {
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
func (w *Watcher) pump(ctx context.Context, reader *dockerapi.LogReader, batches chan<- service.LogBatch) {
	defer close(batches)
	defer w.releaseFollow()
	defer reader.Close()
	lines := make(chan service.LogLine, service.MaxLinesPerBatch)
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
			case lines <- service.LogLine{At: line.At, Stream: line.Stream, Text: line.Text}:
			case <-ctx.Done():
				return
			}
		}
	}()

	ticker := time.NewTicker(batchInterval)
	defer ticker.Stop()
	var pending []service.LogLine
	flush := func(last service.LogBatch) bool {
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
			if !flush(service.LogBatch{}) {
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
				last := service.LogBatch{Done: true}
				if !errors.Is(err, io.EOF) && ctx.Err() == nil {
					last = service.LogBatch{Error: logsError(err)}
				}
				flush(last)
				return
			}
			pending = append(pending, line)
			if len(pending) >= service.MaxLinesPerBatch && !flush(service.LogBatch{}) {
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
