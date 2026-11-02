package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	ossignal "os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

const (
	testPublicPort = 18000
	waitBound      = 30 * time.Second
)

// Les variables qui font du binaire de test un faux rest-server (voir
// TestFakeRestServerProcess) : les deux fifos de la poignée de main.
const (
	fakeReadyEnv = "OPENCLOUD_FAKE_REST_READY"
	fakeHupEnv   = "OPENCLOUD_FAKE_REST_HUP"
)

// fakeRestServer écrit un faux rest-server : il dit sa version, et sinon
// tourne jusqu'à son arrêt. Il écrit « ready » dans une fifo une fois son
// écoute de SIGHUP posée, puis « hup » à chaque SIGHUP.
//
// Le faux est un programme Go (le binaire de test relancé), pas un script
// shell : un shell non interactif ne peut pas piéger un signal que son père
// ignorait (POSIX), et make ci lancé sous nohup ou en tâche de fond ignore
// SIGHUP. signal.Notify, comme celui du vrai rest-server, le réactive.
func fakeRestServer(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	ready, hup := filepath.Join(dir, "ready.fifo"), filepath.Join(dir, "hup.fifo")
	for _, fifo := range []string{ready, hup} {
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := strings.Join([]string{
		"#!/bin/sh",
		"exec env " + fakeReadyEnv + "=" + ready + " " + fakeHupEnv + "=" + hup + " " + self + " -test.run '^TestFakeRestServerProcess$' -test.timeout 0 -- \"$@\"",
	}, "\n") + "\n"
	binary := filepath.Join(dir, "rest-server")
	if err := writeProgram(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary, ready, hup
}

// TestFakeRestServerProcess n'est pas un test : c'est le faux rest-server
// que fakeRestServer relance. Sans ses variables, il ne fait rien.
func TestFakeRestServerProcess(t *testing.T) {
	ready, hup := os.Getenv(fakeReadyEnv), os.Getenv(fakeHupEnv)
	if ready == "" || hup == "" {
		t.Skip("processus d'aide : lancé par fakeRestServer")
	}
	if slices.Contains(flag.Args(), "--version") {
		fmt.Println("rest-server 0.14.0 (fake)")
		os.Exit(0)
	}
	hups := make(chan os.Signal, 8)
	ossignal.Notify(hups, syscall.SIGHUP)
	if err := os.WriteFile(ready, []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range hups {
		if err := os.WriteFile(hup, []byte("hup\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// readFifo lit une ligne d'une fifo à part, et la rend sur le canal.
func readFifo(path string) <-chan string {
	read := make(chan string, 1)
	go func() {
		content, _ := os.ReadFile(path)
		read <- strings.TrimSpace(string(content))
	}()
	return read
}

func receive[T any](t *testing.T, channel <-chan T, what string) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(waitBound):
		t.Fatalf("waited too long for %s", what)
		var zero T
		return zero
	}
}

// targetBench est un agent cible : le droit restic.target sur un dossier,
// un faux rest-server, les lancements suivis.
type targetBench struct {
	*resticBench
	dir     string
	binary  string
	ready   string
	hup     string
	started chan int
	mu      sync.Mutex
	args    map[string][]string
}

func newTargetBench(t *testing.T, extraRights ...string) *targetBench {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "depots")
	b := &targetBench{resticBench: newResticBench(t, append([]string{"restic.target=" + dir}, extraRights...)...), dir: dir, started: make(chan int, 16), args: map[string][]string{}}
	b.binary, b.ready, b.hup = fakeRestServer(t)
	b.runner.checks.findRestServer = func() (string, error) { return b.binary, nil }
	b.runner.restic.target.tuneSupervisor = func(supervisor *processSupervisor) {
		supervisor.backoff.min, supervisor.backoff.max = 10*time.Millisecond, 50*time.Millisecond
		launch := supervisor.launch
		supervisor.launch = func(ctx context.Context) *exec.Cmd {
			command := launch(ctx)
			b.mu.Lock()
			b.args[supervisor.name] = command.Args
			b.mu.Unlock()
			return command
		}
		supervisor.onStart = func(pid int) { b.started <- pid }
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		b.runner.restic.target.mu.Lock()
		b.runner.restic.target.stopLocked()
		b.runner.restic.target.mu.Unlock()
	})
	b.runner.startRestic(ctx)
	return b
}

func setupParams() agentwire.ResticTargetSetupParams {
	return agentwire.ResticTargetSetupParams{Listen: "127.0.0.1", Port: testPublicPort, Hosts: []string{"nas", "192.0.2.10"}}
}

func (b *targetBench) launched(name string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.args[name]
}

// La préparation tire un certificat ECDSA P-256 d'un an pour les adresses
// annoncées et celle du relais (la boucle locale), garde la clé dans l'état
// (0600), et lance un seul rest-server, en écriture seule, TLS 1.3, avec ses
// utilisateurs : jamais une instance sans authentification.
func TestResticTarget_SetupWritesTheCertificateAndLaunchesOneAuthenticatedRestServer(t *testing.T) {
	b := newTargetBench(t)
	ready := readFifo(b.ready)
	result := expectResult(t, b.run(t, agentwire.KindResticTargetSetup, setupParams()), agentwire.ActionSucceeded, "")
	receive(t, b.started, "rest-server to start")
	receive(t, ready, "rest-server to be ready")

	certificatePEM, err := os.ReadFile(filepath.Join(b.stateDir.Name(), resticDir, targetCertificateFile))
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := agentwire.ParsePublicCertificate(string(certificatePEM))
	if err != nil {
		t.Fatal(err)
	}
	key, isECDSA := certificate.PublicKey.(*ecdsa.PublicKey)
	if !isECDSA || key.Curve != elliptic.P256() {
		t.Fatalf("key %T", certificate.PublicKey)
	}
	if !slices.Equal(certificateHosts(certificate), []string{"127.0.0.1", "192.0.2.10", "nas"}) {
		t.Fatalf("hosts %v", certificateHosts(certificate))
	}
	if lifetime := certificate.NotAfter.Sub(certificate.NotBefore); lifetime < 364*24*time.Hour || lifetime > 366*24*time.Hour {
		t.Fatalf("lifetime %s", lifetime)
	}
	if info, err := os.Stat(filepath.Join(b.stateDir.Name(), resticDir, targetKeyFile)); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file %v %v", info, err)
	}
	if !hasLine(result.Output, "certificate sha256 "+agentwire.CertificateFingerprint(certificate.Raw)) {
		t.Fatalf("output %+v", result.Output)
	}

	public := strings.Join(b.launched(publicProcessName), " ")
	for _, want := range []string{"--path " + b.dir, "--listen 127.0.0.1:" + strconv.Itoa(testPublicPort), "--tls --tls-cert", "--tls-min-ver 1.3", "--append-only --private-repos --htpasswd-file"} {
		if !strings.Contains(public, want) {
			t.Errorf("public rest-server %q lacks %q", public, want)
		}
	}
	b.mu.Lock()
	launchedCount := len(b.args)
	b.mu.Unlock()
	if launchedCount != 1 || strings.Contains(public, "--no-auth") {
		t.Errorf("%d rest-server launched, the public one %q", launchedCount, public)
	}
	if !hasLine(result.Output, "retention floor daily 7 weekly 0 monthly 0") {
		t.Errorf("output %+v", result.Output)
	}

	report := b.runner.restic.target.report(context.Background(), b.dir, b.runner.checks)
	if report.State != agentwire.ResticTargetRunning || report.Version != "rest-server 0.14.0 (fake)" || report.Host != "nas" || report.Port != testPublicPort {
		t.Fatalf("report %+v", report)
	}
	if report.Fingerprint != agentwire.CertificateFingerprint(certificate.Raw) || report.DiskTotal <= 0 {
		t.Fatalf("report %+v", report)
	}
	if report.RetentionFloor == nil || *report.RetentionFloor != agentwire.DefaultResticRetentionFloor() {
		t.Fatalf("floor %+v", report.RetentionFloor)
	}
	if err := (agentwire.ResticReport{Target: &report}).Validate(); err != nil {
		t.Fatal(err)
	}

	// Une seconde préparation aux mêmes adresses garde le certificat : les
	// sources n'ont rien à réépingler.
	expectResult(t, b.run(t, agentwire.KindResticTargetSetup, setupParams()), agentwire.ActionSucceeded, "")
	again, _ := os.ReadFile(filepath.Join(b.stateDir.Name(), resticDir, targetCertificateFile))
	if string(again) != string(certificatePEM) {
		t.Fatal("the certificate changed although the hosts did not")
	}
}

// Un rest-server qui meurt est relancé par l'agent.
func TestResticTarget_RestartsRestServerAfterItDies(t *testing.T) {
	b := newTargetBench(t)
	expectResult(t, b.run(t, agentwire.KindResticTargetSetup, setupParams()), agentwire.ActionSucceeded, "")
	first := receive(t, b.started, "rest-server to start")
	if err := syscall.Kill(first, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	second := receive(t, b.started, "rest-server to restart")
	if second == first {
		t.Fatal("the same process was reported twice")
	}
}

// Un certificat tiré avant que la cible ne serve le relais n'a pas
// l'adresse de la boucle locale : il est tiré à neuf, une fois.
func TestResticTarget_CertificateIsRedrawnWhenTheRelayNameIsMissing(t *testing.T) {
	b := newTargetBench(t)
	resticRoot, err := openResticDir(b.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer resticRoot.Close()
	before, err := ensureTargetCertificate(resticRoot, setupParams().Hosts, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	expectResult(t, b.run(t, agentwire.KindResticTargetSetup, setupParams()), agentwire.ActionSucceeded, "")
	after, _ := os.ReadFile(filepath.Join(b.stateDir.Name(), resticDir, targetCertificateFile))
	if string(after) == before {
		t.Fatal("the certificate without the relay name was kept")
	}
	certificate, err := agentwire.ParsePublicCertificate(string(after))
	if err != nil || !slices.Contains(certificateHosts(certificate), "127.0.0.1") {
		t.Fatalf("hosts %v %v", certificateHosts(certificate), err)
	}

	// Au redémarrage de l'agent aussi : une cible préparée avant garde sa
	// configuration et reçoit le nom du relais.
	if _, err := newTargetCertificateFiles(resticRoot, setupParams().Hosts); err != nil {
		t.Fatal(err)
	}
	b.runner.startRestic(context.Background())
	booted, _ := os.ReadFile(filepath.Join(b.stateDir.Name(), resticDir, targetCertificateFile))
	certificate, err = agentwire.ParsePublicCertificate(string(booted))
	if err != nil || !slices.Contains(certificateHosts(certificate), "127.0.0.1") {
		t.Fatalf("after a restart, hosts %v %v", certificateHosts(certificate), err)
	}
}

// newTargetCertificateFiles remplace la paire de la cible par une paire neuve
// aux seules adresses données, comme l'aurait laissée une version d'avant.
func newTargetCertificateFiles(resticRoot *os.Root, hosts []string) (string, error) {
	certificatePEM, keyPEM, err := newTargetCertificate(hosts, time.Now())
	if err != nil {
		return "", err
	}
	if err := resticRoot.WriteFile(targetKeyFile, keyPEM, 0o600); err != nil {
		return "", err
	}
	return string(certificatePEM), resticRoot.WriteFile(targetCertificateFile, certificatePEM, 0o600)
}

// Ajouter une source : la cible reçoit le mot de passe REST dans le secret
// de l'ordre, en calcule elle-même l'empreinte bcrypt (htpasswd), garde le
// mot de passe en 0600 pour le relais, et fait relire le fichier par SIGHUP.
// Le même mot de passe renvoyé ne change pas la ligne ; retirer la source
// enlève la ligne et le mot de passe.
func TestResticTarget_UsersAreWrittenThenReloadedBySighup(t *testing.T) {
	b := newTargetBench(t)
	ready := readFifo(b.ready)
	expectResult(t, b.run(t, agentwire.KindResticTargetSetup, setupParams()), agentwire.ActionSucceeded, "")
	receive(t, ready, "rest-server to be ready")

	hup := readFifo(b.hup)
	b.addUser(t, seededRESTPassword)
	if got := receive(t, hup, "SIGHUP after the user was added"); got != "hup" {
		t.Fatalf("fifo %q", got)
	}
	htpasswd := filepath.Join(b.stateDir.Name(), resticDir, htpasswdFile)
	content, err := os.ReadFile(htpasswd)
	if err != nil {
		t.Fatal(err)
	}
	user, hash, _ := strings.Cut(strings.TrimSpace(string(content)), ":")
	if user != resticUser || !agentwire.IsBcryptHash(hash) || !restic.PasswordMatches(hash, []byte(seededRESTPassword)) {
		t.Fatalf("htpasswd %q", content)
	}
	kept := filepath.Join(b.stateDir.Name(), resticDir, relayPasswordsDir, resticUser)
	if info, err := os.Stat(kept); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("relay password %v %v", info, err)
	}
	if password, ok := b.runner.restic.target.relayPassword(resticUser); !ok || password != seededRESTPassword {
		t.Fatal("the target does not keep the REST password for the relay")
	}
	report := b.runner.restic.target.report(context.Background(), b.dir, b.runner.checks)
	if !slices.Equal(report.Users, []string{resticUser}) {
		t.Fatalf("users %v", report.Users)
	}
	encoded, _ := json.Marshal(report)
	if strings.Contains(string(encoded), hash) || strings.Contains(string(encoded), seededRESTPassword) {
		t.Fatal("the target report carries a password or its hash")
	}

	hup = readFifo(b.hup)
	b.addUser(t, seededRESTPassword)
	receive(t, hup, "SIGHUP after the same user was sent again")
	if again, _ := os.ReadFile(htpasswd); string(again) != string(content) {
		t.Fatal("the same password rewrote the line")
	}

	hup = readFifo(b.hup)
	expectResult(t, b.run(t, agentwire.KindResticUserRemove, agentwire.ResticUserRemoveParams{User: resticUser}), agentwire.ActionSucceeded, "")
	receive(t, hup, "SIGHUP after the user was removed")
	if content, _ := os.ReadFile(htpasswd); len(content) != 0 {
		t.Fatalf("htpasswd %q", content)
	}
	if _, err := os.Stat(kept); !os.IsNotExist(err) {
		t.Fatalf("the relay password outlived the user: %v", err)
	}
}

// addUser envoie restic.user.add avec le mot de passe REST dans le secret.
func (b *targetBench) addUser(t *testing.T, password string) {
	t.Helper()
	secret := agentwire.ResticUserSecret{RESTPassword: password}
	expectResult(t, b.runWithSecret(t, agentwire.KindResticUserAdd, agentwire.ResticUserAddParams{User: resticUser}, secret), agentwire.ActionSucceeded, "")
}

// Un restic.user.add sans secret, ou au mot de passe hors forme, est
// illisible : rien ne s'écrit.
func TestResticTarget_UserAddWithoutAPasswordIsUnreadable(t *testing.T) {
	b := newTargetBench(t)
	expectResult(t, b.run(t, agentwire.KindResticTargetSetup, setupParams()), agentwire.ActionSucceeded, "")
	params := agentwire.ResticUserAddParams{User: resticUser}
	expectResult(t, b.run(t, agentwire.KindResticUserAdd, params), agentwire.ActionFailed, agentwire.ReasonBadOrder)
	weak := agentwire.ResticUserSecret{RESTPassword: "p4ss"}
	expectResult(t, b.runWithSecret(t, agentwire.KindResticUserAdd, params, weak), agentwire.ActionFailed, agentwire.ReasonBadOrder)
	if content, _ := os.ReadFile(filepath.Join(b.stateDir.Name(), resticDir, htpasswdFile)); len(content) != 0 {
		t.Fatalf("htpasswd %q", content)
	}
}

// Sans rest-server sur la machine, l'agent ne télécharge rien : l'ordre
// échoue sur sa raison, le rapport dit error/binary_missing.
func TestResticTarget_MissingRestServerIsReportedNotDownloaded(t *testing.T) {
	b := newTargetBench(t)
	b.runner.checks.findRestServer = func() (string, error) { return "", exec.ErrNotFound }
	expectResult(t, b.run(t, agentwire.KindResticTargetSetup, setupParams()), agentwire.ActionFailed, agentwire.ReasonRestServerMissing)
	report := b.runner.restic.target.report(context.Background(), b.dir, b.runner.checks)
	if report.State != agentwire.ResticTargetError || report.Reason != agentwire.ResticErrorBinaryMissing {
		t.Fatalf("report %+v", report)
	}
	unavailable := judgeRights(context.Background(), b.runner.identity, b.runner.checks)
	if unavailable[machine.CapabilityResticTarget] != agentwire.ReasonRestServerMissing {
		t.Fatalf("unavailable %v", unavailable)
	}
}

// Les ordres de la cible demandent restic.target.
func TestResticTarget_OrdersAreRefusedWithoutTheRight(t *testing.T) {
	b := newResticBench(t, "restic.produce")
	expectResult(t, b.run(t, agentwire.KindResticTargetSetup, setupParams()), agentwire.ActionRefused, agentwire.RightMissing(machine.CapabilityResticTarget))
	secret := agentwire.ResticUserSecret{RESTPassword: seededRESTPassword}
	expectResult(t, b.runWithSecret(t, agentwire.KindResticUserAdd, agentwire.ResticUserAddParams{User: resticUser}, secret), agentwire.ActionRefused, agentwire.RightMissing(machine.CapabilityResticTarget))
	retain := agentwire.ResticRetainParams{User: resticUser, KeepDaily: 7}
	expectResult(t, b.runWithSecret(t, agentwire.KindResticRetain, retain, agentwire.ResticRetainSecret{RepoPassword: seededRepoPassword}), agentwire.ActionRefused, agentwire.RightMissing(machine.CapabilityResticTarget))
}

// relayAgentServer joue les routes de la cible chez le serveur : le corps à
// tirer, la réponse poussée.
type relayAgentServer struct {
	*httptest.Server
	body      string
	responses chan relayAnswer
}

type relayAnswer struct {
	head agentwire.ResticRelayHead
	body string
}

func newRelayAgentServer(t *testing.T, body string) *relayAgentServer {
	t.Helper()
	server := &relayAgentServer{body: body, responses: make(chan relayAnswer, 4)}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer relay-session" {
			http.Error(w, `{"error":"no_session"}`, http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, agentwire.ResticRelayBodyRoute):
			_, _ = io.WriteString(w, server.body)
		case strings.HasPrefix(r.URL.Path, agentwire.ResticRelayResponseRoute):
			var head agentwire.ResticRelayHead
			_ = json.Unmarshal([]byte(r.Header.Get(agentwire.HeaderResticRelayHead)), &head)
			content, _ := io.ReadAll(r.Body)
			server.responses <- relayAnswer{head: head, body: string(content)}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// targetRestServer tient l'écoute de rest-server à la place du faux binaire
// (qui n'écoute rien) : TLS avec le certificat et la clé de la cible, et
// l'htpasswd appliqué comme --private-repos le fait. Il note ce qu'il voit.
type targetRestServer struct {
	mu     sync.Mutex
	called []string
}

func (s *targetRestServer) calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.called)
}

func startTargetRestServer(t *testing.T, b *targetBench, port int, password string) *targetRestServer {
	t.Helper()
	base := filepath.Join(b.stateDir.Name(), resticDir)
	pair, err := tls.LoadX509KeyPair(filepath.Join(base, targetCertificateFile), filepath.Join(base, targetKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(loopbackHost, strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	fake := &targetRestServer{}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, given, hasAuth := r.BasicAuth()
		fake.mu.Lock()
		fake.called = append(fake.called, r.Method+" "+r.URL.Path+" as "+user)
		fake.mu.Unlock()
		if !hasAuth || user != resticUser || given != password || !strings.HasPrefix(r.URL.Path, "/"+user+"/") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		content, _ := io.ReadAll(r.Body)
		_, _ = io.WriteString(w, "stored "+string(content))
	}))
	server.Listener = listener
	server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13}
	server.StartTLS()
	t.Cleanup(server.Close)
	return fake
}

// freeLoopbackPort rend un port libre de la boucle locale, pour un test qui
// y écoute à la place de rest-server.
func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", net.JoinHostPort(loopbackHost, "0"))
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

// Une requête relayée par le serveur est rejouée sur le rest-server de la
// cible, le même que le direct : en TLS (le certificat de la cible épinglé),
// sous l'utilisateur du dépôt et son mot de passe REST. Sans ce mot de passe,
// rien ne part ; une requête qui efface est refusée sans toucher rest-server.
func TestResticTarget_ServesARelayedRequestOnTheSameAuthenticatedRestServer(t *testing.T) {
	b := newTargetBench(t)
	params := setupParams()
	params.Port = freeLoopbackPort(t)
	expectResult(t, b.run(t, agentwire.KindResticTargetSetup, params), agentwire.ActionSucceeded, "")
	restServer := startTargetRestServer(t, b, params.Port, seededRESTPassword)

	server := newRelayAgentServer(t, "paquet")
	client, err := NewClient(server.URL, "", "v0.0.1", false)
	if err != nil {
		t.Fatal(err)
	}
	b.runner.fetch.use(client)
	b.runner.fetch.setSession("relay-session")
	name := strings.Repeat("a", 64)
	write := agentwire.ResticRelayRequest{ID: strings.Repeat("1", 32), Repo: resticUser, Method: http.MethodPost, Path: "/data/" + name, ContentLength: 6}

	b.runner.serveRelay(context.Background(), write)
	unknown := receive(t, server.responses, "the answer without a user")
	if unknown.head.Status != http.StatusForbidden || !strings.Contains(unknown.body, "user_unknown") || len(restServer.calls()) != 0 {
		t.Fatalf("without the user: %+v, rest-server saw %v", unknown, restServer.calls())
	}

	b.addUser(t, seededRESTPassword)
	b.runner.serveRelay(context.Background(), write)
	answer := receive(t, server.responses, "the relayed answer")
	if answer.head.Status != http.StatusOK || answer.body != "stored paquet" {
		t.Fatalf("answer %+v", answer)
	}
	b.runner.serveRelay(context.Background(), agentwire.ResticRelayRequest{ID: strings.Repeat("2", 32), Repo: resticUser, Method: http.MethodDelete, Path: "/data/" + name})
	refused := receive(t, server.responses, "the refusal")
	if refused.head.Status != http.StatusForbidden {
		t.Fatalf("refusal %+v", refused)
	}
	if !slices.Equal(restServer.calls(), []string{"POST /" + resticUser + "/data/" + name + " as " + resticUser}) {
		t.Fatalf("rest-server saw %v", restServer.calls())
	}
}

// Un agent sans restic.target refuse toute requête relayée.
func TestResticTarget_RelayIsRefusedWithoutTheRight(t *testing.T) {
	b := newResticBench(t, "restic.produce")
	server := newRelayAgentServer(t, "")
	client, err := NewClient(server.URL, "", "v0.0.1", false)
	if err != nil {
		t.Fatal(err)
	}
	b.runner.fetch.use(client)
	b.runner.fetch.setSession("relay-session")
	b.runner.serveRelay(context.Background(), agentwire.ResticRelayRequest{ID: strings.Repeat("3", 32), Repo: resticUser, Method: http.MethodGet, Path: "/config"})
	refused := receive(t, server.responses, "the refusal")
	if refused.head.Status != http.StatusForbidden || !strings.Contains(refused.body, relayRightMissing) {
		t.Fatalf("refusal %+v", refused)
	}
}

// La clé privée de la cible ne se lit que dans son fichier : jamais dans le
// certificat rapporté.
func certificateKeyOf(t *testing.T, stateDir *os.Root) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(stateDir.Name(), resticDir, targetKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(content)
	if block == nil {
		t.Fatal("key file is not PEM")
	}
	if _, err := x509.ParseECPrivateKey(block.Bytes); err != nil {
		t.Fatal(err)
	}
	return string(content)
}

// Un rechargement demandé entre l'appel à Start et la prise du processus
// (le fils a déjà lu son fichier) n'est pas perdu : il part dès la prise. Le
// processus est celui du test, qui attrape le signal.
func TestProcessSupervisor_AReloadAskedWhileStartingIsDeliveredOnceTheProcessIsTaken(t *testing.T) {
	signals := make(chan os.Signal, 1)
	ossignal.Notify(signals, syscall.SIGHUP)
	defer ossignal.Stop(signals)
	self, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	supervisor := newProcessSupervisor("test", nil, slog.New(slog.DiscardHandler), nil)

	supervisor.beginStart()
	if err := supervisor.reload(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-signals:
		t.Fatal("the signal left before the process was taken")
	default:
	}
	supervisor.attach(self)
	receive(t, signals, "the deferred SIGHUP")

	// Entre deux processus, rien n'est gardé : le suivant lit le fichier à son lancement.
	supervisor.mu.Lock()
	supervisor.process = nil
	supervisor.mu.Unlock()
	if err := supervisor.reload(); err != nil {
		t.Fatal(err)
	}
	supervisor.beginStart()
	supervisor.attach(self)
	select {
	case <-signals:
		t.Fatal("a reload asked between two processes was delivered to the next one")
	case <-time.After(100 * time.Millisecond):
	}
}
