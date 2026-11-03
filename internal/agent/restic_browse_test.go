package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

const browseSnapshot = "3333333333333333333333333333333333333333333333333333333333333333"

// fakeBrowseBinary écrit un faux restic qui répond à snapshots et ls, note ce
// qu'il a reçu, et sème dans toutes ses sorties (standard, d'erreur et noms de
// fichiers voisins) des valeurs qui ne doivent jamais remonter.
func fakeBrowseBinary(t *testing.T, mode string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
printf '%s\n' "$@" >> ` + dir + `/args
env > ` + dir + `/env
cp "$RESTIC_PASSWORD_FILE" ` + dir + `/password
printf '%s' "$RESTIC_PASSWORD_FILE" > ` + dir + `/path
mode="` + mode + `"
echo "stderr: password $(cat "$RESTIC_PASSWORD_FILE")" >&2
case "$4" in
snapshots)
  [ "$mode" = missing ] && exit 10
  [ "$mode" = fails ] && exit 1
  echo '[{"id":"` + snapshotOlder + `","time":"2026-10-30T02:00:00Z","paths":["/srv/forgejo"],"hostname":"docker1","tags":["secret-tag"]},{"id":"` + browseSnapshot + `","time":"2026-11-01T02:00:00Z","paths":["/srv/forgejo","/etc/../x"]}]'; exit 0 ;;
ls)
  [ "$mode" = fails ] && exit 1
  echo '{"struct_type":"snapshot","id":"x","hostname":"docker1"}'
  echo '{"struct_type":"node","name":"srv","type":"dir","path":"/srv","size":0,"mtime":"2026-10-01T00:00:00Z","uid":0}'
  echo '{"struct_type":"node","name":"app.ini","type":"file","path":"/srv/app.ini","size":3400,"mtime":"2026-10-02T10:00:00Z","content":["abc"],"user":"git"}'
  echo '{"struct_type":"node","name":"data","type":"dir","path":"/srv/data","size":0,"mtime":"2026-10-03T00:00:00Z"}'
  echo '{"struct_type":"node","name":"deep.txt","type":"file","path":"/srv/data/deep.txt","size":1}'
  echo '{"struct_type":"node","name":"lien","type":"symlink","path":"/srv/lien","size":0,"linktarget":"/etc/shadow"}'
  echo '{"struct_type":"node","name":"..","type":"file","path":"/srv/..","size":0}'
  exit 0 ;;
