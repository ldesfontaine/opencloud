package transport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/actiondir"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// fakeSSH est une copie du script testdata/fake-ssh dans un dossier
// temporaire : il y dépose ce qu'il a reçu et y lit ce qu'il doit rejouer.
type fakeSSH struct {
	directory string
	binary    string
}

func newFakeSSH(t *testing.T) *fakeSSH {
	t.Helper()

	directory := t.TempDir()
	script, err := os.ReadFile("testdata/fake-ssh")
	if err != nil {
		t.Fatalf("lire le faux ssh : %v", err)
	}
	binary := filepath.Join(directory, "ssh")
	if err := os.WriteFile(binary, script, 0o700); err != nil {
		t.Fatalf("poser le faux ssh : %v", err)
	}
	return &fakeSSH{directory: directory, binary: binary}
}

func (f *fakeSSH) replies(t *testing.T, exitCode int, output string) {
	t.Helper()
	f.write(t, "exit-code", strconv.Itoa(exitCode))
	f.write(t, "stdout", output)
}

func (f *fakeSSH) write(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.directory, name), []byte(content), 0o600); err != nil {
		t.Fatalf("écrire %s : %v", name, err)
	}
}

func (f *fakeSSH) read(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(f.directory, name))
	if err != nil {
		t.Fatalf("relire %s : %v", name, err)
	}
	return string(content)
}

func (f *fakeSSH) arguments(t *testing.T) []string {
	t.Helper()
	return strings.Split(strings.TrimSuffix(f.read(t, "args"), "\n"), "\n")
}

// remoteCommand : le dernier argument du vecteur, ce que le shell du compte
// distant recevra.
func (f *fakeSSH) remoteCommand(t *testing.T) string {
	t.Helper()
	arguments := f.arguments(t)
	return arguments[len(arguments)-1]
}

func (f *fakeSSH) client() *SSH {
	return NewSSH(Endpoint{
		Address:        "127.0.0.1",
		Port:           22,
		Account:        "opencloud",
		IdentityFile:   "/var/lib/opencloud/machines/local/id_ed25519",
		KnownHostsFile: "/var/lib/opencloud/machines/local/known_hosts",
		Binary:         f.binary,
	})
}

func TestProbe_JoueLeVecteurBorneOptionParOption(t *testing.T) {
	fake := newFakeSSH(t)
	if err := fake.client().Probe(context.Background()); err != nil {
		t.Fatalf("Probe : %v", err)
	}

	expected, err := os.ReadFile("testdata/vector-probe.txt")
	if err != nil {
		t.Fatalf("lire le vecteur attendu : %v", err)
	}
	if got := fake.read(t, "args"); got != string(expected) {
		t.Errorf("vecteur inattendu :\n%s\nattendu :\n%s", got, expected)
	}
}

func TestProbe_NeTransmetQuUnEnvironnementRemplace(t *testing.T) {
	t.Setenv("OPENCLOUD_MARQUEUR_DE_TEST", "présent")

	fake := newFakeSSH(t)
	if err := fake.client().Probe(context.Background()); err != nil {
		t.Fatalf("Probe : %v", err)
	}

	environment := fake.read(t, "env")
	if strings.Contains(environment, "OPENCLOUD_MARQUEUR_DE_TEST") {
		t.Errorf("l'environnement de l'appelant a fuité :\n%s", environment)
	}
	for _, expected := range []string{"PATH=/usr/bin:/bin", "LC_ALL=C"} {
		if !strings.Contains(environment, expected) {
			t.Errorf("%s manque dans :\n%s", expected, environment)
		}
	}
}

func TestProbe_CodeDeuxCentCinquanteCinqDitLaMachineInjoignable(t *testing.T) {
	fake := newFakeSSH(t)
	fake.replies(t, 255, "")
	fake.write(t, "stderr", "ssh: connect to host 127.0.0.1 port 22: Connection refused")

	err := fake.client().Probe(context.Background())
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("attendu ErrUnreachable, obtenu %v", err)
	}
}

func TestPut_DeposeParStdinEtRenommeApresCoup(t *testing.T) {
	fake := newFakeSSH(t)
	content := []byte("#!/bin/bash\nset -euo pipefail\n")

	if err := fake.client().Put(context.Background(), "abc123", "run.sh", content, 0o755); err != nil {
		t.Fatalf("Put : %v", err)
	}

	if got := fake.read(t, "stdin"); got != string(content) {
		t.Errorf("contenu passé autrement que par stdin : %q", got)
	}
	expected := "umask 077 && mkdir -p -- /var/lib/opencloud/actions/abc123 && " +
		"cat > /var/lib/opencloud/actions/abc123/run.sh.tmp && " +
		"chmod 0755 /var/lib/opencloud/actions/abc123/run.sh.tmp && " +
		"mv -f -- /var/lib/opencloud/actions/abc123/run.sh.tmp /var/lib/opencloud/actions/abc123/run.sh"
	if got := fake.remoteCommand(t); got != expected {
		t.Errorf("commande distante :\n%s\nattendue :\n%s", got, expected)
	}
}

