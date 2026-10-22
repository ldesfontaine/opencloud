package agent

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/hostinfo"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/resource"
	"github.com/ldesfontaine/opencloud/internal/server"
	"github.com/ldesfontaine/opencloud/internal/settings"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// Un vrai serveur openCloud sur un port éphémère, et un vrai agent contre lui.
type bench struct {
	url       string
	machines  *machine.Service
	actions   *action.Service
	serverKey *action.Key
	db        *store.DB
	resources *resource.Service
	probes    *probe.Service
	stateDir  *os.Root
	logger    *slog.Logger
}

func newBench(t *testing.T) *bench {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	db := storetest.Open(t)
	machines := machine.New(db, machine.NewSessions(), logger)
	resources := resource.New(db, logger)
	// L'agent envoie sa section services dès que son veilleur a parlé : sans
	// composant conteneur, le signal paniquerait côté serveur.
	containers := container.New(db, logger)
	// Le flux qui s'ouvre repart avec le jeu de sondes de sa machine : le
	// serveur de test a donc besoin du composant, même vide.
	probes := probe.New(db, logger)
	updates := update.New(db, logger)
	// Les ordres : signés par une clé tirée pour le banc, poussés par le flux.
	actions := action.New(db, machines, logger)
	serverKey := action.GenerateKey()
	actions.SetKey(serverKey)
	actions.SetAuditor(audit.New(db, logger))
	handler, err := server.New(server.Options{Logger: logger, Version: "v0.0.1", Settings: settings.New(db, logger), Machines: machines, Resources: resources, Containers: containers, Probes: probes, Updates: updates, Actions: actions})
	if err != nil {
		t.Fatal(err)
	}
	actions.SetCommander(handler)
	probes.SetCommander(handler)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	agentRoot, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { agentRoot.Close() })
	return &bench{url: server.URL, machines: machines, actions: actions, serverKey: serverKey, db: db, resources: resources, probes: probes, stateDir: agentRoot, logger: logger}
}

// Une borne large : elle ne sert qu'à ne pas bloquer la CI si le code est
// cassé ; un runner chargé la consomme à peine.
const benchTimeout = 30 * time.Second

func (b *bench) options(token string) Options {
	return Options{StateDir: b.stateDir, Server: b.url, Token: token, Language: lang.English, Version: "v0.0.1", SignalInterval: 50 * time.Millisecond, SampleInterval: 20 * time.Millisecond, Logger: b.logger}
}