esac
exit 2
`
	binary := filepath.Join(dir, "restic")
	if err := writeProgram(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary, dir
}

func browseBench(t *testing.T, mode string) (*targetBench, string) {
	t.Helper()
	b := newTargetBench(t, "restic.browse")
	expectResult(t, b.run(t, agentwire.KindResticTargetSetup, setupParams()), agentwire.ActionSucceeded, "")
	binary, noted := fakeBrowseBinary(t, mode)
	b.runner.checks.findRestic = func() (string, error) { return binary, nil }
	return b, noted
}

func (b *targetBench) browse(t *testing.T, params agentwire.ResticBrowseParams) []agentwire.ActionResult {
	t.Helper()
	params.User = resticUser
	return b.runWithSecret(t, agentwire.KindResticBrowse, params, agentwire.ResticBrowseSecret{RepoPassword: seededRepoPassword})
}

// Liste blanche : seuls l'identifiant, l'instant et les dossiers d'un
// instantané remontent, et par dossier, nom, nature, taille et date des fils
// directs. Le mot de passe, le nom d'hôte, les étiquettes, le propriétaire et
// la cible d'un lien restent à la cible : un banc de non-fuite les sème tous.
func TestResticBrowse_SnapshotsAndFolderLeaveOnlyTheAllowedFields(t *testing.T) {
	b, noted := browseBench(t, "ok")

	result := expectResult(t, b.browse(t, agentwire.ResticBrowseParams{}), agentwire.ActionSucceeded, "")
	if result.Browse == nil || len(result.Browse.Snapshots) != 2 || result.Browse.Snapshots[0].ID != browseSnapshot {
		t.Fatalf("snapshots, newest first: %+v", result.Browse)
	}
	if paths := result.Browse.Snapshots[0].Paths; len(paths) != 1 || paths[0] != "/srv/forgejo" {
		t.Fatalf("a path outside its form was kept: %v", paths)
	}
	listed := b.browse(t, agentwire.ResticBrowseParams{Snapshot: browseSnapshot, Path: "/srv"})
	folder := expectResult(t, listed, agentwire.ActionSucceeded, "")
	names := []string{}
	for _, entry := range folder.Browse.Entries {
		names = append(names, entry.Kind+":"+entry.Name)
	}
	if strings.Join(names, " ") != "dir:data dir:srv file:app.ini link:lien" && strings.Join(names, " ") != "dir:data file:app.ini link:lien" {
		t.Fatalf("direct children, folders first: %v", names)
	}
	for _, entry := range folder.Browse.Entries {
		if entry.Name == "deep.txt" || entry.Name == ".." || entry.Name == "srv" {
			t.Fatalf("%q is not a direct child of /srv", entry.Name)
		}
	}

	args := readNoted(t, noted, "args")
	for _, want := range []string{"snapshots", "ls", "--json", browseSnapshot, "/srv"} {
		if !strings.Contains(args, want) {
			t.Errorf("args lack %q:\n%s", want, args)
		}
	}
	if strings.Contains(args, seededRepoPassword) || strings.Contains(readNoted(t, noted, "env"), seededRepoPassword) {
		t.Fatal("the password reached an argument or the environment")
	}
	if _, err := os.Stat(readNoted(t, noted, "path")); !os.IsNotExist(err) {
		t.Fatal("the password file outlived the order")
	}
	everything := b.logs.String()
	for _, results := range [][]agentwire.ActionResult{b.browse(t, agentwire.ResticBrowseParams{}), listed} {
		for _, r := range results {
			encoded, _ := jsonOf(r)
			everything += encoded
		}
	}
	// « srv » est un nom légitime ; ce qui ne doit jamais sortir est le reste.
	for _, leaked := range []string{seededRepoPassword, "docker1", "secret-tag", "/etc/shadow", "git", "abc"} {
		if strings.Contains(everything, leaked) {
			t.Fatalf("%q reached the result or the journal:\n%s", leaked, everything)
		}
	}
}

func TestResticBrowse_NamesWhatWentWrong(t *testing.T) {
	b, _ := browseBench(t, "missing")
	expectResult(t, b.browse(t, agentwire.ResticBrowseParams{}), agentwire.ActionFailed, agentwire.ReasonRepoEmpty)
	b, _ = browseBench(t, "fails")
	failed := expectResult(t, b.browse(t, agentwire.ResticBrowseParams{}), agentwire.ActionFailed, agentwire.ReasonResticFailed)
	if failed.Browse != nil {
		t.Fatal("a failed read reports content")
	}
	for _, line := range failed.Output {
		if strings.Contains(line.Line, seededRepoPassword) {
			t.Fatalf("the password reached the output: %q", line.Line)
		}
	}
}

func TestResticBrowse_RefusesABadRequestWithoutRunningRestic(t *testing.T) {
	b, noted := browseBench(t, "ok")
	for _, params := range []agentwire.ResticBrowseParams{
		{Snapshot: "latest", Path: "/"}, {Snapshot: browseSnapshot, Path: "../etc"}, {Snapshot: browseSnapshot, Path: "/a/../b"}, {Path: "/srv"},
	} {
		expectResult(t, b.browse(t, params), agentwire.ActionFailed, agentwire.ReasonBadOrder)
	}
	expectResult(t, b.run(t, agentwire.KindResticBrowse, agentwire.ResticBrowseParams{User: resticUser}), agentwire.ActionFailed, agentwire.ReasonBadOrder)
	if _, err := os.Stat(filepath.Join(noted, "args")); !os.IsNotExist(err) {
		t.Fatal("restic ran for a refused order")
	}
}

// Le droit restic.browse se juge à l'ordre.
func TestResticBrowse_NeedsItsRight(t *testing.T) {
	b := newTargetBench(t)
	expectResult(t, b.browse(t, agentwire.ResticBrowseParams{}), agentwire.ActionRefused, agentwire.RightMissing("restic.browse"))
}

func jsonOf(v any) (string, error) {
	encoded, err := json.Marshal(v)
	return string(encoded), err
}
