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
// les tests un dossier temporaire. Ceux de la séquence elle-même — sudoers,
// drop-in sshd, compte — vivent dans le script, et nulle part ailleurs.
const (
	passwdPath       = "etc/passwd"
	packagedLauncher = "opt/opencloud/bin/oc-launch"
	launcherDir      = "usr/local/sbin"
	launcherName     = "oc-launch"
	hostKeyDir       = "etc/ssh"
)

// Les constats qu'un script écrit, et que l'amorçage écrit comme lui
// (15-catalogue-actions.md §1).
const (
	resultDone      = "fait"
	resultUnchanged = "inchangé"
	resultRefused   = "refusé"
	resultFailed    = "échoué"
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
	Scripts    ScriptRunner
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
		Scripts:    SystemScripts{},
		Transport:  func(endpoint transport.Endpoint) LocalTransport { return transport.NewSSH(endpoint) },
		SSHBinary:  sshBinary,
		Chown:      os.Chown,
		Now:        time.Now,
		ProbeWait:  probeInterval,
	}
}

// EnrollLocal enrôle la machine openCloud sur elle-même, en root : il joue le
// script de l'action Enrôler — le même que sur une machine distante — et fait
// autour ce que ce script ne peut pas faire seul. Il écrit une ligne par
// étape, dit « inchangé » quand une étape n'a rien à faire, et se termine par
// un constat, comme un script d'action (15-catalogue-actions.md).
func EnrollLocal(ctx context.Context, deps Deps) error {
	run := &enrollment{deps: deps, steps: &steps{out: deps.Out}}

	err := run.play(ctx)
	switch {
	case err == nil:
		run.steps.result()
		return nil
	case isRefusal(err):
		fmt.Fprintf(deps.Out, "%s %s\n", catalog.ResultPrefix, resultRefused)
		return err
	default:
		fmt.Fprintf(deps.Out, "%s %s\n", catalog.ResultPrefix, resultFailed)
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

// play tient l'ordre. Le script porte celui de la séquence — le compte, puis
// sudo, puis sshd, la clé en dernier (§3 du catalogue) ; la paire de clés vient
// avant lui, puisqu'il pose la clé publique, et le lanceur après, comme sur une
// machine distante.
func (e *enrollment) play(ctx context.Context) error {
	if err := e.preflight(); err != nil {
		return err
	}
	for _, step := range []func(context.Context) error{
		e.prepareIdentity,
		e.playEnroler,
		e.installLauncher,
		e.writeKnownHosts,
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
	fmt.Fprintf(s.out, "%s %s — %s\n", catalog.StepPrefix, fmt.Sprintf(format, args...), resultUnchanged)
}

// markChanged retient qu'une étape jouée ailleurs — le script — a changé
// quelque chose : le constat final vaut pour toute la séquence.
func (s *steps) markChanged() {
	s.changed = true
}

func (s *steps) result() {
	if s.changed {
		fmt.Fprintf(s.out, "%s %s\n", catalog.ResultPrefix, resultDone)
		return
	}
	fmt.Fprintf(s.out, "%s %s\n", catalog.ResultPrefix, resultUnchanged)
}
