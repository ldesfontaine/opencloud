package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/anchor"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

// Les secrets semés : le serveur les enverrait dans le secret des ordres ;
// le banc prouve ensuite qu'aucun ne sort de la machine.
const (
	seededRESTPassword = "SEEDED-rest-password-4b1d9c0e7a5f"
	seededRepoPassword = "SEEDED-repository-password-8f2e6d"
	resticTargetID     = "vps"
	resticUser         = "11111111-2222-4333-8444-555555555555"
	testSnapshotID     = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

// fakeCall est ce qu'une commande restic aurait vu : ses arguments, son
// environnement, et le contenu des fichiers qu'il nomme.
type fakeCall struct {
	args       []string
	env        map[string]string
	repository string
	caCert     string
}

// fakeRestic joue restic sans le lancer : answer écrit sa sortie et rend son
// code ; par défaut, un résumé et 0.
type fakeRestic struct {
	mu     sync.Mutex
	calls  []fakeCall
	answer func(call fakeCall, number int, stdout, stderr io.Writer) int
}

func (f *fakeRestic) run(_ context.Context, command restic.Command, stdout, stderr io.Writer) (int, error) {
	call := fakeCall{args: command.Args, env: map[string]string{}}
	for _, variable := range command.Env {
		name, value, _ := strings.Cut(variable, "=")
		call.env[name] = value
	}
	if content, err := os.ReadFile(call.env["RESTIC_REPOSITORY_FILE"]); err == nil {
		call.repository = string(content)
	}
	if content, err := os.ReadFile(call.env["RESTIC_CACERT"]); err == nil {
		call.caCert = string(content)
	}
	f.mu.Lock()
	f.calls = append(f.calls, call)
	number := len(f.calls)
	answer := f.answer
	f.mu.Unlock()
	if answer == nil {
		return writeSummary(stdout), nil
	}
	return answer(call, number, stdout, stderr), nil
}

func (f *fakeRestic) callsMade() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func writeSummary(stdout io.Writer) int {
	_, _ = io.WriteString(stdout, `{"message_type":"status","percent_done":0.5}`+"\n")
	_, _ = io.WriteString(stdout, `{"message_type":"summary","snapshot_id":"`+testSnapshotID+`","data_added":4096,"total_duration":1.5}`+"\n")
	return 0
}

// refusingDialer : la cible ne répond pas en TCP (pare-feu, éteinte).
type refusingDialer struct {
	mu     sync.Mutex
	dialed []string
}

func (d *refusingDialer) DialContext(_ context.Context, _, address string) (net.Conn, error) {
	d.mu.Lock()
	d.dialed = append(d.dialed, address)
	d.mu.Unlock()
	return nil, errors.New("connection refused")
}

// relayRequest est ce que le faux relais du serveur a reçu.
type relayRequest struct {
	method, path string
	header       http.Header
}

// fakeRelay joue les routes du relais du serveur : il note chaque requête
// et répond 200, comme rest-server par la cible.
type fakeRelay struct {
	*httptest.Server
	mu       sync.Mutex
	requests []relayRequest
}

func newFakeRelay(t *testing.T) *fakeRelay {
	t.Helper()
	relay := &fakeRelay{}
	relay.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relay.mu.Lock()
		relay.requests = append(relay.requests, relayRequest{method: r.Method, path: r.URL.Path, header: r.Header.Clone()})
		relay.mu.Unlock()
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.WriteString(w, "config")
	}))
	t.Cleanup(relay.Close)
	return relay
}

func (r *fakeRelay) received() []relayRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.requests)
}

// resticBench est un agent source et cible : un juge d'ordres, son worker,
// le faux restic, les secrets semés et le journal capturé.
type resticBench struct {
	*orderBench
	logs     *lockedBuffer
	restic   *fakeRestic
	relay    *fakeRelay
	agentKey ed25519.PrivateKey
	root     string
	seq      int64
	// withoutTrust : le banc ne pose pas l'ancre du certificat avant un
	// restic.provision (les tests des ancres).
	withoutTrust bool
}

