package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

// fakeDocker écrit un exécutable de test à la place de /usr/bin/docker : il
// note ses arguments et son environnement à côté de lui, puis se comporte
// comme le dossier le lui dit. Rien ne lance Docker.
func fakeDocker(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	program := filepath.Join(dir, "docker")
	content := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > \"" + dir + "/args\"\n" +
		"env > \"" + dir + "/env\"\n" + script
	if err := os.WriteFile(program, []byte(content), 0o755); err != nil { // #nosec G306 -- un exécutable de test.
		t.Fatal(err)
	}
	return program
}

func readNote(t *testing.T, program, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(filepath.Dir(program), name))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

// L'environnement de Compose est remplacé, pas hérité : un secret ou un
// proxy de l'agent n'y passe pas.
func TestExecCompose_ReplacesTheEnvironment(t *testing.T) {
	t.Setenv("OPENCLOUD_TEST_SECRET", "ne-doit-pas-passer")
	program := fakeDocker(t, "echo 2.29.7\n")
	compose := execCompose{path: program, environment: composeEnvironment("/home/opencloud")}
	version, err := compose.Version(context.Background())
	if err != nil || version != "2.29.7" {
		t.Fatalf("version %q %v", version, err)
	}
	if got := readNote(t, program, "args"); got != "compose\nversion\n--short\n" {
		t.Errorf("version arguments %q", got)
	}
	assertComposeEnvironment(t, readNote(t, program, "env"), "HOME=/home/opencloud")

	if _, err := compose.Up(context.Background(), "/srv/workspace/nextcloud", "nextcloud"); err != nil {
		t.Fatal(err)
	}
	assertComposeEnvironment(t, readNote(t, program, "env"), "HOME=/home/opencloud")
}

// Sans dossier personnel connu, Compose n'a pas de HOME : il ne cherche pas
// les identifiants d'un autre compte, et l'environnement de l'agent ne passe pas
// non plus (un Env nul serait hérité).
func TestExecCompose_WithoutAHomeHasNoHome(t *testing.T) {
	t.Setenv("OPENCLOUD_TEST_SECRET", "ne-doit-pas-passer")
	t.Setenv("HOME", "/home/someone-else")
	program := fakeDocker(t, "echo 2.29.7\n")
	for name, compose := range map[string]execCompose{
		"empty home":        {path: program, environment: composeEnvironment("")},
		"environment unset": {path: program},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := compose.Version(context.Background()); err != nil {
				t.Fatal(err)
			}
			env := readNote(t, program, "env")
			assertComposeEnvironment(t, env, "")
			if strings.Contains(env, "HOME=") {
				t.Errorf("a HOME was passed:\n%s", env)
			}
		})
	}
}

func assertComposeEnvironment(t *testing.T, env, home string) {
	t.Helper()
	// Le shell de test ajoute ses propres variables (PWD, SHLVL, _) : on
	// vérifie ce qui compte, que les trois voulues sont là et que rien de
	// l'agent n'est passé.
	wanted := []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	if home != "" {
		wanted = append(wanted, home)
	}
	for _, want := range wanted {
		if !strings.Contains(env, want+"\n") {
			t.Errorf("the environment lacks %s:\n%s", want, env)
		}
	}
	if strings.Contains(env, "OPENCLOUD_TEST_SECRET") || strings.Contains(env, "ne-doit-pas-passer") {
		t.Errorf("the agent's environment leaked:\n%s", env)
	}
}

// Les arguments sont une liste fixe : le dossier est le seul qui varie.
func TestExecCompose_UpRunsTheProjectOfTheFolderOnly(t *testing.T) {
	program := fakeDocker(t, "echo 'Container a  Started'\necho 'une erreur' >&2\n")
	dir := "/srv/workspace/nextcloud"
	lines, err := execCompose{path: program}.Up(context.Background(), dir, "nextcloud")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"compose", "--project-directory", dir, "-f", dir + "/compose.yml", "-p", "nextcloud", "up", "-d", "--remove-orphans", "--pull", "missing",
	}, "\n") + "\n"
	if got := readNote(t, program, "args"); got != want {
		t.Errorf("arguments %q, want %q", got, want)
	}
	if len(lines) != 2 {
		t.Fatalf("lines %+v", lines)
	}
	streams := map[string]string{}
	for _, line := range lines {
		streams[line.Stream] = line.Line
	}
	if streams["stdout"] != "Container a  Started" || streams["stderr"] != "une erreur" {
		t.Errorf("lines %+v", lines)
	}
}

