package restic

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/wait/waittest"
)

const restV2 = "application/vnd.x.restic.rest.v2"

// Une source, une cible : init, config, un paquet écrit puis relu en entier
// et par plage, la liste, le verrou posé puis libéré. Tout passe par le
// mandataire, le courtier, l'exécuteur, et atterrit dans le dépôt de la
// source.
func TestRelay_RoundTripFromSourceThroughProxyBrokerAndExecutor(t *testing.T) {
	h := newHarness(t)
	sourceID, sourceKey := h.enroll()
	targetID, _ := h.enroll()
	h.connectTarget(targetID)
	repo := h.link(sourceID, targetID)
	base := h.startProxy(sourceID, sourceKey, targetID, ProxyConfig{})
	pack := strings.Repeat("restic-pack-", 1000)

	steps := []struct {
		method, path string
		body         []byte
		want         int
	}{
		{"POST", "?create=true", nil, http.StatusOK},
		{"POST", "config", []byte("config-blob"), http.StatusOK},
		{"GET", "config", nil, http.StatusOK},
		{"POST", "data/" + sampleName, []byte(pack), http.StatusOK},
		{"HEAD", "data/" + sampleName, nil, http.StatusOK},
		{"POST", "locks/" + otherName, []byte("lock"), http.StatusOK},
		{"DELETE", "locks/" + otherName, nil, http.StatusOK},
	}
	for _, step := range steps {
		status, body, _ := call(t, step.method, base+step.path, step.body, nil)
		if status != step.want {
			t.Fatalf("%s %s: %d %s", step.method, step.path, status, body)
		}
	}

	status, body, _ := call(t, "GET", base+"data/"+sampleName, nil, nil)
	if status != http.StatusOK || body != pack {
		t.Fatalf("read back %d, %d bytes", status, len(body))
	}
	status, body, header := call(t, "GET", base+"data/"+sampleName, nil, http.Header{"Range": {"bytes=12-23"}})
	if status != http.StatusPartialContent || body != "restic-pack-" || header.Get("Content-Range") != "bytes 12-23/12000" {
		t.Fatalf("range %d %q %v", status, body, header)
	}
	status, body, header = call(t, "GET", base+"data/", nil, http.Header{"Accept": {restV2}})
	if status != http.StatusOK || !strings.Contains(body, sampleName) || header.Get("Content-Type") != restV2 {
		t.Fatalf("list %d %q %v", status, body, header)
	}
	if h.rest.fileCount(repo) != 2 {
		t.Errorf("repo holds %d files, want config and the pack", h.rest.fileCount(repo))
	}
	for _, seen := range h.rest.requests() {
		if !strings.HasPrefix(strings.SplitN(seen, " ", 2)[1], "/"+repo+"/") {
			t.Errorf("rest-server saw %q outside the source repo", seen)
		}
	}
}

