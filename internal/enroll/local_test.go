package enroll

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/transport"
)

// fakeCommands enregistre ce qui aurait été exécuté et rejoue ce que le test a
// décidé. Rien de l'amorçage ne s'exécute sur le poste de développement.
type fakeCommands struct {
	calls   []string
	replies map[string]Result
}

func (f *fakeCommands) Run(_ context.Context, name string, args ...string) (Result, error) {
	f.calls = append(f.calls, strings.TrimSpace(name+" "+strings.Join(args, " ")))

	key := name
	if len(args) > 0 {
		key = name + " " + args[0]
	}
	if reply, found := f.replies[key]; found {
		return reply, nil
	}
	return Result{}, nil
}

func (f *fakeCommands) played(prefix string) bool {
	for _, call := range f.calls {
		if strings.HasPrefix(call, prefix) {
			return true
		}
	}
	return false
}

type fakeTransport struct {
	probes   int
	probeErr error
	reply    transport.LauncherReply
}

func (f *fakeTransport) Probe(context.Context) error {
	f.probes++
	return f.probeErr
}

func (f *fakeTransport) CheckLauncher(context.Context) (transport.LauncherReply, error) {
	return f.reply, nil
}

type harness struct {
	deps       Deps
	systemRoot string
	stateDir   string
	commands   *fakeCommands
	sshClient  *fakeTransport
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	systemRoot := t.TempDir()
	home := filepath.Join(systemRoot, "var/lib/opencloud")
	for _, directory := range []string{
		"run/systemd/system", "usr/bin", "usr/sbin", "usr/local/sbin",
		"etc/sudoers.d", "etc/ssh/sshd_config.d", "opt/opencloud/bin", "var/lib/opencloud/state",
	} {
		if err := os.MkdirAll(filepath.Join(systemRoot, directory), 0o755); err != nil {
			t.Fatalf("préparer la racine de test : %v", err)
		}
	}
	for _, file := range []string{
		"usr/bin/sudo", "usr/sbin/visudo", "usr/sbin/sshd", "usr/sbin/usermod",
		"usr/bin/passwd", "usr/bin/systemctl",
	} {
		writeTestFile(t, filepath.Join(systemRoot, file), "#!/bin/sh\n", 0o755)
	}
	writeTestFile(t, filepath.Join(systemRoot, "opt/opencloud/bin/oc-launch"), "le lanceur, version une\n", 0o755)
	writeTestFile(t, filepath.Join(systemRoot, "etc/ssh/ssh_host_ed25519_key.pub"),
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExempleDeCleDHote root@machine\n", 0o644)
	writeTestFile(t, filepath.Join(systemRoot, "etc/ssh/ssh_host_rsa_key.pub"),
		"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCexemple root@machine\n", 0o644)
	writeTestFile(t, filepath.Join(systemRoot, "etc/passwd"),
		"root:x:0:0:root:/root:/bin/bash\n"+
			"opencloud:x:997:997:openCloud:"+home+":/usr/sbin/nologin\n", 0o644)
	writeTestFile(t, filepath.Join(systemRoot, "etc/group"),
		"root:x:0:\nsystemd-journal:x:999:\nopencloud:x:997:\n", 0o644)

	stateDir := filepath.Join(home, "state")
	root, err := os.OpenRoot(stateDir)
	if err != nil {
		t.Fatalf("ouvrir le répertoire d'état : %v", err)
	}
	t.Cleanup(func() { root.Close() })

	commands := &fakeCommands{replies: map[string]Result{
		// Le paquet crée le compte avec un mot de passe non verrouillé.
		"/usr/bin/passwd -S":           {Output: "opencloud P 09/06/2026 0 99999 7 -1"},
		"/usr/bin/systemctl is-active": {Output: "inactive", ExitCode: 3},
	}}
	sshClient := &fakeTransport{reply: transport.LauncherReply{ExitCode: 2, Output: "usage: oc-launch <id>"}}

	deps := Deps{
		Root:       root,
		SystemRoot: systemRoot,
		UID:        0,
		Out:        &strings.Builder{},
		Commands:   commands,
		Transport:  func(transport.Endpoint) LocalTransport { return sshClient },
		SSHBinary:  "/usr/bin/ssh",
		Chown:      func(string, int, int) error { return nil }, // le test ne tourne pas en root
		Now:        func() time.Time { return time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC) },
		ProbeWait:  time.Millisecond,
	}
	return &harness{deps: deps, systemRoot: systemRoot, stateDir: stateDir, commands: commands, sshClient: sshClient}
}

