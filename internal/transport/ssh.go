package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/actiondir"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

const (
	// Ce que ssh rend quand il n'a pas pu parler à la machine : la seule
	// valeur qu'il se réserve.
	sshUnreachableExitCode = 255

	// Sortie bornée : une machine bavarde ne remplit pas la mémoire d'openCloud.
	maxOutputBytes = 8 << 10

	connectTimeoutSeconds   = 10
	serverAliveIntervalSecs = 15

	// La commande que sudoers autorise pour poser le lanceur, à arguments fixes.
	installPath = "/usr/bin/install"
)

// ErrRefusedName : l'identifiant d'action, le nom de fichier ou le curseur ne
// peut pas apparaître dans une commande distante. Revalidé ici, jamais supposé.
var ErrRefusedName = errors.New("refused name")

// Endpoint est tout ce qu'il faut pour joindre une machine. Binary est
// /usr/bin/ssh en production ; les tests y mettent un faux.
type Endpoint struct {
	Address        string
	Port           int
	Account        string
	IdentityFile   string
	KnownHostsFile string
	Binary         string
}

// SSH est la seule mise en œuvre de Transport. Elle satisfait le contrat, et
// expose en plus CheckLauncher, dont l'amorçage a besoin.
type SSH struct {
	endpoint Endpoint
}

func NewSSH(endpoint Endpoint) *SSH {
	return &SSH{endpoint: endpoint}
}

func (s *SSH) Put(ctx context.Context, actionID, name string, content []byte, mode fs.FileMode) error {
	target, err := remotePath(actionID, name)
	if err != nil {
		return err
	}

	// Dépôt atomique : le contenu passe par stdin, jamais par la ligne de
	// commande, et le fichier n'apparaît sous son nom qu'entier.
	temporary := target + ".tmp"
	remote := fmt.Sprintf("umask 077 && mkdir -p -- %s && cat > %s && chmod %04o %s && mv -f -- %s %s",
		path.Dir(target), temporary, uint32(mode.Perm()), temporary, temporary, target)

	result, err := s.run(ctx, remote, bytes.NewReader(content))
	if err != nil {
		return err
	}
	if result.exitCode != 0 {
		return fmt.Errorf("deposit %s: code %d: %s", name, result.exitCode, result.output)
	}
	return nil
}

func (s *SSH) Launch(ctx context.Context, actionID string) error {
	if !actiondir.ValidID(actionID) {
		return fmt.Errorf("%w: %q", ErrRefusedName, actionID)
	}

	result, err := s.run(ctx, "sudo -n "+actiondir.LauncherPath+" "+actionID, nil)
	if err != nil {
		return err
	}
	if result.exitCode == 0 {
		return nil
	}
	return launchFailure(result)
}

// StagedLauncherPath : là où le compte opencloud dépose le lanceur avant que
// sudo le pose en root. La règle sudoers nomme ce chemin exactement.
const StagedLauncherPath = "/var/lib/opencloud/oc-launch.new"

// InstallLauncher dépose le lanceur par stdin puis le fait poser par sudo,
// avec la règle à arguments fixes que l'enrôlement a écrite. Le même chemin
// sert à le mettre à jour à chaque version.
func (s *SSH) InstallLauncher(ctx context.Context, binary []byte) error {
	temporary := StagedLauncherPath + ".tmp"
	remote := fmt.Sprintf("umask 077 && cat > %s && mv -f -- %s %s && "+
		"sudo -n %s -o root -g root -m 0755 %s %s && rm -f -- %s",
		temporary, temporary, StagedLauncherPath,
		installPath, StagedLauncherPath, actiondir.LauncherPath, StagedLauncherPath)

	result, err := s.run(ctx, remote, bytes.NewReader(binary))
	if err != nil {
		return err
	}
	if result.exitCode == 0 {
		return nil
	}
	if refused := sudoRefusal(result.output); refused != nil {
		return refused
	}
	return fmt.Errorf("%w: code %d: %s", ErrLaunchRefused, result.exitCode, result.output)
}

// LauncherReply est ce que le lanceur a répondu, sans interprétation :
// l'amorçage attend un code 2 du lanceur appelé sans argument.
type LauncherReply struct {
	ExitCode int
	Output   string
}

// CheckLauncher joue le vecteur de lancement sans identifiant. Hors du contrat
// Transport : c'est la preuve, par le chemin réel, que sudoers a pris.
func (s *SSH) CheckLauncher(ctx context.Context) (LauncherReply, error) {
	result, err := s.run(ctx, "sudo -n "+actiondir.LauncherPath, nil)
	if err != nil {
		return LauncherReply{}, err
	}
	return LauncherReply{ExitCode: result.exitCode, Output: result.output}, nil
}

func (s *SSH) Probe(ctx context.Context) error {
	result, err := s.run(ctx, "true", nil)
	if err != nil {
		return err
	}
	if result.exitCode != 0 {
		return fmt.Errorf("%w: code %d: %s", ErrUnreachable, result.exitCode, result.output)
	}
	return nil
}

// launchFailure nomme ce que le lanceur, sudo ou systemd-run ont refusé.
func launchFailure(result commandResult) error {
	if strings.Contains(result.output, "already exists") {
		return fmt.Errorf("%w: %s", ErrAlreadyLaunched, result.output)
	}
	if refused := sudoRefusal(result.output); refused != nil {
		return refused
	}
	return fmt.Errorf("%w: code %d: %s", ErrLaunchRefused, result.exitCode, result.output)
}