// Le cœur : DELETE d'un paquet refusé par le mandataire, par le courtier
// quand on le contourne, par l'exécuteur quand le relais est compromis, et
// par rest-server lui-même.
func TestRelay_DeleteDataIsRefusedAtEveryStage(t *testing.T) {
	h := newHarness(t)
	sourceID, sourceKey := h.enroll()
	targetID, _ := h.enroll()
	h.connectTarget(targetID)
	repo := h.link(sourceID, targetID)
	base := h.startProxy(sourceID, sourceKey, targetID, ProxyConfig{})
	call(t, "POST", base+"?create=true", nil, nil)
	call(t, "POST", base+"data/"+sampleName, []byte("pack"), nil)
	pushedBefore := h.pushCount()

	status, body, _ := call(t, "DELETE", base+"data/"+sampleName, nil, nil)
	if status != http.StatusForbidden || body != `{"error":"delete_refused"}` {
		t.Errorf("proxy: %d %s", status, body)
	}

	direct := h.server.URL + "/agent/relay/rest/" + targetID + "/data/" + sampleName
	status, body, _ = call(t, "DELETE", direct, nil, signedHeaders(t, sourceID, sourceKey))
	if status != http.StatusForbidden || body != `{"error":"delete_refused"}` {
		t.Errorf("broker: %d %s", status, body)
	}
	if h.pushCount() != pushedBefore {
		t.Error("the broker pushed a refused request to the target")
	}

	replies := &recordingRelayServer{}
	executor := h.executor(h.passwordOf)
	forged := agentwire.ResticRelayRequest{ID: strings.Repeat("a", 32), Repo: repo, Method: "DELETE", Path: "/data/" + sampleName}
	if err := executor.Handle(context.Background(), replies, forged); err != nil {
		t.Fatal(err)
	}
	if replies.head.Status != http.StatusForbidden || replies.body != `{"error":"delete_refused"}` || replies.fetched {
		t.Errorf("executor: %+v %q fetched=%v", replies.head, replies.body, replies.fetched)
	}

	for _, seen := range h.rest.requests() {
		if strings.HasPrefix(seen, "DELETE") {
			t.Errorf("rest-server saw %q", seen)
		}
	}
	password, _ := h.passwordOf(repo)
	for _, path := range []string{"/data/" + sampleName, "/snapshots/" + otherName} {
		if status := h.callRest("DELETE", "/"+repo+path, repo, password); status != http.StatusForbidden {
			t.Errorf("rest-server append-only, DELETE %s: %d", path, status)
		}
	}
	if h.rest.fileCount(repo) != 1 {
		t.Errorf("rest-server kept %d files", h.rest.fileCount(repo))
	}
}

// rest-server est le même pour le direct et le relais : une écriture sans
// identifiants, ou avec ceux d'un autre producteur, est refusée par lui ; un
// dépôt dont la cible n'a pas le mot de passe ne part pas jusqu'à lui.
func TestRelay_RestServerRefusesWritesWithoutOrWithAnotherProducersCredentials(t *testing.T) {
	h := newHarness(t)
	sourceA, keyA := h.enroll()
	sourceB, _ := h.enroll()
	targetID, _ := h.enroll()
	h.connectTarget(targetID)
	repoA := h.link(sourceA, targetID)
	repoB := h.link(sourceB, targetID)
	base := h.startProxy(sourceA, keyA, targetID, ProxyConfig{})
	call(t, "POST", base+"?create=true", nil, nil)
	passwordB, _ := h.passwordOf(repoB)

	if status := h.callRest("POST", "/"+repoA+"/data/"+sampleName, "", ""); status != http.StatusUnauthorized {
		t.Errorf("no credentials: %d", status)
	}
	if status := h.callRest("POST", "/"+repoA+"/data/"+sampleName, repoB, passwordB); status != http.StatusUnauthorized {
		t.Errorf("B's credentials on A's repository: %d", status)
	}
	if status := h.callRest("POST", "/"+repoA+"/data/"+sampleName, repoA, passwordB); status != http.StatusUnauthorized {
		t.Errorf("A's name with B's password: %d", status)
	}

	// Une cible qui présenterait le mauvais mot de passe pour un dépôt, ou
	// n'en aurait aucun.
	request := agentwire.ResticRelayRequest{ID: strings.Repeat("d", 32), Repo: repoA, Method: "GET", Path: "/config"}
	wrong := &recordingRelayServer{}
	crossed := h.executor(func(string) (string, bool) { return passwordB, true })
	if err := crossed.Handle(context.Background(), wrong, request); err != nil {
		t.Fatal(err)
	}
	if wrong.head.Status != http.StatusUnauthorized {
		t.Errorf("another producer's password through the executor: %+v", wrong.head)
	}
	before := len(h.rest.requests())
	unknown := &recordingRelayServer{}
	if err := h.executor(func(string) (string, bool) { return "", false }).Handle(context.Background(), unknown, request); err != nil {
		t.Fatal(err)
	}
	if unknown.head.Status != http.StatusForbidden || unknown.body != `{"error":"user_unknown"}` || len(h.rest.requests()) != before {
		t.Errorf("unknown user: %+v %q", unknown.head, unknown.body)
	}
	if h.rest.fileCount(repoA) != 0 {
		t.Errorf("rest-server wrote %d files", h.rest.fileCount(repoA))
	}
}

