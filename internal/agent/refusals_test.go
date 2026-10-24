package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/brand"
)

// Les refus que le compte de l'agent impose : chacun doit tomber avant la
// première écriture, avec la raison lisible, et rien ne doit bouger sur la
// machine. Chaque test échoue sans le contrôle qu'il garde.

func deniedDocker(context.Context) string { return agentwire.ReasonDockerDenied }

func deniedPath(string) string { return agentwire.ReasonPathDenied }

// assertUntouched : la racine des services n'a même pas été créée, Compose n'a
// rien lancé.
func (b *dockerBench) assertUntouched(t *testing.T) {
	t.Helper()
	if _, err := os.Lstat(b.root); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the services root was created: %v", err)
	}
	if len(b.compose.upDirs) != 0 {
		t.Errorf("compose ran in %v", b.compose.upDirs)
	}
}

func TestDeployService_RefusesBeforeWritingWhenTheAccountCannotUseDocker(t *testing.T) {
	for name, update := range map[string]bool{"deploy": false, "update": true} {
		t.Run(name, func(t *testing.T) {
			b := newDockerBench(t)
			b.tools.checks.docker = deniedDocker
			output, err := b.tools.deployService(context.Background(), b.job(update))
			if reason := deployReasonOf(t, err); reason != agentwire.ReasonDockerDenied {
				t.Fatalf("got %v", err)
			}
			if len(output) != 0 {
				t.Errorf("output %v", output)
			}
			b.assertUntouched(t)
			result := dockerResultOf("run1", nil, err)
			if result.State != agentwire.ActionRefused || result.Reason != agentwire.ReasonDockerDenied {
				t.Errorf("result %+v", result)
			}
		})
	}
}

func TestDeployService_RefusesBeforeWritingWhenTheAccountDoesNotOwnTheRoot(t *testing.T) {
	b := newDockerBench(t)
	var asked []string
	b.tools.checks.writable = func(path string) string {
		asked = append(asked, path)
		return agentwire.ReasonPathDenied
	}
	_, err := b.tools.deployService(context.Background(), b.job(false))
	if reason := deployReasonOf(t, err); reason != agentwire.ReasonPathDenied {
		t.Fatalf("got %v", err)
	}
	if len(asked) != 1 || asked[0] != b.root {
		t.Errorf("the check was asked about %v, not the services root %s", asked, b.root)
	}
	b.assertUntouched(t)
	result := dockerResultOf("run1", nil, err)
	if result.State != agentwire.ActionRefused || result.Reason != agentwire.ReasonPathDenied {
		t.Errorf("result %+v", result)
	}
}

// path_unwritable jugé avant d'écrire est un refus ; le même mot après une
// écriture ratée reste un échec (le dossier a pu être touché).
func TestDeployService_UnwritableIsARefusalOnlyBeforeTheFirstWrite(t *testing.T) {
	b := newDockerBench(t)
	b.tools.checks.writable = func(string) string { return agentwire.ReasonPathUnwritable }
	_, err := b.tools.deployService(context.Background(), b.job(false))
	if result := dockerResultOf("run1", nil, err); result.State != agentwire.ActionRefused || result.Reason != agentwire.ReasonPathUnwritable {
		t.Errorf("judged before writing: %+v", result)
	}
	b.assertUntouched(t)

	failed := failWith(agentwire.ReasonPathUnwritable, errors.New("write compose.yml"))
	if result := dockerResultOf("run1", nil, failed); result.State != agentwire.ActionFailed {
		t.Errorf("a write that failed after the folder was touched is a failure: %+v", result)
	}
}

// Docker d'abord, la racine ensuite : quand les deux manquent, l'opérateur
// règle le premier, et le second se dit au tour suivant.
func TestDeployService_DockerIsJudgedBeforeTheRoot(t *testing.T) {
	b := newDockerBench(t)
	b.tools.checks.docker = deniedDocker
	b.tools.checks.writable = deniedPath
	_, err := b.tools.deployService(context.Background(), b.job(false))
	if reason := deployReasonOf(t, err); reason != agentwire.ReasonDockerDenied {
		t.Fatalf("got %v", err)
	}
}

// Une racine que le chemin rend invalide reste jugée avant le compte : la
// phrase ne parle pas d'un droit que rien ne pourrait exercer.
func TestDeployService_AnAccountThatCanDoEverythingStillDeploys(t *testing.T) {
	b := newDockerBench(t)
	if _, err := b.tools.deployService(context.Background(), b.job(false)); err != nil {
		t.Fatal(err)
	}
	if len(b.compose.upDirs) != 1 {
		t.Fatalf("compose ran %d times", len(b.compose.upDirs))
	}
}

