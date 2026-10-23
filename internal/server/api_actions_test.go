package server

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/release"
	"github.com/ldesfontaine/opencloud/internal/release/releasetest"
	"github.com/ldesfontaine/opencloud/internal/server/servertest"
)

// seedActions écrit deux runs finis sur la machine distante, à identifiants
// fixes : un ping réussi et un refusé. Les rendus de la liste sont figés
// sur eux.
func (ts *testServer) seedActions(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	status, err := ts.machines.Get(ctx, remoteID)
	if err != nil {
		t.Fatal(err)
	}
	for index, outcome := range []action.Result{
		{RunID: "a0a0a0a0a0a0a0a1", State: action.StateSucceeded, Output: []action.LogLine{{Stream: "stdout", Line: "pong v0.0.1"}}},
		{RunID: "a0a0a0a0a0a0a0a2", State: action.StateRefused, Reason: agentwire.ReasonReplayed},
	} {
		requested := testNow.Add(time.Duration(index) * time.Minute)
		run := action.Run{
			ID: outcome.RunID, Kind: agentwire.KindAgentPing, MachineID: remoteID, MachineName: status.Name, AgentID: status.Agent.ID,
			State: action.StateSent, NotAfter: requested.Add(action.OrderLifetime), RequestedBy: "operator:lucas", RequestedAt: requested,
		}
		if _, err := ts.db.InsertRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ts.db.ApplyRunResult(ctx, remoteID, outcome, requested.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
}

// Un ping part signé, écrit et journalisé avant l'envoi ; l'agent le reçoit
// sur son flux, la réponse par le signal fait avancer le run, que la liste
// montre avec sa sortie.
func TestActions_PingIsSignedSentAndAnsweredThroughTheSignal(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	resp, session, agentStream, cancel := openStream(t, server, enrolled.ID, private)
	defer resp.Body.Close()
	defer cancel()

	got := callAPI(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/ping", map[string]any{})
	if got.Code != http.StatusCreated {
		t.Fatalf("ping: %d %s", got.Code, got.Body.String())
	}
	var sent actionRunJSON
	if err := json.Unmarshal(got.Body.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.State != string(action.StateSent) || sent.Seq != 1 || sent.RequestedBy != "operator:"+testUsername {
		t.Fatalf("run %+v", sent)
	}

	// Ce que l'agent reçoit : l'enveloppe signée par la clé du serveur.
	signed, err := agentwire.DecodeSignedOrder([]byte(readCommand(t, agentStream, agentwire.EventOrder)))
	if err != nil {
		t.Fatal(err)
	}
	public, fingerprint, err := server.actions.ServerKey()
	if err != nil || signed.Key != fingerprint || !ed25519.Verify(public, agentwire.OrderSigningBytes(signed.Body), signed.Signature) {
		t.Fatalf("signature does not verify with the server key: %v", err)
	}
	var body agentwire.OrderBody
	if err := json.Unmarshal(signed.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.RunID != sent.ID || body.MachineID != remoteID || body.AgentID != enrolled.Agent.ID || body.Kind != agentwire.KindAgentPing ||
		body.Seq != 1 || body.NotAfter != testNow.Add(action.OrderLifetime).Unix() {
		t.Fatalf("order %+v", body)
	}

	// Le journal d'audit a l'ordre, sous l'opérateur, avant toute réponse.
	events, err := server.db.ListAuditEvents(context.Background(), audit.Query{Limit: 1, ObjectKind: audit.ObjectAction})
	if err != nil || len(events) != 1 || events[0].Action != audit.ActionOrder || events[0].ObjectID != sent.ID ||
		events[0].Actor.Kind != audit.ActorOperator || events[0].Details["kind"] != "agent.ping" || events[0].Details["machine_id"] != remoteID {
		t.Fatalf("audit %+v %v", events, err)
	}

	report := agentwire.ActionReport{Results: []agentwire.ActionResult{{
		RunID: sent.ID, State: agentwire.ActionSucceeded, Output: []agentwire.OutputLine{{Stream: "stdout", Line: "pong v0.0.1"}},
	}}}
	if got := server.agentPost(session, "/agent/signal", agentwire.SignalRequest{Actions: &report}); got.Code != http.StatusNoContent {
		t.Fatalf("signal: %d %s", got.Code, got.Body.String())
	}
	// Rejoué après une coupure : un run fini ne bouge plus, la sortie ne
	// double pas.
	if got := server.agentPost(session, "/agent/signal", agentwire.SignalRequest{Actions: &report}); got.Code != http.StatusNoContent {
		t.Fatalf("replayed signal: %d %s", got.Code, got.Body.String())
	}
	list := callAPI(server.Server, http.MethodGet, "/api/actions?machine="+remoteID, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list: %d %s", list.Code, list.Body.String())
	}
	compareGolden(t, filepath.Join("testdata", "actions.golden.json"), normalize(list.Body.String()))
}

// Un ordre ne part que vers un agent actif et en ligne : sinon un refus,
// et rien n'est écrit.
func TestActions_PingRefusesWithoutAnActiveAgentOnline(t *testing.T) {
	server := newTestServer(t)
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/ping", map[string]any{}); got.Code != http.StatusNotFound {
		t.Fatalf("unknown machine: %d %s", got.Code, got.Body.String())
	}
	server.enroll(t, "vps-paris-1", remoteID)
	got := callAPI(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/ping", map[string]any{})
	if got.Code != http.StatusConflict || errorCode(t, got) != "action.machine_offline" {
		t.Fatalf("offline: %d %s", got.Code, got.Body.String())
	}
	if err := server.machines.Revoke(context.Background(), remoteID); err != nil {
		t.Fatal(err)
	}
	got = callAPI(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/ping", map[string]any{})
	if got.Code != http.StatusConflict || errorCode(t, got) != "machine.no_agent" {
		t.Fatalf("revoked: %d %s", got.Code, got.Body.String())
	}
	runs, err := server.db.ListActionRuns(context.Background(), action.Query{Limit: 10})
	if err != nil || len(runs) != 0 {
		t.Fatalf("runs written for refused orders: %+v %v", runs, err)
	}
}

// Un rapport hors des bornes du protocole est refusé en section, les autres
// sections passent ; un run d'une autre machine n'est pas touché.
func TestActions_SignalSectionRefusesAnOversizedReportAndAnotherMachinesRun(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	resp, session, _, cancel := openStream(t, server, enrolled.ID, private)
	defer resp.Body.Close()
	defer cancel()

	tooMany := agentwire.ActionReport{Results: make([]agentwire.ActionResult, agentwire.MaxActionResults+1)}
	for index := range tooMany.Results {
		tooMany.Results[index] = agentwire.ActionResult{RunID: "r", State: agentwire.ActionSucceeded}
	}
	got := server.agentPost(session, "/agent/signal", agentwire.SignalRequest{Actions: &tooMany})
	if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), agentwire.SectionRefusal(agentwire.SectionActions)) {
		t.Fatalf("oversized: %d %s", got.Code, got.Body.String())
	}
	foreign := agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: "ffffffffffffffff", State: agentwire.ActionSucceeded}}}
	if got := server.agentPost(session, "/agent/signal", agentwire.SignalRequest{Actions: &foreign}); got.Code != http.StatusNoContent {
		t.Fatalf("unknown run: %d %s", got.Code, got.Body.String())
	}
}

// La clé publique et son empreinte se lisent pour être comparées à ce que
// chaque agent a épinglé.
func TestActions_ServerKeyIsServed(t *testing.T) {
	server := newTestServer(t)
	got := callAPI(server.Server, http.MethodGet, "/api/agents/server-key", nil)
	var key serverKeyJSON
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &key) != nil {
		t.Fatalf("server key: %d %s", got.Code, got.Body.String())
	}
	_, fingerprint, _ := server.actions.ServerKey()
	if key.Fingerprint != fingerprint || key.PublicKey == "" || key.PreviousFingerprint != nil {
		t.Fatalf("key %+v, want %s", key, fingerprint)
	}
	if !strings.Contains(got.Body.String(), `"previous_fingerprint":null`) {
		t.Fatalf("outside a rotation, the previous key is null: %s", got.Body.String())
	}
}

