package restic

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Le banc du relais : un faux rest-server en écriture seule, le courtier
// derrière un serveur HTTP de test (les routes que agentapi montera), des
// agents de cible qui servent leurs événements avec l'exécuteur, et des
// sources avec leur mandataire. Tout passe par la boucle locale.

// fakeRestServer applique ce que rest-server --tls --append-only
// --private-repos --htpasswd-file applique : un utilisateur et son mot de
// passe par requête, un dossier par dépôt ouvert à son seul utilisateur,
// rien ne s'écrase, rien ne s'efface hors des verrous. Il note chaque
// requête reçue.
type fakeRestServer struct {
	mu    sync.Mutex
	users map[string]string
	repos map[string]bool
	files map[string][]byte
	seen  []string
}

// newFakeRestServer rend le faux, son adresse TLS et un client qui
// n'épingle que son certificat, comme l'exécuteur de la cible.
func newFakeRestServer(t *testing.T) (*fakeRestServer, *url.URL, *http.Client) {
	t.Helper()
	fake := &fakeRestServer{users: map[string]string{}, repos: map[string]bool{}, files: map[string][]byte{}}
	server := httptest.NewTLSServer(fake)
	t.Cleanup(server.Close)
	location, _ := url.Parse(server.URL)
	return fake, location, server.Client()
}

// addUser pose la ligne htpasswd d'un utilisateur.
func (f *fakeRestServer) addUser(user, password string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[user] = password
}

func (f *fakeRestServer) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen...)
}

func (f *fakeRestServer) fileCount(repo string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for name := range f.files {
		if strings.HasPrefix(name, "/"+repo+"/") {
			count++
		}
	}
	return count
}

func (f *fakeRestServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, r.Method+" "+r.URL.RequestURI())
	repo, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	// htpasswd, puis --private-repos : l'utilisateur n'ouvre que son dépôt.
	user, password, hasAuth := r.BasicAuth()
	known, isUser := f.users[user]
	if !hasAuth || !isUser || known != password || user != repo {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if rest == "" && r.Method == http.MethodPost && r.URL.RawQuery == "create=true" {
		f.repos[repo] = true
		return
	}
	if !f.repos[repo] {
		http.NotFound(w, r)
		return
	}
	switch {
	case rest == "":
		w.WriteHeader(http.StatusMethodNotAllowed)
	case strings.HasSuffix(rest, "/"):
		f.list(w, r)
	default:
		f.file(w, r, rest)
	}
}

func (f *fakeRestServer) list(w http.ResponseWriter, r *http.Request) {
	type entry struct {
		Name string `json:"name"`
		Size int    `json:"size"`
	}
	entries := []entry{}
	for name, content := range f.files {
		if base, ok := strings.CutPrefix(name, r.URL.Path); ok {
			entries = append(entries, entry{Name: base, Size: len(content)})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	w.Header().Set("Content-Type", "application/vnd.x.restic.rest.v2")
	_ = json.NewEncoder(w).Encode(entries)
}

func (f *fakeRestServer) file(w http.ResponseWriter, r *http.Request, rest string) {
	content, exists := f.files[r.URL.Path]
	switch r.Method {
	case http.MethodPost:
		if exists {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.files[r.URL.Path] = body
	case http.MethodDelete:
		if !strings.HasPrefix(rest, "locks/") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		delete(f.files, r.URL.Path)
	case http.MethodGet, http.MethodHead:
		if !exists {
			http.NotFound(w, r)
			return
		}
		serveRange(w, r, content)
	}
}

// serveRange ne connaît que « bytes=a-b », ce que restic demande.
func serveRange(w http.ResponseWriter, r *http.Request, content []byte) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Accept-Ranges", "bytes")
	var first, last int
	if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &first, &last); err == nil && last < len(content) {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", first, last, len(content)))
		w.Header().Set("Content-Length", strconv.Itoa(last-first+1))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(content[first : last+1])
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	_, _ = w.Write(content)
}

// keyStore : la seule lecture dont machine.Service a besoin pour
// Authenticate. Le reste de l'interface n'est jamais appelé.
type keyStore struct {
	machine.Store
	keys map[string]ed25519.PublicKey
}

func (s keyStore) GetMachine(_ context.Context, id string) (machine.Machine, error) {
	key, ok := s.keys[id]
	if !ok {
		return machine.Machine{}, machine.ErrNotFound
	}
	return machine.Machine{ID: id, Agent: machine.Agent{ID: "agent-" + id, MachineID: id, PublicKey: key}}, nil
}

type linkTable map[[2]string]string

func (l linkTable) RelayRepo(_ context.Context, sourceID, targetID string) (string, error) {
	repo, ok := l[[2]string{sourceID, targetID}]
	if !ok {
		return "", ErrNotLinked
	}
	return repo, nil
}

// harness est le serveur : courtier, flux simulés des cibles, routes HTTP.
// passwords est ce que l'agent de la cible garde : le mot de passe REST de
// chaque dépôt.
type harness struct {
	t          *testing.T
	broker     *Broker
	server     *httptest.Server
	keys       keyStore
	links      linkTable
	rest       *fakeRestServer
	restURL    *url.URL
	restClient *http.Client

	mu        sync.Mutex
	targets   map[string]chan agentwire.ResticRelayRequest
	pushed    int
	passwords map[string]string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	h := &harness{
		t:         t,
		keys:      keyStore{keys: map[string]ed25519.PublicKey{}},
		links:     linkTable{},
		targets:   map[string]chan agentwire.ResticRelayRequest{},
		passwords: map[string]string{},
	}
	h.rest, h.restURL, h.restClient = newFakeRestServer(t)
	machines := machine.New(h.keys, machine.NewSessions(), logger)
	h.broker = NewBroker(machines, h.links, h, logger)
	h.server = httptest.NewServer(http.HandlerFunc(h.route))
	t.Cleanup(h.server.Close)
	return h
}

// PushResticRelay : le flux de la cible, s'il est ouvert.
func (h *harness) PushResticRelay(targetID string, request agentwire.ResticRelayRequest) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	events, ok := h.targets[targetID]
	if !ok {
		return machine.ErrNotConnected
	}
	h.pushed++
	events <- request
	return nil
}

func (h *harness) pushCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.pushed
}

