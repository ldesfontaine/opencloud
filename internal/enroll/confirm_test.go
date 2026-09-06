package enroll

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/transport"
)

// Deux clés d'hôte d'une machine jetée, avec les empreintes que « ssh-keygen
// -lf » en donne : c'est ce que l'opérateur recopie.
const (
	temoinEd25519Key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICmnSYYAur2g3s4UChIbqTsqoMB/gfzBOC0L+FjggtwP"
	temoinEd25519Sum = "SHA256:5r2jmeuasrHsl5F9LhDP93HWqAvStYhSHLJkhl3KIwo"
	temoinRSAKey     = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQDZ/CT2Fl/jvzam6UMtMXKDoiaw8xs37kqf3NbPo5ukylly/kP7rfc7kAA+V1ljhQZSpxJyP4oj2i5THYlrD2BUjcT32U2I1DUoHwZRdgIWPaUUoYpjBQyYRFDlA5f2doE6hKABVjaq/hf8BURJOmyRkByIuVGZyxjl2t1Ye4Eq/T57NeN8VP+CAj+wulmdlvHBCjmuEKU9HXIA3+RF9mqDWCWoj2V6rDBWSl+4QaFcO9G4wTK4vmFFOAU9au1XMUZ7sR+VaqNZYj/0bHVsLCLZaSLAtoIM101v2c+nFOMobNSTd5RgfGnvy37WKAxl9PqLZjXttjg3ol+K80NIX1rV"
	temoinRSASum     = "SHA256:P37N7ItYffEkYEdkhtloRV/x1JmTCGRNpt4TjNdzEWw"
)

// fakeRemote rejoue ce qu'une machine distante aurait répondu et retient ce
// qu'on lui a posé.
type fakeRemote struct {
	probes     int
	probeErr   error
	installed  []byte
	installErr error
	reply      transport.LauncherReply
}

func (f *fakeRemote) Probe(context.Context) error {
	f.probes++
	return f.probeErr
}

func (f *fakeRemote) InstallLauncher(_ context.Context, binary []byte) error {
	if f.installErr != nil {
		return f.installErr
	}
	f.installed = binary
	return nil
}

func (f *fakeRemote) CheckLauncher(context.Context) (transport.LauncherReply, error) {
	return f.reply, nil
}

type confirmHarness struct {
	deps    ConfirmDeps
	root    *os.Root
	machine store.Machine
	remote  *fakeRemote
	scans   []string
}

func newConfirmHarness(t *testing.T) *confirmHarness {
	t.Helper()

	root := openTestRoot(t)
	if _, err := PrepareRemote(root, "temoin"); err != nil {
		t.Fatalf("PrepareRemote : %v", err)
	}

	harness := &confirmHarness{
		root:    root,
		machine: store.Machine{ID: "temoin", Name: "témoin", Address: "192.168.1.10", Port: 2222, Account: "opencloud"},
		remote:  &fakeRemote{reply: transport.LauncherReply{ExitCode: 2, Output: "usage: oc-launch <id>"}},
	}
	harness.deps = ConfirmDeps{
		Root:      root,
		Commands:  &keyscanStub{harness: harness, output: "192.168.1.10 " + temoinEd25519Key + "\n192.168.1.10 " + temoinRSAKey + "\n"},
		Transport: func(transport.Endpoint) RemoteTransport { return harness.remote },
		Launcher:  func() ([]byte, error) { return []byte("le lanceur"), nil },
		Now:       func() time.Time { return time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC) },
		ProbeWait: time.Millisecond,
	}
	return harness
}

// keyscanStub joue le relevé des clés d'hôte : rien ne sort sur le réseau.
type keyscanStub struct {
	harness *confirmHarness
	output  string
}

