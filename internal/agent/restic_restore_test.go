package agent

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

const restoreStamp = "20260912-140000"

type restoreBench struct {
	*resticBench
	area string
}

// restoreBenchFor : une source liée à une cible qui répond, avec restic.restore
// et files.write sur un dossier de travail. Le faux restic « restaure » le
// contenu semé là où on le lui dit (--target), sous le chemin demandé.
func restoreBenchFor(t *testing.T, content string) *restoreBench {
	t.Helper()
	area, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := newResticBench(t, "restic.produce", "restic.restore", "files.write="+area)
	port, certificatePEM := reachableTarget(t)
	b.provision(t, "127.0.0.1", port, certificatePEM)
	b.restic.answer = func(call fakeCall, _ int, _, stderr io.Writer) int {
		// Un restic bavard : il recopie le mot de passe qu'il a lu, comme le ferait un échec.
		password, _ := os.ReadFile(call.env["RESTIC_PASSWORD_FILE"])
		_, _ = io.WriteString(stderr, "warning: password "+string(password)+"\n")
		target := call.args[slices.Index(call.args, "--target")+1]
		include := strings.NewReplacer(`\*`, `*`, `\?`, `?`, `\[`, `[`, `\\`, `\`).Replace(call.args[slices.Index(call.args, "--include")+1])
		if content == "" {
			return 0
		}
		restored := filepath.Join(target, include)
		if err := os.MkdirAll(filepath.Dir(restored), 0o700); err != nil {
			return 1
		}
		if strings.HasSuffix(include, "dossier") {
			_ = os.MkdirAll(restored, 0o700)
			_ = os.WriteFile(filepath.Join(restored, "dedans"), []byte(content), 0o600)
			return 0
		}
		_ = os.WriteFile(restored, []byte(content), 0o600)
		return 0
	}
	return &restoreBench{resticBench: b, area: area}
}

func (b *restoreBench) original(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(b.area, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func (b *restoreBench) restore(t *testing.T, path string) []agentwire.ActionResult {
	t.Helper()
	return b.run(t, agentwire.KindResticRestore, agentwire.ResticRestoreParams{Target: resticTargetID, Snapshot: testSnapshotID, Path: path, Stamp: restoreStamp})
}

func readText(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

// La copie revient à côté de l'original, sous son nom horodaté ; l'original n'a
// pas bougé, aucun dossier temporaire ne reste, restic n'a posé aucun verrou
// (--no-lock : aucune écriture ni DELETE sur le dépôt) et le mot de passe du
// dépôt, semé, n'est dans aucune sortie ni dans le journal.
func TestResticRestore_PutsTheCopyBesideTheOriginalAndNothingElse(t *testing.T) {
	b := restoreBenchFor(t, "contenu restauré")
	original := b.original(t, "app.ini", "contenu actuel")

	results := b.restore(t, original)
	result := expectResult(t, results, agentwire.ActionSucceeded, "")

	destination := original + ".restaure-" + restoreStamp
	if readText(t, destination) != "contenu restauré" || readText(t, original) != "contenu actuel" {
		t.Fatal("the copy is not beside the original, or the original changed")
	}
	entries, _ := os.ReadDir(b.area)
	if len(entries) != 2 {
		t.Fatalf("only the original and the copy may exist, found %v", entries)
	}
	calls := b.restic.callsMade()
	if len(calls) != 1 {
		t.Fatalf("restic ran %d times", len(calls))
	}
	args := calls[0].args
	for _, want := range []string{"restore", "--no-lock", "--target", "--include", testSnapshotID} {
		if !slices.Contains(args, want) {
			t.Errorf("args lack %q: %v", want, args)
		}
	}
	if slices.Contains(args, "--delete") || slices.Contains(args, "--overwrite") {
		t.Fatalf("restic was asked to delete or overwrite: %v", args)
	}
	everything := b.logs.String()
	for _, r := range results {
		encoded, _ := json.Marshal(r)
		everything += string(encoded)
	}
	for _, leaked := range []string{seededRepoPassword, seededRESTPassword} {
		if strings.Contains(everything, leaked) {
			t.Fatalf("%q reached a result or the journal:\n%s", leaked, everything)
		}
	}
	if !strings.Contains(everything, "restored "+destination) || result.Reason != "" {
		t.Fatalf("the result does not name the destination:\n%s", everything)
	}
}

func TestResticRestore_RestoresAFolderBesideItToo(t *testing.T) {
	b := restoreBenchFor(t, "x")
	folder := filepath.Join(b.area, "dossier")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	expectResult(t, b.restore(t, folder), agentwire.ActionSucceeded, "")
	if readText(t, folder+".restaure-"+restoreStamp+"/dedans") != "x" {
		t.Fatal("the folder was not restored beside the original")
	}
}

// Une destination qui existe est un refus net : ni écrasement, ni suffixe
// numéroté en silence, et restic ne part pas.
func TestResticRestore_RefusesADestinationThatExists(t *testing.T) {
	b := restoreBenchFor(t, "contenu restauré")
	original := b.original(t, "app.ini", "contenu actuel")
	destination := original + ".restaure-" + restoreStamp
	if err := os.WriteFile(destination, []byte("déjà là"), 0o600); err != nil {
		t.Fatal(err)
	}
	expectResult(t, b.restore(t, original), agentwire.ActionRefused, agentwire.ReasonPathExists)
	if readText(t, destination) != "déjà là" || readText(t, original) != "contenu actuel" {
		t.Fatal("a file was overwritten")
	}
	entries, _ := os.ReadDir(b.area)
	if len(entries) != 2 || len(b.restic.callsMade()) != 0 {
		t.Fatalf("nothing may be created or run: %v, %d calls", entries, len(b.restic.callsMade()))
	}
}

// Un dossier déjà là, ou un lien pendant (qui ne mène nulle part, mais
// qu'écrire suivrait), ne reçoit jamais la copie : refusé sans rien lancer.
func TestResticRestore_RefusesAnExistingFolderAndADanglingLink(t *testing.T) {
	b := restoreBenchFor(t, "contenu restauré")
	original := b.original(t, "app.ini", "contenu actuel")
	destination := original + ".restaure-" + restoreStamp
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	expectResult(t, b.restore(t, original), agentwire.ActionRefused, agentwire.ReasonPathExists)
	if err := os.Remove(destination); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(b.area, "n-existe-pas"), destination); err != nil {
		t.Fatal(err)
	}
	expectResult(t, b.restore(t, original), agentwire.ActionRefused, agentwire.ReasonPathExists)
	if len(b.restic.callsMade()) != 0 || readText(t, original) != "contenu actuel" {
		t.Fatal("a refused restore ran restic or touched the original")
	}
}

// Hors du droit (files.write), par un lien symbolique ou hors forme : refusé
// avant toute écriture.
func TestResticRestore_RefusesAPathOutsideWhatTheAgentMayWrite(t *testing.T) {
	b := restoreBenchFor(t, "x")
	elsewhere, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(b.area, "sortie")
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Fatal(err)
	}
	expectResult(t, b.restore(t, elsewhere+"/passwd"), agentwire.ActionRefused, agentwire.ReasonPathOutsideRight)
	expectResult(t, b.restore(t, link+"/passwd"), agentwire.ActionRefused, agentwire.ReasonPathOutsideRight)
	for _, path := range []string{"/", "/a/../b", "relatif", b.area + "/a//b", b.area + "/a/"} {
		expectResult(t, b.restore(t, path), agentwire.ActionFailed, agentwire.ReasonBadOrder)
	}
	if len(b.restic.callsMade()) != 0 {
		t.Fatal("restic ran for a refused order")
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatalf("something was written outside: %v", entries)
	}
}

// Un chemin que l'instantané n'a pas : échec dit, rien d'écrit, le dossier
// temporaire de l'agent est retiré. Un échec de restic ne laisse rien non plus.
func TestResticRestore_NamesWhatWentWrongAndLeavesNothingBehind(t *testing.T) {
	b := restoreBenchFor(t, "")
	original := b.original(t, "app.ini", "contenu actuel")
	result := expectResult(t, b.restore(t, original), agentwire.ActionFailed, agentwire.ReasonPathNotInSnapshot)
	for _, line := range result.Output {
		if strings.Contains(line.Line, seededRepoPassword) {
			t.Fatalf("the password reached the output: %q", line.Line)
		}
	}
	b.restic.answer = func(_ fakeCall, _ int, _, stderr io.Writer) int {
		_, _ = io.WriteString(stderr, "Fatal: password "+seededRepoPassword+"\n")
		return 1
	}
	failed := expectResult(t, b.restore(t, original), agentwire.ActionFailed, agentwire.ReasonRestoreFailed)
	for _, line := range failed.Output {
		if strings.Contains(line.Line, seededRepoPassword) {
			t.Fatalf("the password reached the output: %q", line.Line)
		}
	}
	entries, _ := os.ReadDir(b.area)
	if len(entries) != 1 || readText(t, original) != "contenu actuel" {
		t.Fatalf("a failed restore left something: %v", entries)
	}
}

func TestResticRestore_NeedsItsRightAndTakesNoSecret(t *testing.T) {
	area, _ := filepath.EvalSymlinks(t.TempDir())
	without := newResticBench(t, "files.write="+area)
	expectResult(t, without.run(t, agentwire.KindResticRestore, agentwire.ResticRestoreParams{Target: resticTargetID, Snapshot: testSnapshotID, Path: area + "/a", Stamp: restoreStamp}), agentwire.ActionRefused, agentwire.RightMissing("restic.restore"))

	b := restoreBenchFor(t, "x")
	original := b.original(t, "app.ini", "contenu actuel")
	params := agentwire.ResticRestoreParams{Target: resticTargetID, Snapshot: testSnapshotID, Path: original, Stamp: restoreStamp}
	expectResult(t, b.runWithSecret(t, agentwire.KindResticRestore, params, agentwire.ResticVerifySecret{RepoPassword: seededRepoPassword}), agentwire.ActionFailed, agentwire.ReasonBadOrder)
	if len(b.restic.callsMade()) != 0 {
		t.Fatal("restic ran with an order that carried a secret")
	}
}