// enroll inscrit une machine et rend son identifiant et sa clé.
func (h *harness) enroll() (string, ed25519.PrivateKey) {
	h.t.Helper()
	id, err := machine.NewID()
	if err != nil {
		h.t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		h.t.Fatal(err)
	}
	h.keys.keys[id] = public
	return id, private
}

// connectTarget ouvre le flux d'une cible : chaque événement est servi par
// l'exécuteur, comme le fera l'agent.
func (h *harness) connectTarget(targetID string) {
	events := make(chan agentwire.ResticRelayRequest, 16)
	h.mu.Lock()
	h.targets[targetID] = events
	h.mu.Unlock()
	executor := h.executor(h.passwordOf)
	ctx, cancel := context.WithCancel(context.Background())
	h.t.Cleanup(cancel)
	relayServer := httpRelayServer{base: h.server.URL, target: targetID}
	go func() {
		for {
			select {
			case request := <-events:
				go func() { _ = executor.Handle(ctx, relayServer, request) }()
			case <-ctx.Done():
				return
			}
		}
	}()
}

// executor est celui de l'agent de la cible : rest-server en TLS épinglé,
// les mots de passe que passwordOf rend.
func (h *harness) executor(passwordOf PasswordOf) *Executor {
	return NewExecutor(h.restURL, h.restClient, passwordOf, slog.New(slog.DiscardHandler))
}

func (h *harness) passwordOf(repo string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	password, ok := h.passwords[repo]
	return password, ok
}

// link déclare le lien source → cible ; le dépôt est l'utilisateur de la
// source, posé ici comme le fera le serveur, avec son mot de passe REST :
// une ligne htpasswd chez rest-server, le mot de passe gardé par la cible.
func (h *harness) link(sourceID, targetID string) string {
	repo, err := agentwire.ResticUserFor(sourceID)
	if err != nil {
		h.t.Fatal(err)
	}
	h.links[[2]string{sourceID, targetID}] = repo
	password := "rest-password-of-" + repo
	h.rest.addUser(repo, password)
	h.mu.Lock()
	h.passwords[repo] = password
	h.mu.Unlock()
	return repo
}

// callRest joue une requête droit sur rest-server, sous un utilisateur
// (vide : sans identifiants).
func (h *harness) callRest(method, path, user, password string) int {
	h.t.Helper()
	request, err := http.NewRequest(method, h.restURL.String()+path, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	if user != "" {
		request.SetBasicAuth(user, password)
	}
	response, err := h.restClient.Do(request)
	if err != nil {
		h.t.Fatal(err)
	}
	_ = response.Body.Close()
	return response.StatusCode
}

// startProxy démarre le mandataire d'une source vers une cible et rend
// l'adresse HTTP que restic utiliserait.
func (h *harness) startProxy(sourceID string, key ed25519.PrivateKey, targetID string, config ProxyConfig) string {
	h.t.Helper()
	endpoint, _ := url.Parse(h.server.URL + "/agent/relay/rest/" + targetID)
	config.Endpoint = endpoint
	config.Client = http.DefaultClient
	config.MachineID = sourceID
	config.Sign = func(payload []byte) []byte { return machine.Sign(key, payload) }
	proxy := NewProxy(config)
	repositoryURL, err := proxy.Start()
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = proxy.Close() })
	return strings.TrimPrefix(repositoryURL, "rest:")
}