// Par le chemin d'un ordre : le résultat est rapporté refusé avec la raison, le
// .env n'est pas écrit, et la racine n'existe pas.
func TestDockerDeploy_ARefusalByTheAccountIsReportedAndWritesNothing(t *testing.T) {
	cases := map[string]struct {
		checks func(*dockerFlow)
		reason string
	}{
		"docker closed":  {func(f *dockerFlow) { f.runner.docker.checks.docker = deniedDocker }, agentwire.ReasonDockerDenied},
		"root not owned": {func(f *dockerFlow) { f.runner.docker.checks.writable = deniedPath }, agentwire.ReasonPathDenied},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newDockerFlow(t, "docker.deploy")
			c.checks(f)
			body := longOrder(1, agentwire.KindDockerDeploy, agentwire.DockerParams{
				ServiceID: "svc_1", Project: "nextcloud", Compose: testCompose, DataDirs: []string{"app"}, EnvSent: true,
			})
			body.Secret, _ = json.Marshal(agentwire.DockerSecret{Env: "A=1\n"})
			f.runner.receive(f.signed(t, body))
			results := f.until(t, body.RunID)
			last := results[len(results)-1]
			if last.State != agentwire.ActionRefused || last.Reason != c.reason {
				t.Fatalf("results %+v", results)
			}
			f.assertUntouched(t)
		})
	}
}

// backup.receive : le chemin couvert par le droit mais fermé au compte est
// refusé tout de suite, sans être accepté ni demander le colis au serveur.
func TestBackupReceive_RefusesAFolderTheAccountCannotWrite(t *testing.T) {
	files := map[string]string{archiveName: ageLike("x")}
	b := newLongBench(t, files)
	var asked []string
	b.runner.checks.writable = func(path string) string {
		asked = append(asked, path)
		return agentwire.ReasonPathDenied
	}
	body := longOrder(1, agentwire.KindBackupReceive, agentwire.BackupReceiveParams{Parcel: "colis1", Path: b.dir, Files: listed(files)})
	b.runner.receive(b.signed(t, body))
	report := b.results.take()
	if report == nil || len(report.Results) != 1 || report.Results[0].State != agentwire.ActionRefused || report.Results[0].Reason != agentwire.ReasonPathDenied {
		t.Fatalf("report %+v", report)
	}
	if len(asked) != 1 || asked[0] != b.dir {
		t.Errorf("the check was asked about %v, not the granted folder %s", asked, b.dir)
	}
	if len(b.server.requests) != 0 {
		t.Errorf("the server was asked: %v", b.server.requests)
	}
	assertDirEmpty(t, b.dir)
}

// Le droit d'abord : un chemin hors du droit se dit « hors du droit », sans
// révéler ce que le compte voit ailleurs.
func TestBackupReceive_OutsideTheRightIsSaidBeforeTheAccount(t *testing.T) {
	files := map[string]string{archiveName: ageLike("x")}
	b := newLongBench(t, files)
	asked := 0
	b.runner.checks.writable = func(string) string {
		asked++
		return agentwire.ReasonPathDenied
	}
	body := longOrder(1, agentwire.KindBackupReceive, agentwire.BackupReceiveParams{Parcel: "colis1", Path: b.dir + "2", Files: listed(files)})
	b.runner.receive(b.signed(t, body))
	report := b.results.take()
	if report == nil || report.Results[0].Reason != agentwire.ReasonPathOutsideRight {
		t.Fatalf("report %+v", report)
	}
	if asked != 0 {
		t.Errorf("the account was asked about a folder outside the right")
	}
}

// backup.prune : même refus, et aucune sauvegarde n'est effacée.
func TestBackupPrune_RefusesAFolderTheAccountCannotWrite(t *testing.T) {
	b := newLongBench(t, nil)
	b.runner.checks.writable = deniedPath
	names := []string{"opencloud-20261001-030000.tar.age", "opencloud-20261002-030000.tar.age", "opencloud-20261003-030000.tar.age"}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(b.dir, name), []byte(ageHeader), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	body := longOrder(1, agentwire.KindBackupPrune, agentwire.BackupPruneParams{Path: b.dir, Prefix: "opencloud", Keep: 1})
	b.runner.receive(b.signed(t, body))
	report := b.results.take()
	if report == nil || len(report.Results) != 1 || report.Results[0].State != agentwire.ActionRefused || report.Results[0].Reason != agentwire.ReasonPathDenied {
		t.Fatalf("report %+v", report)
	}
	entries, err := os.ReadDir(b.dir)
	if err != nil || len(entries) != len(names) {
		t.Errorf("a prune that was refused touched the folder: %d entries (%v)", len(entries), err)
	}
}

