package agentapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/sampler"
	"github.com/ldesfontaine/opencloud/internal/server/servertest"
)

// Une lecture plausible, dans les bornes de ce qu'un agent mesure.
func reading(at time.Time, cpu float64) sampler.Reading {
	return sampler.Reading{
		SampledAt: at, CPUPercent: cpu, CPUCores: 4, Load1: 0.9,
		MemUsed: 3_328_599_654, MemTotal: 8_589_934_592, SwapUsed: 0, SwapTotal: 2_147_483_648,
		DiskUsed: 366_145_961_984, DiskTotal: 622_770_257_920, NetRxPerSecond: 1_048_576, NetTxPerSecond: 209_715,
		Disks: []sampler.Disk{
			{MountPoint: "/", Device: "/dev/vda1", Used: 44_023_414_784, Total: 85_899_345_920},
		},
	}
}

// L'agent qui présente l'empreinte de la machine du serveur s'enrôle comme
// la machine openCloud : l'identifiant rendu est LocalID, que l'agent garde.
// Un second, tant que le premier est actif, est refusé sans consommer son
// jeton.
func TestAgentEnroll_FromTheServerMachine_TakesTheStableID(t *testing.T) {
	server := newBench(t)
	// Le banc a déjà l'agent de la machine openCloud : révoqué, le suivant
	// reprend sa ligne.
	if err := server.machines.Revoke(context.Background(), machine.LocalID); err != nil {
		t.Fatal(err)
	}
	server.machines.SetServerHost("f00d", "oc-host")
	enroll := func(name, fingerprint string) *httptest.ResponseRecorder {
		cleartext, _, err := server.machines.CreateToken(context.Background(), name, nil)
		if err != nil {
			t.Fatal(err)
		}
		public, _, _ := ed25519.GenerateKey(nil)
		return server.postJSON("/agent/enroll", agentwire.EnrollRequest{
			MachineID: remoteID, PublicKey: base64.StdEncoding.EncodeToString(public), Token: cleartext, Hostname: "oc-host", HostFingerprint: fingerprint,
		})
	}
	accepted := enroll("oc-host", "f00d")
	var response agentwire.EnrollResponse
	if accepted.Code != http.StatusCreated || json.Unmarshal(accepted.Body.Bytes(), &response) != nil || response.MachineID != machine.LocalID {
		t.Fatalf("status %d: %s", accepted.Code, accepted.Body.String())
	}
	refused := enroll("twin", "f00d")
	if refused.Code != http.StatusConflict || !strings.Contains(refused.Body.String(), agentwire.CodeLocalAgentActive) {
		t.Fatalf("status %d: %s", refused.Code, refused.Body.String())
	}
	if pending, _ := server.machines.PendingTokens(context.Background()); len(pending) != 1 {
		t.Fatalf("the refused token must stay pending: %+v", pending)
	}
}