func (k *keyscanStub) Run(_ context.Context, name string, args ...string) (Result, error) {
	k.harness.scans = append(k.harness.scans, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	return Result{Output: k.output}, nil
}

func (h *confirmHarness) knownHosts(t *testing.T) string {
	t.Helper()
	content, err := h.root.ReadFile("machines/temoin/known_hosts")
	if err != nil {
		t.Fatalf("relire known_hosts : %v", err)
	}
	return string(content)
}

func (h *confirmHarness) hasFile(name string) bool {
	_, err := h.root.Stat(name)
	return err == nil
}

func expectRefusal(t *testing.T, err error, expected string) refusal.Refusal {
	t.Helper()

	var refused refusal.Refusal
	if !errors.As(err, &refused) {
		t.Fatalf("attendu un refus nommé, obtenu %v", err)
	}
	if !strings.Contains(refused.Cause, expected) {
		t.Fatalf("cause %q, attendait %q dedans", refused.Cause, expected)
	}
	if refused.Remedy == "" {
		t.Fatal("refus sans remède")
	}
	return refused
}

func TestConfirm_EcritToutesLesClesRelevees_PoseLeLanceurEtMarqueLaMachine(t *testing.T) {
	harness := newConfirmHarness(t)

	if err := Confirm(context.Background(), harness.deps, harness.machine, temoinEd25519Sum); err != nil {
		t.Fatalf("Confirm : %v", err)
	}

	expected := "[192.168.1.10]:2222 " + temoinEd25519Key + "\n[192.168.1.10]:2222 " + temoinRSAKey + "\n"
	if got := harness.knownHosts(t); got != expected {
		t.Errorf("known_hosts :\n%s\nattendu :\n%s", got, expected)
	}
	if string(harness.remote.installed) != "le lanceur" {
		t.Errorf("lanceur posé : %q", harness.remote.installed)
	}
	marker, err := harness.root.ReadFile("machines/temoin/enrolled")
	if err != nil {
		t.Fatalf("relire le marqueur : %v", err)
	}
	if string(marker) != "2026-09-06T10:00:00Z\n" {
		t.Errorf("marqueur : %q", marker)
	}
}

func TestConfirm_RelevelesClesParUnVecteurBorne(t *testing.T) {
	harness := newConfirmHarness(t)

	if err := Confirm(context.Background(), harness.deps, harness.machine, temoinEd25519Sum); err != nil {
		t.Fatalf("Confirm : %v", err)
	}

	expected := "/usr/bin/ssh-keyscan -p 2222 -T 10 -t ed25519,rsa,ecdsa -- 192.168.1.10"
	if len(harness.scans) != 1 || harness.scans[0] != expected {
		t.Errorf("relevé : %v, attendu %q", harness.scans, expected)
	}
}

func TestConfirm_UneEmpreinteQuiNeCorrespondAAucuneCleEstUnRefusQuiLeDit(t *testing.T) {
	harness := newConfirmHarness(t)

	err := Confirm(context.Background(), harness.deps, harness.machine, "SHA256:"+strings.Repeat("A", 43))

	expectRefusal(t, err, "quelqu'un se fait passer pour la machine")
	if harness.hasFile("machines/temoin/known_hosts") {
		t.Error("known_hosts a été écrit alors qu'aucune clé ne correspond")
	}
	if harness.remote.probes != 0 {
		t.Error("la machine a été jointe malgré l'empreinte qui ne correspond pas")
	}
}

func TestConfirm_UneEmpreinteHorsFormeEstRefuseeAvantLeMoindreRelevé(t *testing.T) {
	for _, fingerprint := range []string{
		"", "SHA256:", "5r2jmeuasrHsl5F9LhDP93HWqAvStYhSHLJkhl3KIwo",
		"MD5:5r2jmeuasrHsl5F9LhDP93HWqAvStYhSHLJkhl3KIwo",
		"SHA256:5r2jmeuasrHsl5F9LhDP93HWqAvStYhSHLJkhl3KIwo=",
		"SHA256:5r2jmeuasrHsl5F9LhDP93HWqAvStYhSHLJkhl3KIw",
	} {
		harness := newConfirmHarness(t)

		err := Confirm(context.Background(), harness.deps, harness.machine, fingerprint)

		expectRefusal(t, err, "empreinte")
		if len(harness.scans) != 0 {
			t.Errorf("%q : les clés ont été relevées avant la vérification de forme", fingerprint)
		}
	}
}

func TestConfirm_SansAucuneCleReleveeLeRefusDitQueLaMachineNeRepondPas(t *testing.T) {
	harness := newConfirmHarness(t)
	harness.deps.Commands = &keyscanStub{harness: harness, output: "# 192.168.1.10:2222 SSH-2.0-OpenSSH_9.2p1\n"}

	err := Confirm(context.Background(), harness.deps, harness.machine, temoinEd25519Sum)

	expectRefusal(t, err, "aucune clé d'hôte relevée")
	if harness.hasFile("machines/temoin/known_hosts") {
		t.Error("known_hosts a été écrit sans clé relevée")
	}
}

func TestConfirm_UneAdresseOuUnPortHorsFormeEstUnRefusNomme(t *testing.T) {
	cases := map[string]store.Machine{
		"nom d'hôte":      {ID: "temoin", Address: "temoin.exemple.com", Port: 22, Account: "opencloud"},
		"port nul":        {ID: "temoin", Address: "192.168.1.10", Port: 0, Account: "opencloud"},
		"port trop grand": {ID: "temoin", Address: "192.168.1.10", Port: 70000, Account: "opencloud"},
	}
	for name, machine := range cases {
		harness := newConfirmHarness(t)

		err := Confirm(context.Background(), harness.deps, machine, temoinEd25519Sum)

		refused := expectRefusal(t, err, "")
		if !strings.Contains(refused.Cause, "adresse") && !strings.Contains(refused.Cause, "port") {
			t.Errorf("%s : cause %q", name, refused.Cause)
		}
		if len(harness.scans) != 0 {
			t.Errorf("%s : les clés ont été relevées malgré le refus", name)
		}
	}
}

func TestConfirm_UnLanceurQuiNeRefusePasLaisseKnownHostsEtPasDeMarqueur(t *testing.T) {
	harness := newConfirmHarness(t)
	harness.remote.reply = transport.LauncherReply{ExitCode: 0}

	err := Confirm(context.Background(), harness.deps, harness.machine, temoinEd25519Sum)

	expectRefusal(t, err, "lanceur")
	if !harness.hasFile("machines/temoin/known_hosts") {
		t.Error("known_hosts doit rester : rejouer Confirm reprend là où ça s'est arrêté")
	}
	if harness.hasFile("machines/temoin/enrolled") {
		t.Error("la machine est marquée enrôlée alors que le lanceur ne répond pas")
	}
}

func TestConfirm_UneMachineInjoignableEstSondeeTroisFoisAvantDAbandonner(t *testing.T) {
	harness := newConfirmHarness(t)
	harness.remote.probeErr = fmt.Errorf("%w: connexion refusée", transport.ErrUnreachable)

	err := Confirm(context.Background(), harness.deps, harness.machine, temoinEd25519Sum)

	expectRefusal(t, err, "ne répond pas en SSH")
	if harness.remote.probes != probeAttempts {
		t.Errorf("%d sondes, attendu %d", harness.remote.probes, probeAttempts)
	}
	if harness.hasFile("machines/temoin/enrolled") {
		t.Error("la machine est marquée enrôlée alors qu'elle ne répond pas")
	}
}

func TestConfirm_RejoueeReprendLaOuElleSestArretee(t *testing.T) {
	harness := newConfirmHarness(t)
	harness.remote.installErr = errors.New("écriture refusée")

	if err := Confirm(context.Background(), harness.deps, harness.machine, temoinEd25519Sum); err == nil {
		t.Fatal("attendu un échec de la pose du lanceur")
	}
	if !harness.hasFile("machines/temoin/known_hosts") {
		t.Fatal("known_hosts doit rester en place")
	}

	harness.remote.installErr = nil
	if err := Confirm(context.Background(), harness.deps, harness.machine, temoinEd25519Sum); err != nil {
		t.Fatalf("Confirm rejouée : %v", err)
	}
	if !harness.hasFile("machines/temoin/enrolled") {
		t.Error("le marqueur manque après la reprise")
	}
}

func TestUpdateAccess_ReecritKnownHostsSansToucherAuLanceur(t *testing.T) {
	harness := newConfirmHarness(t)
	if err := Confirm(context.Background(), harness.deps, harness.machine, temoinEd25519Sum); err != nil {
		t.Fatalf("Confirm : %v", err)
	}

	harness.remote.installed = nil
	harness.machine.Port = 22
	harness.deps.Commands = &keyscanStub{harness: harness, output: "192.168.1.10 " + temoinRSAKey + "\n"}

	if err := UpdateAccess(context.Background(), harness.deps, harness.machine, temoinRSASum); err != nil {
		t.Fatalf("UpdateAccess : %v", err)
	}

	// Le port 22 s'écrit sans crochets, comme ssh l'écrit lui-même.
	expected := "192.168.1.10 " + temoinRSAKey + "\n"
	if got := harness.knownHosts(t); got != expected {
		t.Errorf("known_hosts :\n%s\nattendu :\n%s", got, expected)
	}
	if harness.remote.installed != nil {
		t.Error("changer l'accès a reposé le lanceur")
	}
}

func TestFingerprint_DitCeQueSshKeygenDitDeLaMemeCle(t *testing.T) {
	cases := map[string]string{
		temoinEd25519Key: temoinEd25519Sum,
		temoinRSAKey:     temoinRSASum,
	}
	for line, expected := range cases {
		got, err := Fingerprint(line)
		if err != nil {
			t.Fatalf("Fingerprint : %v", err)
		}
		if got != expected {
			t.Errorf("Fingerprint = %q, attendu %q", got, expected)
		}
	}
}

func TestFingerprint_CeQuiNestPasUneCleEstUneErreur(t *testing.T) {
	for _, line := range []string{"", "ssh-ed25519", "ssh-ed25519 pas-du-base64", "# commentaire"} {
		if _, err := Fingerprint(line); err == nil {
			t.Errorf("%q accepté comme clé d'hôte", line)
		}
	}
}