// Deux sources sur la même cible : chacune ne voit que son dépôt, quoi
// qu'elle écrive dans le chemin.
func TestRelay_TwoSourcesAreIsolated(t *testing.T) {
	h := newHarness(t)
	sourceA, keyA := h.enroll()
	sourceB, keyB := h.enroll()
	targetID, _ := h.enroll()
	otherTarget, _ := h.enroll()
	h.connectTarget(targetID)
	repoA := h.link(sourceA, targetID)
	repoB := h.link(sourceB, targetID)
	baseA := h.startProxy(sourceA, keyA, targetID, ProxyConfig{})
	baseB := h.startProxy(sourceB, keyB, targetID, ProxyConfig{})
	call(t, "POST", baseA+"?create=true", nil, nil)
	call(t, "POST", baseA+"snapshots/"+sampleName, []byte("snapshot of A"), nil)
	call(t, "POST", baseB+"?create=true", nil, nil)

	if status, _, _ := call(t, "GET", baseB+"snapshots/"+sampleName, nil, nil); status != http.StatusNotFound {
		t.Errorf("B reads A's snapshot name in its own repo: %d", status)
	}
	if status, _, _ := call(t, "POST", baseB+"snapshots/"+sampleName, []byte("B"), nil); status != http.StatusOK {
		t.Errorf("B writes in its own repo: %d", status)
	}
	direct := h.server.URL + "/agent/relay/rest/" + targetID + "/"
	for _, hostile := range []string{repoA + "/config", "../" + repoA + "/config", "%2e%2e/" + repoA + "/config"} {
		status, body, _ := call(t, "GET", direct+hostile, nil, signedHeaders(t, sourceB, keyB))
		if status != http.StatusForbidden || body != `{"error":"path_refused"}` {
			t.Errorf("%s: %d %s", hostile, status, body)
		}
	}
	status, body, _ := call(t, "GET", h.server.URL+"/agent/relay/rest/"+otherTarget+"/config", nil, signedHeaders(t, sourceB, keyB))
	if status != http.StatusForbidden || body != `{"error":"link_refused"}` {
		t.Errorf("unlinked target: %d %s", status, body)
	}
	if h.rest.fileCount(repoA) != 1 || h.rest.fileCount(repoB) != 1 {
		t.Errorf("files A=%d B=%d", h.rest.fileCount(repoA), h.rest.fileCount(repoB))
	}
}

// Une preuve rejouée, ou signée par B au nom de A, est refusée : le couple
// (machine, aléa) est retenu par machine.Service.
func TestRelay_ReplayedOrForgedProofIsRefused(t *testing.T) {
	h := newHarness(t)
	sourceA, keyA := h.enroll()
	sourceB, keyB := h.enroll()
	targetID, _ := h.enroll()
	h.connectTarget(targetID)
	h.link(sourceA, targetID)
	direct := h.server.URL + "/agent/relay/rest/" + targetID + "/config"
	proof := signedHeaders(t, sourceA, keyA)

	if status, body, _ := call(t, "GET", direct, nil, proof); status != http.StatusNotFound {
		t.Fatalf("first use: %d %s", status, body)
	}
	if status, body, _ := call(t, "GET", direct, nil, proof); status != http.StatusUnauthorized || body != `{"error":"bad_proof"}` {
		t.Errorf("replay: %d %s", status, body)
	}
	forged := signedHeaders(t, sourceB, keyB)
	forged.Set(agentwire.HeaderMachine, sourceA)
	if status, _, _ := call(t, "GET", direct, nil, forged); status != http.StatusUnauthorized {
		t.Errorf("forged: %d", status)
	}
}