func writeTestFile(t *testing.T, name, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("créer %s : %v", filepath.Dir(name), err)
	}
	if err := os.WriteFile(name, []byte(content), mode); err != nil {
		t.Fatalf("écrire %s : %v", name, err)
	}
}

func (h *harness) run(t *testing.T) string {
	t.Helper()
	output := &strings.Builder{}
	h.deps.Out = output
	if err := EnrollLocal(context.Background(), h.deps); err != nil {
		t.Fatalf("EnrollLocal : %v\n%s", err, output.String())
	}
	return output.String()
}

func (h *harness) runExpectingRefusal(t *testing.T) (refusal.Refusal, string) {
	t.Helper()
	output := &strings.Builder{}
	h.deps.Out = output
	err := EnrollLocal(context.Background(), h.deps)

	var refused refusal.Refusal
	if !errors.As(err, &refused) {
		t.Fatalf("attendu un refus nommé, obtenu %v\n%s", err, output.String())
	}
	return refused, output.String()
}

func (h *harness) systemFile(t *testing.T, relative string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(h.systemRoot, relative))
	if err != nil {
		t.Fatalf("relire %s : %v", relative, err)
	}
	return string(content)
}

func (h *harness) stateFile(t *testing.T, relative string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(h.stateDir, relative))
	if err != nil {
		t.Fatalf("relire %s : %v", relative, err)
	}
	return string(content)
}

func TestEnrollLocal_PoseLeLanceurLaRegleSudoLeDropInEtLaCle(t *testing.T) {
	harness := newHarness(t)
	output := harness.run(t)

	if got := harness.systemFile(t, "usr/local/sbin/oc-launch"); got != "le lanceur, version une\n" {
		t.Errorf("lanceur posé : %q", got)
	}
	if got := harness.systemFile(t, "etc/sudoers.d/opencloud"); got != sudoersContent {
		t.Errorf("règle sudo :\n%s", got)
	}
	if got := harness.systemFile(t, "etc/ssh/sshd_config.d/opencloud.conf"); got != sshdDropInContent {
		t.Errorf("drop-in sshd :\n%s", got)
	}

	expectedKnownHosts := "127.0.0.1 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExempleDeCleDHote\n" +
		"127.0.0.1 ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCexemple\n"
	if got := harness.stateFile(t, "machines/local/known_hosts"); got != expectedKnownHosts {
		t.Errorf("known_hosts :\n%s", got)
	}

	publicKey := harness.stateFile(t, "machines/local/id_ed25519.pub")
	if !strings.HasPrefix(publicKey, "ssh-ed25519 ") || !strings.HasSuffix(publicKey, " opencloud@local\n") {
		t.Errorf("clé publique : %q", publicKey)
	}
	if got := harness.systemFile(t, "var/lib/opencloud/.ssh/authorized_keys"); got != publicKey {
		t.Errorf("authorized_keys ne porte pas exactement la clé :\n%s", got)
	}
	if got := harness.stateFile(t, "machines/local/enrolled"); got != "2026-09-06T10:00:00Z\n" {
		t.Errorf("marqueur : %q", got)
	}
	if !strings.HasSuffix(output, "résultat: fait\n") {
		t.Errorf("constat final :\n%s", output)
	}
}

func TestEnrollLocal_LaCleEstFermeeEtLeDossierAussi(t *testing.T) {
	harness := newHarness(t)
	harness.run(t)

	for name, expected := range map[string]os.FileMode{
		"machines/local":            0o700,
		"machines/local/id_ed25519": 0o600,
	} {
		info, err := os.Stat(filepath.Join(harness.stateDir, name))
		if err != nil {
			t.Fatalf("lire %s : %v", name, err)
		}
		if info.Mode().Perm() != expected {
			t.Errorf("%s en %04o, attendu %04o", name, info.Mode().Perm(), expected)
		}
	}

	info, err := os.Stat(filepath.Join(harness.systemRoot, "etc/sudoers.d/opencloud"))
	if err != nil {
		t.Fatalf("lire la règle sudo : %v", err)
	}
	if info.Mode().Perm() != 0o440 {
		t.Errorf("règle sudo en %04o, attendu 0440", info.Mode().Perm())
	}
}