func TestAgentEnroll_CreatesTheMachineAndMapsRefusals(t *testing.T) {
	server := newBench(t)
	cleartext, _, err := server.machines.CreateToken(context.Background(), "vps-paris-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	public, _, _ := ed25519.GenerateKey(nil)
	request := agentwire.EnrollRequest{MachineID: remoteID, PublicKey: base64.StdEncoding.EncodeToString(public), Token: cleartext, Hostname: "vps", OS: "Debian 12", Arch: "amd64", AgentVersion: "v0.0.1"}

	recorder := server.postJSON("/agent/enroll", request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var response agentwire.EnrollResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.MachineID != remoteID || response.Name != "vps-paris-1" {
		t.Fatalf("response %s", recorder.Body.String())
	}
	// L'agent repart avec son identifiant et la clé qui signera les ordres.
	serverKey, _, _ := server.actions.ServerKey()
	if !strings.HasPrefix(response.AgentID, "agt_") || response.ServerKey != base64.StdEncoding.EncodeToString(serverKey) {
		t.Fatalf("agent %q, server key %q", response.AgentID, response.ServerKey)
	}
	status, err := server.machines.Get(context.Background(), remoteID)
	if err != nil || status.Address != "192.0.2.1" || status.Online {
		t.Fatalf("machine %+v %v", status, err)
	}

	cases := map[string]struct {
		mutate func(*agentwire.EnrollRequest)
		status int
		code   string
	}{
		"consumed": {func(*agentwire.EnrollRequest) {}, http.StatusConflict, "token_consumed"},
		"unknown":  {func(r *agentwire.EnrollRequest) { r.Token = "oc_nope" }, http.StatusNotFound, "token_not_found"},
		"bad key":  {func(r *agentwire.EnrollRequest) { r.PublicKey = "AAAA" }, http.StatusBadRequest, "bad_request"},
		"bad id":   {func(r *agentwire.EnrollRequest) { r.MachineID = "local" }, http.StatusBadRequest, "bad_request"},
	}
	for name, tc := range cases {
		again := request
		tc.mutate(&again)
		got := server.postJSON("/agent/enroll", again)
		if got.Code != tc.status || !strings.Contains(got.Body.String(), tc.code) {
			t.Errorf("%s: %d %s", name, got.Code, got.Body.String())
		}
	}
}

func TestAgentStream_RefusesABadProof(t *testing.T) {
	server := newBench(t)
	server.enroll(t, "vps-paris-1", remoteID)
	request := httptest.NewRequest(http.MethodGet, "/agent/stream", nil)
	request.Header.Set(agentwire.HeaderProtocol, agentwire.ProtocolVersion)
	request.Header.Set(agentwire.HeaderMachine, remoteID)
	request.Header.Set(agentwire.HeaderNonce, base64.StdEncoding.EncodeToString(make([]byte, machine.NonceSize)))
	request.Header.Set(agentwire.HeaderTimestamp, strconv.FormatInt(testNow.Unix(), 10))
	request.Header.Set(agentwire.HeaderSignature, base64.StdEncoding.EncodeToString(make([]byte, 64)))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestAgentRemoval_ClosesTheStream(t *testing.T) {
	server := newBench(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	resp, _, _, cancel := server.openStream(t, enrolled.ID, private)
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
	server := newBench(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	resp, session, _, cancel := server.openStream(t, enrolled.ID, private)
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
	readings := []sampler.Reading{reading(testNow.Add(-10*time.Second), 42)}
	if got := signal(agentwire.SignalRequest{Readings: readings}); got.Code != http.StatusNoContent {
		t.Fatalf("signal with readings: %d %s", got.Code, got.Body.String())
	}
	current, err := server.resources.Current(context.Background(), remoteID)
	if err != nil || !current.Available || current.Sample.CPUPercent != 42 {
		t.Fatalf("recorded %+v %v", current, err)
	}
	bad := []sampler.Reading{reading(testNow, 250)}
	if got := signal(agentwire.SignalRequest{Readings: bad}); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "bad_readings") {
		t.Fatalf("bad readings: %d %s", got.Code, got.Body.String())
	}
	if got := signal(nil); got.Code != http.StatusNoContent {
		t.Fatalf("bare signal: %d", got.Code)
	}
	if got := signal("not an object"); got.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", got.Code)
	}
}

// L'ouverture du flux est ouverte à tous : son débit est borné par adresse,
// dans le seau de l'enrôlement, pour qu'on n'essaie pas des preuves à la
// chaîne. Une autre adresse passe encore.
func TestAgentStream_IsRateLimitedBySource(t *testing.T) {
	server := newBench(t)
	_, private := server.enroll(t, "vps-paris-1", remoteID)
	unknown := "11111111-1111-4111-8111-111111111111"
	for i := range perSourceBurst {
		if code, body := streamRefusal(t, server, unknown, private, servertest.NewNonce(t)); code != http.StatusNotFound {
			t.Fatalf("request %d: %d %s", i+1, code, body)
		}
	}
	request := servertest.SignedStreamRequest(t, "http://example.test", unknown, private, servertest.NewNonce(t))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") == "" {
		t.Fatalf("over the burst: %d %v", recorder.Code, recorder.Header())
	}
	request = servertest.SignedStreamRequest(t, "http://example.test", unknown, private, servertest.NewNonce(t))
	request.RemoteAddr = "203.0.113.50:4000"
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("another source: %d", recorder.Code)
	}
}

// streamRefusal rejoue la requête du flux sans l'ouvrir : un refus rentre
// tout de suite, et le test lit son code.
func streamRefusal(t *testing.T, server *bench, machineID string, private ed25519.PrivateKey, nonce string) (int, string) {
	t.Helper()
	request := servertest.SignedStreamRequest(t, "http://example.test", machineID, private, nonce)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String()
}

// Protocole 2 : l'agent tire son aléa, le serveur n'émet plus de défi. Une
// preuve déjà acceptée ne sert pas deux fois (replayed, passager : l'agent
// en signe une autre) ; une signature fausse est bad_proof, que l'agent
// prend pour un refus ; un aléa de la mauvaise taille ne se lit pas.
func TestAgentStream_RefusesAReplayAWrongKeyAndAMalformedNonce(t *testing.T) {
	server := newBench(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	nonce := servertest.NewNonce(t)
	live := httptest.NewServer(server)
	defer live.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opened, err := live.Client().Do(servertest.SignedStreamRequest(t, live.URL, enrolled.ID, private, nonce).WithContext(ctx))
	if err != nil || opened.StatusCode != http.StatusOK {
		t.Fatalf("first proof: %v %v", opened, err)
	}
	defer opened.Body.Close()

	if code, body := streamRefusal(t, server, enrolled.ID, private, nonce); code != http.StatusUnauthorized || !strings.Contains(body, agentwire.CodeReplayed) {
		t.Errorf("replay: %d %s", code, body)
	}
	_, wrongKey, _ := ed25519.GenerateKey(nil)
	if code, body := streamRefusal(t, server, enrolled.ID, wrongKey, servertest.NewNonce(t)); code != http.StatusUnauthorized || !strings.Contains(body, "bad_proof") {
		t.Errorf("wrong signature: %d %s", code, body)
	}
	short := servertest.SignedStreamRequest(t, "http://example.test", enrolled.ID, private, servertest.NewNonce(t))
	short.Header.Set(agentwire.HeaderNonce, base64.StdEncoding.EncodeToString(make([]byte, machine.NonceSize-1)))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, short)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "bad_proof") {
		t.Errorf("short nonce: %d %s", recorder.Code, recorder.Body.String())
	}
	server.machines.SetClock(func() time.Time { return testNow.Add(6 * time.Minute) })
	if code, body := streamRefusal(t, server, enrolled.ID, private, servertest.NewNonce(t)); code != http.StatusUnauthorized || !strings.Contains(body, "clock_skew") {
		t.Errorf("late proof: %d %s", code, body)
	}
}

