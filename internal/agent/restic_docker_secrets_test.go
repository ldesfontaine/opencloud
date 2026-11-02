package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

// La graine des mots de passe des bases : chaque variable des conteneurs en
// porte une variante. Elle ne doit sortir de nulle part.
const seededDatabaseSecret = "hunter2-graine-secrete"

// fakeDockerProgram écrit un faux docker qui joue le conteneur : « cp » rend
// un tar factice ; « exec » lance le script reçu avec sh, dans un
// environnement de conteneur qui porte les mots de passe semés et des outils
// de dump bavards (ils écrivent le mot de passe sur leur sortie d'erreur).
// Chaque appel note ses arguments (séparés par \x1f) et son environnement.
func fakeDockerProgram(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	tools := filepath.Join(dir, "bin")
	if err := os.Mkdir(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	pg := seededDatabaseSecret + "-pg"
	maria := seededDatabaseSecret + "-maria"
	// Une apostrophe : le fichier YAML de mongodump doit la doubler.
	mongo := seededDatabaseSecret + "-mongo'q"
	writeScript(t, filepath.Join(tools, "pg_dumpall"), `echo "pg_dumpall: password $PGPASSWORD refused" >&2
[ -f "`+dir+`/fail" ] && exit 2
[ "$PGPASSWORD" = "`+pg+`" ] && echo "-- authenticated dump"
`)
	// L'image n'a que mysqldump : le script de MariaDB s'y replie.
	writeScript(t, filepath.Join(tools, "mysqldump"), `echo "mysqldump: using password $MYSQL_PWD" >&2
[ "$MYSQL_PWD" = "`+maria+`" ] && echo "-- authenticated dump"
`)
	writeScript(t, filepath.Join(tools, "mongodump"), `config=""
while [ $# -gt 0 ]; do [ "$1" = "--config" ] && config="$2"; shift; done
cat "$config" >&2
[ "$(cat "$config")" = "password: '`+strings.ReplaceAll(mongo, "'", "''")+`'" ] && echo "authenticated archive"
`)
	containerEnv := `PATH="` + tools + `:/usr/bin:/bin" POSTGRES_PASSWORD='` + pg + `' MARIADB_ROOT_PASSWORD='` + maria +
		`' MONGO_INITDB_ROOT_USERNAME=root MONGO_INITDB_ROOT_PASSWORD="` + mongo + `"`
	program := filepath.Join(dir, "docker")
	writeScript(t, program, `printf '%s\037' "$@" >> "`+dir+`/calls"
printf '\n' >> "`+dir+`/calls"
env >> "`+dir+`/env"
case "$1" in
cp) printf 'tar of %s' "$2"; exit 0 ;;
exec) exec env -i `+containerEnv+` /bin/sh -c "$5" ;;
esac
echo "unexpected docker call" >&2
exit 97
`)
	return program
}

func writeScript(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(name, []byte("#!/bin/sh\n"+body), 0o755); err != nil { // #nosec G306 -- un exécutable de test.
		t.Fatal(err)
	}
}

// producerRestic joue restic --stdin-from-command : il lance le producteur
// avec son propre environnement, garde la sortie standard (le contenu
// sauvegardé) et recopie la sortie d'erreur, comme restic. Un producteur qui
// sort non nul ne fait pas d'instantané.
type producerRestic struct {
	contents []string
}

func (p *producerRestic) answer(call fakeCall, _ int, stdout, stderr io.Writer) int {
	index := slices.Index(call.args, "--")
	if !slices.Contains(call.args, "--stdin-from-command") {
		return writeSummary(stdout)
	}
	producer := call.args[index+1:]
	command := exec.Command(producer[0], producer[1:]...) // #nosec G204 -- le producteur du test.
	for name, value := range call.env {
		command.Env = append(command.Env, name+"="+value)
	}
	var content bytes.Buffer
	command.Stdout, command.Stderr = &content, stderr
	err := command.Run()
	p.contents = append(p.contents, content.String())
	if err != nil {
		_, _ = io.WriteString(stderr, "Fatal: unable to save snapshot: "+err.Error()+"\n")
		return 1
	}
	return writeSummary(stdout)
}

// Le banc de non-fuite de restic.docker : les mots de passe des bases vivent
// dans l'environnement des conteneurs (que le faux démon rend aussi à
// l'inspection) et des outils bavards les écrivent sur leur sortie d'erreur.
// Après une sauvegarde réussie puis une en échec, la graine n'est dans aucun
// résultat, aucune ligne, aucun rapport, aucun journal, aucun argument ni
// environnement passé à restic ou à docker, aucun fichier de l'état. Le faux
// docker n'est appelé qu'avec les arguments attendus.
func TestResticDocker_NeverLeaksDatabaseSecrets(t *testing.T) {
	b, _ := newServiceBench(t, "restic.docker")
	containers, volumes := testContainers(seededDatabaseSecret)
	client, _ := serveFakeDaemon(t, containers, volumes)
	b.runner.restic.source.inventory = client
	program := fakeDockerProgram(t)
	b.runner.restic.source.dockerProgram = program
	producers := &producerRestic{}
	b.restic.answer = producers.answer

	params := dockerBackup("nextcloud", []string{"app_data"}, []string{"db-1", "maria-1", "mongo-1"})
	var outputs []string
	succeeded := expectResult(t, b.run(t, agentwire.KindResticBackup, params), agentwire.ActionSucceeded, "")
	// Les scripts ont bien lu les mots de passe dans le conteneur : chaque
	// outil s'est authentifié.
	authenticated := 0
	for _, content := range producers.contents {
		if strings.Contains(content, "authenticated") {
			authenticated++
		}
	}
	if authenticated != 3 || !slices.Contains(producers.contents, "tar of app-1:/var/www/html/.") {
		t.Fatalf("producers gave %q", producers.contents)
	}

	if err := os.WriteFile(filepath.Join(filepath.Dir(program), "fail"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	failed := expectResult(t, b.run(t, agentwire.KindResticBackup, params), agentwire.ActionFailed, agentwire.ReasonDumpFailed)
	if failed.Restic == nil || failed.Restic.Parts[2].State != agentwire.ResticPartFailed || failed.Restic.Parts[3].State != agentwire.ResticPartOK {
		t.Fatalf("failed %+v", failed.Restic)
	}

	for _, result := range []agentwire.ActionResult{succeeded, failed} {
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, string(encoded))
	}
	outputs = append(outputs, b.logs.String(), readNote(t, program, "env"))
	for _, call := range b.restic.callsMade() {
		outputs = append(outputs, strings.Join(call.args, " "))
		for name, value := range call.env {
			outputs = append(outputs, name+"="+value)
		}
	}
	outputs = append(outputs, stateFiles(t, b.stateDir.Name())...)
	for _, output := range outputs {
		if strings.Contains(output, seededDatabaseSecret) {
			t.Errorf("a database password leaked into: %.300s", output)
		}
	}

	expected := [][]string{
		{"cp", "app-1:/var/www/html/.", "-"},
		{"exec", "db-1", "sh", "-c", restic.DumpScript(agentwire.EnginePostgres)},
		{"exec", "maria-1", "sh", "-c", restic.DumpScript(agentwire.EngineMariaDB)},
		{"exec", "mongo-1", "sh", "-c", restic.DumpScript(agentwire.EngineMongo)},
	}
	calls := strings.Split(strings.TrimSuffix(readNote(t, program, "calls"), "\n"), "\n")
	// Deux ordres, dont le second rejoue la base en échec : 4 + 4 + 1.
	if len(calls) != 9 {
		t.Fatalf("docker ran %d times", len(calls))
	}
	for _, call := range calls {
		args := strings.Split(strings.TrimSuffix(call, "\x1f"), "\x1f")
		if !slices.ContainsFunc(expected, func(want []string) bool { return slices.Equal(args, want) }) {
			t.Errorf("docker called with %q", args)
		}
		if strings.Contains(call, seededDatabaseSecret) {
			t.Error("a password in docker's arguments")
		}
	}
}

// stateFiles lit tous les fichiers de l'état de l'agent.
func stateFiles(t *testing.T, root string) []string {
	t.Helper()
	var contents []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		contents = append(contents, path+": "+string(content))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) == 0 {
		t.Fatal("the state holds no file")
	}
	return contents
}
