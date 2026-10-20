// Package servertest rassemble ce que les tests de server et de ses
// sous-paquets partagent : la base temporaire, l'horloge figée, une machine
// enrôlée et le flux de son agent.
package servertest

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/seal"
	"github.com/ldesfontaine/opencloud/internal/store"
)

// Now est l'horloge figée des tests : « vu il y a » se calcule depuis elle.
func Now() time.Time {
	return time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
}

// RemoteID est l'identifiant de la machine distante des tests, fixe pour des
// rendus figés.
const RemoteID = "11111111-2222-4333-8444-555555555555"

// OpenDB ouvre une vraie base SQLite dans un dossier temporaire, avec son
// trousseau ; elle est fermée à la fin du test. Le dossier est rendu pour
// les composants qui y écrivent aussi.
func OpenDB(t *testing.T) (*store.DB, *os.Root) {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	db, err := store.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	db.SetSealer(seal.Generate())
	t.Cleanup(func() { _ = db.Close() })
	return db, root
}

// PeerAddress est l'adresse du pair, sans mandataire : ce que les bancs des
// sous-paquets donnent pour ClientAddress. La règle des mandataires de
// confiance vit à la racine et s'y teste.
func PeerAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Enroll enrôle une machine distante avec un identifiant fixe, comme l'agent
// le ferait, et rend sa clé privée.
func Enroll(t *testing.T, machines *machine.Service, name, id string) (machine.Machine, ed25519.PrivateKey) {
	t.Helper()
	cleartext, _, err := machines.CreateToken(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	enrolled, err := machines.Enroll(context.Background(), machine.Enrollment{
		MachineID: id, PublicKey: public, Token: cleartext,
		Hostname: name, Address: "51.15.20.114", OS: "Debian 12", Arch: "amd64", AgentVersion: "v0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return enrolled, private
}

// RequestChallenge demande un défi comme n'importe quel appelant de
// /agent/challenge et rend son contenu en base64 ; source vide, l'adresse
// par défaut des requêtes de test.
func RequestChallenge(t *testing.T, handler http.Handler, machineID, source string) string {
	t.Helper()
	body, _ := json.Marshal(machine.ChallengeRequest{MachineID: machineID})
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
	var response machine.ChallengeResponse
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
	request.Header.Set(machine.HeaderMachine, machineID)
	request.Header.Set(machine.HeaderNonce, challenge)
	request.Header.Set(machine.HeaderTimestamp, strconv.FormatInt(Now().Unix(), 10))
	request.Header.Set(machine.HeaderSignature, base64.StdEncoding.EncodeToString(machine.Sign(private, payload)))
	request.Header.Set(machine.HeaderAgentVersion, "v0.0.2")
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