func (h *harness) route(w http.ResponseWriter, r *http.Request) {
	path := r.URL.EscapedPath()
	if rest, ok := strings.CutPrefix(path, "/agent/relay/rest/"); ok {
		h.serveRest(w, r, rest)
		return
	}
	target := r.Header.Get("Test-Target")
	if id, ok := strings.CutPrefix(path, "/agent/relay/body/"); ok {
		body, err := h.broker.TakeBody(target, id)
		if err != nil {
			status, code := RelayStatus(err)
			writeRelayError(w, status, code)
			return
		}
		_, _ = io.Copy(w, body)
		return
	}
	if id, ok := strings.CutPrefix(path, "/agent/relay/response/"); ok {
		var head agentwire.ResticRelayHead
		if json.Unmarshal([]byte(r.Header.Get(agentwire.HeaderResticRelayHead)), &head) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := h.broker.Deliver(target, id, head, r.Body); err != nil {
			status, code := RelayStatus(err)
			writeRelayError(w, status, code)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.NotFound(w, r)
}

// serveRest est ce que le handler de agentapi fera : lire la preuve, appeler
// le courtier, recopier la réponse.
func (h *harness) serveRest(w http.ResponseWriter, r *http.Request, rest string) {
	target, repoPath, _ := strings.Cut(rest, "/")
	proof, err := testProofFromHeaders(r.Header)
	if err != nil {
		writeRelayError(w, http.StatusUnauthorized, "bad_proof")
		return
	}
	response, err := h.broker.Submit(r.Context(), proof, target, RelayRequest{
		Method: r.Method, Path: "/" + repoPath, Query: r.URL.RawQuery,
		Accept: r.Header.Get("Accept"), Range: r.Header.Get("Range"),
		ContentLength: r.ContentLength, Body: r.Body,
	})
	if err != nil {
		status, code := RelayStatus(err)
		writeRelayError(w, status, code)
		return
	}
	defer response.Body.Close()
	for name, values := range response.Header {
		w.Header()[name] = values
	}
	w.WriteHeader(response.Status)
	_, _ = io.Copy(w, response.Body)
}

func testProofFromHeaders(header http.Header) (machine.Proof, error) {
	nonce, err := base64.StdEncoding.DecodeString(header.Get(agentwire.HeaderNonce))
	if err != nil {
		return machine.Proof{}, err
	}
	signature, err := base64.StdEncoding.DecodeString(header.Get(agentwire.HeaderSignature))
	if err != nil {
		return machine.Proof{}, err
	}
	timestamp, err := strconv.ParseInt(header.Get(agentwire.HeaderTimestamp), 10, 64)
	if err != nil {
		return machine.Proof{}, err
	}
	return machine.Proof{MachineID: header.Get(agentwire.HeaderMachine), Nonce: nonce, Timestamp: timestamp, Signature: signature}, nil
}

// httpRelayServer est le côté serveur vu de l'agent de la cible.
type httpRelayServer struct {
	base   string
	target string
}

func (s httpRelayServer) FetchBody(ctx context.Context, id string) (io.ReadCloser, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/agent/relay/body/"+id, nil)
	request.Header.Set("Test-Target", s.target)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return nil, errors.New(response.Status)
	}
	return response.Body, nil
}

func (s httpRelayServer) SendResponse(ctx context.Context, id string, head agentwire.ResticRelayHead, body io.Reader) error {
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.base+"/agent/relay/response/"+id, body)
	encoded, _ := json.Marshal(head)
	request.Header.Set(agentwire.HeaderResticRelayHead, string(encoded))
	request.Header.Set("Test-Target", s.target)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return errors.New(response.Status)
	}
	return nil
}

// call joue une requête de restic contre une adresse et rend statut, corps
// et en-têtes.
func call(t *testing.T, method, address string, body []byte, header http.Header) (int, string, http.Header) {
	t.Helper()
	status, content, responseHeader, err := send(method, address, body, header)
	if err != nil {
		t.Fatal(err)
	}
	return status, content, responseHeader
}

// send est call sans *testing.T, pour une goroutine.
func send(method, address string, body []byte, header http.Header) (int, string, http.Header, error) {
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	request, err := http.NewRequest(method, address, reader)
	if err != nil {
		return 0, "", nil, err
	}
	for name, values := range header {
		request.Header[name] = values
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, "", nil, err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)
	return response.StatusCode, string(content), response.Header, err
}
