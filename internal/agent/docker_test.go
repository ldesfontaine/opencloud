package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/brand"
)

// testCompose est celui que le serveur rendrait : l'agent refuse tout autre
// fichier (agentwire.CheckCompose). Seule l'unité app monte des données.
var testCompose = func() string {
	compose, _, err := agentwire.RenderCompose("nextcloud", "svc_1", []agentwire.ComposeUnit{
		{Name: "app", Image: "nextcloud:29.0.4", Ports: []string{"8080:80"}, DataPath: "/var/www/html"},
		{Name: "db", Image: "postgres:16.4"},
	})
	if err != nil {
		panic(err)
	}
	return string(compose)
}()

// fakeCompose tient lieu de Docker Compose : il ne lance rien.
type fakeCompose struct {
	version    string
	versionErr error
	lines      []agentwire.OutputLine
	upErr      error
	upDirs     []string
	projects   []string
}

func (f *fakeCompose) Version(context.Context) (string, error) { return f.version, f.versionErr }

func (f *fakeCompose) Up(_ context.Context, dir, project string) ([]agentwire.OutputLine, error) {
	f.upDirs = append(f.upDirs, dir)
	f.projects = append(f.projects, project)
	return f.lines, f.upErr
}

// dockerBench : des outils sans Docker, sur une racine temporaire qui
// n'existe pas encore.
type dockerBench struct {
	tools   dockerTools
	compose *fakeCompose
	root    string
	free    uint64
}

func newDockerBench(t *testing.T) *dockerBench {
	t.Helper()
	b := &dockerBench{
		compose: &fakeCompose{version: "2.29.7", lines: []agentwire.OutputLine{{Stream: "stderr", Line: "Container nextcloud-app-1  Started"}}},
		root:    filepath.Join(cleanTempDir(t), "workspace"),
		free:    10 << 30,
	}
	b.tools = dockerTools{
		compose:    b.compose,
		freeBytes:  func(string) (uint64, error) { return b.free, nil },
		writeFiles: writeServiceFiles,
	}
	return b
}

// cleanTempDir : un dossier dont le chemin passe le juge des racines (ni
// accent ni virgule, que le nom d'un sous-test mettrait dans t.TempDir()).
func cleanTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "workspace")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func (b *dockerBench) job(update bool) dockerJob {
	return dockerJob{
		params: agentwire.DockerParams{ServiceID: "svc_1", Project: "nextcloud", Compose: testCompose, DataDirs: []string{"app", "db"}},
		update: update, root: b.root,
	}
}

func (b *dockerBench) path(parts ...string) string {
	return filepath.Join(append([]string{b.root}, parts...)...)
}