func TestPut_UnFichierRenduVaSousFiles(t *testing.T) {
	fake := newFakeSSH(t)

	if err := fake.client().Put(context.Background(), "abc123", "files/traefik/route.yml", []byte("x"), 0o600); err != nil {
		t.Fatalf("Put : %v", err)
	}
	if !strings.Contains(fake.remoteCommand(t), "mkdir -p -- /var/lib/opencloud/actions/abc123/files/traefik ") {
		t.Errorf("le dossier du fichier n'est pas créé : %s", fake.remoteCommand(t))
	}
	if !strings.Contains(fake.remoteCommand(t), "chmod 0600 ") {
		t.Errorf("mode inattendu : %s", fake.remoteCommand(t))
	}
}

func TestPut_RefuseToutNomQuiNestPasInoffensif(t *testing.T) {
	for _, name := range []string{
		"", "autre.sh", "../run.sh", "files", "files/", "files/../../etc/passwd",
		"files/a b", "files/-rf", "files//x", "files/x;reboot", "files/x'y", "run.sh ",
	} {
		fake := newFakeSSH(t)
		err := fake.client().Put(context.Background(), "abc123", name, []byte("x"), 0o600)
		if !errors.Is(err, ErrRefusedName) {
			t.Errorf("%q accepté (%v)", name, err)
		}
	}
}

func TestPut_RevalideLIdentifiantDAction(t *testing.T) {
	fake := newFakeSSH(t)
	err := fake.client().Put(context.Background(), "abc; reboot", "run.sh", []byte("x"), 0o600)
	if !errors.Is(err, ErrRefusedName) {
		t.Fatalf("identifiant accepté : %v", err)
	}
}

func TestLaunch_JoueLeVecteurFixeDuLanceur(t *testing.T) {
	fake := newFakeSSH(t)
	if _, err := fake.client().Launch(context.Background(), "abc123"); err != nil {
		t.Fatalf("Launch : %v", err)
	}
	if got, expected := fake.remoteCommand(t), "sudo -n /usr/local/sbin/oc-launch abc123"; got != expected {
		t.Errorf("commande distante %q, attendue %q", got, expected)
	}
}

func TestLaunch_RelitLeNombreDeDossiersPurgesParLeLanceur(t *testing.T) {
	fake := newFakeSSH(t)
	fake.replies(t, 0, actiondir.FormatPurged(4))

	purged, err := fake.client().Launch(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("Launch : %v", err)
	}
	if purged != 4 {
		t.Errorf("dossiers purgés = %d, attendu 4", purged)
	}
}

func TestLaunch_UneSortieDeLanceurInattendueNeComptePas(t *testing.T) {
	for _, output := range []string{"", "purged-directories: beaucoup", "purged-directories: -3", "bonjour"} {
		fake := newFakeSSH(t)
		fake.replies(t, 0, output)

		purged, err := fake.client().Launch(context.Background(), "abc123")
		if err != nil {
			t.Fatalf("%q : Launch : %v", output, err)
		}
		if purged != 0 {
			t.Errorf("%q : dossiers purgés = %d, attendu 0", output, purged)
		}
	}
}

func TestLaunch_RevalideLIdentifiantDAction(t *testing.T) {
	fake := newFakeSSH(t)
	if _, err := fake.client().Launch(context.Background(), "ABC 123"); !errors.Is(err, ErrRefusedName) {
		t.Fatalf("identifiant accepté : %v", err)
	}
}

func TestLaunch_UneUniteDejaLanceeSeSuitAuLieuDeSeRelancer(t *testing.T) {
	fake := newFakeSSH(t)
	fake.replies(t, 1, "Failed to start transient service unit: Unit oc-action-abc123.service already exists.")

	_, err := fake.client().Launch(context.Background(), "abc123")
	if !errors.Is(err, ErrAlreadyLaunched) {
		t.Fatalf("attendu ErrAlreadyLaunched, obtenu %v", err)
	}
}

func TestLaunch_LesDeuxRefusDeSudoSontDistincts(t *testing.T) {
	cases := []struct {
		output string
		cause  string
	}{
		{"sudo: a password is required", "mot de passe"},
		{"sudo: a terminal is required to read the password", "terminal"},
	}
	for _, testCase := range cases {
		fake := newFakeSSH(t)
		fake.replies(t, 1, testCase.output)

		_, err := fake.client().Launch(context.Background(), "abc123")
		if !errors.Is(err, ErrLaunchRefused) {
			t.Fatalf("%q : attendu ErrLaunchRefused, obtenu %v", testCase.output, err)
		}
		var refused refusal.Refusal
		if !errors.As(err, &refused) {
			t.Fatalf("%q : refus non nommé : %v", testCase.output, err)
		}
		if !strings.Contains(refused.Cause, testCase.cause) {
			t.Errorf("%q : cause %q, attendait %q dedans", testCase.output, refused.Cause, testCase.cause)
		}
	}
}