// Une cible sans flux, une file pleine, une cible muette : 503 ou 504, que
// restic réessaie et que le mandataire borne.
func TestRelay_BoundsTheQueueAndTheWait(t *testing.T) {
	h := newHarness(t)
	sourceID, sourceKey := h.enroll()
	targetID, _ := h.enroll()
	h.link(sourceID, targetID)
	direct := h.server.URL + "/agent/relay/rest/" + targetID + "/config"

	status, body, _ := call(t, "GET", direct, nil, signedHeaders(t, sourceID, sourceKey))
	if status != http.StatusServiceUnavailable || body != `{"error":"target_offline"}` {
		t.Errorf("offline: %d %s", status, body)
	}

	// Un flux ouvert que personne ne sert.
	h.mu.Lock()
	h.targets[targetID] = make(chan agentwire.ResticRelayRequest, 16)
	h.mu.Unlock()
	h.broker.SetLimits(BrokerLimits{QueuePerTarget: 1, HeadTimeout: 200 * time.Millisecond})
	done := make(chan int)
	firstProof := signedHeaders(t, sourceID, sourceKey)
	go func() {
		status, _, _, _ := send("GET", direct, nil, firstProof)
		done <- status
	}()
	waittest.Until(t, "the first push to reach the target", func() bool { return h.pushCount() == 1 })
	status, body, _ = call(t, "GET", direct, nil, signedHeaders(t, sourceID, sourceKey))
	if status != http.StatusServiceUnavailable || body != `{"error":"target_busy"}` {
		t.Errorf("busy: %d %s", status, body)
	}
	if status := <-done; status != http.StatusGatewayTimeout {
		t.Errorf("silent target: %d", status)
	}
}

// Seule la cible de la requête tire son corps, une seule fois ; une réponse
// à une requête inconnue est refusée.
func TestBroker_BodyAndResponseBelongToTheTargetOnly(t *testing.T) {
	h := newHarness(t)
	sourceID, sourceKey := h.enroll()
	targetID, _ := h.enroll()
	intruderID, _ := h.enroll()
	h.link(sourceID, targetID)
	events := make(chan agentwire.ResticRelayRequest, 1)
	h.mu.Lock()
	h.targets[targetID] = events
	h.mu.Unlock()
	proof := signedHeaders(t, sourceID, sourceKey)
	go func() {
		_, _, _, _ = send("POST", h.server.URL+"/agent/relay/rest/"+targetID+"/data/"+sampleName, []byte("pack"), proof)
	}()
	request := <-events

	if _, err := h.broker.TakeBody(intruderID, request.ID); !errors.Is(err, ErrUnknownRequest) {
		t.Errorf("intruder took the body: %v", err)
	}
	body, err := h.broker.TakeBody(targetID, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if content, _ := io.ReadAll(body); string(content) != "pack" {
		t.Errorf("body %q", content)
	}
	if _, err := h.broker.TakeBody(targetID, request.ID); !errors.Is(err, ErrUnknownRequest) {
		t.Errorf("second take: %v", err)
	}
	if err := h.broker.Deliver(intruderID, request.ID, agentwire.ResticRelayHead{Status: 200}, strings.NewReader("")); !errors.Is(err, ErrUnknownRequest) {
		t.Errorf("intruder delivered: %v", err)
	}
	if err := h.broker.Deliver(targetID, request.ID, agentwire.ResticRelayHead{Status: 200}, strings.NewReader("")); err != nil {
		t.Errorf("deliver: %v", err)
	}
}

// La patience du mandataire : les 5xx passent à restic tant qu'ils durent
// moins que la patience, puis 403 ; un 403 du relais ne remet pas le
// compteur à zéro, un 2xx si.
func TestProxy_AnswersForbiddenAfterPatienceOfContinuousServerErrors(t *testing.T) {
	relay := &scriptedRelay{status: http.StatusServiceUnavailable}
	server := httptest.NewServer(relay)
	t.Cleanup(server.Close)
	endpoint, _ := url.Parse(server.URL + "/agent/relay/rest/nas")
	clock := &manualClock{now: time.Unix(1_800_000_000, 0)}
	sourceID, _ := machine.NewID()
	proxy := NewProxy(ProxyConfig{
		Endpoint: endpoint, Client: http.DefaultClient, MachineID: sourceID,
		Sign: func([]byte) []byte { return []byte("signature") }, Now: clock.Now,
	})
	repositoryURL, err := proxy.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })
	config := strings.TrimPrefix(repositoryURL, "rest:") + "config"

	expect := func(step string, want int) {
		t.Helper()
		if status, body, _ := call(t, "GET", config, nil, nil); status != want {
			t.Errorf("%s: %d %s", step, status, body)
		}
	}
	expect("first 503 passes", http.StatusServiceUnavailable)
	clock.advance(10 * time.Second)
	relay.set(http.StatusForbidden)
	expect("a 403 passes", http.StatusForbidden)
	relay.set(http.StatusServiceUnavailable)
	clock.advance(4 * time.Second)
	expect("still patient at 14 s", http.StatusServiceUnavailable)
	clock.advance(time.Second)
	expect("out of patience at 15 s", http.StatusForbidden)
	relay.set(http.StatusOK)
	expect("a 2xx passes", http.StatusOK)
	relay.set(http.StatusBadGateway)
	clock.advance(time.Minute)
	expect("the counter restarted", http.StatusBadGateway)
}