func mode(t *testing.T, name string) fs.FileMode {
	t.Helper()
	info, err := os.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func lineTexts(output []agentwire.OutputLine) string {
	texts := make([]string, 0, len(output))
	for _, line := range output {
		texts = append(texts, line.Line)
	}
	return strings.Join(texts, "\n")
}

func deployReasonOf(t *testing.T, err error) string {
	t.Helper()
	var failed *orderError
	if !errors.As(err, &failed) {
		t.Fatalf("not an order error: %v", err)
	}
	return failed.reason
}

func TestDeployService_WritesComposeAndEnvThenRunsUp(t *testing.T) {
	b := newDockerBench(t)
	job := b.job(false)
	job.env, job.hasEnv = "CLE=une valeur de test\n", true // gitleaks:allow
	job.params.EnvSent = true

	output, err := b.tools.deployService(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}

	// La racine est neuve : créée en 0755 avec son témoin.
	if got := mode(t, b.root); got != 0o755 {
		t.Errorf("root mode %v", got)
	}
	if info, err := os.Lstat(b.path(brand.WorkspaceMarker)); err != nil || !info.Mode().IsRegular() {
		t.Errorf("the witness file is missing: %v", err)
	}
	if got := mode(t, b.path("nextcloud")); got != 0o755 {
		t.Errorf("service folder mode %v", got)
	}
	if content, err := os.ReadFile(b.path("nextcloud", "compose.yml")); err != nil || string(content) != testCompose || mode(t, b.path("nextcloud", "compose.yml")) != 0o644 {
		t.Errorf("compose.yml %q %v", content, err)
	}
	if content, err := os.ReadFile(b.path("nextcloud", ".env")); err != nil || string(content) != job.env || mode(t, b.path("nextcloud", ".env")) != 0o600 {
		t.Errorf(".env %q %v", content, err)
	}
	for _, name := range []string{"data", "data/app", "data/db"} {
		if got := mode(t, b.path("nextcloud", filepath.FromSlash(name))); got != 0o755 {
			t.Errorf("%s mode %v", name, got)
		}
	}
	if len(b.compose.upDirs) != 1 || b.compose.upDirs[0] != b.path("nextcloud") || b.compose.projects[0] != "nextcloud" {
		t.Errorf("compose up ran in %v as %v", b.compose.upDirs, b.compose.projects)
	}
	text := lineTexts(output)
	for _, want := range []string{"compose 2.29.7", "written compose.yml", "written .env", "data app db", "Container nextcloud-app-1  Started"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "valeur de test") {
		t.Errorf("the .env reached the output:\n%s", text)
	}
	// Aucun reste d'écriture à côté des fichiers.
	entries, _ := os.ReadDir(b.path("nextcloud"))
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if strings.Join(names, " ") != ".env compose.yml data" {
		t.Errorf("the folder holds %v", names)
	}
}

func TestDeployService_WithoutEnvWritesNone(t *testing.T) {
	b := newDockerBench(t)
	if _, err := b.tools.deployService(context.Background(), b.job(false)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(b.path("nextcloud", ".env")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("an .env was written without one: %v", err)
	}
}

func TestDeployService_RefusesAnExistingDirectory(t *testing.T) {
	b := newDockerBench(t)
	if err := os.MkdirAll(b.path("nextcloud"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b.path(brand.WorkspaceMarker), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b.path("nextcloud", "compose.yml"), []byte("à la main"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := b.tools.deployService(context.Background(), b.job(false))
	if deployReasonOf(t, err) != agentwire.ReasonServiceDirExists {
		t.Fatalf("got %v", err)
	}
	if content, _ := os.ReadFile(b.path("nextcloud", "compose.yml")); string(content) != "à la main" {
		t.Fatal("the existing folder was touched")
	}
	if len(b.compose.upDirs) != 0 {
		t.Fatal("compose ran")
	}
	result := dockerResultOf("run1", nil, err)
	if result.State != agentwire.ActionRefused || result.Reason != agentwire.ReasonServiceDirExists {
		t.Fatalf("result %+v", result)
	}
}

func TestUpdateService_RefusesAMissingDirectory(t *testing.T) {
	b := newDockerBench(t)
	_, err := b.tools.deployService(context.Background(), b.job(true))
	if deployReasonOf(t, err) != agentwire.ReasonServiceDirMissing {
		t.Fatalf("got %v", err)
	}
	if _, statErr := os.Lstat(b.root); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("a refusal created the root: %v", statErr)
	}
}

// Un redéploiement réécrit le compose.yml et laisse l'.env en place s'il n'en
// vient pas un neuf ; un dossier de données qui existe garde son mode.
func TestUpdateService_KeepsTheEnvWithoutSecret(t *testing.T) {
	b := newDockerBench(t)
	first := b.job(false)
	first.env, first.hasEnv = "CLE=ancienne\n", true // gitleaks:allow
	first.params.EnvSent = true
	if _, err := b.tools.deployService(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(b.path("nextcloud", "data", "db"), 0o700); err != nil {
		t.Fatal(err)
	}

	second := b.job(true)
	second.params.Compose = strings.Replace(testCompose, "29.0.4", "30.0.0", 1)
	output, err := b.tools.deployService(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	if content, _ := os.ReadFile(b.path("nextcloud", "compose.yml")); !strings.Contains(string(content), "30.0.0") {
		t.Errorf("compose.yml not replaced: %s", content)
	}
	if content, _ := os.ReadFile(b.path("nextcloud", ".env")); string(content) != "CLE=ancienne\n" {
		t.Errorf(".env changed: %q", content)
	}
	if got := mode(t, b.path("nextcloud", "data", "db")); got != 0o700 {
		t.Errorf("an existing data folder changed mode: %v", got)
	}
	if strings.Contains(lineTexts(output), "written .env") {
		t.Errorf("output says an .env was written:\n%s", lineTexts(output))
	}
	if len(b.compose.upDirs) != 2 {
		t.Errorf("compose ran %d times", len(b.compose.upDirs))
	}

	// Un .env neuf remplace l'ancien.
	third := b.job(true)
	third.env, third.hasEnv = "CLE=neuve\n", true // gitleaks:allow
	third.params.EnvSent = true
	if _, err := b.tools.deployService(context.Background(), third); err != nil {
		t.Fatal(err)
	}
	if content, _ := os.ReadFile(b.path("nextcloud", ".env")); string(content) != "CLE=neuve\n" || mode(t, b.path("nextcloud", ".env")) != 0o600 {
		t.Errorf(".env %q", content)
	}
}

func TestDeployService_JudgesTheRoot(t *testing.T) {
	foreign := func(t *testing.T, b *dockerBench) {
		if err := os.MkdirAll(b.root, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(b.path("autre-chose"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("non vide sans témoin : refusée, rien n'est écrit", func(t *testing.T) {
		b := newDockerBench(t)
		foreign(t, b)
		_, err := b.tools.deployService(context.Background(), b.job(false))
		if deployReasonOf(t, err) != agentwire.ReasonRootNotOurs {
			t.Fatalf("got %v", err)
		}
		entries, _ := os.ReadDir(b.root)
		if len(entries) != 1 || len(b.compose.upDirs) != 0 {
			t.Fatalf("the foreign root was touched: %v", entries)
		}
	})
	t.Run("non vide avec témoin : la nôtre", func(t *testing.T) {
		b := newDockerBench(t)
		foreign(t, b)
		if err := os.WriteFile(b.path(brand.WorkspaceMarker), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := b.tools.deployService(context.Background(), b.job(false)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("vide : adoptée, le témoin est posé", func(t *testing.T) {
		b := newDockerBench(t)
		if err := os.MkdirAll(b.root, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := b.tools.deployService(context.Background(), b.job(false)); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(b.path(brand.WorkspaceMarker)); err != nil {
			t.Fatalf("no witness: %v", err)
		}
	})
	t.Run("témoin qui est un lien : pas un témoin", func(t *testing.T) {
		b := newDockerBench(t)
		foreign(t, b)
		if err := os.Symlink(b.path("autre-chose"), b.path(brand.WorkspaceMarker)); err != nil {
			t.Fatal(err)
		}
		_, err := b.tools.deployService(context.Background(), b.job(false))
		if deployReasonOf(t, err) != agentwire.ReasonRootNotOurs {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("racine mal écrite dans le fichier", func(t *testing.T) {
		b := newDockerBench(t)
		job := b.job(false)
		job.root = "/srv/../etc"
		_, err := b.tools.deployService(context.Background(), job)
		if deployReasonOf(t, err) != agentwire.ReasonPathUnwritable {
			t.Fatalf("got %v", err)
		}
	})
}

func TestDeployService_StopsOnMissingPrerequisites(t *testing.T) {
	cases := []struct {
		name   string
		change func(*dockerBench)
		reason string
	}{
		{"docker absent", func(b *dockerBench) { b.compose.versionErr = errDockerMissing }, agentwire.ReasonDockerMissing},
		{"greffon compose absent", func(b *dockerBench) { b.compose.versionErr = errors.New("'compose' is not a docker command") }, agentwire.ReasonComposeTooOld},
		{"compose 2.23", func(b *dockerBench) { b.compose.version = "2.23.3" }, agentwire.ReasonComposeTooOld},
		{"compose 1.29", func(b *dockerBench) { b.compose.version = "1.29.2" }, agentwire.ReasonComposeTooOld},
		{"version illisible", func(b *dockerBench) { b.compose.version = "dev" }, agentwire.ReasonComposeTooOld},
		{"disque presque plein", func(b *dockerBench) { b.free = 2<<30 - 1 }, agentwire.ReasonDiskFull},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newDockerBench(t)
			c.change(b)
			_, err := b.tools.deployService(context.Background(), b.job(false))
			if deployReasonOf(t, err) != c.reason {
				t.Fatalf("got %v, want %s", err, c.reason)
			}
			if _, statErr := os.Lstat(b.root); !errors.Is(statErr, fs.ErrNotExist) {
				t.Fatalf("a refused deployment left the root behind: %v", statErr)
			}
			if len(b.compose.upDirs) != 0 {
				t.Fatal("compose ran")
			}
			if result := dockerResultOf("r", nil, err); result.State != agentwire.ActionRefused {
				t.Fatalf("state %s, nothing was written: it is a refusal", result.State)
			}
		})
	}
}

func TestIsComposeNewEnough(t *testing.T) {
	for version, want := range map[string]bool{
		"2.24.0": true, "2.24.0\n": true, "v2.24.0": true, "2.29.7": true, "2.24.0-desktop.1": true, "3.0.0": true,
		"2.100.1": true, "2.23.9": false, "1.99.9": false, "2.24": false, "": false, "latest": false,
	} {
		if got := isComposeNewEnough(version); got != want {
			t.Errorf("%q: %v, want %v", version, got, want)
		}
	}
}

// Compose qui échoue : le dossier reste (le redéploiement le reprend), la fin
// de sa sortie part avec la raison.
func TestDeployService_ComposeFailureKeepsTheFolderAndTheOutput(t *testing.T) {
	b := newDockerBench(t)
	b.compose.lines = []agentwire.OutputLine{{Stream: "stderr", Line: "pull access denied for nextcloud"}}
	b.compose.upErr = errors.New("exit status 1")
	output, err := b.tools.deployService(context.Background(), b.job(false))
	if deployReasonOf(t, err) != agentwire.ReasonComposeFailed {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(lineTexts(output), "pull access denied") {
		t.Errorf("the output of compose is lost:\n%s", lineTexts(output))
	}
	if _, statErr := os.Lstat(b.path("nextcloud", "compose.yml")); statErr != nil {
		t.Errorf("the folder must stay after a compose failure: %v", statErr)
	}
	result := dockerResultOf("r", output, err)
	if result.State != agentwire.ActionFailed || result.Reason != agentwire.ReasonComposeFailed || len(result.Output) != len(output) {
		t.Errorf("result %+v", result)
	}
}

// Une écriture qui manque avant Compose retire le dossier qu'elle venait de
// créer : le déploiement suivant ne trouvera pas « service_dir_exists ».
func TestDeployService_AWriteFailureRemovesTheNewFolder(t *testing.T) {
	b := newDockerBench(t)
	b.tools.writeFiles = func(dir *os.Root, job dockerJob) ([]agentwire.OutputLine, error) {
		if err := dir.WriteFile("compose.yml", []byte("à moitié"), 0o644); err != nil {
			t.Fatal(err)
		}
		return nil, failWith(agentwire.ReasonPathUnwritable, errors.New("disque en lecture seule"))
	}
	_, err := b.tools.deployService(context.Background(), b.job(false))
	if deployReasonOf(t, err) != agentwire.ReasonPathUnwritable {
		t.Fatalf("got %v", err)
	}
	if _, statErr := os.Lstat(b.path("nextcloud")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("the half-written folder stayed: %v", statErr)
	}
	if len(b.compose.upDirs) != 0 {
		t.Fatal("compose ran after a failed write")
	}
	// Un redéploiement, lui, ne retire jamais le dossier d'un déploiement.
	b2 := newDockerBench(t)
	if _, err := b2.tools.deployService(context.Background(), b2.job(false)); err != nil {
		t.Fatal(err)
	}
	b2.tools.writeFiles = func(*os.Root, dockerJob) ([]agentwire.OutputLine, error) {
		return nil, failWith(agentwire.ReasonPathUnwritable, errors.New("disque en lecture seule"))
	}
	if _, err := b2.tools.deployService(context.Background(), b2.job(true)); err == nil {
		t.Fatal("the failure was swallowed")
	}
	if _, statErr := os.Lstat(b2.path("nextcloud", "compose.yml")); statErr != nil {
		t.Fatalf("a failed redeploy removed the deployed folder: %v", statErr)
	}
}

// Aucun lien symbolique n'est suivi : ni sur le dossier du service, ni sur
// ceux de ses données.
func TestUpdateService_FollowsNoSymbolicLink(t *testing.T) {
	outside := t.TempDir()
	t.Run("le dossier du service", func(t *testing.T) {
		b := newDockerBench(t)
		if _, err := b.tools.deployService(context.Background(), b.job(false)); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(b.path("nextcloud")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, b.path("nextcloud")); err != nil {
			t.Fatal(err)
		}
		_, err := b.tools.deployService(context.Background(), b.job(true))
		if deployReasonOf(t, err) != agentwire.ReasonPathOutsideRight {
			t.Fatalf("got %v", err)
		}
		if entries, _ := os.ReadDir(outside); len(entries) != 0 {
			t.Fatalf("something was written through the link: %v", entries)
		}
	})
	t.Run("un dossier de données", func(t *testing.T) {
		b := newDockerBench(t)
		if _, err := b.tools.deployService(context.Background(), b.job(false)); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(b.path("nextcloud", "data", "db")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, b.path("nextcloud", "data", "db")); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(b.path("nextcloud", "compose.yml"))
		changed := b.job(true)
		changed.params.Compose = "autre chose\n"
		_, err := b.tools.deployService(context.Background(), changed)
		if deployReasonOf(t, err) != agentwire.ReasonPathOutsideRight {
			t.Fatalf("got %v", err)
		}
		if after, _ := os.ReadFile(b.path("nextcloud", "compose.yml")); string(after) != string(before) {
			t.Fatal("a refused redeployment wrote the compose file")
		}
		if entries, _ := os.ReadDir(outside); len(entries) != 0 {
			t.Fatalf("something was written through the link: %v", entries)
		}
	})
}

func TestReadDockerOrder_JudgesWhatTheOrderCarries(t *testing.T) {
	good := agentwire.DockerParams{ServiceID: "svc_1", Project: "nextcloud", Compose: testCompose, DataDirs: []string{"app"}}
	encode := func(params agentwire.DockerParams) json.RawMessage {
		data, _ := json.Marshal(params)
		return data
	}
	withEnv := good
	withEnv.EnvSent = true
	cases := []struct {
		name   string
		params json.RawMessage
		secret string
		ok     bool
	}{
		{"valide", encode(good), "", true},
		{"valide avec .env", encode(withEnv), `{"env":"A=1\n"}`, true},
		{"illisible", json.RawMessage(`{`), "", false},
		{"projet qui remonte", encode(agentwire.DockerParams{Project: "../etc", Compose: testCompose}), "", false},
		{"projet en majuscules", encode(agentwire.DockerParams{Project: "Next", Compose: testCompose}), "", false},
		{"projet vide", encode(agentwire.DockerParams{Compose: testCompose}), "", false},
		{"compose vide", encode(agentwire.DockerParams{Project: "nextcloud"}), "", false},
		{"compose trop gros", encode(agentwire.DockerParams{Project: "nextcloud", Compose: strings.Repeat("x", agentwire.MaxComposeBytes+1)}), "", false},
		{"compose à octet nul", encode(agentwire.DockerParams{Project: "nextcloud", Compose: "a\x00b"}), "", false},
		{"dossier de données qui remonte", encode(agentwire.DockerParams{Project: "nextcloud", Compose: testCompose, DataDirs: []string{".."}}), "", false},
		{"dossier de données avec barre", encode(agentwire.DockerParams{Project: "nextcloud", Compose: testCompose, DataDirs: []string{"a/b"}}), "", false},
		{"dossier de données en double", encode(agentwire.DockerParams{Project: "nextcloud", Compose: testCompose, DataDirs: []string{"a", "a"}}), "", false},
		{"trop de dossiers de données", encode(agentwire.DockerParams{Project: "nextcloud", Compose: testCompose, DataDirs: manyNames(agentwire.MaxDataDirs + 1)}), "", false},
		{"un secret que les paramètres ne disent pas", encode(good), `{"env":"A=1"}`, false},
		{".env annoncé mais absent", encode(withEnv), "", false},
		{".env annoncé mais vide", encode(withEnv), `{"env":""}`, false},
		{".env à octet nul", encode(withEnv), `{"env":"A=\u0000"}`, false},
		// Le compose est rejugé par l'agent : signé ou non, un fichier qui
		// n'est pas celui que le serveur rend est refusé.
		{"compose écrit à la main", encode(agentwire.DockerParams{ServiceID: "svc_1", Project: "nextcloud", Compose: "name: nextcloud\nservices:\n  app:\n    image: nextcloud:29.0.4\n", DataDirs: []string{"app"}}), "", false},
		{"compose privilégié", encode(withCompose(good, strings.Replace(testCompose, "    restart: unless-stopped\n", "    restart: unless-stopped\n    privileged: true\n", 1))), "", false},
		{"compose qui monte l'hôte", encode(withCompose(good, strings.Replace(testCompose, "./data/app:/var/www/html", "/:/host", 1))), "", false},
		{"compose d'un autre projet", encode(withCompose(good, strings.Replace(testCompose, "name: nextcloud", "name: autre", 1))), "", false},
		{"compose d'un autre service", encode(agentwire.DockerParams{ServiceID: "svc_2", Project: "nextcloud", Compose: testCompose, DataDirs: []string{"app"}}), "", false},
		{"dossiers de données qui ne sont pas ceux du compose", encode(agentwire.DockerParams{ServiceID: "svc_1", Project: "nextcloud", Compose: testCompose, DataDirs: []string{"app", "db"}}), "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := agentwire.OrderBody{RunID: "run1", Params: c.params}
			if c.secret != "" {
				body.Secret = json.RawMessage(c.secret)
			}
			job, refused := readDockerOrder(body, "/srv/workspace", false)
			if c.ok != (refused == nil) {
				t.Fatalf("job %+v refused %+v", job, refused)
			}
			if refused != nil && (refused.State != agentwire.ActionFailed || refused.Reason != agentwire.ReasonBadOrder || refused.RunID != "run1") {
				t.Fatalf("refusal %+v", refused)
			}
		})
	}
}

func withCompose(params agentwire.DockerParams, compose string) agentwire.DockerParams {
	params.Compose = compose
	return params
}

func manyNames(count int) []string {
	names := make([]string, 0, count)
	for index := range count {
		names = append(names, fmt.Sprintf("unit%02d", index))
	}
	return names
}

// dockerFlow : le juge d'ordres, son worker, et des outils sans Docker.
type dockerFlow struct {
	*orderBench
	*dockerBench
	logs *bytes.Buffer
}

func newDockerFlow(t *testing.T, rights ...string) *dockerFlow {
	t.Helper()
	b := newOrderBenchWithRights(t, append([]string{"observe"}, rights...))
	tools := newDockerBench(t)
	b.runner.identity.RootDir = tools.root
	b.runner.docker = tools.tools
	logs := &bytes.Buffer{}
	b.runner.logger = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.runner.worker.run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return &dockerFlow{orderBench: b, dockerBench: tools, logs: logs}
}

// Le chemin d'un ordre : jugé, accepté, fait par le worker. Le .env ne se
// retrouve ni dans les résultats, ni dans le journal, ni dans l'identité.
func TestDockerDeploy_IsAcceptedThenDoneAndTheEnvStaysOnDisk(t *testing.T) {
	f := newDockerFlow(t, "docker.deploy", "docker.update")
	const env = "MOT_DE_PASSE=une valeur de test qui ne sort pas\n" // gitleaks:allow
	body := longOrder(1, agentwire.KindDockerDeploy, agentwire.DockerParams{
		ServiceID: "svc_1", Project: "nextcloud", Compose: testCompose, DataDirs: []string{"app"}, EnvSent: true,
	})
	body.Secret, _ = json.Marshal(agentwire.DockerSecret{Env: env})
	f.runner.receive(f.signed(t, body))

	results := f.until(t, body.RunID)
	if len(results) != 2 || results[0].State != agentwire.ActionAccepted || results[1].State != agentwire.ActionSucceeded {
		t.Fatalf("results %+v", results)
	}
	if content, err := os.ReadFile(f.path("nextcloud", ".env")); err != nil || string(content) != env {
		t.Fatalf(".env %q %v", content, err)
	}
	reported, _ := json.Marshal(results)
	if strings.Contains(string(reported), "MOT_DE_PASSE") || strings.Contains(f.logs.String(), "MOT_DE_PASSE") || strings.Contains(f.logs.String(), "valeur de test") {
		t.Fatalf("the .env leaked:\n%s\n%s", reported, f.logs.String())
	}
	if identity, _ := f.stateDir.ReadFile(identityFile); strings.Contains(string(identity), "MOT_DE_PASSE") {
		t.Fatal("the .env reached the identity file")
	}

	// Le même dossier existe : le second déploiement est refusé par l'agent.
	again := longOrder(2, agentwire.KindDockerDeploy, agentwire.DockerParams{ServiceID: "svc_1", Project: "nextcloud", Compose: testCompose, DataDirs: []string{"app"}})
	f.runner.receive(f.signed(t, again))
	results = f.until(t, again.RunID)
	last := results[len(results)-1]
	if last.State != agentwire.ActionRefused || last.Reason != agentwire.ReasonServiceDirExists {
		t.Fatalf("second deploy: %+v", results)
	}

	// Redéployer passe par docker.update.
	update := longOrder(3, agentwire.KindDockerUpdate, agentwire.DockerParams{ServiceID: "svc_1", Project: "nextcloud", Compose: testCompose, DataDirs: []string{"app"}})
	f.runner.receive(f.signed(t, update))
	results = f.until(t, update.RunID)
	if last := results[len(results)-1]; last.State != agentwire.ActionSucceeded {
		t.Fatalf("redeploy: %+v", results)
	}
}

// Les droits sont jugés comme pour tout ordre : sans docker.update, un
// redéploiement est refusé tout de suite, rien n'est écrit.
func TestDockerOrders_NeedTheirOwnRight(t *testing.T) {
	f := newDockerFlow(t, "docker.deploy")
	body := longOrder(1, agentwire.KindDockerUpdate, agentwire.DockerParams{Project: "nextcloud", Compose: testCompose})
	f.runner.receive(f.signed(t, body))
	results := f.until(t, body.RunID)
	if len(results) != 1 || results[0].State != agentwire.ActionRefused || results[0].Reason != agentwire.RightMissing("docker.update") {
		t.Fatalf("results %+v", results)
	}
	if _, err := os.Lstat(f.root); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a refused order created the root: %v", err)
	}
}

func TestDockerDeploy_ABadOrderIsNotAccepted(t *testing.T) {
	f := newDockerFlow(t, "docker.deploy")
	body := longOrder(1, agentwire.KindDockerDeploy, agentwire.DockerParams{Project: "../..", Compose: testCompose})
	f.runner.receive(f.signed(t, body))
	results := f.until(t, body.RunID)
	if len(results) != 1 || results[0].State != agentwire.ActionFailed || results[0].Reason != agentwire.ReasonBadOrder {
		t.Fatalf("results %+v", results)
	}
}

// La racine est celle du fichier de l'agent : l'ordre n'en porte aucune.
func TestIdentity_RootDefaultsAndIsKept(t *testing.T) {
	if got := (Identity{}).Root(); got != brand.WorkspaceRoot {
		t.Errorf("default root %q", got)
	}
	if got := (Identity{RootDir: "/srv/apps"}).Root(); got != "/srv/apps" {
		t.Errorf("root %q", got)
	}
	stateDir, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer stateDir.Close()
	if err := SaveIdentity(stateDir, Identity{MachineID: "m", RootDir: "/srv/apps"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadIdentity(stateDir)
	if err != nil || loaded.RootDir != "/srv/apps" {
		t.Fatalf("loaded %+v %v", loaded, err)
	}
}

// Compose cite parfois une ligne de l'.env dans son erreur : les valeurs ne
// partent pas au serveur, que l'.env vienne de l'ordre ou de la machine.
func TestDeployService_MasksTheEnvInComposeOutput(t *testing.T) {
	const secret = "une-valeur-de-test-qui-ne-sort-pas" // gitleaks:allow
	b := newDockerBench(t)
	b.compose.lines = []agentwire.OutputLine{
		{Stream: "stderr", Line: `failed to read .env: line 2: unexpected character "-" in variable name near "MOT DE PASSE=` + secret + `"`},
		{Stream: "stderr", Line: "Container nextcloud-app-1  Error 8080"},
	}
	b.compose.upErr = errors.New("exit status 1")
	job := b.job(false)
	job.env, job.hasEnv = "PORT=8080\nMOT DE PASSE="+secret+"\nAUTRE=\"entre guillemets\"\n", true
	job.params.EnvSent = true
	output, err := b.tools.deployService(context.Background(), job)
	if deployReasonOf(t, err) != agentwire.ReasonComposeFailed {
		t.Fatalf("got %v", err)
	}
	text := lineTexts(output)
	if strings.Contains(text, secret) || !strings.Contains(text, "[.env]") {
		t.Fatalf("the .env reached the output:\n%s", text)
	}
	// Une valeur courte ne se masque pas : elle n'est pas un secret, et la
	// sortie resterait lisible.
	if !strings.Contains(text, "Error 8080") {
		t.Errorf("a short value was masked:\n%s", text)
	}

	// Le redéploiement sans .env masque celui que la machine garde.
	b.compose.upErr = nil
	b.compose.lines = []agentwire.OutputLine{{Stream: "stderr", Line: "warning: " + secret + " entre guillemets"}}
	output, err = b.tools.deployService(context.Background(), b.job(true))
	if err != nil {
		t.Fatal(err)
	}
	if text := lineTexts(output); strings.Contains(text, secret) || strings.Contains(text, "entre guillemets") {
		t.Fatalf("the kept .env reached the output:\n%s", text)
	}
}

func TestEnvSecrets_ReadsTheValuesOfAnEnv(t *testing.T) {
	got := envSecrets("# commentaire ignoré\nA=1\nLONGUE=une valeur longue\n  export CLE = 'quotée ici'  \nLIGNE SANS EGAL\n\n")
	// Les plus longues d'abord : une valeur qui en contient une autre se
	// masque en entier.
	want := []string{"une valeur longue", "LIGNE SANS EGAL", "'quotée ici'", "quotée ici"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("secrets %q, want %q", got, want)
	}
}