func TestLaunch_UnAutreCodeEstUnRefusDeLancement(t *testing.T) {
	fake := newFakeSSH(t)
	fake.replies(t, 2, "oc-launch: identifiant refusé")

	_, err := fake.client().Launch(context.Background(), "abc123")
	if !errors.Is(err, ErrLaunchRefused) {
		t.Fatalf("attendu ErrLaunchRefused, obtenu %v", err)
	}
	if !strings.Contains(err.Error(), "identifiant refusé") {
		t.Errorf("la sortie du lanceur manque au message : %v", err)
	}
}

func TestLaunch_MachineInjoignableNestPasUnEchecDeLAction(t *testing.T) {
	fake := newFakeSSH(t)
	fake.replies(t, 255, "")

	_, err := fake.client().Launch(context.Background(), "abc123")
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("attendu ErrUnreachable, obtenu %v", err)
	}
}

func TestInstallLauncher_DeposeParStdinPuisFaitPoserParSudo(t *testing.T) {
	fake := newFakeSSH(t)
	binary := []byte("\x7fELF le lanceur")

	if err := fake.client().InstallLauncher(context.Background(), binary); err != nil {
		t.Fatalf("InstallLauncher : %v", err)
	}

	if got := fake.read(t, "stdin"); got != string(binary) {
		t.Errorf("le lanceur est passé autrement que par stdin : %q", got)
	}
	expected := "umask 077 && cat > /var/lib/opencloud/oc-launch.new.tmp && " +
		"mv -f -- /var/lib/opencloud/oc-launch.new.tmp /var/lib/opencloud/oc-launch.new && " +
		"sudo -n /usr/bin/install -o root -g root -m 0755 " +
		"/var/lib/opencloud/oc-launch.new /usr/local/sbin/oc-launch && " +
		"rm -f -- /var/lib/opencloud/oc-launch.new"
	if got := fake.remoteCommand(t); got != expected {
		t.Errorf("commande distante :\n%s\nattendue :\n%s", got, expected)
	}
}

func TestInstallLauncher_SudoQuiDemandeUnMotDePasseEstUnRefusNomme(t *testing.T) {
	fake := newFakeSSH(t)
	fake.replies(t, 1, "sudo: a password is required")

	err := fake.client().InstallLauncher(context.Background(), []byte("le lanceur"))

	if !errors.Is(err, ErrLaunchRefused) {
		t.Fatalf("attendu ErrLaunchRefused, obtenu %v", err)
	}
	var refused refusal.Refusal
	if !errors.As(err, &refused) {
		t.Fatalf("refus non nommé : %v", err)
	}
	if !strings.Contains(refused.Cause, "mot de passe") {
		t.Errorf("cause : %q", refused.Cause)
	}
}

func TestInstallLauncher_MachineInjoignableEstDiteTelleQuelle(t *testing.T) {
	fake := newFakeSSH(t)
	fake.replies(t, 255, "")

	err := fake.client().InstallLauncher(context.Background(), []byte("le lanceur"))
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("attendu ErrUnreachable, obtenu %v", err)
	}
}

func TestCheckLauncher_AppelleLeLanceurSansArgument(t *testing.T) {
	fake := newFakeSSH(t)
	fake.replies(t, 2, "usage: oc-launch <id>")

	reply, err := fake.client().CheckLauncher(context.Background())
	if err != nil {
		t.Fatalf("CheckLauncher : %v", err)
	}
	if got, expected := fake.remoteCommand(t), "sudo -n /usr/local/sbin/oc-launch"; got != expected {
		t.Errorf("commande distante %q, attendue %q", got, expected)
	}
	if reply.ExitCode != 2 || !strings.Contains(reply.Output, "usage") {
		t.Errorf("réponse inattendue : %+v", reply)
	}
}

func TestSortie_EstBorneeEnTaille(t *testing.T) {
	fake := newFakeSSH(t)
	fake.replies(t, 1, strings.Repeat("a", 3*maxOutputBytes))

	_, err := fake.client().Launch(context.Background(), "abc123")
	if err == nil {
		t.Fatal("attendu une erreur")
	}
	if len(err.Error()) > maxOutputBytes+200 {
		t.Errorf("message de %d octets, sortie non bornée", len(err.Error()))
	}
	if !strings.Contains(err.Error(), "tronquée") {
		t.Errorf("la troncature n'est pas dite : %d octets", len(err.Error()))
	}
}

func TestPut_UnSshTueParUnSignalEstInjoignable(t *testing.T) {
	fake := newFakeSSH(t)
	fake.write(t, "kill-self", "TERM")

	err := fake.client().Put(context.Background(), "abc123", "run.sh", []byte("#!/bin/bash\n"), 0o755)

	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("un ssh tué en plein dépôt se lit « on ne sait rien pour l'instant », obtenu %v", err)
	}
}
