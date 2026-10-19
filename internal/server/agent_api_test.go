package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/sampler"
)

func parsePrefixes(t *testing.T, cidrs ...string) []netip.Prefix {
	t.Helper()
	var prefixes []netip.Prefix
	for _, cidr := range cidrs {
		prefixes = append(prefixes, netip.MustParsePrefix(cidr))
	}
	return prefixes
}

func postJSON(server *Server, path string, payload any) *httptest.ResponseRecorder {
	body, _ := json.Marshal(payload)
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

// Un agent sur la machine openCloud présente la même empreinte qu'elle :
// refusé, et le jeton sert encore à une autre machine.
func TestAgentEnroll_OnTheOpenCloudMachine_IsRefusedAndKeepsTheToken(t *testing.T) {
	server := newTestServer(t)
	server.machines.SetLocalFingerprint("f00d")
	cleartext, _, err := server.machines.CreateToken(context.Background(), "vps-paris-1")
	if err != nil {
		t.Fatal(err)
	}
	public, _, _ := ed25519.GenerateKey(nil)
	request := machine.EnrollRequest{MachineID: remoteID, PublicKey: base64.StdEncoding.EncodeToString(public), Token: cleartext, Hostname: "opencloud", HostFingerprint: "f00d"}

	refused := postJSON(server.Server, "/agent/enroll", request)
	if refused.Code != http.StatusConflict || !strings.Contains(refused.Body.String(), "machine_is_opencloud") {
		t.Fatalf("status %d: %s", refused.Code, refused.Body.String())
	}
	request.HostFingerprint = "beef"
	if accepted := postJSON(server.Server, "/agent/enroll", request); accepted.Code != http.StatusCreated {
		t.Fatalf("the token stays valid: %d %s", accepted.Code, accepted.Body.String())
	}
}

func TestAgentEnroll_CreatesTheMachineAndMapsRefusals(t *testing.T) {
	server := newTestServer(t)
	cleartext, _, err := server.machines.CreateToken(context.Background(), "vps-paris-1")
	if err != nil {
		t.Fatal(err)
	}
	public, _, _ := ed25519.GenerateKey(nil)
	request := machine.EnrollRequest{MachineID: remoteID, PublicKey: base64.StdEncoding.EncodeToString(public), Token: cleartext, Hostname: "vps", OS: "Debian 12", Arch: "amd64", AgentVersion: "v0.0.1"}

	recorder := postJSON(server.Server, "/agent/enroll", request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var response machine.EnrollResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.MachineID != remoteID || response.Name != "vps-paris-1" {
		t.Fatalf("response %s", recorder.Body.String())
	}
	status, err := server.machines.Get(context.Background(), remoteID)
	if err != nil || status.Address != "192.0.2.1" || status.Online {
		t.Fatalf("machine %+v %v", status, err)
	}

	cases := map[string]struct {
		mutate func(*machine.EnrollRequest)
		status int
		code   string
	}{
		"consumed": {func(*machine.EnrollRequest) {}, http.StatusConflict, "token_consumed"},
		"unknown":  {func(r *machine.EnrollRequest) { r.Token = "oc_nope" }, http.StatusNotFound, "token_not_found"},
		"bad key":  {func(r *machine.EnrollRequest) { r.PublicKey = "AAAA" }, http.StatusBadRequest, "bad_request"},
		"bad id":   {func(r *machine.EnrollRequest) { r.MachineID = "local" }, http.StatusBadRequest, "bad_request"},
	}
	for name, tc := range cases {
		again := request
		tc.mutate(&again)
		got := postJSON(server.Server, "/agent/enroll", again)
		if got.Code != tc.status || !strings.Contains(got.Body.String(), tc.code) {
			t.Errorf("%s: %d %s", name, got.Code, got.Body.String())
		}
	}
}

// openStream ouvre le flux d'un agent et rend le tampon qui l'a lu : ce
// que le serveur pousse juste après la session arrive souvent dans le même
// paquet, et un second tampon le perdrait.
func openStream(t *testing.T, server *testServer, machineID string, private ed25519.PrivateKey) (*http.Response, string, *bufio.Reader, context.CancelFunc) {
	t.Helper()
	return openStreamOn(t, server, machineID, private, requestChallenge(t, server, machineID, ""))
}

// requestChallenge demande un défi comme n'importe quel appelant de
// /agent/challenge et rend son contenu en base64 ; source vide, l'adresse
// par défaut des requêtes de test.
func requestChallenge(t *testing.T, server *testServer, machineID, source string) string {
	t.Helper()
	body, _ := json.Marshal(machine.ChallengeRequest{MachineID: machineID})
	request := httptest.NewRequest(http.MethodPost, "/agent/challenge", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if source != "" {
		request.RemoteAddr = source
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("challenge: %d %s", recorder.Code, recorder.Body.String())
	}
	var response machine.ChallengeResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Nonce
}

// signedStreamRequest construit la requête du flux avec la preuve que
// l'agent signe pour ce défi.
func signedStreamRequest(t *testing.T, url, machineID string, private ed25519.PrivateKey, challenge string) *http.Request {
	t.Helper()
	nonce, err := base64.StdEncoding.DecodeString(challenge)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := machine.SignedPayload(nonce, machineID, testNow.Unix())
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodGet, url+"/agent/stream", nil)
	request.Header.Set(machine.HeaderMachine, machineID)
	request.Header.Set(machine.HeaderNonce, challenge)
	request.Header.Set(machine.HeaderTimestamp, strconv.FormatInt(testNow.Unix(), 10))
	request.Header.Set(machine.HeaderSignature, base64.StdEncoding.EncodeToString(machine.Sign(private, payload)))
	request.Header.Set(machine.HeaderAgentVersion, "v0.0.2")
	return request
}

// openStreamOn ouvre le flux avec un défi déjà demandé.
func openStreamOn(t *testing.T, server *testServer, machineID string, private ed25519.PrivateKey, challenge string) (*http.Response, string, *bufio.Reader, context.CancelFunc) {
	t.Helper()
	live := httptest.NewServer(server.Server)
	t.Cleanup(live.Close)
	ctx, cancel := context.WithCancel(context.Background())
	request := signedStreamRequest(t, live.URL, machineID, private, challenge).WithContext(ctx)
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

func TestAgentStream_OpensASessionThatSignalsAndCloses(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	resp, session, _, cancel := openStream(t, server, enrolled.ID, private)
	defer resp.Body.Close()
	defer cancel()

	status, _ := server.machines.Get(context.Background(), remoteID)
	if !status.Online || status.AgentVersion != "v0.0.2" {
		t.Fatalf("after stream: %+v", status)
	}
	signal := httptest.NewRequest(http.MethodPost, "/agent/signal", nil)
	signal.Header.Set("Authorization", "Bearer "+session)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, signal)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("signal: %d %s", recorder.Code, recorder.Body.String())
	}
	bogus := httptest.NewRequest(http.MethodPost, "/agent/signal", nil)
	bogus.Header.Set("Authorization", "Bearer nope")
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, bogus)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("bogus signal: %d", recorder.Code)
	}

	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if status, _ := server.machines.Get(context.Background(), remoteID); !status.Online {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("machine still online after the stream was cut")
}