// Un agent du protocole 1 (sans l'en-tête de version) lit pourquoi il n'est
// pas servi, sur chaque route, défi compris : 426 protocol_unsupported. Un
// chemin inconnu, en protocole 2, répond 404.
func TestAgentRoutes_RefuseAnAgentOfProtocolOne(t *testing.T) {
	server := newBench(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	stream := servertest.SignedStreamRequest(t, "http://example.test", enrolled.ID, private, servertest.NewNonce(t))
	stream.Header.Del(agentwire.HeaderProtocol)
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/agent/enroll", strings.NewReader(`{}`)),
		httptest.NewRequest(http.MethodPost, "/agent/challenge", strings.NewReader(`{"machine_id":"`+remoteID+`"}`)),
		stream,
	} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUpgradeRequired || !strings.Contains(recorder.Body.String(), agentwire.CodeProtocolUnsupported) {
			t.Errorf("%s %s: %d %s", request.Method, request.URL.Path, recorder.Code, recorder.Body.String())
		}
	}
	if got := server.postJSON("/agent/challenge", map[string]string{"machine_id": remoteID}); got.Code != http.StatusNotFound {
		t.Errorf("an unknown path in protocol 2: %d %s", got.Code, got.Body.String())
	}
}

// Un contenu long part en plusieurs lignes « data: » bornées, que l'agent
// recolle sans séparateur ; au-delà d'un événement, rien ne part.
func TestWriteAgentEvent_SplitsLongDataIntoBoundedLines(t *testing.T) {
	data := []byte(strings.Repeat("a", 2*agentwire.EventDataLine+5))
	recorder := httptest.NewRecorder()
	if err := writeAgentEvent(recorder, machine.StreamEvent{Name: "probes", Data: data}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(recorder.Body.String(), "\n\n"), "\n")
	if len(lines) != 4 || lines[0] != "event: probes" {
		t.Fatalf("lines %d, first %q", len(lines), lines[0])
	}
	joined := ""
	for _, line := range lines[1:] {
		value := strings.TrimPrefix(line, "data: ")
		if len(value) > agentwire.EventDataLine {
			t.Fatalf("a line of %d bytes", len(value))
		}
		joined += value
	}
	if joined != string(data) {
		t.Fatal("the lines do not join back into the data")
	}
	if err := writeAgentEvent(httptest.NewRecorder(), machine.StreamEvent{Name: "probes", Data: make([]byte, agentwire.MaxEventBytes+1)}); err == nil {
		t.Fatal("an event beyond the bound was written")
	}
}