// sudoRefusal : un secret ne fabrique pas un terminal, les deux refus de sudo
// sont donc distincts. Rend nil quand la sortie ne vient pas de sudo.
func sudoRefusal(output string) error {
	switch {
	case strings.Contains(output, "a password is required"):
		return errors.Join(ErrLaunchRefused, refusal.Refusal{
			Cause:  "sudo demande un mot de passe au compte opencloud sur cette machine",
			Remedy: "rejouer l'enrôlement : la règle sudoers NOPASSWD vers /usr/local/sbin/oc-launch manque ou a été retirée",
		})
	case strings.Contains(output, "a terminal is required"):
		return errors.Join(ErrLaunchRefused, refusal.Refusal{
			Cause:  "sudo exige un terminal pour le compte opencloud sur cette machine",
			Remedy: "retirer requiretty de sudoers pour ce compte : openCloud n'ouvre jamais de terminal",
		})
	}
	return nil
}

// remotePath dérive le chemin du fichier sur la machine et refuse tout nom qui
// ne serait pas inoffensif dans la commande distante — elle passe par le shell
// du compte, donc seules des formes étroites y entrent.
func remotePath(actionID, name string) (string, error) {
	if !actiondir.ValidID(actionID) {
		return "", fmt.Errorf("%w: %q", ErrRefusedName, actionID)
	}
	if !validRemoteName(name) {
		return "", fmt.Errorf("%w: %q", ErrRefusedName, name)
	}
	return path.Join(actiondir.Dir(actionID), name), nil
}

// Un composant de chemin commence par une lettre ou un chiffre : ni « - » qui
// deviendrait une option, ni « .. » qui remonterait, ni espace ni guillemet.
var remoteComponentPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func validRemoteName(name string) bool {
	switch name {
	case actiondir.ScriptName, actiondir.ParamsName, actiondir.TimeoutName:
		return true
	}

	rest, found := strings.CutPrefix(name, actiondir.FilesDirName+"/")
	if !found || rest == "" {
		return false
	}
	for _, component := range strings.Split(rest, "/") {
		if component == "." || component == ".." || !remoteComponentPattern.MatchString(component) {
			return false
		}
	}
	return true
}

// arguments est une liste positive : chaque option retire quelque chose. Rien
// n'est hérité de l'environnement de l'appelant, et rien n'est appris.
func (s *SSH) arguments(remoteCommand string) []string {
	return []string{
		"-F", "/dev/null",
		"-o", "IdentitiesOnly=yes",
		"-i", s.endpoint.IdentityFile,
		"-o", "UserKnownHostsFile=" + s.endpoint.KnownHostsFile,
		"-o", "StrictHostKeyChecking=yes",
		"-o", "BatchMode=yes",
		"-o", "ClearAllForwardings=yes",
		"-o", "RequestTTY=no",
		"-o", "ConnectTimeout=" + strconv.Itoa(connectTimeoutSeconds),
		"-o", "ServerAliveInterval=" + strconv.Itoa(serverAliveIntervalSecs),
		"-o", "LogLevel=ERROR",
		"-p", strconv.Itoa(s.endpoint.Port),
		"-l", s.endpoint.Account,
		s.endpoint.Address,
		"--",
		remoteCommand,
	}
}

// command prépare ssh : environnement remplacé, dossier neutre, aucun shell
// local. Le vecteur ne porte que des constantes et des valeurs revalidées.
func (s *SSH) command(ctx context.Context, remoteCommand string) *exec.Cmd {
	// #nosec G204 -- bounded: binaire de l'endpoint, options constantes, et une
	// commande distante bâtie de gabarits où seuls un identifiant et un curseur
	// revalidés varient.
	command := exec.CommandContext(ctx, s.endpoint.Binary, s.arguments(remoteCommand)...)
	command.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
	command.Dir = "/"
	return command
}

type commandResult struct {
	exitCode int
	output   string
}

// run joue une commande brève et rend son code. Une machine injoignable n'est
// pas un échec de l'action : elle a son erreur à elle.
func (s *SSH) run(ctx context.Context, remoteCommand string, input io.Reader) (commandResult, error) {
	command := s.command(ctx, remoteCommand)
	command.Stdin = input

	output := &boundedBuffer{limit: maxOutputBytes}
	command.Stdout = output
	command.Stderr = output

	err := command.Run()
	text := strings.TrimSpace(output.String())

	var exitError *exec.ExitError
	switch {
	case err == nil:
		return commandResult{exitCode: 0, output: text}, nil
	case ctx.Err() != nil:
		return commandResult{}, ctx.Err()
	case errors.As(err, &exitError):
		if exitError.ExitCode() == sshUnreachableExitCode {
			return commandResult{}, fmt.Errorf("%w: %s", ErrUnreachable, text)
		}
		return commandResult{exitCode: exitError.ExitCode(), output: text}, nil
	default:
		return commandResult{}, fmt.Errorf("run ssh: %w", err)
	}
}

// boundedBuffer garde les premiers octets et jette le reste : la sortie d'une
// machine est une entrée non fiable, sa taille comprise.
type boundedBuffer struct {
	limit    int
	buffer   bytes.Buffer
	overflow bool
}

func (b *boundedBuffer) Write(chunk []byte) (int, error) {
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.overflow = true
		return len(chunk), nil
	}
	if len(chunk) > remaining {
		b.buffer.Write(chunk[:remaining])
		b.overflow = true
		return len(chunk), nil
	}
	b.buffer.Write(chunk)
	return len(chunk), nil
}

func (b *boundedBuffer) String() string {
	if b.overflow {
		return b.buffer.String() + "… (sortie tronquée)"
	}
	return b.buffer.String()
}
