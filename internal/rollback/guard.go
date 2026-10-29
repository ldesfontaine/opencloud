package rollback

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

// Les codes de sortie que le lanceur rend à systemd, ceux de l'agent : 3 dit
// « relance-moi » (mise à jour, retour arrière), 2 « ne me relance pas ».
const (
	ExitRestart = 3
	ExitRefused = 2
	// Le code d'un arrêt anormal dont on ne connaît pas la cause : le binaire
	// n'a pas pu être lancé.
	exitFailed = 1
	// GuardVersion est la version du lanceur que ce code sait être : les agents
	// qui s'y fient la lisent dans l'environnement (brand.AgentGuardEnv).
	GuardVersion = 1
)

// Exit est la fin du binaire lancé. Code vaut -1 s'il a été tué par un signal.
type Exit struct {
	Code int
}

// Child est le binaire que le lanceur a démarré.
type Child interface {
	// Wait attend la fin du binaire.
	Wait() Exit
	// Stop demande l'arrêt (SIGTERM), puis tue le binaire s'il tarde.
	Stop()
}

// Launcher démarre le binaire. Une erreur dit qu'il ne s'est pas lancé du tout
// (absent, non exécutable) : c'est le cas d'un binaire mort-né.
type Launcher func(binary string, args, environment []string) (Child, error)

// Guard est le lanceur stable : une copie du binaire que l'agent ne remplace
// jamais, posée par root hors de bin/. Il lance bin/current sous le compte de
// l'agent, tient le marqueur et rebascule sur la version d'avant quand la
// nouvelle n'a pas confirmé à l'échéance, même si elle ne s'exécute pas : un
// agent cassé ne peut pas se réparer lui-même.
type Guard struct {
	// L'état de l'agent et son dossier bin/.
	State *os.Root
	Bin   *os.Root
	// Le binaire à lancer (bin/current, en chemin absolu) et ses arguments.
	Binary string
	Args   []string
	Launch Launcher
	Now    func() time.Time
	// After rend un canal qui reçoit à l'issue du délai ; remplaçable par les
	// tests, qui ne dorment pas.
	After  func(time.Duration) <-chan time.Time
	Logger *slog.Logger
}

// NewGuard monte le lanceur sur la vraie machine.
func NewGuard(state, bin *os.Root, binary string, args []string, logger *slog.Logger) *Guard {
	return &Guard{
		State: state, Bin: bin, Binary: binary, Args: args,
		Launch: ExecLauncher, Now: time.Now, After: time.After, Logger: logger,
	}
}

// Run lance le binaire une fois et rend le code de sortie que le lanceur doit
// rendre à systemd, qui le relance selon son Restart=. Toute la décision de
// retour arrière se prend ici, dans cet ordre : juger l'échéance avant de
// lancer, surveiller l'échéance pendant, juger la fin après.
func (g *Guard) Run(ctx context.Context) int {
	marker, present := g.loadMarker()
	if present {
		marker = g.judgeDue(marker)
	}
	startedAt := g.Now()
	child, err := g.Launch(g.Binary, g.Args, g.environment())
	if err != nil {
		g.Logger.Error("agent did not start", "error", err)
		return g.afterExit(Exit{Code: exitFailed}, startedAt)
	}
	outcome := g.watch(ctx, child, marker, present)
	switch {
	case outcome.interrupted:
		return 0
	case outcome.expired:
		// Le binaire ne confirme pas et ne s'arrête pas : arrêté, il est
		// rebasculé, et systemd relance sur la version d'avant.
		if current, found := g.loadMarker(); found {
			g.judgeDue(current)
		}
		return ExitRestart
	}
	return g.afterExit(outcome.exit, startedAt)
}

func (g *Guard) environment() []string {
	return append(os.Environ(), brand.AgentGuardEnv+"="+strconv.Itoa(GuardVersion))
}

type outcome struct {
	exit        Exit
	interrupted bool
	expired     bool
}

// watch attend la fin du binaire, un arrêt demandé par systemd, ou l'échéance
// d'une mise à jour que personne n'a confirmée. À l'échéance il relit le
// marqueur : un agent qui a confirmé l'a retiré, et on ne le tue pas.
func (g *Guard) watch(ctx context.Context, child Child, marker Marker, present bool) outcome {
	exited := make(chan Exit, 1)
	go func() { exited <- child.Wait() }()
	var deadline <-chan time.Time
	if present && marker.State == StatePending {
		deadline = g.After(marker.Deadline.Sub(g.Now()))
	}
	for {
		select {
		case exit := <-exited:
			return outcome{exit: exit}
		case <-ctx.Done():
			child.Stop()
			<-exited
			return outcome{interrupted: true}
		case <-deadline:
			deadline = nil
			current, found := g.loadMarker()
			if !found || !current.IsDue(g.Now()) {
				continue
			}
			g.Logger.Error("update not confirmed in time, stopping the agent", "target", current.Target)
			child.Stop()
			<-exited
			return outcome{expired: true}
		}
	}
}