func TestEnrollLocal_LOrdreDesEtapesEstUneProprieteDeSecurite(t *testing.T) {
	harness := newHarness(t)
	harness.run(t)

	expected := []string{
		"/usr/sbin/usermod -s /bin/sh opencloud",
		"/usr/bin/passwd -S opencloud",
		"/usr/bin/passwd -l opencloud",
		"/usr/sbin/usermod -aG systemd-journal opencloud",
		"/usr/sbin/visudo -c -f " + filepath.Join(harness.systemRoot, "etc/sudoers.d/opencloud.tmp"),
		"/usr/sbin/sshd -t",
		"/usr/bin/systemctl is-active ssh.service",
		"/usr/bin/systemctl enable --now ssh.service",
	}
	if len(harness.commands.calls) != len(expected) {
		t.Fatalf("commandes jouées :\n%s", strings.Join(harness.commands.calls, "\n"))
	}
	for index, call := range expected {
		if harness.commands.calls[index] != call {
			t.Errorf("commande %d : %q, attendue %q", index, harness.commands.calls[index], call)
		}
	}
}

func TestEnrollLocal_RejoueDeuxFoisNeChangeRien(t *testing.T) {
	harness := newHarness(t)
	harness.run(t)

	firstKey := harness.stateFile(t, "machines/local/id_ed25519")
	harness.commands.calls = nil
	harness.commands.replies["/usr/bin/passwd -S"] = Result{Output: "opencloud L 09/06/2026 0 99999 7 -1"}
	// Le paquet a mis nologin ; le premier passage a posé /bin/sh.
	writeTestFile(t, filepath.Join(harness.systemRoot, "etc/passwd"),
		"root:x:0:0:root:/root:/bin/bash\n"+
			"opencloud:x:997:997:openCloud:"+filepath.Join(harness.systemRoot, "var/lib/opencloud")+":/bin/sh\n", 0o644)
	// Et usermod -aG a mis le compte dans le groupe du journal.
	writeTestFile(t, filepath.Join(harness.systemRoot, "etc/group"),
		"root:x:0:\nsystemd-journal:x:999:opencloud\nopencloud:x:997:\n", 0o644)

	output := harness.run(t)

	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if strings.HasPrefix(line, "étape:") && !strings.HasSuffix(line, "inchangé") &&
			!strings.Contains(line, "vérifié par SSH") {
			t.Errorf("une étape a changé quelque chose au second passage : %q", line)
		}
	}
	if !strings.HasSuffix(output, "résultat: inchangé\n") {
		t.Errorf("constat final :\n%s", output)
	}
	if harness.commands.played("/usr/sbin/usermod") || harness.commands.played("/usr/bin/passwd -l") {
		t.Errorf("le compte a été retouché :\n%s", strings.Join(harness.commands.calls, "\n"))
	}
	if harness.commands.played("/usr/bin/systemctl reload") || harness.commands.played("/usr/bin/systemctl enable") {
		t.Errorf("sshd a été rechargé pour rien :\n%s", strings.Join(harness.commands.calls, "\n"))
	}
	if got := harness.stateFile(t, "machines/local/id_ed25519"); got != firstKey {
		t.Error("la clé a été remplacée")
	}
}

func TestEnrollLocal_ChaqueManqueDuPreflightEstUnRefusNomme(t *testing.T) {
	cases := []struct {
		name    string
		damage  func(t *testing.T, h *harness)
		expects string
	}{
		{"hors root", func(_ *testing.T, h *harness) { h.deps.UID = 1000 }, "root"},
		{"sans systemd", func(t *testing.T, h *harness) { removeAll(t, h, "run/systemd/system") }, "systemd"},
		{"sans sudo", func(t *testing.T, h *harness) { removeAll(t, h, "usr/bin/sudo") }, "sudo"},
		{"sans visudo", func(t *testing.T, h *harness) { removeAll(t, h, "usr/sbin/visudo") }, "visudo"},
		{"sans sshd", func(t *testing.T, h *harness) { removeAll(t, h, "usr/sbin/sshd") }, "sshd"},
		{"sans sshd_config.d", func(t *testing.T, h *harness) { removeAll(t, h, "etc/ssh/sshd_config.d") }, "sshd_config.d"},
		{"sans lanceur", func(t *testing.T, h *harness) { removeAll(t, h, "opt/opencloud/bin/oc-launch") }, "oc-launch"},
		{"sans compte", func(t *testing.T, h *harness) {
			writeTestFile(t, filepath.Join(h.systemRoot, "etc/passwd"), "root:x:0:0:root:/root:/bin/bash\n", 0o644)
		}, "opencloud"},
	}

	for _, testCase := range cases {
		harness := newHarness(t)
		testCase.damage(t, harness)

		refused, output := harness.runExpectingRefusal(t)
		if !strings.Contains(refused.Cause, testCase.expects) {
			t.Errorf("%s : cause %q, attendait %q dedans", testCase.name, refused.Cause, testCase.expects)
		}
		if refused.Remedy == "" {
			t.Errorf("%s : refus sans remède", testCase.name)
		}
		if strings.Contains(output, "étape:") {
			t.Errorf("%s : une étape a eu lieu avant le refus :\n%s", testCase.name, output)
		}
		if _, err := os.Stat(filepath.Join(harness.systemRoot, "etc/sudoers.d/opencloud")); err == nil {
			t.Errorf("%s : la règle sudo a été posée malgré le refus", testCase.name)
		}
	}
}