// Les ordres bien formés d'un compte qui peut tout passent comme avant.
func TestBackupReceive_AnAccountThatCanWriteStillReceives(t *testing.T) {
	files := map[string]string{archiveName: ageLike("sauvegarde")}
	b := newLongBench(t, files)
	body := longOrder(1, agentwire.KindBackupReceive, agentwire.BackupReceiveParams{Parcel: "colis1", Path: b.dir, Files: listed(files)})
	b.runner.receive(b.signed(t, body))
	results := b.until(t, body.RunID)
	if last := results[len(results)-1]; last.State != agentwire.ActionSucceeded {
		t.Fatalf("results %+v", results)
	}
}

// agent.update : le dossier bin/ fermé au compte est refusé avant de rien
// télécharger, et le lien current n'est pas touché.
func TestAgentUpdate_RefusesWhenTheAccountCannotWriteTheBinFolder(t *testing.T) {
	b := newOrderBenchWithRights(t, []string{"observe", "agent.update"})
	update := newUpdateBench(t, "v0.0.1")
	b.runner.updater = update.updater
	published := update.publish(t, "v0.0.2", "v0.0.2", update.private)
	b.runner.fetch.use(published.server.client(t))
	b.runner.fetch.setSession(parcelSession)

	var judged []string
	checks := allowAll()
	checks.agentUpdate = func() string {
		return b.runner.updater.unavailableReason(func(path string) string {
			judged = append(judged, path)
			return agentwire.ReasonPathDenied
		})
	}
	useHostChecks(b.runner, checks)

	body := order(1)
	body.Kind = agentwire.KindAgentUpdate
	body.Params, _ = json.Marshal(published.params)
	result := b.send(t, body, b.private)
	if result.State != agentwire.ActionRefused || result.Reason != agentwire.ReasonPathDenied {
		t.Fatalf("result %+v", result)
	}
	if want := filepath.Join(update.state, brand.AgentBinDir); len(judged) != 1 || judged[0] != want {
		t.Errorf("the account was asked about %v, not %s", judged, want)
	}
	if len(published.server.requests) != 0 {
		t.Errorf("the server was asked: %v", published.server.requests)
	}
	update.untouched(t)
}

// Le juge de agent.update : hors du dossier bin/ d'abord, puis le compte, et les
// liens se suivent avant de juger.
func TestSelfUpdater_UnavailableReason(t *testing.T) {
	b := newUpdateBench(t, "v0.0.1")
	judged := func(reason string) func(string) string {
		return func(string) string { return reason }
	}
	if got := b.updater.unavailableReason(judged("")); got != "" {
		t.Errorf("an agent that can update itself: %q", got)
	}
	if got := b.updater.unavailableReason(judged(agentwire.ReasonPathDenied)); got != agentwire.ReasonPathDenied {
		t.Errorf("a bin folder closed to the account: %q", got)
	}
	if got := b.updater.unavailableReason(judged(agentwire.ReasonPathUnwritable)); got != agentwire.ReasonPathUnwritable {
		t.Errorf("a bin folder on a read-only mount: %q", got)
	}

	// Hors du dossier bin/ : not_self_updatable, sans demander au disque.
	b.updater.executable = func() (string, error) { return "/usr/local/bin/opencloud", nil }
	asked := false
	got := b.updater.unavailableReason(func(string) string {
		asked = true
		return ""
	})
	if got != agentwire.ReasonNotSelfUpdatable || asked {
		t.Errorf("outside bin/: %q (disk asked: %v)", got, asked)
	}
}

// Le dossier bin/ derrière un lien se juge là où il mène : c'est là que
// l'écriture se fera.
func TestSelfUpdater_JudgesTheBinFolderOnceLinksAreFollowed(t *testing.T) {
	b := newUpdateBench(t, "v0.0.1")
	var judged string
	b.updater.unavailableReason(func(path string) string {
		judged = path
		return ""
	})
	real, err := filepath.EvalSymlinks(filepath.Join(b.state, brand.AgentBinDir))
	if err != nil {
		t.Fatal(err)
	}
	if judged != real {
		t.Errorf("judged %q, want %q", judged, real)
	}
}
