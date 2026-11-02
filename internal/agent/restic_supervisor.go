package agent

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/wait"
)

// La relance de rest-server : un délai qui double d'une seconde à une
// minute, remis au plus court quand le processus a tenu stableProcess. Un
// arrêt lui laisse processStopDelay pour finir les écritures en cours.
const (
	minProcessBackoff = time.Second
	maxProcessBackoff = time.Minute
	stableProcess     = 30 * time.Second
	processStopDelay  = 10 * time.Second
	// La fin de la sortie d'erreur de rest-server, gardée pour dire pourquoi
	// il est tombé (un port déjà pris).
	processTailLines = 8
)

// processSupervisor tient un processus fils en vie tant que son contexte
// vit : il le relance après une mort, le recharge par SIGHUP, l'arrête
// proprement (SIGTERM) à l'annulation. launch rebâtit la commande à chaque
// lancement : une exec.Cmd ne sert qu'une fois.
type processSupervisor struct {
	name   string
	launch func(ctx context.Context) *exec.Cmd
	logger *slog.Logger
	// changed est appelé à chaque changement d'état ; onStart à chaque
	// lancement réussi, avec le pid (les tests s'y règlent).
	changed func()
	onStart func(pid int)
	backoff struct{ min, max, stable time.Duration }

	mu      sync.Mutex
	process *os.Process
	state   string
	reason  string
}

func newProcessSupervisor(name string, launch func(ctx context.Context) *exec.Cmd, logger *slog.Logger, changed func()) *processSupervisor {
	supervisor := &processSupervisor{name: name, launch: launch, logger: logger, changed: changed, state: agentwire.ResticTargetStopped}
	supervisor.backoff.min, supervisor.backoff.max, supervisor.backoff.stable = minProcessBackoff, maxProcessBackoff, stableProcess
	return supervisor
}

// run lance et relance le processus jusqu'à l'annulation du contexte.
func (s *processSupervisor) run(ctx context.Context) {
	delay := s.backoff.min
	for {
		startedAt := time.Now()
		s.runOnce(ctx)
		if ctx.Err() != nil {
			s.setState(agentwire.ResticTargetStopped, "")
			return
		}
		if time.Since(startedAt) >= s.backoff.stable {
			delay = s.backoff.min
		}
		s.logger.Warn("supervised process exited, restarting", "process", s.name, "delay", delay.Round(time.Millisecond))
		if !wait.For(ctx, delay) {
			s.setState(agentwire.ResticTargetStopped, "")
			return
		}
		delay = min(delay*2, s.backoff.max)
	}
}

// runOnce lance le processus et attend sa fin. L'état dit pourquoi il est
// tombé : binaire absent, port pris, ou un autre échec.
func (s *processSupervisor) runOnce(ctx context.Context) {
	command := s.launch(ctx)
	tail := newLineTail(processTailLines)
	command.Stdout = tail.writer("stdout")
	command.Stderr = tail.writer("stderr")
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	command.WaitDelay = processStopDelay
	if err := command.Start(); err != nil {
		s.setState(agentwire.ResticTargetError, startFailureReason(err))
		s.logger.Warn("supervised process did not start", "process", s.name, "error", err)
		return
	}
	s.mu.Lock()
	s.process = command.Process
	s.mu.Unlock()
	s.setState(agentwire.ResticTargetRunning, "")
	if s.onStart != nil {
		s.onStart(command.Process.Pid)
	}
	err := command.Wait()
	tail.flush()
	s.mu.Lock()
	s.process = nil
	s.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	s.setState(agentwire.ResticTargetError, exitReason(tail.lines()))
	s.logger.Warn("supervised process died", "process", s.name, "error", err, "last_lines", len(tail.lines()))
}

// startFailureReason : un binaire qui n'est plus là se dit à part.
func startFailureReason(err error) string {
	if errors.Is(err, fs.ErrNotExist) {
		return agentwire.ResticErrorBinaryMissing
	}
	return agentwire.ResticErrorStartFailed
}

// exitReason lit la fin de la sortie : un port déjà pris se dit à part.
func exitReason(lines []agentwire.OutputLine) string {
	for _, line := range lines {
		if strings.Contains(line.Line, "address already in use") {
			return agentwire.ResticErrorPortInUse
		}
	}
	return agentwire.ResticErrorStartFailed
}

func (s *processSupervisor) setState(state, reason string) {
	s.mu.Lock()
	changed := s.state != state || s.reason != reason
	s.state, s.reason = state, reason
	s.mu.Unlock()
	if changed && s.changed != nil {
		s.changed()
	}
}

// status rend l'état et la raison d'une erreur.
func (s *processSupervisor) status() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, s.reason
}

// reload envoie SIGHUP au processus qui tourne : rest-server relit son
// fichier htpasswd sans couper les sauvegardes en cours. Sans processus, il
// relira le fichier à son prochain lancement.
func (s *processSupervisor) reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.process == nil {
		return nil
	}
	return s.process.Signal(syscall.SIGHUP)
}