func removeAll(t *testing.T, h *harness, relative string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(h.systemRoot, relative)); err != nil {
		t.Fatalf("retirer %s : %v", relative, err)
	}
}

func TestEnrollLocal_UneRegleSudoRefuseeParVisudoNestPasPosee(t *testing.T) {
	harness := newHarness(t)
	harness.commands.replies["/usr/sbin/visudo -c"] = Result{ExitCode: 1, Output: ">>> syntax error near line 2 <<<"}

	refused, _ := harness.runExpectingRefusal(t)
	if !strings.Contains(refused.Cause, "visudo") {
		t.Errorf("cause : %q", refused.Cause)
	}
	if _, err := os.Stat(filepath.Join(harness.systemRoot, "etc/sudoers.d/opencloud")); err == nil {
		t.Error("la règle a été posée alors que visudo l'a refusée")
	}
	if _, err := os.Stat(filepath.Join(harness.systemRoot, "etc/sudoers.d/opencloud.tmp")); err == nil {
		t.Error("le temporaire est resté dans /etc/sudoers.d")
	}
}

func TestEnrollLocal_UnDropInQueSshdRefuseEstRetire(t *testing.T) {
	harness := newHarness(t)
	harness.commands.replies["/usr/sbin/sshd -t"] = Result{ExitCode: 1, Output: "/etc/ssh/sshd_config line 12: Bad configuration option"}

	refused, _ := harness.runExpectingRefusal(t)
	if !strings.Contains(refused.Cause, "sshd") {
		t.Errorf("cause : %q", refused.Cause)
	}
	if _, err := os.Stat(filepath.Join(harness.systemRoot, "etc/ssh/sshd_config.d/opencloud.conf")); err == nil {
		t.Error("le drop-in est resté alors que sshd -t l'a refusé")
	}
	if harness.commands.played("/usr/bin/systemctl reload") {
		t.Error("sshd a été rechargé avec un drop-in refusé")
	}
}

func TestEnrollLocal_SshdActifEstRechargeEtNonRedemarre(t *testing.T) {
	harness := newHarness(t)
	harness.commands.replies["/usr/bin/systemctl is-active"] = Result{Output: "active"}

	harness.run(t)
	if !harness.commands.played("/usr/bin/systemctl reload ssh.service") {
		t.Errorf("commandes :\n%s", strings.Join(harness.commands.calls, "\n"))
	}
}

func TestEnrollLocal_SudoQuiDemandeUnMotDePasseEstUnRefusNomme(t *testing.T) {
	harness := newHarness(t)
	harness.sshClient.reply = transport.LauncherReply{ExitCode: 1, Output: "sudo: a password is required"}

	refused, _ := harness.runExpectingRefusal(t)
	if !strings.Contains(refused.Cause, "règle sudo n'a pas pris") {
		t.Errorf("cause : %q", refused.Cause)
	}
}

func TestEnrollLocal_UnLanceurQuiNeRefusePasEstUnRefus(t *testing.T) {
	harness := newHarness(t)
	harness.sshClient.reply = transport.LauncherReply{ExitCode: 0}

	refused, _ := harness.runExpectingRefusal(t)
	if !strings.Contains(refused.Cause, "lanceur") {
		t.Errorf("cause : %q", refused.Cause)
	}
}