// La rotation de la clé du serveur, de bout en bout côté serveur : un agent
// qui ouvre son flux sur la clé précédente reçoit la neuve, par un ordre que
// la précédente signe ; sa réponse fait noter la neuve, qu'il redit à la
// connexion suivante, et ses ordres sont dès lors signés par elle.
func TestActions_ServerKeyRotationReachesAnAgentOnThePreviousKey(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	old := testServerKey(t)
	rotated, err := old.Rotated()
	if err != nil {
		t.Fatal(err)
	}
	server.actions.SetKey(rotated)

	resp, session, agentStream, cancel := servertest.OpenStreamPinned(t, server.Server, enrolled.ID, private, old.Fingerprint())
	signed, err := agentwire.DecodeSignedOrder([]byte(readCommand(t, agentStream, agentwire.EventOrder)))
	if err != nil {
		t.Fatal(err)
	}
	if signed.Key != old.Fingerprint() || !ed25519.Verify(old.PublicKey(), agentwire.OrderSigningBytes(signed.Body), signed.Signature) {
		t.Fatal("the new key is not delivered under the previous one")
	}
	var body agentwire.OrderBody
	var params agentwire.ServerKeyParams
	if json.Unmarshal(signed.Body, &body) != nil || json.Unmarshal(body.Params, &params) != nil ||
		body.Kind != agentwire.KindServerKey || params.PublicKey != base64.StdEncoding.EncodeToString(rotated.PublicKey()) {
		t.Fatalf("order %s", signed.Body)
	}
	key := callAPI(server.Server, http.MethodGet, "/api/agents/server-key", nil)
	if !strings.Contains(key.Body.String(), `"previous_fingerprint":"`+old.Fingerprint()+`"`) {
		t.Fatalf("the rotation does not show: %s", key.Body.String())
	}
	report := agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: body.RunID, State: agentwire.ActionSucceeded}}}
	if got := server.agentPost(session, "/agent/signal", agentwire.SignalRequest{Actions: &report}); got.Code != http.StatusNoContent {
		t.Fatalf("signal: %d %s", got.Code, got.Body.String())
	}
	if status, _ := server.machines.Get(context.Background(), remoteID); status.Agent.ServerKeyFingerprint != rotated.Fingerprint() {
		t.Fatalf("pinned %q", status.Agent.ServerKeyFingerprint)
	}
	cancel()
	resp.Body.Close()

	// Reconnecté sur la neuve : rien à livrer, le ping est signé par elle.
	resp, _, agentStream, cancel = servertest.OpenStreamPinned(t, server.Server, enrolled.ID, private, rotated.Fingerprint())
	defer resp.Body.Close()
	defer cancel()
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/ping", map[string]any{}); got.Code != http.StatusCreated {
		t.Fatalf("ping: %d %s", got.Code, got.Body.String())
	}
	signed, err = agentwire.DecodeSignedOrder([]byte(readCommand(t, agentStream, agentwire.EventOrder)))
	if err != nil || signed.Key != rotated.Fingerprint() || !strings.Contains(string(signed.Body), `"kind":"agent.ping"`) {
		t.Fatalf("after the rotation: %+v %v", signed, err)
	}
}

