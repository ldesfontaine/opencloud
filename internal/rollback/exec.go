package rollback

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// stopGrace est le temps laissé au binaire pour s'arrêter après SIGTERM.
const stopGrace = 10 * time.Second

type execChild struct {
	command *exec.Cmd
}

// ExecLauncher démarre le binaire sous le compte du lanceur, avec sa sortie
// sur celle du lanceur (le journal de l'unité). Aucun shell : un chemin et des
// arguments.
func ExecLauncher(binary string, args, environment []string) (Child, error) {
	// bounded: binary est bin/current sous l'état de l'agent, args vient de la ligne de l'unité.
	command := exec.Command(binary, args...) // #nosec G204 -- chemin posé par l'unité, jamais une entrée du réseau
	command.Env = environment
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return nil, err
	}
	return &execChild{command: command}, nil
}

func (c *execChild) Wait() Exit {
	err := c.command.Wait()
	var failed *exec.ExitError
	switch {
	case err == nil:
		return Exit{Code: 0}
	case errors.As(err, &failed):
		return Exit{Code: failed.ExitCode()}
	}
	return Exit{Code: exitFailed}
}

func (c *execChild) Stop() {
	if err := c.command.Process.Signal(syscall.SIGTERM); err != nil {
		return
	}
	time.AfterFunc(stopGrace, func() { _ = c.command.Process.Kill() })
}