func TestEnrollLocal_LaMachineEstSondeeTroisFoisAvantDAbandonner(t *testing.T) {
	harness := newHarness(t)
	harness.sshClient.probeErr = fmt.Errorf("%w: connexion refusée", transport.ErrUnreachable)

	refused, _ := harness.runExpectingRefusal(t)
	if harness.sshClient.probes != probeAttempts {
		t.Errorf("%d sondes, attendu %d", harness.sshClient.probes, probeAttempts)
	}
	if !strings.Contains(refused.Cause, "ne répond pas en SSH") {
		t.Errorf("cause : %q", refused.Cause)
	}
}

func TestEnrollLocal_AuthorizedKeysNeGardeQueLaCleDOpenCloud(t *testing.T) {
	harness := newHarness(t)
	writeTestFile(t, filepath.Join(harness.systemRoot, "var/lib/opencloud/.ssh/authorized_keys"),
		"ssh-rsa AAAAB3NzaC1yc2E cle-d-un-tiers\n", 0o600)

	harness.run(t)

	publicKey := harness.stateFile(t, "machines/local/id_ed25519.pub")
	if got := harness.systemFile(t, "var/lib/opencloud/.ssh/authorized_keys"); got != publicKey {
		t.Errorf("authorized_keys :\n%s", got)
	}
}

func TestEnrollLocal_UnLanceurDifferentEstRepose(t *testing.T) {
	harness := newHarness(t)
	harness.run(t)

	writeTestFile(t, filepath.Join(harness.systemRoot, "opt/opencloud/bin/oc-launch"), "le lanceur, version deux\n", 0o755)
	output := harness.run(t)

	if got := harness.systemFile(t, "usr/local/sbin/oc-launch"); got != "le lanceur, version deux\n" {
		t.Errorf("lanceur : %q", got)
	}
	if !strings.Contains(output, "lanceur /usr/local/sbin/oc-launch posé") {
		t.Errorf("étapes :\n%s", output)
	}
}

func TestReadStatus_DitDepuisQuandEtAvecQuelleCle(t *testing.T) {
	harness := newHarness(t)
	harness.run(t)

	status, err := ReadStatus(harness.deps.Root, LocalMachineID)
	if err != nil {
		t.Fatalf("ReadStatus : %v", err)
	}
	if !status.Enrolled {
		t.Fatal("machine annoncée non enrôlée")
	}
	if !status.Since.Equal(time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("date %s", status.Since)
	}
	if !strings.HasPrefix(status.PublicKey, "ssh-ed25519 ") {
		t.Errorf("clé publique %q", status.PublicKey)
	}
}

func TestReadStatus_UneMachineInconnueNestPasUneErreur(t *testing.T) {
	harness := newHarness(t)

	status, err := ReadStatus(harness.deps.Root, LocalMachineID)
	if err != nil {
		t.Fatalf("ReadStatus : %v", err)
	}
	if status.Enrolled {
		t.Error("machine annoncée enrôlée sans amorçage")
	}
}

func TestLocalEndpoint_PointeVersLocalhostAvecLesFichiersDeLaMachine(t *testing.T) {
	harness := newHarness(t)

	endpoint := LocalEndpoint(harness.deps.Root)
	if endpoint.Address != "127.0.0.1" || endpoint.Port != 22 || endpoint.Account != "opencloud" {
		t.Errorf("endpoint %+v", endpoint)
	}
	if endpoint.IdentityFile != filepath.Join(harness.stateDir, "machines/local/id_ed25519") {
		t.Errorf("clé : %s", endpoint.IdentityFile)
	}
	if endpoint.KnownHostsFile != filepath.Join(harness.stateDir, "machines/local/known_hosts") {
		t.Errorf("known_hosts : %s", endpoint.KnownHostsFile)
	}
	if endpoint.Binary != "/usr/bin/ssh" {
		t.Errorf("binaire : %s", endpoint.Binary)
	}
}

func TestEnrollLocal_SansLeGroupeDuJournalLeCompteNeVerraitRien(t *testing.T) {
	harness := newHarness(t)
	writeTestFile(t, filepath.Join(harness.systemRoot, "etc/group"), "root:x:0:\nopencloud:x:997:\n", 0o644)

	refused, _ := harness.runExpectingRefusal(t)

	if !strings.Contains(refused.Cause, "systemd-journal") {
		t.Fatalf("le refus doit nommer le groupe : %s", refused.Cause)
	}
	if harness.commands.played("/usr/sbin/visudo") {
		t.Fatal("rien ne doit être posé après le refus")
	}
}
