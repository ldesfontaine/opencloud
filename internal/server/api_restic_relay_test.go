package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

// fakeRestServer joue rest-server --append-only --private-repos sur la boucle
// locale : un dossier par dépôt, rien ne s'écrase, rien ne s'efface hors des
// verrous. Aucun vrai rest-server ne tourne dans les tests.
type fakeRestServer struct {
	mu    sync.Mutex
	files map[string][]byte
	seen  []string
}

func newFakeRestServer(t *testing.T) (*fakeRestServer, *url.URL) {
	t.Helper()
	fake := &fakeRestServer{files: map[string][]byte{}}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	location, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return fake, location
}

func (f *fakeRestServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, r.Method+" "+r.URL.RequestURI())
	_, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodPost && rest == "" && r.URL.RawQuery == "create=true":
		return
	case r.Method == http.MethodPost:
		if _, exists := f.files[r.URL.Path]; exists {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.files[r.URL.Path] = body
	case r.Method == http.MethodGet:
		content, exists := f.files[r.URL.Path]
		if !exists {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		_, _ = w.Write(content)
	case r.Method == http.MethodDelete && strings.HasPrefix(rest, "locks/"):
		delete(f.files, r.URL.Path)
	default:
		w.WriteHeader(http.StatusForbidden)
	}
}

func (f *fakeRestServer) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen...)
}

// targetAgentLink est le côté serveur vu de l'agent de la cible : tirer le
// corps, pousser la réponse, sous le jeton de session de son flux.
type targetAgentLink struct {
	base    string
	session string
	client  *http.Client
}

func (l targetAgentLink) FetchBody(ctx context.Context, id string) (io.ReadCloser, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, l.base+agentwire.ResticRelayBodyPath(id), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set(agentwire.HeaderProtocol, agentwire.ProtocolVersion)
	request.Header.Set("Authorization", "Bearer "+l.session)
	response, err := l.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return nil, errors.New(response.Status)
	}
	return response.Body, nil
}

func (l targetAgentLink) SendResponse(ctx context.Context, id string, head agentwire.ResticRelayHead, body io.Reader) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, l.base+agentwire.ResticRelayResponsePath(id), body)
	if err != nil {
		return err
	}
	encoded, _ := json.Marshal(head)
	request.Header.Set(agentwire.HeaderProtocol, agentwire.ProtocolVersion)
	request.Header.Set("Authorization", "Bearer "+l.session)
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set(agentwire.HeaderResticRelayHead, string(encoded))
	response, err := l.client.Do(request)
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return errors.New(response.Status)
	}
	return nil
}

// serveTargetRelays fait l'agent de la cible : chaque requête relayée que le
// serveur pousse sur son flux est rejugée et rejouée sur le faux rest-server.
func serveTargetRelays(t *testing.T, session *machine.Session, link targetAgentLink, restServer *url.URL) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	anyPassword := func(string) (string, bool) { return "relay-test-password", true }
	executor := restic.NewExecutor(restServer, http.DefaultClient, anyPassword, slog.New(slog.DiscardHandler))
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-session.Events():
				if event.Name != string(agentwire.KindResticRelay) {
					continue
				}
				command, err := agentwire.DecodeObservation(event.Name, event.Data)
				if err != nil {
					continue
				}
				go func() { _ = executor.Handle(ctx, link, *command.Relay) }()
			}
		}
	}()
}

