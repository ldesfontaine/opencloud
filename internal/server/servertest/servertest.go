// Package servertest rassemble ce que les tests de server et de ses
// sous-paquets partagent : l'horloge figée, le défi et le flux d'un agent.
// La base temporaire et l'enrôlement viennent de storetest.
package servertest

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Now est l'horloge figée des tests : « vu il y a » se calcule depuis elle.
func Now() time.Time {
	return time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
}

// RemoteID est l'identifiant de la machine distante des tests, fixe pour des
// rendus figés.
const RemoteID = "11111111-2222-4333-8444-555555555555"

// RequestChallenge demande un défi comme n'importe quel appelant de
// /agent/challenge et rend son contenu en base64 ; source vide, l'adresse
// par défaut des requêtes de test.
func RequestChallenge(t *testing.T, handler http.Handler, machineID, source string) string {
	t.Helper()
	body, _ := json.Marshal(agentwire.ChallengeRequest{MachineID: machineID})
	request := httptest.NewRequest(http.MethodPost, "/agent/challenge", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if source != "" {
		request.RemoteAddr = source
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("challenge: %d %s", recorder.Code, recorder.Body.String())
	}
	var response agentwire.ChallengeResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Nonce
}

// SignedStreamRequest construit la requête du flux avec la preuve que
// l'agent signe pour ce défi.
func SignedStreamRequest(t *testing.T, url, machineID string, private ed25519.PrivateKey, challenge string) *http.Request {
	t.Helper()
	nonce, err := base64.StdEncoding.DecodeString(challenge)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := machine.SignedPayload(nonce, machineID, Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodGet, url+"/agent/stream", nil)
	request.Header.Set(agentwire.HeaderMachine, machineID)
	request.Header.Set(agentwire.HeaderNonce, challenge)
	request.Header.Set(agentwire.HeaderTimestamp, strconv.FormatInt(Now().Unix(), 10))
	request.Header.Set(agentwire.HeaderSignature, base64.StdEncoding.EncodeToString(machine.Sign(private, payload)))
	request.Header.Set(agentwire.HeaderAgentVersion, "v0.0.2")
	return request
}

// OpenStream ouvre le flux d'un agent avec un défi déjà demandé, et rend le
// tampon qui l'a lu : ce que le serveur pousse juste après la session arrive
// souvent dans le même paquet, et un second tampon le perdrait.
func OpenStream(t *testing.T, handler http.Handler, machineID string, private ed25519.PrivateKey, challenge string) (*http.Response, string, *bufio.Reader, context.CancelFunc) {
	t.Helper()
	live := httptest.NewServer(handler)
	t.Cleanup(live.Close)
	ctx, cancel := context.WithCancel(context.Background())
	request := SignedStreamRequest(t, live.URL, machineID, private, challenge).WithContext(ctx)
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