func TestProxy_RefusesOtherLocalProcessesAndSignsEachRequest(t *testing.T) {
	relay := &scriptedRelay{status: http.StatusOK}
	server := httptest.NewServer(relay)
	t.Cleanup(server.Close)
	endpoint, _ := url.Parse(server.URL + "/agent/relay/rest/nas")
	sourceID, _ := machine.NewID()
	proxy := NewProxy(ProxyConfig{Endpoint: endpoint, Client: http.DefaultClient, MachineID: sourceID, Sign: func([]byte) []byte { return []byte("s") }})
	repositoryURL, err := proxy.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })
	base := strings.TrimPrefix(repositoryURL, "rest:")
	withoutToken := base[:strings.Index(base[len("http://"):], "/")+len("http://")] + "/config"

	if status, body, _ := call(t, "GET", withoutToken, nil, nil); status != http.StatusForbidden || body != `{"error":"token_refused"}` {
		t.Errorf("no token: %d %s", status, body)
	}
	call(t, "GET", base+"config", nil, nil)
	call(t, "GET", base+"config", nil, nil)
	nonces := relay.nonces()
	if len(nonces) != 2 || nonces[0] == nonces[1] || nonces[0] == "" {
		t.Errorf("nonces %q", nonces)
	}
	if !slices.Contains(relay.paths(), "/agent/relay/rest/nas/config") {
		t.Errorf("paths %q", relay.paths())
	}
}

// scriptedRelay répond le statut qu'on lui donne et note ce qu'il reçoit.
type scriptedRelay struct {
	mu     sync.Mutex
	status int
	seen   []string
	proofs []string
}

func (s *scriptedRelay) set(status int) {
	s.mu.Lock()
	s.status = status
	s.mu.Unlock()
}

func (s *scriptedRelay) nonces() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.proofs)
}

func (s *scriptedRelay) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.seen)
}

func (s *scriptedRelay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, r.URL.Path)
	s.proofs = append(s.proofs, r.Header.Get(agentwire.HeaderNonce))
	w.WriteHeader(s.status)
}

type manualClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) advance(step time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(step)
	c.mu.Unlock()
}

// recordingRelayServer garde la réponse que l'exécuteur pousse.
type recordingRelayServer struct {
	head    agentwire.ResticRelayHead
	body    string
	fetched bool
}

func (r *recordingRelayServer) FetchBody(context.Context, string) (io.ReadCloser, error) {
	r.fetched = true
	return io.NopCloser(strings.NewReader("")), nil
}

func (r *recordingRelayServer) SendResponse(_ context.Context, _ string, head agentwire.ResticRelayHead, body io.Reader) error {
	content, err := io.ReadAll(body)
	r.head, r.body = head, string(content)
	return err
}

// signedHeaders signe une preuve neuve comme le mandataire.
func signedHeaders(t *testing.T, machineID string, key ed25519.PrivateKey) http.Header {
	t.Helper()
	nonce := machine.NewNonce()
	timestamp := time.Now().Unix()
	payload, err := machine.SignedPayload(nonce, machineID, timestamp)
	if err != nil {
		t.Fatal(err)
	}
	header := http.Header{}
	header.Set(agentwire.HeaderMachine, machineID)
	header.Set(agentwire.HeaderNonce, base64.StdEncoding.EncodeToString(nonce))
	header.Set(agentwire.HeaderTimestamp, strconv.FormatInt(timestamp, 10))
	header.Set(agentwire.HeaderSignature, base64.StdEncoding.EncodeToString(machine.Sign(key, payload)))
	return header
}