func relayCall(t *testing.T, client *http.Client, method, address, body string, header http.Header) (int, string) {
	t.Helper()
	request, err := http.NewRequest(method, address, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body == "" {
		request.Body = http.NoBody
	}
	for name, values := range header {
		request.Header[name] = values
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	content, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(content)
}

// signedRelayHeader signe une requête comme le mandataire de la source.
func signedRelayHeader(t *testing.T, machineID string, sign func([]byte) []byte) http.Header {
	t.Helper()
	nonce := machine.NewNonce()
	payload, err := machine.SignedPayload(nonce, machineID, testNow.Unix())
	if err != nil {
		t.Fatal(err)
	}
	header := http.Header{}
	header.Set(agentwire.HeaderProtocol, agentwire.ProtocolVersion)
	header.Set(agentwire.HeaderMachine, machineID)
	header.Set(agentwire.HeaderNonce, base64.StdEncoding.EncodeToString(nonce))
	header.Set(agentwire.HeaderTimestamp, strconv.FormatInt(testNow.Unix(), 10))
	header.Set(agentwire.HeaderSignature, base64.StdEncoding.EncodeToString(sign(payload)))
	return header
}

// De bout en bout, côté serveur : une source qui ne joint pas sa cible
// passe par le relais (son mandataire local, les routes /agent/relay/, le
// flux de la cible, rest-server). Le dépôt est celui que le serveur nomme ;
// une suppression est refusée, au mandataire comme au serveur, et n'atteint
// jamais rest-server.
func TestRestic_ARelayedSourceReachesItsRepositoryAndCannotDelete(t *testing.T) {
	bench := readyResticBench(t)
	bench.createServiceBackup(t)
	live := httptest.NewServer(bench.Server)
	t.Cleanup(live.Close)
	rest, restURL := newFakeRestServer(t)
	serveTargetRelays(t, bench.target, targetAgentLink{base: live.URL, session: bench.target.Token, client: live.Client()}, restURL)

	endpoint, err := url.Parse(live.URL + agentwire.ResticRelayPath(resticTargetID))
	if err != nil {
		t.Fatal(err)
	}
	sign := func(payload []byte) []byte { return machine.Sign(bench.sourceKey, payload) }
	proxy := restic.NewProxy(restic.ProxyConfig{
		Endpoint: endpoint, Client: live.Client(), MachineID: resticSourceID, Sign: sign, Now: func() time.Time { return testNow },
	})
	repositoryURL, err := proxy.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })
	base := strings.TrimPrefix(repositoryURL, "rest:")
	pack := strings.Repeat("c", 64)

	if status, body := relayCall(t, http.DefaultClient, http.MethodPost, base+"?create=true", "", nil); status != http.StatusOK {
		t.Fatalf("init: %d %s", status, body)
	}
	if status, body := relayCall(t, http.DefaultClient, http.MethodPost, base+"data/"+pack, "un paquet chiffré", nil); status != http.StatusOK {
		t.Fatalf("upload: %d %s", status, body)
	}
	if status, body := relayCall(t, http.DefaultClient, http.MethodGet, base+"data/"+pack, "", nil); status != http.StatusOK || body != "un paquet chiffré" {
		t.Fatalf("read back: %d %q", status, body)
	}
	if status, body := relayCall(t, http.DefaultClient, http.MethodDelete, base+"data/"+pack, "", nil); status != http.StatusForbidden || !strings.Contains(body, "delete_refused") {
		t.Fatalf("delete through the proxy: %d %s", status, body)
	}
	// Le mandataire contourné : la preuve est bonne, le serveur refuse quand même.
	direct := signedRelayHeader(t, resticSourceID, sign)
	status, body := relayCall(t, live.Client(), http.MethodDelete, live.URL+agentwire.ResticRelayPath(resticTargetID)+"/data/"+pack, "", direct)
	if status != http.StatusForbidden || !strings.Contains(body, "delete_refused") {
		t.Fatalf("delete at the server: %d %s", status, body)
	}
	// Un chemin ré-encodé ne passe pas non plus : le serveur juge le chemin tel qu'il est arrivé.
	encoded := signedRelayHeader(t, resticSourceID, sign)
	if status, _ := relayCall(t, live.Client(), http.MethodGet, live.URL+agentwire.ResticRelayPath(resticTargetID)+"/data%2F..%2Fconfig", "", encoded); status != http.StatusForbidden {
		t.Fatalf("encoded path: %d", status)
	}
	// Une preuve fausse au nom de la cible : rien ne passe.
	forged := signedRelayHeader(t, resticTargetID, func([]byte) []byte { return make([]byte, 64) })
	if status, _ := relayCall(t, live.Client(), http.MethodGet, live.URL+agentwire.ResticRelayPath(resticSourceID)+"/config", "", forged); status != http.StatusUnauthorized {
		t.Fatalf("a forged proof: %d", status)
	}
	// Une source sans sauvegarde vers cette cible : le relais refuse le lien.
	_, strangerKey := bench.enroll(t, "inconnue", dockerVMID)
	stranger := signedRelayHeader(t, dockerVMID, func(payload []byte) []byte { return machine.Sign(strangerKey, payload) })
	if status, body := relayCall(t, live.Client(), http.MethodGet, live.URL+agentwire.ResticRelayPath(resticTargetID)+"/config", "", stranger); status != http.StatusForbidden ||
		!strings.Contains(body, "link_refused") {
		t.Fatalf("a source without a link: %d %s", status, body)
	}

	seen := rest.requests()
	for _, line := range seen {
		if strings.HasPrefix(line, http.MethodDelete) {
			t.Errorf("a deletion reached rest-server: %v", seen)
		}
	}
	want := "POST /" + resticSourceID + "/data/" + pack
	found := false
	for _, line := range seen {
		found = found || line == want
	}
	if !found {
		t.Fatalf("the pack did not land in the repository the server names: %v", seen)
	}
}
