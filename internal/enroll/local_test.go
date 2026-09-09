package enroll

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/transport"
)

// Ce que le script écrit quand il a posé la séquence, puis quand il n'a plus
// rien à poser. L'amorçage relaie ces lignes et retient le constat.
var (
	scriptDidWork = []string{
		"étape: le compte opencloud",
		"étape: compte opencloud créé",
		"étape: la règle sudo /etc/sudoers.d/opencloud",
		"étape: le drop-in sshd /etc/ssh/sshd_config.d/opencloud.conf",
		"étape: la clé autorisée /var/lib/opencloud/.ssh/authorized_keys",
		"résultat: fait",
	}
	scriptDidNothing = []string{
		"étape: le compte opencloud — inchangé",
		"étape: la règle sudo /etc/sudoers.d/opencloud — inchangé",
		"résultat: inchangé",
	}
)

// fakeScripts joue ce que le test a décidé à la place du script : rien de
// l'amorçage ne s'exécute sur le poste de développement.
type fakeScripts struct {
	runs     int
	played   LocalScript
	lines    []string
	exitCode int
	// before est joué avant les lignes : de quoi observer l'état de la machine
	// au moment précis où le script tourne.
	before func()
}

func (f *fakeScripts) Run(_ context.Context, script LocalScript, line func(string)) (int, error) {
	f.runs++
	f.played = script
	if f.before != nil {
		f.before()
	}
	for _, written := range f.lines {
		line(written)
	}
	return f.exitCode, nil
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
	scripts    *fakeScripts
	sshClient  *fakeTransport
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	systemRoot := t.TempDir()
	home := filepath.Join(systemRoot, "var/lib/opencloud")
	for _, directory := range []string{
		"bin", "usr/local/sbin", "opt/opencloud/bin", "var/lib/opencloud/state",
	} {
		if err := os.MkdirAll(filepath.Join(systemRoot, directory), 0o755); err != nil {
			t.Fatalf("préparer la racine de test : %v", err)
		}
	}
	writeTestFile(t, filepath.Join(systemRoot, "bin/bash"), "#!/bin/sh\n", 0o755)
	writeTestFile(t, filepath.Join(systemRoot, "opt/opencloud/bin/oc-launch"), "le lanceur, version une\n", 0o755)
	writeTestFile(t, filepath.Join(systemRoot, "etc/ssh/ssh_host_ed25519_key.pub"),
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExempleDeCleDHote root@machine\n", 0o644)
	writeTestFile(t, filepath.Join(systemRoot, "etc/ssh/ssh_host_rsa_key.pub"),
		"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCexemple root@machine\n", 0o644)
	writeTestFile(t, filepath.Join(systemRoot, "etc/passwd"),
		"root:x:0:0:root:/root:/bin/bash\n"+
			"opencloud:x:997:997:openCloud:"+home+":/usr/sbin/nologin\n", 0o644)

	stateDir := filepath.Join(home, "state")
	root, err := os.OpenRoot(stateDir)
	if err != nil {
		t.Fatalf("ouvrir le répertoire d'état : %v", err)
	}
	t.Cleanup(func() { root.Close() })

	scripts := &fakeScripts{lines: scriptDidWork}
	sshClient := &fakeTransport{reply: transport.LauncherReply{ExitCode: 2, Output: "usage: oc-launch <id>"}}

	deps := Deps{
		Root:       root,
		SystemRoot: systemRoot,
		UID:        0,
		Out:        &strings.Builder{},
		Scripts:    scripts,
		Transport:  func(transport.Endpoint) LocalTransport { return sshClient },
		SSHBinary:  "/usr/bin/ssh",
		Chown:      func(string, int, int) error { return nil }, // le test ne tourne pas en root
		Now:        func() time.Time { return time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC) },
		ProbeWait:  time.Millisecond,
	}
	return &harness{deps: deps, systemRoot: systemRoot, stateDir: stateDir, scripts: scripts, sshClient: sshClient}
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

func (h *harness) hasSystemFile(relative string) bool {
	_, err := os.Stat(filepath.Join(h.systemRoot, relative))
	return err == nil
}

func TestEnrollLocal_PoseLaCleLeLanceurEtLesClesDHote(t *testing.T) {
	harness := newHarness(t)
	output := harness.run(t)

	if got := harness.systemFile(t, "usr/local/sbin/oc-launch"); got != "le lanceur, version une\n" {
		t.Errorf("lanceur posé : %q", got)
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
	if got := harness.stateFile(t, "machines/local/enrolled"); got != "2026-09-06T10:00:00Z\n" {
		t.Errorf("marqueur : %q", got)
	}
	if !strings.HasSuffix(output, "résultat: fait\n") {
		t.Errorf("constat final :\n%s", output)
	}
}

// La séquence — compte, sudo, sshd, clé — est jouée par le script de l'action
// Enrôler, celui-là même qu'un opérateur colle sur une machine distante.
func TestEnrollLocal_JoueLeScriptEnrolerAvecLaCleDansSonEnvironnement(t *testing.T) {
	harness := newHarness(t)
	harness.run(t)

	if harness.scripts.runs != 1 {
		t.Fatalf("%d passage(s) du script, attendu 1", harness.scripts.runs)
	}
	played := harness.scripts.played
	if !strings.HasPrefix(string(played.Content), "#!/bin/bash\n") {
		t.Errorf("le script joué ne commence pas par le shebang : %.20q", played.Content)
	}
	if !strings.Contains(string(played.Content), "OC_PUBLIC_KEY") {
		t.Error("le script joué n'est pas celui de l'enrôlement")
	}
	if played.Timeout <= 0 {
		t.Error("le script est joué sans délai maximum")
	}

	publicKey := strings.TrimSpace(harness.stateFile(t, "machines/local/id_ed25519.pub"))
	expected := []string{"OC_PUBLIC_KEY=" + publicKey}
	if !slices.Equal(played.Environment, expected) {
		t.Errorf("environnement %v, attendu %v", played.Environment, expected)
	}
}

// L'ordre est une propriété de sécurité : la clé existe avant que le script la
// pose, et le lanceur arrive après la séquence, comme sur une machine distante.
func TestEnrollLocal_LOrdreDesEtapesEstUneProprieteDeSecurite(t *testing.T) {
	harness := newHarness(t)
	var keyReady, launcherAlreadyThere bool
	harness.scripts.before = func() {
		_, err := os.Stat(filepath.Join(harness.stateDir, "machines/local/id_ed25519"))
		keyReady = err == nil
		launcherAlreadyThere = harness.hasSystemFile("usr/local/sbin/oc-launch")
	}

	harness.run(t)

	if !keyReady {
		t.Error("le script est joué avant que la paire de clés existe")
	}
	if launcherAlreadyThere {
		t.Error("le lanceur est posé avant la séquence : il vient après, une fois le compte en place")
	}
}

// Une seule ligne de constat pour toute la séquence : celle du script est
// retenue, pas relayée.
func TestEnrollLocal_RelaieLesLignesDuScriptSaufSonConstat(t *testing.T) {
	harness := newHarness(t)
	output := harness.run(t)

	for _, line := range scriptDidWork[:len(scriptDidWork)-1] {
		if !strings.Contains(output, line+"\n") {
			t.Errorf("le script écrit %q, absent de la sortie :\n%s", line, output)
		}
	}
	if got := strings.Count(output, "résultat:"); got != 1 {
		t.Errorf("%d lignes de constat, attendu 1 :\n%s", got, output)
	}
}

func TestEnrollLocal_RejoueDeuxFoisNeChangeRien(t *testing.T) {
	harness := newHarness(t)
	harness.run(t)

	firstKey := harness.stateFile(t, "machines/local/id_ed25519")
	harness.scripts.lines = scriptDidNothing

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
	if got := harness.stateFile(t, "machines/local/id_ed25519"); got != firstKey {
		t.Error("la clé a été remplacée")
	}
}

// Un refus du script est celui de l'opérateur : sa cause et son remède
// traversent tels quels, et rien ne continue derrière.
func TestEnrollLocal_UnRefusDuScriptEstRenduTelQuel(t *testing.T) {
	harness := newHarness(t)
	harness.scripts.exitCode = 2
	harness.scripts.lines = []string{
		"étape: la règle sudo /etc/sudoers.d/opencloud",
		"résultat: refusé — visudo refuse la règle sudo d'openCloud : syntax error",
		"→ corriger /etc/sudoers, puis rejouer la commande",
	}

	refused, output := harness.runExpectingRefusal(t)

	if refused.Cause != "visudo refuse la règle sudo d'openCloud : syntax error" {
		t.Errorf("cause : %q", refused.Cause)
	}
	if refused.Remedy != "corriger /etc/sudoers, puis rejouer la commande" {
		t.Errorf("remède : %q", refused.Remedy)
	}
	if harness.hasSystemFile("usr/local/sbin/oc-launch") {
		t.Error("le lanceur a été posé malgré le refus")
	}
	if harness.sshClient.probes != 0 {
		t.Error("la machine a été sondée malgré le refus")
	}
	if !strings.HasSuffix(output, "résultat: refusé\n") {
		t.Errorf("constat final :\n%s", output)
	}
}

func TestEnrollLocal_UnEchecDuScriptArreteLAmorcage(t *testing.T) {
	harness := newHarness(t)
	harness.scripts.exitCode = 1
	harness.scripts.lines = []string{"résultat: échoué — une commande de la séquence a échoué, ligne 120"}

	output := &strings.Builder{}
	harness.deps.Out = output
	err := EnrollLocal(context.Background(), harness.deps)

	if err == nil || isRefusal(err) {
		t.Fatalf("attendu une erreur, obtenu %v", err)
	}
	if !strings.Contains(err.Error(), "ligne 120") {
		t.Errorf("erreur : %v", err)
	}
	if !strings.HasSuffix(output.String(), "résultat: échoué\n") {
		t.Errorf("constat final :\n%s", output)
	}
}

// Un script qui rend 0 sans rien conclure n'a pas fini son travail : on ne
// vérifie pas une machine sur une sortie muette.
func TestEnrollLocal_UnScriptSansConstatEstUneErreur(t *testing.T) {
	harness := newHarness(t)
	harness.scripts.lines = []string{"étape: le compte opencloud"}

	err := EnrollLocal(context.Background(), harness.deps)
	if err == nil || !strings.Contains(err.Error(), "sans constat") {
		t.Fatalf("erreur : %v", err)
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
}

func TestEnrollLocal_ChaqueManqueDuPreflightEstUnRefusNomme(t *testing.T) {
	cases := []struct {
		name    string
		damage  func(t *testing.T, h *harness)
		expects string
	}{
		{"hors root", func(_ *testing.T, h *harness) { h.deps.UID = 1000 }, "root"},
		{"sans bash", func(t *testing.T, h *harness) { removeAll(t, h, "bin/bash") }, "bash"},
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
		if harness.scripts.runs != 0 {
			t.Errorf("%s : le script a été joué malgré le refus", testCase.name)
		}
	}
}

func removeAll(t *testing.T, h *harness, relative string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(h.systemRoot, relative)); err != nil {
		t.Fatalf("retirer %s : %v", relative, err)
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

func TestEnrollLocal_LeDossierMachinesEtSonParentAppartiennentAuCompte(t *testing.T) {
	harness := newHarness(t)
	var owned []string
	harness.deps.Chown = func(name string, uid, gid int) error {
		if uid == 997 && strings.HasPrefix(name, harness.stateDir) {
			owned = append(owned, strings.TrimPrefix(name, harness.stateDir+"/"))
		}
		return nil
	}

	harness.run(t)

	for _, expected := range []string{"machines", "machines/local"} {
		if !slices.Contains(owned, expected) {
			t.Errorf("%s n'a pas été donné au compte opencloud ; donnés : %v", expected, owned)
		}
	}
}
