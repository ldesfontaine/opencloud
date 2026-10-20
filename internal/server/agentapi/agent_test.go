package agentapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

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

// Un agent sur la machine openCloud présente la même empreinte qu'elle :
// refusé, et le jeton sert encore à une autre machine.
func TestAgentEnroll_OnTheOpenCloudMachine_IsRefusedAndKeepsTheToken(t *testing.T) {
	server := newBench(t)
	server.machines.SetLocalFingerprint("f00d")
	cleartext, _, err := server.machines.CreateToken(context.Background(), "vps-paris-1")
	if err != nil {
		t.Fatal(err)
	}
	public, _, _ := ed25519.GenerateKey(nil)
	request := machine.EnrollRequest{MachineID: remoteID, PublicKey: base64.StdEncoding.EncodeToString(public), Token: cleartext, Hostname: "opencloud", HostFingerprint: "f00d"}

	refused := server.postJSON("/agent/enroll", request)
	if refused.Code != http.StatusConflict || !strings.Contains(refused.Body.String(), "machine_is_opencloud") {
		t.Fatalf("status %d: %s", refused.Code, refused.Body.String())
	}
	request.HostFingerprint = "beef"
	if accepted := server.postJSON("/agent/enroll", request); accepted.Code != http.StatusCreated {
		t.Fatalf("the token stays valid: %d %s", accepted.Code, accepted.Body.String())
	}
}

func TestAgentEnroll_CreatesTheMachineAndMapsRefusals(t *testing.T) {
	server := newBench(t)
	cleartext, _, err := server.machines.CreateToken(context.Background(), "vps-paris-1")
	if err != nil {
		t.Fatal(err)
	}
	public, _, _ := ed25519.GenerateKey(nil)
	request := machine.EnrollRequest{MachineID: remoteID, PublicKey: base64.StdEncoding.EncodeToString(public), Token: cleartext, Hostname: "vps", OS: "Debian 12", Arch: "amd64", AgentVersion: "v0.0.1"}

	recorder := server.postJSON("/agent/enroll", request)
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
	request.Header.Set(machine.HeaderMachine, remoteID)
	request.Header.Set(machine.HeaderNonce, base64.StdEncoding.EncodeToString(make([]byte, machine.NonceSize)))
	request.Header.Set(machine.HeaderTimestamp, strconv.FormatInt(testNow.Unix(), 10))
	request.Header.Set(machine.HeaderSignature, base64.StdEncoding.EncodeToString(make([]byte, 64)))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status %d %s", recorder.Code, recorder.Body.String())
	}
	if got := server.postJSON("/agent/challenge", machine.ChallengeRequest{MachineID: "local"}); got.Code != http.StatusNotFound {
		t.Fatalf("challenge for the local machine: %d", got.Code)
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
	if got := signal(machine.SignalRequest{Readings: readings}); got.Code != http.StatusNoContent {
		t.Fatalf("signal with readings: %d %s", got.Code, got.Body.String())
	}
	current, err := server.resources.Current(context.Background(), remoteID)
	if err != nil || !current.Available || current.Sample.CPUPercent != 42 {
		t.Fatalf("recorded %+v %v", current, err)
	}
	bad := []sampler.Reading{reading(testNow, 250)}
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
	server := newBench(t)
	unknown := machine.ChallengeRequest{MachineID: "11111111-1111-4111-8111-111111111111"}
	for i := range challengePerSourceBurst {
		if got := server.postJSON("/agent/challenge", unknown); got.Code != http.StatusNotFound {
			t.Fatalf("request %d: %d", i+1, got.Code)
		}
	}
	got := server.postJSON("/agent/challenge", unknown)
	if got.Code != http.StatusTooManyRequests || got.Header().Get("Retry-After") == "" {
		t.Fatalf("over the burst: %d %v", got.Code, got.Header())
	}
	body, _ := json.Marshal(unknown)
	request := httptest.NewRequest(http.MethodPost, "/agent/challenge", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = "203.0.113.50:4000"
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("another source: %d", recorder.Code)
	}
}

// streamRefusal rejoue la requête du flux sans l'ouvrir : un refus rentre
// tout de suite, et le test lit son code.
func streamRefusal(t *testing.T, server *bench, machineID string, private ed25519.PrivateKey, challenge string) (int, string) {
	t.Helper()
	request := servertest.SignedStreamRequest(t, "http://example.test", machineID, private, challenge)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String()
}

// Le scénario de l'audit : des défis anonymes pour la même machine, entre
// le défi de l'agent et son flux, ne lui prennent rien. Chacun vient d'une
// source différente, donc sans toucher au limiteur par source.
func TestAgentStream_AnonymousChallengesDoNotDisplaceTheAgents(t *testing.T) {
	server := newBench(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	agents := server.requestChallenge(t, enrolled.ID, "")
	for i := range 8 {
		server.requestChallenge(t, enrolled.ID, "203.0.113."+strconv.Itoa(10+i)+":4000")
	}
	resp, session, _, cancel := servertest.OpenStream(t, server, enrolled.ID, private, agents)
	defer resp.Body.Close()
	defer cancel()
	if session == "" {
		t.Fatal("no session after the anonymous challenges")
	}
}

// Un défi qui ne sert plus se redemande (challenge_expired, passager) ; seule
// une signature fausse est bad_proof, que l'agent prend pour un refus.
func TestAgentStream_ADefectiveChallengeIsExpiredNotBadProof(t *testing.T) {
	server := newBench(t)
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
	good := server.requestChallenge(t, enrolled.ID, "")
	raw, _ := base64.StdEncoding.DecodeString(good)
	raw[0] ^= 0x01
	expectExpired("altered", enrolled.ID, private, base64.StdEncoding.EncodeToString(raw))

	// Émis pour une autre machine.
	other := server.requestChallenge(t, "99999999-2222-4333-8444-555555555555", "")
	expectExpired("another machine's", enrolled.ID, private, other)

	// Expiré.
	late := server.requestChallenge(t, enrolled.ID, "")
	server.machines.SetClock(func() time.Time { return testNow.Add(3 * time.Minute) })
	expectExpired("expired", enrolled.ID, private, late)
	server.machines.SetClock(func() time.Time { return testNow })

	// Une signature fausse reste bad_proof, sur un défi valable.
	_, wrongKey, _ := ed25519.GenerateKey(nil)
	valid := server.requestChallenge(t, enrolled.ID, "")
	if code, body := streamRefusal(t, server, enrolled.ID, wrongKey, valid); code != http.StatusUnauthorized || !strings.Contains(body, "bad_proof") {
		t.Errorf("wrong signature: %d %s", code, body)
	}
}

// Une preuve déjà acceptée ne sert pas deux fois.
func TestAgentStream_ARepliedProofIsRefused(t *testing.T) {
	server := newBench(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	challenge := server.requestChallenge(t, enrolled.ID, "")
	resp, _, _, cancel := servertest.OpenStream(t, server, enrolled.ID, private, challenge)
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
	server := newBench(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	challenge := server.requestChallenge(t, enrolled.ID, "")

	restarted := machine.New(server.db, machine.NewSessions(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	restarted.SetClock(func() time.Time { return testNow })
	server.machines = restarted
	server.mount()
	code, body := streamRefusal(t, server, enrolled.ID, private, challenge)
	if code != http.StatusUnauthorized || !strings.Contains(body, "challenge_expired") {
		t.Fatalf("challenge from before the restart: %d %s", code, body)
	}
	resp, _, _, cancel := server.openStream(t, enrolled.ID, private)
	defer resp.Body.Close()
	defer cancel()
}