func (b *bench) waitFor(t *testing.T, name string, want func(machine.Status) bool) machine.Status {
	t.Helper()
	deadline := time.Now().Add(benchTimeout)
	for time.Now().Before(deadline) {
		statuses, _ := b.machines.List(context.Background())
		for _, status := range statuses {
			if status.Name == name && want(status) {
				return status
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("machine %s never reached the wanted state", name)
	return machine.Status{}
}

func TestRun_EnrollsConnectsSignalsAndComesBackWithItsIdentity(t *testing.T) {
	b := newBench(t)
	token, _, err := b.machines.CreateToken(context.Background(), "vps-paris-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, b.options(token)) }()

	online := b.waitFor(t, "vps-paris-1", func(s machine.Status) bool { return s.Online })
	if online.Address == "" || online.Agent.Version != "v0.0.1" {
		t.Fatalf("connected machine %+v", online)
	}
	b.waitFor(t, "vps-paris-1", func(s machine.Status) bool { return s.LastSeenAt.After(online.LastSeenAt) })
	// L'agent a mesuré cette machine et l'a livré avec son signal.
	deadline := time.Now().Add(benchTimeout)
	for {
		current, err := b.resources.Current(context.Background(), online.ID)
		if err == nil && current.Available && current.Sample.CPUCores > 0 && current.Sample.MemTotal > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no reading reached the server: %+v %v", current, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
	b.waitFor(t, "vps-paris-1", func(s machine.Status) bool { return !s.Online })

	identity, err := LoadIdentity(b.stateDir)
	if err != nil || identity.MachineID != online.ID || identity.Server != b.url || identity.Language != lang.English {
		t.Fatalf("identity %+v %v", identity, err)
	}
	// Second démarrage : plus de jeton, l'identité suffit.
	ctx, cancel = context.WithCancel(context.Background())
	go func() {
		done <- Run(ctx, Options{StateDir: b.stateDir, Version: "v0.0.1", SignalInterval: 50 * time.Millisecond, Logger: b.logger})
	}()
	b.waitFor(t, "vps-paris-1", func(s machine.Status) bool { return s.Online })
	cancel()
	<-done
}

func TestRun_WithoutIdentityNorToken_Refuses(t *testing.T) {
	b := newBench(t)
	err := Run(context.Background(), Options{StateDir: b.stateDir, Logger: b.logger})
	if !errors.Is(err, ErrNotEnrolled) {
		t.Fatalf("got %v", err)
	}
}

func TestRun_BadToken_FailsClearly(t *testing.T) {
	b := newBench(t)
	err := Run(context.Background(), b.options("oc_nope"))
	var refused *ServerError
	if !errors.As(err, &refused) || refused.Code != "token_not_found" {
		t.Fatalf("got %v", err)
	}
	if _, err := LoadIdentity(b.stateDir); !errors.Is(err, ErrNotEnrolled) {
		t.Fatal("identity written despite the refusal")
	}
}

func TestRun_RemovedMachine_StopsForGood(t *testing.T) {
	b := newBench(t)
	token, _, _ := b.machines.CreateToken(context.Background(), "vps-lyon-2", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, b.options(token)) }()
	online := b.waitFor(t, "vps-lyon-2", func(s machine.Status) bool { return s.Online })
	if err := b.machines.Remove(context.Background(), online.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrIdentityRefused) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(benchTimeout):
		t.Fatal("agent kept retrying after removal")
	}
}

// La ligne d'installation donne les droits (-rights) : l'agent les écrit
// dans son fichier, les redit à l'ouverture du flux, et s'arrête pour de bon
// quand l'opérateur le révoque.
func TestRun_KeepsItsRightsReportsThemAndStopsWhenRevoked(t *testing.T) {
	b := newBench(t)
	token, _, _ := b.machines.CreateToken(context.Background(), "vps-lyon-2", []machine.Capability{{Name: machine.CapabilityAgentUpdate}})
	rights, err := machine.ParseCapabilities("agent.update,files.write=/srv/workspace")
	if err != nil {
		t.Fatal(err)
	}
	options := b.options(token)
	options.Rights = rights
	options.RootDir = "/srv/apps"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, options) }()
	reported := b.waitFor(t, "vps-lyon-2", func(s machine.Status) bool { return !s.Agent.ReportedAt.IsZero() })
	// La racine des services part avec les droits, dans l'en-tête du flux.
	if reported.Agent.RootDir != "/srv/apps" {
		t.Fatalf("reported root %q", reported.Agent.RootDir)
	}
	if machine.FormatCapabilities(reported.Agent.Capabilities) != "observe,agent.update,files.write=/srv/workspace" || !reported.Agent.CapabilitiesDiffer() {
		t.Fatalf("reported %+v", reported.Agent)
	}
	identity, err := LoadIdentity(b.stateDir)
	if err != nil || strings.Join(identity.Rights, ",") != "observe,agent.update,files.write=/srv/workspace" {
		t.Fatalf("identity rights %+v %v", identity.Rights, err)
	}

	if err := b.machines.Revoke(context.Background(), reported.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		var refused *ServerError
		if !errors.Is(err, ErrIdentityRefused) || !errors.As(err, &refused) || refused.Code != agentwire.CodeRevoked {
			t.Fatalf("got %v", err)
		}
		catalogs, loadErr := lang.Load()
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if message, isRefusal := Explain(err, catalogs.For(lang.English)); !isRefusal || message != catalogs.For(lang.English).Get("agent.revoked") {
			t.Fatalf("explained as %q (%v)", message, isRefusal)
		}
	case <-time.After(benchTimeout):
		t.Fatal("the revoked agent kept retrying")
	}
}

// waitForRun attend que le run atteigne l'état voulu ; la réponse de l'agent
// arrive par son signal.
func (b *bench) waitForRun(t *testing.T, runID string, want action.State) action.Run {
	t.Helper()
	deadline := time.Now().Add(benchTimeout)
	for time.Now().Before(deadline) {
		runs, err := b.db.ListActionRuns(context.Background(), action.Query{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		for _, run := range runs {
			if run.ID == runID && run.State == want {
				return run
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %s never reached %s", runID, want)
	return action.Run{}
}

// De bout en bout : l'agent épingle la clé du serveur à l'enrôlement, la
// redit à l'ouverture du flux ; un ping signé part, l'agent le vérifie,
// l'exécute, garde son rang et répond par le signal.
func TestRun_PinsTheServerKeyAndAnswersASignedPing(t *testing.T) {
	b := newBench(t)
	token, _, _ := b.machines.CreateToken(context.Background(), "vps-paris-1", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, b.options(token)) }()

	_, fingerprint, err := b.actions.ServerKey()
	if err != nil {
		t.Fatal(err)
	}
	online := b.waitFor(t, "vps-paris-1", func(s machine.Status) bool { return s.Online && s.Agent.ServerKeyFingerprint != "" })
	if online.Agent.ServerKeyFingerprint != fingerprint {
		t.Fatalf("pinned %q, server key %q", online.Agent.ServerKeyFingerprint, fingerprint)
	}
	identity, err := LoadIdentity(b.stateDir)
	if err != nil || identity.ServerKeyFingerprint() != fingerprint || identity.AgentID != online.Agent.ID {
		t.Fatalf("identity %+v %v", identity, err)
	}

	sent, err := b.actions.Ping(audit.WithActor(context.Background(), audit.Operator("lucas")), online.ID)
	if err != nil {
		t.Fatal(err)
	}
	done1 := b.waitForRun(t, sent.ID, action.StateSucceeded)
	if len(done1.Output) != 1 || done1.Output[0].Line != "pong v0.0.1" {
		t.Fatalf("output %+v", done1.Output)
	}
	identity, err = LoadIdentity(b.stateDir)
	if err != nil || identity.LastOrderSeq != sent.Seq {
		t.Fatalf("seq kept %d, want %d (%v)", identity.LastOrderSeq, sent.Seq, err)
	}
	// Le second prend le rang suivant.
	second, err := b.actions.Ping(audit.WithActor(context.Background(), audit.Operator("lucas")), online.ID)
	if err != nil || second.Seq != sent.Seq+1 {
		t.Fatalf("second %+v %v", second, err)
	}
	b.waitForRun(t, second.ID, action.StateSucceeded)
	cancel()
	<-done
}

// La rotation de la clé du serveur, de bout en bout : l'agent a épinglé
// l'ancienne ; le serveur redémarre avec une neuve devant elle ; à la
// reconnexion, l'agent reçoit la neuve sous la signature de l'ancienne,
// l'épingle, et les ordres suivants, signés par la neuve, passent.
func TestRun_FollowsARotationOfTheServerKey(t *testing.T) {
	b := newBench(t)
	token, _, _ := b.machines.CreateToken(context.Background(), "vps-nantes-3", nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, b.options(token)) }()
	online := b.waitFor(t, "vps-nantes-3", func(s machine.Status) bool { return s.Online && s.Agent.ServerKeyFingerprint != "" })
	cancel()
	<-done
	b.waitFor(t, "vps-nantes-3", func(s machine.Status) bool { return !s.Online })

	rotated, err := b.serverKey.Rotated()
	if err != nil {
		t.Fatal(err)
	}
	b.actions.SetKey(rotated)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	go func() { done <- Run(ctx, b.options("")) }()
	b.waitFor(t, "vps-nantes-3", func(s machine.Status) bool { return s.Agent.ServerKeyFingerprint == rotated.Fingerprint() })
	identity, err := LoadIdentity(b.stateDir)
	if err != nil || identity.ServerKeyFingerprint() != rotated.Fingerprint() {
		t.Fatalf("the agent did not pin the new key: %v", err)
	}
	sent, err := b.actions.Ping(audit.WithActor(context.Background(), audit.Operator("lucas")), online.ID)
	if err != nil {
		t.Fatal(err)
	}
	b.waitForRun(t, sent.ID, action.StateSucceeded)
	cancel()
	<-done
}

// Les droits s'appliquent des deux côtés : le serveur ne pousse pas un ordre
// dont l'agent n'a pas rapporté le droit (409, rien d'écrit) ; un agent qui
// a le droit juge lui-même, et son refus revient au serveur, journalisé.
func TestRun_AppliesItsRightsAndItsRefusalIsJournaled(t *testing.T) {
	b := newBench(t)
	token, _, _ := b.machines.CreateToken(context.Background(), "vps-lyon-2", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, b.options(token)) }()
	online := b.waitFor(t, "vps-lyon-2", func(s machine.Status) bool { return s.Online && !s.Agent.ReportedAt.IsZero() })
	operator := audit.WithActor(context.Background(), audit.Operator("lucas"))
	if _, err := b.actions.Send(operator, online.ID, agentwire.KindAgentUpdate, nil); !errors.Is(err, action.ErrRightMissing) {
		t.Fatalf("observe only: %v", err)
	}
	cancel()
	<-done

	// Ré-enrôlée avec le droit : l'ordre part, l'agent, qui ne tourne pas
	// depuis son dossier bin/, le refuse ; le refus est journalisé sous l'agent.
	reenroll, _, err := b.machines.CreateReenrollToken(context.Background(), online.ID, []machine.Capability{{Name: machine.CapabilityAgentUpdate}})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.stateDir.Remove(identityFile); err != nil {
		t.Fatal(err)
	}
	options := b.options(reenroll)
	options.Rights = []machine.Capability{{Name: machine.CapabilityAgentUpdate}}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	go func() { done <- Run(ctx, options) }()
	granted := b.waitFor(t, "vps-lyon-2", func(s machine.Status) bool {
		return s.Online && s.Agent.ID != online.Agent.ID && machine.HasCapability(s.Agent.Capabilities, machine.CapabilityAgentUpdate)
	})
	sent, err := b.actions.Send(operator, granted.ID, agentwire.KindAgentUpdate, nil)
	if err != nil {
		t.Fatal(err)
	}
	refused := b.waitForRun(t, sent.ID, action.StateRefused)
	if refused.Reason != agentwire.ReasonNotSelfUpdatable {
		t.Fatalf("refused %+v", refused)
	}
	events, err := b.db.ListAuditEvents(context.Background(), audit.Query{Limit: 1, ObjectKind: audit.ObjectAction})
	if err != nil || len(events) != 1 || events[0].Action != audit.ActionRefused || events[0].Actor.Kind != audit.ActorAgent {
		t.Fatalf("audit %+v %v", events, err)
	}
	cancel()
	<-done
}

// Une ligne plus longue que le tampon du lecteur, ou un événement plus long
// que MaxEventBytes, coupe le flux ; des lignes « data: » se recollent sans
// séparateur.
func TestReadEvent_IsBoundedAndJoinsDataLines(t *testing.T) {
	reader := bufio.NewReaderSize(strings.NewReader("event: probes\ndata: {\"a\":\ndata: 1}\n\n"), agentwire.MaxEventLineBytes)
	name, data, err := readEvent(reader)
	if err != nil || name != "probes" || data != `{"a":1}` {
		t.Fatalf("%q %q %v", name, data, err)
	}
	long := "event: probes\ndata: " + strings.Repeat("a", agentwire.MaxEventLineBytes) + "\n\n"
	if _, _, err := readEvent(bufio.NewReaderSize(strings.NewReader(long), agentwire.MaxEventLineBytes)); !errors.Is(err, ErrEventTooLarge) {
		t.Fatalf("long line: %v", err)
	}
	var many strings.Builder
	many.WriteString("event: probes\n")
	line := "data: " + strings.Repeat("a", agentwire.EventDataLine) + "\n"
	for written := 0; written <= agentwire.MaxEventBytes; written += agentwire.EventDataLine {
		many.WriteString(line)
	}
	many.WriteString("\n")
	if _, _, err := readEvent(bufio.NewReaderSize(strings.NewReader(many.String()), agentwire.MaxEventLineBytes)); !errors.Is(err, ErrEventTooLarge) {
		t.Fatalf("large event: %v", err)
	}
}

// Une redirection n'est pas suivie : ni l'enrôlement ni le flux ne partent
// ailleurs que vers le serveur donné, et la redirection se lit comme un
// refus.
func TestClient_DoesNotFollowARedirect(t *testing.T) {
	elsewhere := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { elsewhere++ }))
	defer target.Close()
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer redirecting.Close()
	client, err := NewClient(redirecting.URL, "", "v", false)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := NewIdentity(redirecting.URL, "", lang.French, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, enrollErr := client.Enroll(context.Background(), identity, "oc_x", hostinfo.Info{})
	_, streamErr := client.OpenStream(context.Background(), identity)
	for name, err := range map[string]error{"enroll": enrollErr, "stream": streamErr} {
		var refused *ServerError
		if !errors.As(err, &refused) || refused.Status != http.StatusTemporaryRedirect {
			t.Errorf("%s: %v", name, err)
		}
	}
	if elsewhere != 0 {
		t.Fatalf("%d requests followed the redirect", elsewhere)
	}
}

func TestReadEvent_ParsesEventsAndSkipsComments(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader(": ping\n\nevent: session\ndata: abc\n\nevent: closed\ndata: bye\n\n"))
	name, data, err := readEvent(reader)
	if err != nil || name != "session" || data != "abc" {
		t.Fatalf("%q %q %v", name, data, err)
	}
	name, data, err = readEvent(reader)
	if err != nil || name != "closed" || data != "bye" {
		t.Fatalf("%q %q %v", name, data, err)
	}
	if _, _, err := readEvent(reader); err == nil {
		t.Fatal("no EOF at the end")
	}
}

func TestPinnedTLS_RejectsAMalformedPin(t *testing.T) {
	if _, err := NewClient("https://example", "sha256:zz", "v", false); err == nil {
		t.Fatal("bad pin accepted")
	}
	if _, err := NewClient("example.com", "", "v", false); err == nil {
		t.Fatal("url without scheme accepted")
	}
}

// http:// ne passe que vers cette machine, ou en le demandant expressément.
func TestNewClient_RefusesPlainHTTPToARemoteServer(t *testing.T) {
	for _, server := range []string{"http://127.0.0.1:8080", "http://localhost:8080", "http://[::1]:8080"} {
		if _, err := NewClient(server, "", "v", false); err != nil {
			t.Errorf("%s refused: %v", server, err)
		}
	}
	for _, server := range []string{"http://192.168.1.10:8080", "http://oc.example.fr"} {
		if _, err := NewClient(server, "", "v", false); !errors.Is(err, ErrPlainRefused) {
			t.Errorf("%s: %v", server, err)
		}
		if _, err := NewClient(server, "", "v", true); err != nil {
			t.Errorf("%s with allow-plain: %v", server, err)
		}
	}
	if _, err := NewClient("https://192.168.1.10", "", "v", false); err != nil {
		t.Errorf("https refused: %v", err)
	}
}

// L'agent épinglé contre un serveur TLS au mauvais certificat refuse avant
// d'envoyer quoi que ce soit, et le dit comme un refus.
func TestPinnedClient_RefusesAnotherCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	client, err := NewClient(server.URL, strings.Repeat("00", 32), "v", false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Signal(context.Background(), "session", agentwire.SignalRequest{})
	if !errors.Is(err, ErrPinMismatch) {
		t.Fatalf("got %v", err)
	}
	catalogs, _ := lang.Load()
	if message, refused := Explain(err, catalogs.For(lang.French)); !refused || message == "" {
		t.Fatalf("explain: %q %v", message, refused)
	}
}