func TestAgentStream_RefusesABadProof(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	request := httptest.NewRequest(http.MethodGet, "/agent/stream", nil)
	request.Header.Set(machine.HeaderMachine, remoteID)
	request.Header.Set(machine.HeaderNonce, base64.StdEncoding.EncodeToString(make([]byte, machine.NonceSize)))
	request.Header.Set(machine.HeaderTimestamp, strconv.FormatInt(testNow.Unix(), 10))
	request.Header.Set(machine.HeaderSignature, base64.StdEncoding.EncodeToString(make([]byte, 64)))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status %d %s", recorder.Code, recorder.Body.String())
	}
	if got := postJSON(server.Server, "/agent/challenge", machine.ChallengeRequest{MachineID: "local"}); got.Code != http.StatusNotFound {
		t.Fatalf("challenge for the local machine: %d", got.Code)
	}
}

func TestAgentRemoval_ClosesTheStream(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	resp, _, _, cancel := openStream(t, server, enrolled.ID, private)
	defer cancel()
	defer resp.Body.Close()
	if err := server.machines.Remove(context.Background(), enrolled.ID); err != nil {
		t.Fatal(err)
	}
	rest, _ := readAll(resp, 2*time.Second)
	if !strings.Contains(rest, "event: closed") {
		t.Fatalf("no closed event: %q", rest)
	}
}