// publishRelease branche la bibliothèque des versions sur l'état de test, avec
// une clé tirée ici, et dit que ce serveur est cette version. Rend de quoi
// importer une version signée.
func (ts *testServer) publishRelease(t *testing.T, serverVersion string) func(version string) {
	t.Helper()
	public, private, err := release.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	library := release.NewLibrary(ts.state, public)
	ts.actions.SetReleases(library, serverVersion)
	return func(version string) {
		t.Helper()
		dir := releasetest.Write(t, private, version, releasetest.Binaries(version, "amd64", "arm64"))
		if _, err := library.Import(dir); err != nil {
			t.Fatal(err)
		}
	}
}

// La fiche d'une machine lit l'offre : sans version importée, la raison ;
// avec, la version du serveur. L'ordre n'part qu'alors, avec un colis que
// l'agent télécharge.
func TestActions_AgentUpdateOfferThenOrder(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	resp, session, agentStream, cancel := openStream(t, server, enrolled.ID, private)
	defer resp.Body.Close()
	defer cancel()
	// Le banc ouvre les flux avec un agent en v0.0.2.
	importRelease := server.publishRelease(t, "v0.0.3")

	got := callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID+"/agent-update", nil)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"reason":"no_release"`) || !strings.Contains(got.Body.String(), `"available":false`) {
		t.Fatalf("before the import: %d %s", got.Code, got.Body.String())
	}
	got = callAPI(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/update-agent", map[string]any{})
	if got.Code != http.StatusConflict || errorCode(t, got) != "action.update_unavailable" {
		t.Fatalf("order before the import: %d %s", got.Code, got.Body.String())
	}

	importRelease("v0.0.3")
	got = callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID+"/agent-update", nil)
	if got.Code != http.StatusOK {
		t.Fatalf("offer: %d %s", got.Code, got.Body.String())
	}
	compareGolden(t, filepath.Join("testdata", "agent_update_offer.golden.json"), normalize(got.Body.String()))

	got = callAPI(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/update-agent", map[string]any{})
	if got.Code != http.StatusCreated {
		t.Fatalf("order: %d %s", got.Code, got.Body.String())
	}
	var sent actionRunJSON
	if err := json.Unmarshal(got.Body.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Kind != "agent.update" || sent.State != string(action.StateSent) || sent.RequestedBy != "operator:"+testUsername {
		t.Fatalf("run %+v", sent)
	}
	signed, err := agentwire.DecodeSignedOrder([]byte(readCommand(t, agentStream, agentwire.EventOrder)))
	if err != nil {
		t.Fatal(err)
	}
	var body agentwire.OrderBody
	var params agentwire.AgentUpdateParams
	if json.Unmarshal(signed.Body, &body) != nil || json.Unmarshal(body.Params, &params) != nil {
		t.Fatalf("order %s", signed.Body)
	}
	if body.Kind != agentwire.KindAgentUpdate || params.Version != "v0.0.3" || params.Binary != "opencloud-linux-amd64" || len(params.Files) != 3 {
		t.Fatalf("order %+v params %+v", body, params)
	}

	// Le colis se télécharge par la route de l'agent, sous le jeton de sa
	// session ; la somme est celle de l'ordre.
	for _, file := range params.Files {
		request := httptest.NewRequest(http.MethodGet, agentwire.ParcelPath(params.Parcel, file.Name), nil)
		request.Header.Set(agentwire.HeaderProtocol, agentwire.ProtocolVersion)
		request.Header.Set("Authorization", "Bearer "+session)
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		digest := sha256.Sum256(recorder.Body.Bytes())
		if recorder.Code != http.StatusOK || int64(recorder.Body.Len()) != file.Size || hex.EncodeToString(digest[:]) != file.SHA256 {
			t.Fatalf("%s: %d, %d bytes", file.Name, recorder.Code, recorder.Body.Len())
		}
	}
	// L'ordre est journalisé sous l'opérateur, avec sa version dans le run.
	events, err := server.db.ListAuditEvents(context.Background(), audit.Query{Limit: 1, ObjectKind: audit.ObjectAction})
	if err != nil || len(events) != 1 || events[0].Action != audit.ActionOrder || events[0].Details["kind"] != "agent.update" {
		t.Fatalf("audit %+v %v", events, err)
	}
}

func TestActions_AgentUpdateOfferOfUnknownOrOfflineMachines(t *testing.T) {
	server := newTestServer(t)
	server.publishRelease(t, "v0.0.3")
	if got := callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID+"/agent-update", nil); got.Code != http.StatusNotFound {
		t.Fatalf("unknown machine: %d %s", got.Code, got.Body.String())
	}
	server.enroll(t, "vps-paris-1", remoteID)
	got := callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID+"/agent-update", nil)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"reason":"offline"`) {
		t.Fatalf("offline: %d %s", got.Code, got.Body.String())
	}
	got = callAPI(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/update-agent", map[string]any{})
	if got.Code != http.StatusConflict || errorCode(t, got) != "action.machine_offline" {
		t.Fatalf("order to an offline machine: %d %s", got.Code, got.Body.String())
	}
	if runs, err := server.db.ListActionRuns(context.Background(), action.Query{Limit: 10}); err != nil || len(runs) != 0 {
		t.Fatalf("runs written for a refused order: %+v %v", runs, err)
	}
}

