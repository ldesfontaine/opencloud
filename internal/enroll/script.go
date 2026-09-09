package enroll

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/catalog"
)

// Le shell qui joue le script : celui de son shebang, celui de toutes les
// actions.
const bashPath = "/bin/bash"

// Une ligne de script est une ligne d'étape ou de constat ; au-delà, ce n'est
// plus une ligne qu'on montre à l'opérateur.
const maxScriptLineBytes = 64 << 10

// Un enfant laissé derrière par le script tiendrait la sortie ouverte, et
// l'attente ne finirait jamais : passé ce délai, on ferme et on rend la main.
const scriptWaitDelay = 2 * time.Second

// Ce que le remède d'un refus porte en tête, écrit par refuse()
// (internal/scripts/lib.sh).
const scriptRemedyPrefix = "→ "

// Ce qui sépare un constat de sa cause : « résultat: refusé — … ».
const scriptCauseSeparator = " — "

// LocalScript est le script d'une action tel qu'il sera joué en root sur cette
// machine : le fichier à poser, les valeurs qui entrent par l'environnement, et
// le délai au-delà duquel on l'arrête.
type LocalScript struct {
	Content     []byte
	Environment []string
	Timeout     time.Duration
}

// ScriptRunner joue un script en root et rend son code de retour ; chaque
// ligne écrite passe par line, au fil de l'eau. Les tests en posent un faux :
// rien de l'amorçage ne s'exécute sur le poste de développement.
type ScriptRunner interface {
	Run(ctx context.Context, script LocalScript, line func(string)) (int, error)
}

// SystemScripts est la seule mise en œuvre réelle : le script est posé dans un
// dossier temporaire à root, joué par bash, environnement remplacé. Aucune
// valeur saisie ne passe par la ligne de commande — l'environnement tient ici
// le rôle qu'EnvironmentFile= tient pour une action lancée par le lanceur.
type SystemScripts struct{}

func (SystemScripts) Run(ctx context.Context, script LocalScript, line func(string)) (int, error) {
	name, cleanup, err := depositScript(script.Content)
	if err != nil {
		return 0, err
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(ctx, script.Timeout)
	defer cancel()

	command := exec.CommandContext(ctx, bashPath, "--", name) // #nosec G204 -- bounded: bash par constante, script posé par openCloud lui-même
	command.Env = append([]string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}, script.Environment...)
	command.Dir = "/"
	command.WaitDelay = scriptWaitDelay

	reader, writer := io.Pipe()
	defer reader.Close()
	command.Stdout = writer
	command.Stderr = writer

	if err := command.Start(); err != nil {
		return 0, fmt.Errorf("jouer %s : %w", bashPath, err)
	}
	finished := make(chan error, 1)
	go func() {
		waitErr := command.Wait()
		// Fermer le tuyau termine la lecture ; sans ça, elle attendrait pour rien.
		_ = writer.Close()
		finished <- waitErr
	}()

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(nil, maxScriptLineBytes)
	for scanner.Scan() {
		line(scanner.Text())
	}
	waitErr := <-finished

	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("lire la sortie du script : %w", err)
	}
	return exitCodeOf(ctx, waitErr)
}

// depositScript pose le script à root, hors de tout dossier partagé. 0600 :
// bash lit le fichier qu'on lui nomme, il n'a pas à être exécutable.
func depositScript(content []byte) (name string, cleanup func(), err error) {
	directory, err := os.MkdirTemp("", "opencloud-enroll-")
	if err != nil {
		return "", nil, fmt.Errorf("créer le dossier du script : %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(directory) }

	name = filepath.Join(directory, "run.sh")
	if err := os.WriteFile(name, content, 0o600); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("écrire %s : %w", name, err)
	}
	return name, cleanup, nil
}

// exitCodeOf : un code non nul n'est pas une erreur, c'est un constat que
// l'amorçage interprète (fait, refusé, échoué).
func exitCodeOf(ctx context.Context, waitErr error) (int, error) {
	var exitError *exec.ExitError
	switch {
	case waitErr == nil:
		return catalog.ExitDone, nil
	case ctx.Err() != nil:
		return 0, fmt.Errorf("le script d'enrôlement : %w", ctx.Err())
	case errors.As(waitErr, &exitError):
		return exitError.ExitCode(), nil
	default:
		return 0, fmt.Errorf("jouer le script d'enrôlement : %w", waitErr)
	}
}

// scriptOutcome relit ce que le script écrit : les lignes d'étape vont à
// l'opérateur, la ligne de constat est retenue — c'est l'amorçage qui écrit la
// sienne, une seule pour toute la séquence.
type scriptOutcome struct {
	out    io.Writer
	result string // « fait », « inchangé », « refusé — … », « échoué — … »
	remedy string
}

func (o *scriptOutcome) read(line string) {
	switch {
	case strings.HasPrefix(line, catalog.ResultPrefix):
		o.result = strings.TrimSpace(strings.TrimPrefix(line, catalog.ResultPrefix))
	case o.result != "" && strings.HasPrefix(line, scriptRemedyPrefix):
		o.remedy = strings.TrimSpace(strings.TrimPrefix(line, scriptRemedyPrefix))
	default:
		fmt.Fprintln(o.out, line)
	}
}

// cause rend ce que le script a nommé après son constat, ou le constat entier
// s'il n'a rien nommé.
func (o *scriptOutcome) cause() string {
	_, cause, found := strings.Cut(o.result, scriptCauseSeparator)
	if !found {
		return o.result
	}
	return cause
}

// environmentOf : les valeurs de l'action entrent par l'environnement, jamais
// par une ligne de commande — comme params.env pour une action lancée par le
// lanceur (15-catalogue-actions.md §4).
func environmentOf(prepared catalog.Prepared) []string {
	environment := make([]string, 0, len(prepared.Params))
	for _, spec := range prepared.Definition.Params {
		value, given := prepared.Params[spec.Name]
		if !given {
			continue
		}
		environment = append(environment, catalog.ParamEnvPrefix+strings.ToUpper(spec.Name)+"="+value)
	}
	return environment
}