func readAll(resp *http.Response, timeout time.Duration) (string, error) {
	var buffer bytes.Buffer
	done := make(chan error, 1)
	go func() {
		_, err := buffer.ReadFrom(resp.Body)
		done <- err
	}()
	select {
	case err := <-done:
		return buffer.String(), err
	case <-time.After(timeout):
		return buffer.String(), context.DeadlineExceeded
	}
}

// Le signal porte les lectures : elles s'écrivent au nom de la machine de
// la session, un lot hors de mesure est refusé, un signal nu reste un signal.
func TestAgentSignal_CarriesReadingsAndRefusesBadOnes(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	resp, session, _, cancel := openStream(t, server, enrolled.ID, private)
	defer resp.Body.Close()
	defer cancel()

	signal := func(payload any) *httptest.ResponseRecorder {
		var body bytes.Buffer
		if payload != nil {
			_ = json.NewEncoder(&body).Encode(payload)
		}
		request := httptest.NewRequest(http.MethodPost, "/agent/signal", &body)
		request.Header.Set("Authorization", "Bearer "+session)
		if payload != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		return recorder
	}
	readings := []sampler.Reading{testReading(testNow.Add(-10*time.Second), 42)}
	if got := signal(machine.SignalRequest{Readings: readings}); got.Code != http.StatusNoContent {
		t.Fatalf("signal with readings: %d %s", got.Code, got.Body.String())
	}
	current, err := server.resources.Current(context.Background(), remoteID)
	if err != nil || !current.Available || current.Sample.CPUPercent != 42 {
		t.Fatalf("recorded %+v %v", current, err)
	}
	bad := []sampler.Reading{testReading(testNow, 250)}
	if got := signal(machine.SignalRequest{Readings: bad}); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "bad_readings") {
		t.Fatalf("bad readings: %d %s", got.Code, got.Body.String())
	}
	if got := signal(nil); got.Code != http.StatusNoContent {
		t.Fatalf("bare signal: %d", got.Code)
	}
	if got := signal("not an object"); got.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", got.Code)
	}
}

// /agent/challenge est ouvert à tous : son débit est borné par adresse,
// pour qu'on ne devine pas les identifiants de machine à la chaîne ni ne
// remplisse la table des défis. Une autre adresse passe encore.
func TestAgentChallenge_IsRateLimitedBySource(t *testing.T) {
	server := newTestServer(t)
	unknown := machine.ChallengeRequest{MachineID: "11111111-1111-4111-8111-111111111111"}
	for i := range challengePerSourceBurst {
		if got := postJSON(server.Server, "/agent/challenge", unknown); got.Code != http.StatusNotFound {
			t.Fatalf("request %d: %d", i+1, got.Code)
		}
	}
	got := postJSON(server.Server, "/agent/challenge", unknown)
	if got.Code != http.StatusTooManyRequests || got.Header().Get("Retry-After") == "" {
		t.Fatalf("over the burst: %d %v", got.Code, got.Header())
	}
	server.trustedProxies = parsePrefixes(t, "192.0.2.0/24")
	body, _ := json.Marshal(unknown)
	request := httptest.NewRequest(http.MethodPost, "/agent/challenge", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Forwarded-For", "203.0.113.50")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("another source: %d", recorder.Code)
	}
}

// streamRefusal rejoue la requête du flux sans l'ouvrir : un refus rentre
// tout de suite, et le test lit son code.
func streamRefusal(t *testing.T, server *testServer, machineID string, private ed25519.PrivateKey, challenge string) (int, string) {
	t.Helper()
	request := signedStreamRequest(t, "http://example.test", machineID, private, challenge)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String()
}