// loadMarker lit le marqueur. Un marqueur illisible est écarté, bruyamment :
// sans échéance fiable, rien ne peut être jugé, et l'agent doit pouvoir
// tourner.
func (g *Guard) loadMarker() (Marker, bool) {
	marker, err := Load(g.State)
	switch {
	case err == nil:
		return marker, true
	case errors.Is(err, ErrNoMarker):
		return Marker{}, false
	}
	g.Logger.Error("update marker unreadable, ignored", "error", err)
	if removeErr := Remove(g.State); removeErr != nil {
		g.Logger.Error("remove unreadable update marker", "error", removeErr)
	}
	return Marker{}, false
}

// judgeDue juge un marqueur dont la confirmation n'est pas venue. Une mise à
// jour revient seule à la version d'avant, UNE fois ; un retour arrière
// demandé à la main n'est pas rattrapé, il le dit. Le marqueur rendu est
// celui qui reste écrit.
func (g *Guard) judgeDue(marker Marker) Marker {
	if !marker.IsDue(g.Now()) {
		return marker
	}
	if marker.Kind == KindRollback {
		g.Logger.Error("manual rollback not confirmed, no second recovery", "target", marker.Target, "previous", marker.Previous)
		return g.finish(marker, StateFailed, CauseUnconfirmed)
	}
	cause := CauseTimeout
	if marker.Crashes > 0 {
		cause = CauseCrash
	}
	if err := g.restorePrevious(marker); err != nil {
		g.Logger.Error("cannot return to the previous version", "previous", marker.Previous, "error", err)
		return g.finish(marker, StateFailed, CausePreviousMissing)
	}
	g.Logger.Error("update refused, returned to the previous version",
		"target", marker.Target, "previous", marker.Previous, "cause", string(cause))
	return g.finish(marker, StateRolledBack, cause)
}

// restorePrevious rebascule current sur le binaire de la version d'avant, s'il
// est encore là et exécutable.
func (g *Guard) restorePrevious(marker Marker) error {
	name := BinaryName(marker.Previous)
	info, err := g.Bin.Stat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fs.ErrPermission
	}
	return SwitchLink(g.Bin, brand.AgentCurrentLink, name)
}

func (g *Guard) finish(marker Marker, state State, cause Cause) Marker {
	marker.State, marker.Cause, marker.FinishedAt = state, cause, g.Now()
	if err := Save(g.State, marker); err != nil {
		g.Logger.Error("write update marker", "error", err)
	}
	return marker
}

// afterExit rend le code de sortie du lanceur, et note ce que la fin du
// binaire apprend au marqueur.
func (g *Guard) afterExit(exit Exit, startedAt time.Time) int {
	if exit.Code == ExitRestart || exit.Code == 0 {
		return exit.Code
	}
	marker, present := g.loadMarker()
	if !present {
		return exit.Code
	}
	ranFor := g.Now().Sub(startedAt)
	switch marker.State {
	case StatePending:
		return g.afterPendingExit(marker, exit)
	case StateRolledBack:
		// La version d'avant n'a pas tenu : la remise en état échoue, on
		// s'arrête là (code 2, systemd ne relance pas) plutôt que de boucler.
		if ranFor < SettleAfter {
			g.Logger.Error("the previous version fails too, giving up", "previous", marker.Previous, "code", exit.Code)
			g.finish(marker, StateFailed, CausePreviousCrash)
			return ExitRefused
		}
	case StateFailed:
		// L'échec est déjà dit (et sa cause gardée) : un binaire qui s'arrête
		// aussitôt ne relance rien.
		if ranFor < SettleAfter {
			return ExitRefused
		}
	}
	return exit.Code
}

// afterPendingExit compte l'arrêt anormal de la nouvelle version. Un refus (2)
// serait pris par systemd pour « ne relance pas » : avant l'échéance, ce n'est
// qu'un plantage de plus.
func (g *Guard) afterPendingExit(marker Marker, exit Exit) int {
	marker.Crashes++
	if err := Save(g.State, marker); err != nil {
		g.Logger.Error("write update marker", "error", err)
	}
	g.Logger.Error("the new version stopped before confirming", "target", marker.Target, "code", exit.Code, "crashes", marker.Crashes)
	if exit.Code == ExitRefused {
		return exitFailed
	}
	return exit.Code
}
