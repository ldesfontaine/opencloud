package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
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

// Ouvre le flux avec une preuve signée et rend la réponse en cours et le
// jeton de session lu dans le premier événement.
func openStream(t *testing.T, server *testServer, machineID string, private ed25519.PrivateKey) (*http.Response, string, context.CancelFunc) {
	t.Helper()
	challenge := postJSON(server.Server, "/agent/challenge", machine.ChallengeRequest{MachineID: machineID})
	if challenge.Code != http.StatusOK {
		t.Fatalf("challenge: %d %s", challenge.Code, challenge.Body.String())
	}
	var response machine.ChallengeResponse
	if err := json.Unmarshal(challenge.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	nonce, _ := base64.StdEncoding.DecodeString(response.Nonce)
	payload, err := machine.SignedPayload(nonce, machineID, testNow.Unix())
	if err != nil {
		t.Fatal(err)
	}

	live := httptest.NewServer(server.Server)
	t.Cleanup(live.Close)
	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, live.URL+"/agent/stream", nil)
	request.Header.Set(machine.HeaderMachine, machineID)
	request.Header.Set(machine.HeaderNonce, response.Nonce)
	request.Header.Set(machine.HeaderTimestamp, strconv.FormatInt(testNow.Unix(), 10))
	request.Header.Set(machine.HeaderSignature, base64.StdEncoding.EncodeToString(machine.Sign(private, payload)))
	request.Header.Set(machine.HeaderAgentVersion, "v0.0.2")
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
	return resp, session, cancel
}

func TestAgentStream_OpensASessionThatSignalsAndCloses(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	resp, session, cancel := openStream(t, server, enrolled.ID, private)
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
	resp, _, cancel := openStream(t, server, enrolled.ID, private)
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
