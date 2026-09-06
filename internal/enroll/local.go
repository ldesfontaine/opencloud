package enroll

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/transport"
)

// Les chemins du système, relatifs à la racine : la production monte « / »,
// les tests un dossier temporaire.
const (
	systemdMarkerPath = "run/systemd/system"
	passwdPath        = "etc/passwd"
	groupPath         = "etc/group"
	packagedLauncher  = "opt/opencloud/bin/oc-launch"
	launcherDir       = "usr/local/sbin"
	launcherName      = "oc-launch"
	sudoersDir        = "etc/sudoers.d"
	sudoersName       = "opencloud"
	sshdDropInDir     = "etc/ssh/sshd_config.d"
	sshdDropInName    = "opencloud.conf"
	// sshd -t exige ce dossier ; le service le crée en démarrant, mais sur une
	// machine où sshd n'a jamais tourné (socket activé, Ubuntu) il manque.
	sshdRuntimeDir    = "run/sshd"
	hostKeyDir        = "etc/ssh"
	sudoPath          = "usr/bin/sudo"
	visudoPath        = "usr/sbin/visudo"
	sshdPath          = "usr/sbin/sshd"
	usermodPath       = "usr/sbin/usermod"
	passwdCommandPath = "usr/bin/passwd" // #nosec G101 -- un chemin de binaire, pas un secret
	systemctlPath     = "usr/bin/systemctl"
	accountShell      = "/bin/sh"
	// Sans ce groupe, journalctl ne montre au compte que son propre journal :
	// le suivi d'une unité système ne verrait rien.
	journalGroupName   = "systemd-journal"
	sshServiceUnitName = "ssh.service"
)

// Le rechargement de sshd ferme le port un instant : on ne conclut pas à
// l'injoignable au premier essai.
const (
	probeAttempts = 3
	probeInterval = time.Second
)

// LocalTransport est ce que l'amorçage attend du transport : joindre la
// machine, et prouver que le lanceur répond derrière sudo.
type LocalTransport interface {
	Probe(ctx context.Context) error
	CheckLauncher(ctx context.Context) (transport.LauncherReply, error)
}

// Deps rassemble tout ce qu'EnrollLocal touche en dehors de lui-même. Les
// tests y mettent des dossiers temporaires, un faux exécuteur et un faux ssh.
type Deps struct {
	Root       *os.Root // le répertoire d'état, ouvert par l'appelant
	SystemRoot string   // « / » en production
	UID        int      // uid effectif : l'amorçage exige 0
	Out        io.Writer
	Commands   CommandRunner
	Transport  func(transport.Endpoint) LocalTransport
	SSHBinary  string
	Chown      func(name string, uid, gid int) error
	Now        func() time.Time
	ProbeWait  time.Duration
}

// SystemDeps est le câblage de production : la vraie racine, les vrais
// binaires, le vrai client SSH.
func SystemDeps(root *os.Root, out io.Writer) Deps {
	return Deps{
		Root:       root,
		SystemRoot: "/",
		UID:        os.Geteuid(),
		Out:        out,
		Commands:   SystemCommands{},
		Transport:  func(endpoint transport.Endpoint) LocalTransport { return transport.NewSSH(endpoint) },
		SSHBinary:  sshBinary,
		Chown:      os.Chown,
		Now:        time.Now,
		ProbeWait:  probeInterval,
	}
}

// EnrollLocal enrôle la machine openCloud sur elle-même, en root. Il écrit une
// ligne par étape, dit « inchangé » quand une étape n'a rien à faire, et se
// termine par un constat — comme un script d'action (15-catalogue-actions.md).
func EnrollLocal(ctx context.Context, deps Deps) error {
	run := &enrollment{deps: deps, steps: &steps{out: deps.Out}}

	err := run.play(ctx)
	switch {
	case err == nil:
		run.steps.result()
		return nil
	case isRefusal(err):
		fmt.Fprintf(deps.Out, "%s refusé\n", catalog.ResultPrefix)
		return err
	default:
		fmt.Fprintf(deps.Out, "%s échoué\n", catalog.ResultPrefix)
		return err
	}
}

func isRefusal(err error) bool {
	var refused refusal.Refusal
	return errors.As(err, &refused)
}

type enrollment struct {
	deps      Deps
	steps     *steps
	account   account
	publicKey string
}

// play tient l'ordre, et l'ordre est une propriété de sécurité : le programme,
// puis le compte, puis sudo, puis sshd, la clé en dernier (§3 du catalogue).
func (e *enrollment) play(ctx context.Context) error {
	if err := e.preflight(); err != nil {
		return err
	}
	for _, step := range []func(context.Context) error{
		e.installLauncher,
		e.prepareAccount,
		e.writeSudoers,
		e.configureSSHD,
		e.installKey,
		e.verifyRealPath,
		e.markEnrolled,
	} {
		if err := step(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (e *enrollment) systemPath(relative string) string {
	return filepath.Join(e.deps.SystemRoot, relative)
}

// commandPath : le chemin absolu réel du binaire. La racine de test redirige
// ce qu'on lit, jamais ce qu'on exécute — les tests ont un faux exécuteur.
func commandPath(relative string) string {
	return "/" + relative
}

// steps écrit ce que l'opérateur lit et retient si quelque chose a bougé.
type steps struct {
	out     io.Writer
	changed bool
}

func (s *steps) done(format string, args ...any) {
	s.changed = true
	fmt.Fprintf(s.out, "%s %s\n", catalog.StepPrefix, fmt.Sprintf(format, args...))
}

// note dit une étape qui ne change rien — une vérification, par exemple.
func (s *steps) note(format string, args ...any) {
	fmt.Fprintf(s.out, "%s %s\n", catalog.StepPrefix, fmt.Sprintf(format, args...))
}

func (s *steps) unchanged(format string, args ...any) {
	fmt.Fprintf(s.out, "%s %s — inchangé\n", catalog.StepPrefix, fmt.Sprintf(format, args...))
}

func (s *steps) result() {
	if s.changed {
		fmt.Fprintf(s.out, "%s fait\n", catalog.ResultPrefix)
		return
	}
	fmt.Fprintf(s.out, "%s inchangé\n", catalog.ResultPrefix)
}