func newResticBench(t *testing.T, rights ...string) *resticBench {
	t.Helper()
	b := newOrderBenchWithRights(t, append([]string{"observe"}, rights...))
	logs := &lockedBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	b.runner.logger = logger
	b.runner.restic.logger, b.runner.restic.target.logger, b.runner.restic.source.logger = logger, logger, logger
	fake := &fakeRestic{}
	b.runner.restic.source.runner = fake
	root := t.TempDir()
	b.runner.identity.RootDir = root
	_, agentKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	relay := newFakeRelay(t)
	b.runner.restic.source.relay = func(target string) (restic.ProxyConfig, error) {
		endpoint, err := url.Parse(relay.URL + agentwire.ResticRelayPath(target))
		if err != nil {
			return restic.ProxyConfig{}, err
		}
		sign := func(payload []byte) []byte { return machine.Sign(agentKey, payload) }
		return restic.ProxyConfig{Endpoint: endpoint, Client: relay.Client(), MachineID: orderMachineID, Sign: sign}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.runner.worker.run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return &resticBench{orderBench: b, logs: logs, restic: fake, relay: relay, agentKey: agentKey, root: root}
}

// run envoie un ordre signé et rend ses résultats jusqu'au dernier.
func (b *resticBench) run(t *testing.T, kind agentwire.Kind, params any) []agentwire.ActionResult {
	t.Helper()
	return b.runWithSecret(t, kind, params, nil)
}

// runWithSecret fait comme run, le secret signé avec l'ordre comme le
// serveur l'envoie (action.SendWithSecret).
func (b *resticBench) runWithSecret(t *testing.T, kind agentwire.Kind, params, secret any) []agentwire.ActionResult {
	t.Helper()
	// L'administrateur de la machine a posé l'empreinte de la cible que ce
	// banc lie ; les tests des ancres n'utilisent pas ce raccourci.
	if provision, ok := params.(agentwire.ResticProvisionParams); ok && kind == agentwire.KindResticProvision && !b.withoutTrust {
		if fingerprint := fingerprintOfPEM(provision.Certificate); fingerprint != "" {
			b.trust(t, map[string][]string{anchor.ResticTargetCert: {fingerprint}})
		}
	}
	b.seq++
	body := longOrder(b.seq, kind, params)
	if secret != nil {
		encoded, err := json.Marshal(secret)
		if err != nil {
			t.Fatal(err)
		}
		body.Secret = encoded
	}
	b.runner.receive(b.signed(t, body))
	return b.until(t, body.RunID)
}

// seededLink est le secret de restic.provision, aux valeurs semées.
func seededLink() agentwire.ResticProvisionSecret {
	return agentwire.ResticProvisionSecret{RESTPassword: seededRESTPassword, RepoPassword: seededRepoPassword}
}

// provision lie la source à une cible, avec les mots de passe semés.
func (b *resticBench) provision(t *testing.T, host string, port int, certificatePEM string) {
	t.Helper()
	params := agentwire.ResticProvisionParams{Target: resticTargetID, Host: host, Port: port, User: resticUser, Certificate: certificatePEM}
	expectResult(t, b.runWithSecret(t, agentwire.KindResticProvision, params, seededLink()), agentwire.ActionSucceeded, "")
}

// service crée le dossier d'un service sous la racine de l'agent.
func (b *resticBench) service(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(b.root, name)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func serviceBackup(service string) agentwire.ResticBackupParams {
	return agentwire.ResticBackupParams{Target: resticTargetID, Scope: agentwire.ResticScopeService, Service: service}
}

// reachableTarget ouvre une cible TLS 1.3 sur la boucle locale, au certificat
// tiré comme celui de rest-server ; elle rend son port et le PEM public.
func reachableTarget(t *testing.T) (int, string) {
	t.Helper()
	certificatePEM, keyPEM, err := newTargetCertificate([]string{"127.0.0.1"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(certificatePEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				_ = conn.(*tls.Conn).Handshake()
				_ = conn.Close()
			}()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port, string(certificatePEM)
}

func closedLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

// Cible joignable : restic part en direct, l'URL du dépôt est celle de la
// cible (dans un fichier, mot de passe REST compris), le certificat épinglé
// par RESTIC_CACERT, et aucun mandataire.
func TestResticBackup_ReachableTargetRunsDirectWithThePinnedCertificate(t *testing.T) {
	b := newResticBench(t, "restic.produce")
	port, certificatePEM := reachableTarget(t)
	b.provision(t, "127.0.0.1", port, certificatePEM)
	dir := b.service(t, "forgejo")

	result := expectResult(t, b.run(t, agentwire.KindResticBackup, serviceBackup("forgejo")), agentwire.ActionSucceeded, "")
	if result.Restic == nil || result.Restic.Path != agentwire.ResticPathDirect || result.Restic.Reason != agentwire.ResticReachable {
		t.Fatalf("restic report %+v", result.Restic)
	}
	if result.Restic.SnapshotID != testSnapshotID || result.Restic.BytesAdded != 4096 || result.Restic.TargetHost != "" {
		t.Fatalf("restic report %+v", result.Restic)
	}
	if err := result.Restic.Validate(); err != nil {
		t.Fatal(err)
	}
	calls := b.restic.callsMade()
	if len(calls) != 1 {
		t.Fatalf("restic ran %d times", len(calls))
	}
	call := calls[0]
	wantURL := "rest:https://" + resticUser + ":" + seededRESTPassword + "@127.0.0.1:" + strconv.Itoa(port) + "/" + resticUser + "/"
	if call.repository != wantURL {
		t.Fatalf("repository %q", call.repository)
	}
	if call.caCert != certificatePEM {
		t.Fatal("RESTIC_CACERT is not the pinned certificate")
	}
	if _, viaProxy := call.env["RESTIC_REPOSITORY"]; viaProxy {
		t.Fatal("a direct backup went through the proxy")
	}
	if call.args[0] != "backup" || call.args[len(call.args)-1] != dir {
		t.Fatalf("args %v", call.args)
	}
	if len(b.relay.received()) != 0 {
		t.Fatal("the relay was used on the direct path")
	}
}

// Cible injoignable : la source bascule seule sur le relais, sans erreur ;
// restic voit le mandataire local, qui signe et passe au relais du serveur.
func TestResticBackup_UnreachableTargetFallsBackToTheRelayAlone(t *testing.T) {
	b := newResticBench(t, "restic.produce")
	_, certificatePEM := reachableTarget(t)
	port := closedLoopbackPort(t)
	b.provision(t, "127.0.0.1", port, certificatePEM)
	b.service(t, "forgejo")
	b.restic.answer = func(call fakeCall, _ int, stdout, _ io.Writer) int {
		proxied := strings.TrimPrefix(call.env["RESTIC_REPOSITORY"], "rest:")
		response, err := http.Get(proxied + "config")
		if err != nil || response.StatusCode != http.StatusOK {
			return 1
		}
		_ = response.Body.Close()
		return writeSummary(stdout)
	}

	result := expectResult(t, b.run(t, agentwire.KindResticBackup, serviceBackup("forgejo")), agentwire.ActionSucceeded, "")
	if result.Restic == nil || result.Restic.Path != agentwire.ResticPathRelay || result.Restic.Reason != agentwire.ResticTCPUnreachable {
		t.Fatalf("restic report %+v", result.Restic)
	}
	call := b.restic.callsMade()[0]
	if !strings.HasPrefix(call.env["RESTIC_REPOSITORY"], "rest:http://127.0.0.1:") || call.env["RESTIC_REPOSITORY_FILE"] != "" {
		t.Fatalf("env %v", call.env)
	}
	if strings.Contains(call.env["RESTIC_REPOSITORY"], seededRESTPassword) {
		t.Fatal("the proxy URL carries the REST password")
	}
	received := b.relay.received()
	if len(received) != 1 || received[0].path != agentwire.ResticRelayPath(resticTargetID)+"/config" {
		t.Fatalf("relay got %+v", received)
	}
	assertSignedByTheAgent(t, b, received[0].header)
}

// assertSignedByTheAgent : chaque requête relayée porte la preuve de l'agent,
// la même que celle qui ouvre son flux.
func assertSignedByTheAgent(t *testing.T, b *resticBench, header http.Header) {
	t.Helper()
	nonce, err := base64.StdEncoding.DecodeString(header.Get(agentwire.HeaderNonce))
	if err != nil {
		t.Fatal(err)
	}
	signature, err := base64.StdEncoding.DecodeString(header.Get(agentwire.HeaderSignature))
	if err != nil {
		t.Fatal(err)
	}
	timestamp, err := strconv.ParseInt(header.Get(agentwire.HeaderTimestamp), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := machine.SignedPayload(nonce, header.Get(agentwire.HeaderMachine), timestamp)
	if err != nil || header.Get(agentwire.HeaderMachine) != orderMachineID {
		t.Fatalf("proof %v %q", err, header.Get(agentwire.HeaderMachine))
	}
	public := b.agentKey.Public().(ed25519.PublicKey)
	if !machine.Verify(public, payload, signature) {
		t.Fatal("the relayed request is not signed by the agent")
	}
}

// Le résultat d'un relais dit quoi ouvrir pour passer en direct : l'hôte et
// le port exacts de la cible, que l'interface met en phrase.
func TestResticBackup_RelayResultSaysWhichHostAndPortToOpen(t *testing.T) {
	b := newResticBench(t, "restic.produce")
	_, certificatePEM := reachableTarget(t)
	dialer := &refusingDialer{}
	b.runner.restic.source.dialer = dialer
	b.provision(t, "nas", 8443, certificatePEM)
	b.service(t, "forgejo")

	result := expectResult(t, b.run(t, agentwire.KindResticBackup, serviceBackup("forgejo")), agentwire.ActionSucceeded, "")
	want := agentwire.ResticRunReport{Path: agentwire.ResticPathRelay, Reason: agentwire.ResticTCPUnreachable, TargetHost: "nas", TargetPort: 8443}
	if result.Restic == nil || result.Restic.Path != want.Path || result.Restic.Reason != want.Reason ||
		result.Restic.TargetHost != want.TargetHost || result.Restic.TargetPort != want.TargetPort {
		t.Fatalf("restic report %+v, want the hint %+v", result.Restic, want)
	}
	if !slices.Contains(dialer.dialed, "nas:8443") {
		t.Fatalf("the probe dialed %v", dialer.dialed)
	}
}

// Sans restic.produce, l'ordre est refusé par le juge ; sans backup.produce,
// un invité Proxmox aussi. Rien n'est lancé.
func TestResticBackup_IsRefusedWithoutItsRights(t *testing.T) {
	without := newResticBench(t)
	result := expectResult(t, without.run(t, agentwire.KindResticBackup, serviceBackup("forgejo")), agentwire.ActionRefused, agentwire.RightMissing(machine.CapabilityResticProduce))
	if result.Restic != nil {
		t.Fatalf("a refused order reported a path: %+v", result.Restic)
	}
	provision := agentwire.ResticProvisionParams{Target: resticTargetID, Host: "nas", Port: 8000, User: resticUser, Certificate: "x"}
	expectResult(t, without.run(t, agentwire.KindResticProvision, provision), agentwire.ActionRefused, agentwire.RightMissing(machine.CapabilityResticProduce))

	noProducer := newResticBench(t, "restic.produce")
	guest := agentwire.ResticBackupParams{Target: resticTargetID, Scope: agentwire.ResticScopeVM, VMID: 105, GuestType: agentwire.GuestQEMU}
	expectResult(t, noProducer.run(t, agentwire.KindResticBackup, guest), agentwire.ActionRefused, agentwire.RightMissing(machine.CapabilityBackupProduce))
	if len(without.restic.callsMade())+len(noProducer.restic.callsMade()) != 0 {
		t.Fatal("restic ran on a refused order")
	}
}

// Un service dont le dossier sort de la racine (un lien vers /etc) est
// refusé ; un service absent aussi. Rien n'est lancé.
func TestResticBackup_IsRefusedOutsideTheRoot(t *testing.T) {
	b := newResticBench(t, "restic.produce")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(b.root, "evade")); err != nil {
		t.Fatal(err)
	}
	expectResult(t, b.run(t, agentwire.KindResticBackup, serviceBackup("evade")), agentwire.ActionRefused, agentwire.ReasonPathOutsideRight)
	expectResult(t, b.run(t, agentwire.KindResticBackup, serviceBackup("absent")), agentwire.ActionRefused, agentwire.ReasonServiceDirMissing)
	if len(b.restic.callsMade()) != 0 {
		t.Fatal("restic ran outside the root")
	}
}

// restic absent de la machine : refusé avant de rien lancer, et dit au
// serveur comme un droit indisponible. L'agent ne le télécharge pas.
func TestResticBackup_IsRefusedWhenResticIsMissing(t *testing.T) {
	b := newResticBench(t, "restic.produce")
	b.runner.checks.findRestic = func() (string, error) { return "", errors.New("not found") }
	b.service(t, "forgejo")
	expectResult(t, b.run(t, agentwire.KindResticBackup, serviceBackup("forgejo")), agentwire.ActionRefused, agentwire.ReasonResticMissing)
	unavailable := judgeRights(context.Background(), b.runner.identity, b.runner.checks)
	if unavailable[machine.CapabilityResticProduce] != agentwire.ReasonResticMissing {
		t.Fatalf("unavailable %v", unavailable)
	}
}

// Un échec se rejoue une fois, sur un chemin rechoisi ; deux échecs font un
// échec, avec la sortie d'erreur et le chemin pris.
func TestResticBackup_RetriesOnceThenFails(t *testing.T) {
	b := newResticBench(t, "restic.produce")
	b.runner.restic.source.dialer = &refusingDialer{}
	b.provision(t, "nas", 8000, mustCertificate(t))
	b.service(t, "forgejo")
	b.restic.answer = func(_ fakeCall, number int, stdout, stderr io.Writer) int {
		if number == 1 {
			_, _ = io.WriteString(stderr, "Save(<data/1234>) returned error: 403 Forbidden\n")
			return 1
		}
		return writeSummary(stdout)
	}
	expectResult(t, b.run(t, agentwire.KindResticBackup, serviceBackup("forgejo")), agentwire.ActionSucceeded, "")
	if len(b.restic.callsMade()) != 2 {
		t.Fatalf("restic ran %d times", len(b.restic.callsMade()))
	}

	b.restic.answer = func(_ fakeCall, _ int, _, stderr io.Writer) int {
		_, _ = io.WriteString(stderr, "Fatal: unable to save snapshot\n")
		return 1
	}
	result := expectResult(t, b.run(t, agentwire.KindResticBackup, serviceBackup("forgejo")), agentwire.ActionFailed, agentwire.ReasonResticFailed)
	if len(b.restic.callsMade()) != 4 {
		t.Fatalf("restic ran %d times in all, want two per order", len(b.restic.callsMade()))
	}
	if result.Restic == nil || result.Restic.Path != agentwire.ResticPathRelay || !hasLine(result.Output, "Fatal: unable to save snapshot") {
		t.Fatalf("result %+v", result)
	}
}

// La première sauvegarde crée le dépôt : restic sort en 10, l'agent lance
// init puis la sauvegarde, sans compter un rejeu.
func TestResticBackup_InitializesTheRepositoryTheFirstTime(t *testing.T) {
	b := newResticBench(t, "restic.produce")
	b.runner.restic.source.dialer = &refusingDialer{}
	b.provision(t, "nas", 8000, mustCertificate(t))
	b.service(t, "forgejo")
	b.restic.answer = func(call fakeCall, number int, stdout, _ io.Writer) int {
		if number == 1 {
			return restic.ExitRepositoryMissing
		}
		if call.args[0] == "init" {
			return 0
		}
		return writeSummary(stdout)
	}
	result := expectResult(t, b.run(t, agentwire.KindResticBackup, serviceBackup("forgejo")), agentwire.ActionSucceeded, "")
	var verbs []string
	for _, call := range b.restic.callsMade() {
		verbs = append(verbs, call.args[0])
	}
	if !slices.Equal(verbs, []string{"backup", "init", "backup"}) {
		t.Fatalf("restic ran %v", verbs)
	}
	if result.Restic.Path != agentwire.ResticPathRelay || result.Restic.Reason != agentwire.ResticTCPUnreachable {
		t.Fatalf("restic report %+v", result.Restic)
	}
}

// Sans les mots de passe que le serveur envoie par restic.provision, la
// source ne sauvegarde pas : elle ne tire plus rien elle-même.
func TestResticBackup_IsRefusedWithoutTheProvisionedPasswords(t *testing.T) {
	b := newResticBench(t, "restic.produce")
	b.service(t, "forgejo")
	expectResult(t, b.run(t, agentwire.KindResticBackup, serviceBackup("forgejo")), agentwire.ActionRefused, agentwire.ReasonResticNotProvisioned)
	if len(b.restic.callsMade()) != 0 {
		t.Fatal("restic ran without provisioned passwords")
	}
	link := filepath.Join(b.stateDir.Name(), resticDir, linksDir, resticTargetID)
	for _, name := range []string{linkRESTPassword, linkRepoPassword} {
		if _, err := os.Stat(filepath.Join(link, name)); err == nil {
			t.Errorf("the source drew its own %s", name)
		}
	}

	// Un provision sans secret, ou au secret hors forme, est illisible.
	params := agentwire.ResticProvisionParams{Target: resticTargetID, Host: "nas", Port: 8000, User: resticUser, Certificate: mustCertificate(t)}
	expectResult(t, b.run(t, agentwire.KindResticProvision, params), agentwire.ActionFailed, agentwire.ReasonBadOrder)
	short := agentwire.ResticProvisionSecret{RESTPassword: "p4ss", RepoPassword: seededRepoPassword}
	expectResult(t, b.runWithSecret(t, agentwire.KindResticProvision, params, short), agentwire.ActionFailed, agentwire.ReasonBadOrder)
}

// Le provision écrit les mots de passe reçus en 0600, et l'URL du dépôt dans
// son fichier ; le rapport ne nomme que la cible liée.
func TestResticProvision_KeepsTheReceivedPasswordsPrivate(t *testing.T) {
	b := newResticBench(t, "restic.produce")
	b.provision(t, "nas", 8000, mustCertificate(t))
	link := filepath.Join(b.stateDir.Name(), resticDir, linksDir, resticTargetID)
	want := map[string]string{
		linkRESTPassword:   seededRESTPassword,
		linkRepoPassword:   seededRepoPassword,
		linkRepositoryFile: "rest:https://" + resticUser + ":" + seededRESTPassword + "@nas:8000/" + resticUser + "/",
	}
	for name, content := range want {
		info, err := os.Stat(filepath.Join(link, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", name, info, err)
		}
		if written, _ := os.ReadFile(filepath.Join(link, name)); string(written) != content {
			t.Errorf("%s holds %q", name, written)
		}
	}
	state, _ := os.ReadFile(filepath.Join(link, linkStateFile))
	if strings.Contains(string(state), seededRESTPassword) || strings.Contains(string(state), "hash") {
		t.Errorf("link state %s", state)
	}
	report, _ := b.runner.resticReport(context.Background())
	if report.Source == nil || !slices.Equal(report.Source.Targets, []string{resticTargetID}) {
		t.Fatalf("source report %+v", report.Source)
	}
}

// Un invité passe par vzdump en flux, lancé par restic : le script de root
// sous sudo, sans compression, sous un nom stable.
func TestResticBackup_GuestStreamsVzdumpThroughRestic(t *testing.T) {
	b := newResticBench(t, "restic.produce", "backup.produce=proxmox-vzdump")
	b.runner.restic.source.dialer = &refusingDialer{}
	b.provision(t, "nas", 8000, mustCertificate(t))
	b.runner.checks.lookPath = func(name string) (string, error) {
		if name == "sudo" {
			return "/usr/bin/sudo", nil
		}
		return name, nil
	}
	guest := agentwire.ResticBackupParams{Target: resticTargetID, Scope: agentwire.ResticScopeVM, VMID: 105, GuestType: agentwire.GuestQEMU}
	expectResult(t, b.run(t, agentwire.KindResticBackup, guest), agentwire.ActionSucceeded, "")
	args := strings.Join(b.restic.callsMade()[0].args, " ")
	if !strings.Contains(args, "--stdin-from-command --stdin-filename vzdump-qemu-105.vma -- /usr/bin/sudo -n") ||
		!strings.Contains(args, " 105 --mode snapshot --compress 0 --stdout") {
		t.Fatalf("args %s", args)
	}
}

func mustCertificate(t *testing.T) string {
	t.Helper()
	certificatePEM, _, err := newTargetCertificate([]string{"nas"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return string(certificatePEM)
}
