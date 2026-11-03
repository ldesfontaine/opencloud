package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

// Ce que l'agent tient pour restic, cible comme source, sous resticDir de
// son état (0700) : la configuration, le certificat et le plancher de
// rétention de rest-server, le fichier htpasswd et le mot de passe REST de
// chaque source (pour le relais) ; par cible liée, ce que la source sait
// d'elle et ses deux mots de passe, reçus du serveur. Rien de ce dossier ne
// quitte la machine, sauf le certificat public.
const (
	resticDir         = "restic"
	resticDirMode     = 0o700
	resticFileMode    = 0o600
	resticProgram     = "restic"
	restServerProgram = "rest-server"
	// Un mot de passe relu ne dépasse pas ceci ; au-delà, ce n'est pas le nôtre.
	maxSecretFileBytes = 256
	// La version d'un binaire se lit sous ce délai.
	binaryVersionTimeout = 5 * time.Second
	maxVersionBytes      = 64
)

// resticTools est ce que les ordres restic touchent : l'état de l'agent, la
// cible (rest-server) et la source (restic). Les tests remplacent le
// lanceur de restic et le mandataire du relais.
type resticTools struct {
	stateDir *os.Root
	logger   *slog.Logger
	target   *resticTarget
	source   *resticSource
	// changed réveille la boucle du rapport : un ordre ou rest-server a
	// changé ce qu'elle dirait.
	changed chan struct{}
}

func newResticTools(stateDir *os.Root, logger *slog.Logger) *resticTools {
	tools := &resticTools{stateDir: stateDir, logger: logger, changed: make(chan struct{}, 1)}
	tools.target = newResticTarget(stateDir, logger, tools.nudge)
	tools.source = newResticSource(stateDir, logger)
	return tools
}

// nudge demande un rapport neuf sans attendre la période.
func (t *resticTools) nudge() {
	select {
	case t.changed <- struct{}{}:
	default:
	}
}

// findBinary cherche un programme au chemin déclaré par l'opérateur, sinon
// dans le PATH de l'agent, et rend un chemin absolu : restic.Command n'en
// accepte pas d'autre. L'agent ne télécharge jamais rien.
func findBinary(declared, program string, lookPath func(string) (string, error)) (string, error) {
	candidate := program
	if declared != "" {
		candidate = declared
	}
	found, err := lookPath(candidate)
	if err != nil {
		return "", fmt.Errorf("find %s: %w", program, err)
	}
	absolute, err := filepath.Abs(found)
	if err != nil {
		return "", fmt.Errorf("find %s: %w", program, err)
	}
	return absolute, nil
}

// readBinaryVersion lance « <binaire> <argument> » sous délai et rend sa
// première ligne, nettoyée et bornée : la fiche l'affiche telle quelle.
func readBinaryVersion(ctx context.Context, binary, argument string) string {
	ctx, cancel := context.WithTimeout(ctx, binaryVersionTimeout)
	defer cancel()
	// bounded: le binaire est celui que findBinary a trouvé, l'argument une constante.
	command := exec.CommandContext(ctx, binary, argument) // #nosec G204 -- bounded: voir ci-dessus.
	command.Env = producerEnvironment
	output, err := command.Output()
	if err != nil {
		return ""
	}
	first, _, _ := strings.Cut(string(output), "\n")
	return boundedText(cleanLine(first), maxVersionBytes)
}

// boundedText coupe un texte ASCII à limit octets ; un texte qui ne l'est
// pas est rendu vide, plutôt que coupé au milieu d'un caractère.
func boundedText(text string, limit int) string {
	for _, char := range text {
		if char > 0x7e {
			return ""
		}
	}
	if len(text) > limit {
		return text[:limit]
	}
	return text
}

// openResticDir ouvre le dossier restic de l'état, et le crée s'il manque. Un
// lien à sa place est refusé : os.Root suivrait un lien resté sous l'état.
func openResticDir(stateDir *os.Root) (*os.Root, error) {
	if err := stateDir.Mkdir(resticDir, resticDirMode); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("create restic dir: %w", err)
	}
	info, err := stateDir.Lstat(resticDir)
	if err != nil {
		return nil, fmt.Errorf("stat restic dir: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", resticDir)
	}
	return stateDir.OpenRoot(resticDir)
}

// absoluteStatePath rend le chemin absolu d'un fichier de l'état : rest-server
// et restic le lisent eux-mêmes, hors de l'os.Root de l'agent.
func absoluteStatePath(stateDir *os.Root, parts ...string) (string, error) {
	base, err := filepath.Abs(stateDir.Name())
	if err != nil {
		return "", fmt.Errorf("state dir path: %w", err)
	}
	return filepath.Join(append([]string{base}, parts...)...), nil
}

// resticRunner lance une commande restic, sans shell : le code de sortie, ou
// une erreur si elle n'a pas pu se lancer ou finir. Les tests en donnent un
// faux ; jamais un vrai restic.
type resticRunner interface {
	run(ctx context.Context, command restic.Command, stdout, stderr io.Writer) (int, error)
}

// Un arrêt laisse ce temps à restic pour libérer son verrou (limite n° 4).
const resticStopDelay = 15 * time.Second

type execResticRunner struct{}

func (execResticRunner) run(ctx context.Context, command restic.Command, stdout, stderr io.Writer) (int, error) {
	// bounded: le binaire est celui de findBinary, les arguments ceux de
	// restic.BackupDirectory ou BackupStream (chemins jugés, producteur
	// absolu), l'environnement remplacé ; les secrets sont des fichiers.
	process := exec.CommandContext(ctx, command.Binary, command.Args...) // #nosec G204 -- bounded: voir ci-dessus.
	process.Env = command.Env
	process.Stdout = stdout
	process.Stderr = stderr
	// Un arrêt demande poliment : restic retire son verrou sur SIGTERM.
	process.Cancel = func() error { return process.Process.Signal(syscall.SIGTERM) }
	process.WaitDelay = resticStopDelay
	err := process.Run()
	var exited *exec.ExitError
	if errors.As(err, &exited) && ctx.Err() == nil {
		return exited.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

// summaryWriter ne garde de la sortie de restic --json que le résumé : les
// autres lignes (progression, fichiers) ne vont nulle part.
type summaryWriter struct {
	line    []byte
	summary restic.BackupSummary
	found   bool
}

// Une ligne de résumé tient en moins de 4 Kio ; au-delà, ce n'en est pas une.
const maxSummaryLine = 64 << 10

func (w *summaryWriter) Write(data []byte) (int, error) {
	for _, b := range data {
		if b == '\n' {
			w.scan()
			continue
		}
		if len(w.line) < maxSummaryLine {
			w.line = append(w.line, b)
		}
	}
	return len(data), nil
}

func (w *summaryWriter) scan() {
	if summary, ok := restic.ParseBackupSummary(w.line); ok {
		w.summary, w.found = summary, true
	}
	w.line = w.line[:0]
}

// result lit la dernière ligne si elle n'était pas finie.
func (w *summaryWriter) result() (restic.BackupSummary, bool) {
	w.scan()
	return w.summary, w.found
}

// resticOutput est une ligne de sortie de l'agent pour un ordre restic.
func resticOutput(line string) agentwire.OutputLine {
	return stdout(cleanLine(line))
}