// L'agent redit ses droits à l'ouverture du flux : le serveur les stocke et
// montre l'écart avec ce que son jeton demandait. Révoqué, il prouve encore
// sa clé mais lit « revoked » et s'arrête.
func TestAgentStream_StoresTheReportedRightsThenRefusesTheRevokedAgent(t *testing.T) {
	server := newBench(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	live := httptest.NewServer(server)
	defer live.Close()

	request := servertest.SignedStreamRequest(t, live.URL, enrolled.ID, private, servertest.NewNonce(t))
	request.Header.Set(agentwire.HeaderAgentRights, "observe,agent.update")
	request.Header.Set(agentwire.HeaderAgentRoot, "/srv/workspace")
	_, fingerprint, _ := server.actions.ServerKey()
	request.Header.Set(agentwire.HeaderServerKey, fingerprint)
	ctx, cancel := context.WithCancel(context.Background())
	resp, err := live.Client().Do(request.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream: %d", resp.StatusCode)
	}
	status, err := server.machines.Get(context.Background(), enrolled.ID)
	if err != nil {
		t.Fatal(err)
	}
	if machine.FormatCapabilities(status.Agent.Capabilities) != "observe,agent.update" || !status.Agent.CapabilitiesDiffer() {
		t.Fatalf("reported rights %+v", status.Agent)
	}
	if status.Agent.ServerKeyFingerprint != fingerprint {
		t.Fatalf("pinned key %q, want %q", status.Agent.ServerKeyFingerprint, fingerprint)
	}
	if status.Agent.RootDir != "/srv/workspace" {
		t.Fatalf("reported root %q", status.Agent.RootDir)
	}

	if err := server.machines.Revoke(context.Background(), enrolled.ID); err != nil {
		t.Fatal(err)
	}
	rest, _ := readAll(resp, 2*time.Second)
	cancel()
	resp.Body.Close()
	if !strings.Contains(rest, "event: closed") {
		t.Fatalf("the revoked agent's stream must close: %q", rest)
	}
	again := servertest.SignedStreamRequest(t, live.URL, enrolled.ID, private, servertest.NewNonce(t))
	refused, err := live.Client().Do(again)
	if err != nil {
		t.Fatal(err)
	}
	defer refused.Body.Close()
	body, _ := io.ReadAll(refused.Body)
	if refused.StatusCode != http.StatusUnauthorized || !strings.Contains(string(body), `"`+agentwire.CodeRevoked+`"`) {
		t.Fatalf("revoked agent: %d %s", refused.StatusCode, body)
	}
}

// L'agent redit le compte sous lequel il tourne et les droits que ce compte ne
// peut pas exercer. Le texte vient d'un pair : un en-tête illisible ne refuse
// pas le flux et ne laisse rien derrière lui.
func TestAgentStream_StoresTheAccountAndUnavailableRightsAndIgnoresForgedOnes(t *testing.T) {
	server := newBench(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	live := httptest.NewServer(server)
	defer live.Close()

	open := func(account, unavailable string) machine.Status {
		t.Helper()
		request := servertest.SignedStreamRequest(t, live.URL, enrolled.ID, private, servertest.NewNonce(t))
		request.Header.Set(agentwire.HeaderAgentRights, "observe,agent.update,docker.deploy")
		if account != "" {
			request.Header.Set(agentwire.HeaderAgentAccount, account)
		}
		if unavailable != "" {
			request.Header.Set(agentwire.HeaderAgentUnavailable, unavailable)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		resp, err := live.Client().Do(request.WithContext(ctx))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("stream with %q / %q: %d", account, unavailable, resp.StatusCode)
		}
		status, err := server.machines.Get(context.Background(), enrolled.ID)
		if err != nil {
			t.Fatal(err)
		}
		return status
	}

	status := open("998:opencloud-agent", "docker.deploy=docker_denied")
	if status.Agent.RunAs == nil || status.Agent.RunAs.UID != 998 || status.Agent.RunAs.Name != "opencloud-agent" {
		t.Fatalf("account %+v", status.Agent.RunAs)
	}
	if status.Agent.UnavailableReason(machine.CapabilityDockerDeploy) != agentwire.ReasonDockerDenied {
		t.Fatalf("reasons %+v", status.Agent.Unavailable)
	}

	// Falsifiés : le compte reste celui d'avant, les raisons s'effacent, le
	// flux s'ouvre.
	forged := []struct{ account, unavailable string }{
		{"-1:root", "docker.deploy=../../etc/passwd"},
		{"0:Root", "shell.exec=docker_denied"},
		{"4294967296:x", "docker.deploy=docker_denied,docker.deploy=docker_denied"},
		{"007:x", "docker.deploy"},
		{"998:" + strings.Repeat("a", 33), strings.Repeat("a", 3000)},
	}
	for _, c := range forged {
		status = open(c.account, c.unavailable)
		if status.Agent.RunAs == nil || status.Agent.RunAs.UID != 998 || status.Agent.RunAs.Name != "opencloud-agent" {
			t.Errorf("forged account %q replaced the stored one: %+v", c.account, status.Agent.RunAs)
		}
		if len(status.Agent.Unavailable) != 0 {
			t.Errorf("forged reasons %q were stored: %+v", c.unavailable, status.Agent.Unavailable)
		}
	}
}
