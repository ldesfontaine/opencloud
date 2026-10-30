// Package servertest rassemble ce que les tests de server et de ses
// sous-paquets partagent : l'horloge figée, le défi et le flux d'un agent.
// La base temporaire et l'enrôlement viennent de storetest.
package servertest

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/web"
)

// Now est l'horloge figée des tests : « vu il y a » se calcule depuis elle.
func Now() time.Time {
	return time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
}

// RequireFront saute le test quand le front n'est pas construit : les pages
// que le serveur livre viennent de web/dist, que `make front` remplit. En CI
// (variable CI posée), l'absence est un échec : un saut y masquerait un
// Makefile qui ne construit plus le front.
func RequireFront(t testing.TB) {
	t.Helper()
	dist, err := web.Dist()
	if err == nil {
		for _, page := range []string{"index.html", "statut.html"} {
			if _, err = fs.Stat(dist, page); err != nil {
				break
			}
		}
	}
	if err == nil {
		return
	}
	if os.Getenv("CI") != "" {
		t.Fatalf("front non construit (%v) : la CI doit lancer make front avant les tests", err)
	}
	t.Skip("front non construit : make front")
}

// RemoteID est l'identifiant de la machine distante des tests, fixe pour des
// rendus figés.
const RemoteID = "11111111-2222-4333-8444-555555555555"

// NewNonce tire l'aléa d'une preuve, comme l'agent, en base64.
func NewNonce(t *testing.T) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString(machine.NewNonce())
}

// SignedStreamRequest construit la requête du flux avec la preuve que
// l'agent signe pour cet aléa, à l'heure figée des tests, en protocole 2.
func SignedStreamRequest(t *testing.T, url, machineID string, private ed25519.PrivateKey, nonce string) *http.Request {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(nonce)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := machine.SignedPayload(raw, machineID, Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodGet, url+"/agent/stream", nil)
	request.Header.Set(agentwire.HeaderProtocol, agentwire.ProtocolVersion)
	request.Header.Set(agentwire.HeaderMachine, machineID)
	request.Header.Set(agentwire.HeaderNonce, nonce)
	request.Header.Set(agentwire.HeaderTimestamp, strconv.FormatInt(Now().Unix(), 10))
	request.Header.Set(agentwire.HeaderSignature, base64.StdEncoding.EncodeToString(machine.Sign(private, payload)))
	request.Header.Set(agentwire.HeaderAgentVersion, "v0.0.2")
	// Le banc modèle un agent migré : il dit revenir seul en arrière. Un test
	// d'agent non migré retire l'en-tête.
	request.Header.Set(agentwire.HeaderAgentRollback, "1")
	return request
}

// OpenStream ouvre le flux d'un agent avec une preuve neuve, et rend le
// tampon qui l'a lu : ce que le serveur pousse juste après la session arrive
// souvent dans le même paquet, et un second tampon le perdrait.
func OpenStream(t *testing.T, handler http.Handler, machineID string, private ed25519.PrivateKey) (*http.Response, string, *bufio.Reader, context.CancelFunc) {
	t.Helper()
	return OpenStreamPinned(t, handler, machineID, private, "")
}

// OpenStreamWithoutRollback ouvre le flux d'un agent qui ne dit pas revenir
// seul en arrière : un agent installé avant le retour arrière, ou pas migré.
func OpenStreamWithoutRollback(t *testing.T, handler http.Handler, machineID string, private ed25519.PrivateKey) (*http.Response, context.CancelFunc) {
	t.Helper()
	live := httptest.NewServer(handler)
	t.Cleanup(live.Close)
	ctx, cancel := context.WithCancel(context.Background())
	request := SignedStreamRequest(t, live.URL, machineID, private, NewNonce(t)).WithContext(ctx)
	request.Header.Del(agentwire.HeaderAgentRollback)
	resp, err := live.Client().Do(request)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("stream: %d", resp.StatusCode)
	}
	return resp, cancel
}

// OpenStreamPinned fait de même pour un agent qui dit avoir épinglé cette
// clé du serveur (son empreinte).
func OpenStreamPinned(t *testing.T, handler http.Handler, machineID string, private ed25519.PrivateKey, serverKey string) (*http.Response, string, *bufio.Reader, context.CancelFunc) {
	t.Helper()
	live := httptest.NewServer(handler)
	t.Cleanup(live.Close)
	ctx, cancel := context.WithCancel(context.Background())
	request := SignedStreamRequest(t, live.URL, machineID, private, NewNonce(t)).WithContext(ctx)
	if serverKey != "" {
		request.Header.Set(agentwire.HeaderServerKey, serverKey)
	}
	resp, err := live.Client().Do(request)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("stream: %d", resp.StatusCode)
	}
	reader := bufio.NewReader(resp.Body)
	var session string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if value, ok := strings.CutPrefix(line, "data: "); ok {
			session = strings.TrimSpace(value)
			break
		}
	}
	return resp, session, reader, cancel
}