func TestExecCompose_AFailureKeepsTheEndOfTheOutput(t *testing.T) {
	program := fakeDocker(t, "i=1\nwhile [ $i -le 200 ]; do echo \"ligne $i\"; i=$((i+1)); done\nexit 1\n")
	lines, err := execCompose{path: program}.Up(context.Background(), "/srv/workspace/x", "x")
	if err == nil {
		t.Fatal("exit 1 was not an error")
	}
	if len(lines) != composeTailLines {
		t.Fatalf("%d lines kept, want %d", len(lines), composeTailLines)
	}
	var text []string
	for _, line := range lines {
		text = append(text, line.Line)
	}
	joined := "\n" + strings.Join(text, "\n") + "\n"
	if !strings.Contains(joined, "\nligne 200\n") || lines[0].Line != fmt.Sprintf("ligne %d", 200-composeTailLines+1) {
		t.Errorf("not the last %d lines: %v ... %v", composeTailLines, text[:2], text[len(text)-2:])
	}
}

func TestExecCompose_DockerMissingOrDeniedIsNotInstalled(t *testing.T) {
	_, err := execCompose{path: filepath.Join(t.TempDir(), "docker")}.Version(context.Background())
	if !errors.Is(err, errDockerMissing) {
		t.Fatalf("missing: %v", err)
	}
	program := fakeDocker(t, "exit 1\n")
	if _, err := (execCompose{path: program}).Version(context.Background()); err == nil || errors.Is(err, errDockerMissing) {
		t.Fatalf("docker present but compose failing: %v", err)
	}
}

func TestLineTail_KeepsTheLastLinesOfBothStreamsInOrder(t *testing.T) {
	tail := newLineTail(3)
	out, errw := tail.writer("stdout"), tail.writer("stderr")
	fmt.Fprint(out, "un\ndeux\n")
	fmt.Fprint(errw, "trois\nquatre")
	fmt.Fprint(out, "\r\ncinq\n")
	tail.flush()
	var got []string
	for _, line := range tail.lines() {
		got = append(got, line.Stream+":"+line.Line)
	}
	// « quatre » n'a pas de fin de ligne : il part au flush, après le reste.
	if strings.Join(got, ",") != "stderr:trois,stdout:cinq,stderr:quatre" {
		t.Fatalf("lines %v", got)
	}
}

// Une ligne que le serveur refuserait ferait rejeter le rapport entier : elles
// sortent toutes en UTF-8 valide, sans contrôle, de la taille permise.
func TestCleanLine_MakesALineTheServerAccepts(t *testing.T) {
	for name, line := range map[string]string{
		"octets invalides": strings.Repeat("\xff", 3000),
		"trop longue":      strings.Repeat("é", agentwire.MaxOutputLineBytes),
		"contrôles":        "a\x00b\x1b[31mc\x7f",
	} {
		cleaned := cleanLine(line)
		if len(cleaned) > agentwire.MaxOutputLineBytes || !utf8.ValidString(cleaned) {
			t.Errorf("%s: %d bytes, valid=%v", name, len(cleaned), utf8.ValidString(cleaned))
		}
		for _, r := range cleaned {
			if r < ' ' && r != '\t' || r == 0x7f {
				t.Errorf("%s: control character %q kept", name, r)
			}
		}
	}
	// Un résultat entier tient dans les bornes du protocole.
	tail := newLineTail(composeTailLines)
	writer := tail.writer("stdout")
	for range 500 {
		fmt.Fprintln(writer, strings.Repeat("x", 5000))
	}
	tail.flush()
	if got := len(tail.lines()); got != composeTailLines {
		t.Errorf("%d lines kept", got)
	}
	for _, line := range tail.lines() {
		if len(line.Line) > agentwire.MaxOutputLineBytes {
			t.Fatalf("a line of %d bytes", len(line.Line))
		}
	}
}

// Quelques lignes de l'agent plus la fin de Compose tiennent toujours dans
// ce qu'un rapport accepte.
func TestDeployService_ItsOutputFitsAReport(t *testing.T) {
	b := newDockerBench(t)
	for index := range 300 {
		b.compose.lines = append(b.compose.lines, agentwire.OutputLine{Stream: "stderr", Line: fmt.Sprintf("ligne %d", index)})
	}
	job := b.job(false)
	job.env, job.hasEnv = "A=1\n", true
	job.params.EnvSent = true
	output, err := b.tools.deployService(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if len(output) > agentwire.MaxOutputLines {
		t.Fatalf("%d lines in a result, at most %d", len(output), agentwire.MaxOutputLines)
	}
	if last := output[len(output)-1].Line; last != "ligne 299" {
		t.Errorf("the end of the output is lost: %q", last)
	}
}