// Le scénario de l'audit : des défis anonymes pour la même machine, entre
// le défi de l'agent et son flux, ne lui prennent rien. Chacun vient d'une
// source différente, donc sans toucher au limiteur par source.
func TestAgentStream_AnonymousChallengesDoNotDisplaceTheAgents(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	agents := requestChallenge(t, server, enrolled.ID, "")
	for i := range 8 {
		requestChallenge(t, server, enrolled.ID, "203.0.113."+strconv.Itoa(10+i)+":4000")
	}
	resp, session, _, cancel := openStreamOn(t, server, enrolled.ID, private, agents)
	defer resp.Body.Close()
	defer cancel()
	if session == "" {
		t.Fatal("no session after the anonymous challenges")
	}
}

// Un défi qui ne sert plus se redemande (challenge_expired, passager) ; seule
// une signature fausse est bad_proof, que l'agent prend pour un refus.
func TestAgentStream_ADefectiveChallengeIsExpiredNotBadProof(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	server.enroll(t, "vps-lyon-2", "99999999-2222-4333-8444-555555555555")

	expectExpired := func(name string, machineID string, key ed25519.PrivateKey, challenge string) {
		t.Helper()
		code, body := streamRefusal(t, server, machineID, key, challenge)
		if code != http.StatusUnauthorized || !strings.Contains(body, "challenge_expired") {
			t.Errorf("%s: %d %s", name, code, body)
		}
	}

	// Altéré d'un octet.
	good := requestChallenge(t, server, enrolled.ID, "")
	raw, _ := base64.StdEncoding.DecodeString(good)
	raw[0] ^= 0x01
	expectExpired("altered", enrolled.ID, private, base64.StdEncoding.EncodeToString(raw))

	// Émis pour une autre machine.
	other := requestChallenge(t, server, "99999999-2222-4333-8444-555555555555", "")
	expectExpired("another machine's", enrolled.ID, private, other)

	// Expiré.
	late := requestChallenge(t, server, enrolled.ID, "")
	server.machines.SetClock(func() time.Time { return testNow.Add(3 * time.Minute) })
	expectExpired("expired", enrolled.ID, private, late)
	server.machines.SetClock(func() time.Time { return testNow })

	// Une signature fausse reste bad_proof, sur un défi valable.
	_, wrongKey, _ := ed25519.GenerateKey(nil)
	valid := requestChallenge(t, server, enrolled.ID, "")
	if code, body := streamRefusal(t, server, enrolled.ID, wrongKey, valid); code != http.StatusUnauthorized || !strings.Contains(body, "bad_proof") {
		t.Errorf("wrong signature: %d %s", code, body)
	}
}

// Une preuve déjà acceptée ne sert pas deux fois.
func TestAgentStream_ARepliedProofIsRefused(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	challenge := requestChallenge(t, server, enrolled.ID, "")
	resp, _, _, cancel := openStreamOn(t, server, enrolled.ID, private, challenge)
	defer resp.Body.Close()
	defer cancel()

	code, body := streamRefusal(t, server, enrolled.ID, private, challenge)
	if code != http.StatusUnauthorized || !strings.Contains(body, "challenge_expired") {
		t.Fatalf("replay: %d %s", code, body)
	}
}

// Le serveur redémarre entre le défi et le flux : l'agent lit un défi à
// redemander, pas un refus d'identité, puis se reconnecte avec un autre.
func TestAgentStream_ARestartBetweenChallengeAndStreamIsRecoverable(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	challenge := requestChallenge(t, server, enrolled.ID, "")

	restarted := machine.New(server.db, machine.NewSessions(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	restarted.SetClock(func() time.Time { return testNow })
	server.Server.machines = restarted
	code, body := streamRefusal(t, server, enrolled.ID, private, challenge)
	if code != http.StatusUnauthorized || !strings.Contains(body, "challenge_expired") {
		t.Fatalf("challenge from before the restart: %d %s", code, body)
	}
	resp, _, _, cancel := openStream(t, server, enrolled.ID, private)
	defer resp.Body.Close()
	defer cancel()
}