// Un agent qui rouvre son flux avec la version qu'un ordre agent.update visait
// a fini de se mettre à jour : l'ordre devient « fait » à ce moment, pas
// avant, et pas pour une autre version.
func TestAgentStream_ConfirmsAPendingAgentUpdate(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	found, err := server.machines.Get(context.Background(), remoteID)
	if err != nil {
		t.Fatal(err)
	}
	insert := func(id, version string, state action.State) {
		t.Helper()
		run := action.Run{
			ID: id, Kind: agentwire.KindAgentUpdate, Params: json.RawMessage(`{"version":"` + version + `"}`),
			MachineID: remoteID, MachineName: found.Name, AgentID: found.Agent.ID,
			State: state, NotAfter: testNow.Add(action.OrderLifetime), RequestedBy: "operator:lucas", RequestedAt: testNow,
		}
		if _, err := server.db.InsertRun(context.Background(), run); err != nil {
			t.Fatal(err)
		}
		if err := server.db.SetRunState(context.Background(), id, state, "", time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
	// Le banc ouvre les flux avec un agent en v0.0.2.
	insert("u0u0u0u0u0u0u0u1", "v0.0.2", action.StateAccepted)
	insert("u0u0u0u0u0u0u0u2", "v0.0.3", action.StateAccepted)

	resp, _, _, cancel := openStream(t, server, enrolled.ID, private)
	defer resp.Body.Close()
	defer cancel()

	states := map[string]action.State{}
	runs, err := server.db.ListActionRuns(context.Background(), action.Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		states[run.ID] = run.State
	}
	if states["u0u0u0u0u0u0u0u1"] != action.StateSucceeded || states["u0u0u0u0u0u0u0u2"] != action.StateAccepted {
		t.Fatalf("states %v", states)
	}
}
