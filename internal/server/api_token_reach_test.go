package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/apitoken"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/verb"
)

// Les deux machines du banc : A, l'hôte Proxmox dont le flux est ouvert, et B,
// une autre machine enrôlée.
const (
	reachA = pveID
	reachB = dockerVMID
)

// reachBench ajoute à l'hôte Proxmox une sonde et une tâche par machine : un
// verbe de sonde ou de tâche se juge sur la machine de l'objet, pas sur un
// segment du chemin.
type reachBench struct {
	*testServer
	probes map[string]string
	jobs   map[string]string
}

func newReachBench(t *testing.T) reachBench {
	t.Helper()
	bench := reachBench{testServer: proxmoxTokenBench(t), probes: map[string]string{}, jobs: map[string]string{}}
	for _, machineID := range []string{reachA, reachB} {
		bench.probes[machineID] = bench.createProbeVia(t, probeRequest{Name: "sonde " + machineID, Kind: "http", Target: "https://a.fr/", MachineID: machineID}).ID
		job, err := bench.heartbeats.Create(context.Background(), heartbeat.Definition{Name: "tâche " + machineID, MachineID: machineID, Interval: 5 * time.Minute, Grace: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		bench.jobs[machineID] = job.ID
	}
	return bench
}

// issueReachToken tire un jeton en écriture avec ces verbes et cette portée.
func (b reachBench) issueReachToken(t *testing.T, name string, verbs []string, reach apitoken.Reach) (string, apitoken.Token) {
	t.Helper()
	cleartext, token, err := b.apiTokens.Create(context.Background(), apitoken.Request{Name: name, Scope: apitoken.ScopeWrite, Verbs: verbs, Reach: reach})
	if err != nil {
		t.Fatal(err)
	}
	return cleartext, token
}

// Le numéro d'invité que chaque geste Proxmox vise, choisi pour que le geste
// soit possible sur le faux hôte : 200 est arrêté, 5012 en marche.
var reachGuests = map[string]int{
	"proxmox.vm.start": 200, "proxmox.vm.shutdown": 5012, "proxmox.vm.snapshot": 5012, "proxmox.vm.snapshot_delete": 200,
}

// request compose la requête d'un verbe contre une machine, et le corps que
// la route attend. Un verbe absent d'ici fait échouer le test de couverture.
func (b reachBench) request(t *testing.T, verbID, machineID string) (method, path, body string) {
	t.Helper()
	found, ok := verb.Default.Lookup(verbID)
	if !ok {
		t.Fatalf("verb %s is not registered", verbID)
	}
	method, pattern, _ := strings.Cut(found.Route, " ")
	id := machineID
	switch found.Target {
	case verb.TargetProbe:
		id = b.probes[machineID]
	case verb.TargetJob:
		id = b.jobs[machineID]
	}
	path = strings.ReplaceAll(pattern, "{id}", id)
	if found.TargetsGuest() {
		path = strings.ReplaceAll(path, "{vmid}", strconv.Itoa(reachGuests[verbID]))
	}
	body = "{}"
	if verbID == "proxmox.vm.snapshot_delete" {
		body = `{"snapshot":"oc-20261021-120000"}`
	}
	return method, path, body
}

func (b reachBench) call(t *testing.T, verbID, machineID, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	method, path, body := b.request(t, verbID, machineID)
	return b.callPath(method, path, body, bearer)
}

func (b reachBench) callPath(method, path, body, bearer string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	recorder := httptest.NewRecorder()
	b.ServeHTTP(recorder, request)
	return recorder
}

// requireOutOfScope : 403 out_of_scope, et rien d'autre n'a répondu.
func requireOutOfScope(t *testing.T, label string, got *httptest.ResponseRecorder) {
	t.Helper()
	if got.Code != http.StatusForbidden || errorCode(t, got) != codeAPITokenOutOfScope {
		t.Errorf("%s: %d %s, want 403 %s", label, got.Code, got.Body.String(), codeAPITokenOutOfScope)
	}
}

// requireInScope : la requête a franchi la portée. Le verbe lui-même peut
// encore échouer (404, 409…), mais pas sur un 403 de jeton.
func requireInScope(t *testing.T, label string, got *httptest.ResponseRecorder) {
	t.Helper()
	if got.Code == http.StatusForbidden {
		t.Errorf("%s: refused %s", label, got.Body.String())
	}
}

// Le verbe passe sur un 200 du contrat des verbes, pas seulement « pas 403 » :
// l'ordre est parti ou la sonde a changé d'état.
var reachVerbsThatSucceed = map[string]bool{
	"agent.ping": true, "probe.pause": true, "probe.resume": true, "job.pause": true, "job.resume": true,
	"proxmox.vm.start": true, "proxmox.vm.shutdown": true, "proxmox.vm.snapshot": true, "proxmox.vm.snapshot_delete": true,
}

// Le garde-fou : chaque verbe du registre a une requête dans ce banc. Un
// verbe ajouté sans cas ici, donc sans test de portée, fait échouer.
func TestReach_EveryRegisteredVerbHasACase(t *testing.T) {
	bench := newReachBench(t)
	for _, offered := range verb.Default.All() {
		if offered.Target == "" {
			t.Errorf("%s: no target, its reach cannot be judged", offered.ID)
		}
		_, path, _ := bench.request(t, offered.ID, reachA)
		if strings.Contains(path, "{") {
			t.Errorf("%s: path %q has an unresolved segment", offered.ID, path)
		}
		if offered.TargetsGuest() && reachGuests[offered.ID] == 0 {
			t.Errorf("%s: targets a guest but the bench names none", offered.ID)
		}
	}
}

// Critère : pour chaque route d'action, un jeton limité à A agit sur A, est
// refusé sur B en 403 out_of_scope, journalisé avec son acteur, la machine
// visée et l'invité, et rien n'a été fait.
func TestReach_EveryActionRouteIsJudgedOnItsMachine(t *testing.T) {
	bench := newReachBench(t)
	for _, offered := range verb.Default.All() {
		t.Run(offered.ID, func(t *testing.T) {
			bearer, token := bench.issueReachToken(t, "limité-"+offered.ID, []string{offered.ID}, apitoken.Reach{OnlyMachines: true, Machines: []string{reachA}})

			inScope := bench.call(t, offered.ID, reachA, bearer)
			requireInScope(t, "machine A", inScope)
			if reachVerbsThatSucceed[offered.ID] && inScope.Code != http.StatusOK {
				t.Errorf("machine A: %d %s, want 200", inScope.Code, inScope.Body.String())
			}

			before := bench.latestAuditID(t)
			outOfScope := bench.call(t, offered.ID, reachB, bearer)
			requireOutOfScope(t, "machine B", outOfScope)

			var denied []audit.Event
			for _, event := range bench.auditSince(t, before) {
				if event.Action == audit.APITokenDeny {
					denied = append(denied, event)
				}
				if event.Action == audit.ActionOrder {
					t.Errorf("an order was written for a refused verb: %+v", event)
				}
			}
			if len(denied) != 1 {
				t.Fatalf("%d deny events, want 1: %+v", len(denied), denied)
			}
			event := denied[0]
			if event.Actor.Kind != audit.ActorAPI || event.Actor.Name != token.ID || event.ObjectID != token.ID {
				t.Errorf("actor %+v object %q", event.Actor, event.ObjectID)
			}
			if event.Details["verb"] != offered.ID || event.Details["reason"] != "out_of_scope" || event.Details["machine"] != reachB {
				t.Errorf("details %+v", event.Details)
			}
			if guest := event.Details["guest"]; offered.TargetsGuest() && guest != strconv.Itoa(reachGuests[offered.ID]) || !offered.TargetsGuest() && guest != "" {
				t.Errorf("guest %q for %s", guest, offered.ID)
			}
		})
	}
	// Ni sonde ni tâche de B n'a bougé.
	if status := bench.probeStatus(t, bench.probes[reachB]); status == "paused" {
		t.Error("the probe of machine B was paused")
	}
	if runs, err := bench.db.ListActionRuns(context.Background(), action.Query{MachineID: reachB, Limit: 10}); err != nil || len(runs) != 0 {
		t.Errorf("runs on machine B: %v %v", runs, err)
	}
}

// Critère Proxmox : le geste vise l'hôte (machine) et l'invité. Hôte admis et
// invité refusé, invité admis et hôte refusé : refus dans les deux cas.
func TestReach_AProxmoxGestureNeedsTheHostAndTheGuest(t *testing.T) {
	bench := newReachBench(t)
	cases := []struct {
		name  string
		reach apitoken.Reach
		host  string
		want  bool
	}{
		{"host and guest in reach", apitoken.Reach{OnlyMachines: true, Machines: []string{reachA}, OnlyGuests: true, Guests: []int{200, 5012}}, reachA, true},
		{"host in reach, guest out", apitoken.Reach{OnlyMachines: true, Machines: []string{reachA}, OnlyGuests: true, Guests: []int{999}}, reachA, false},
		{"guest in reach, host out", apitoken.Reach{OnlyMachines: true, Machines: []string{reachB}, OnlyGuests: true, Guests: []int{200, 5012}}, reachA, false},
		{"only guests limited, guest in", apitoken.Reach{OnlyGuests: true, Guests: []int{200, 5012}}, reachA, true},
		{"only guests limited, guest out", apitoken.Reach{OnlyGuests: true, Guests: []int{999}}, reachA, false},
		{"only machines limited, host in", apitoken.Reach{OnlyMachines: true, Machines: []string{reachA}}, reachA, true},
		{"empty guest list", apitoken.Reach{OnlyGuests: true}, reachA, false},
	}
	for _, c := range cases {
		bearer, _ := bench.issueReachToken(t, c.name, proxmoxVerbIDs, c.reach)
		for _, id := range proxmoxVerbIDs {
			got := bench.call(t, id, c.host, bearer)
			if c.want {
				requireInScope(t, c.name+" "+id, got)
			} else {
				requireOutOfScope(t, c.name+" "+id, got)
			}
		}
	}
}

// Les gestes qui ne visent aucun invité ignorent la liste des invités, même
// vide : elle ne les bloque pas.
func TestReach_TheGuestListOnlyJudgesProxmoxGestures(t *testing.T) {
	bench := newReachBench(t)
	bearer, _ := bench.issueReachToken(t, "invités vides", []string{"agent.ping", "probe.pause"}, apitoken.Reach{OnlyGuests: true})
	requireInScope(t, "ping", bench.call(t, "agent.ping", reachA, bearer))
	requireInScope(t, "probe", bench.call(t, "probe.pause", reachB, bearer))
}

// Critère : sans portée, rien ne change ; une portée vide explicite refuse
// tout, sur toutes les machines.
func TestReach_NoReachIsUnchangedAndAnEmptyReachRefusesEverything(t *testing.T) {
	bench := newReachBench(t)
	everyone, _ := bench.issueReachToken(t, "partout", allVerbIDs(), apitoken.Reach{})
	nobody, _ := bench.issueReachToken(t, "nulle part", allVerbIDs(), apitoken.Reach{OnlyMachines: true})
	for _, offered := range verb.Default.All() {
		for _, machineID := range []string{reachA, reachB} {
			requireInScope(t, "no reach "+offered.ID+" on "+machineID, bench.call(t, offered.ID, machineID, everyone))
		}
	}
	// Le débit des refus journalisés est par jeton : les refus restent des
	// refus au-delà, la boucle ne dépend pas de l'audit.
	for _, offered := range verb.Default.All() {
		for _, machineID := range []string{reachA, reachB} {
			requireOutOfScope(t, "empty reach "+offered.ID+" on "+machineID, bench.call(t, offered.ID, machineID, nobody))
		}
	}
}

// Une sonde ou une tâche que le serveur ne sait pas rattacher à une machine
// n'est dans aucune liste : un 403, pas un 404 qui dirait au jeton limité
// qu'elle existe ailleurs. Un jeton sans limite garde le 404.
func TestReach_AnObjectWithoutAMachineIsOutOfScope(t *testing.T) {
	bench := newReachBench(t)
	standalone, err := bench.heartbeats.Create(context.Background(), heartbeat.Definition{Name: "sans machine", Interval: time.Hour, Grace: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	limited, _ := bench.issueReachToken(t, "limité", []string{"job.pause", "probe.pause"}, apitoken.Reach{OnlyMachines: true, Machines: []string{reachA, reachB}})
	requireOutOfScope(t, "job without machine", bench.callPath(http.MethodPost, "/api/jobs/"+standalone.ID+"/actions/pause", "{}", limited))
	requireOutOfScope(t, "unknown probe", bench.callPath(http.MethodPost, "/api/probes/inconnue/actions/pause", "{}", limited))

	free, _ := bench.issueReachToken(t, "libre", []string{"probe.pause"}, apitoken.Reach{})
	if got := bench.callPath(http.MethodPost, "/api/probes/inconnue/actions/pause", "{}", free); got.Code != http.StatusNotFound {
		t.Errorf("unknown probe without reach: %d %s, want 404", got.Code, got.Body.String())
	}
}

// Le refus journalisé a la même limite que les refus de verbe : le premier
// est le signal, mille identiques noieraient l'audit, mais tous sont refusés.
func TestReach_RefusalsAreAuditedWithTheDenyLimit(t *testing.T) {
	bench := newReachBench(t)
	bearer, _ := bench.issueReachToken(t, "insistant", []string{"agent.ping"}, apitoken.Reach{OnlyMachines: true, Machines: []string{reachA}})
	before := bench.latestAuditID(t)
	for range apiVerbDenyBurst + 20 {
		requireOutOfScope(t, "ping B", bench.call(t, "agent.ping", reachB, bearer))
	}
	if written := len(bench.auditSince(t, before)); written != apiVerbDenyBurst {
		t.Fatalf("%d deny events written, want %d", written, apiVerbDenyBurst)
	}
}

// Un identifiant que le client a écrit dans le chemin n'entre pas brut dans
// le journal : borné et sans caractère de contrôle.
func TestReach_AClientWrittenMachineIDIsBoundedInTheAudit(t *testing.T) {
	bench := newReachBench(t)
	bearer, _ := bench.issueReachToken(t, "curieux", []string{"agent.ping"}, apitoken.Reach{OnlyMachines: true, Machines: []string{reachA}})
	before := bench.latestAuditID(t)
	long := strings.Repeat("z", 500)
	requireOutOfScope(t, "long id", bench.callPath(http.MethodPost, "/api/machines/"+long+"/actions/ping", "{}", bearer))
	for _, event := range bench.auditSince(t, before) {
		if event.Action == audit.APITokenDeny && len([]rune(event.Details["machine"])) > maxAuditedMachineRunes {
			t.Fatalf("machine detail of %d runes", len([]rune(event.Details["machine"])))
		}
	}
}

// Critère : un jeton ne modifie ni sa propre portée ni celle d'un autre ; la
// route est celle de l'opérateur, fermée aux jetons même en écriture.
func TestReach_ATokenCannotChangeAReach(t *testing.T) {
	bench := newReachBench(t)
	bearer, token := bench.issueReachToken(t, "ambitieux", allVerbIDs(), apitoken.Reach{OnlyMachines: true, Machines: []string{reachA}})
	_, other := bench.issueReachToken(t, "voisin", []string{"agent.ping"}, apitoken.Reach{OnlyMachines: true})
	for _, target := range []apitoken.Token{token, other} {
		got := bench.callPath(http.MethodPut, "/api/settings/api-tokens/"+target.ID+"/reach", "{}", bearer)
		if got.Code != http.StatusForbidden || errorCode(t, got) != codeAPITokenRouteForbidden {
			t.Errorf("token %s: %d %s", target.Name, got.Code, got.Body.String())
		}
	}
	tokens, err := bench.apiTokens.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, listed := range tokens {
		if !listed.Reach.OnlyMachines {
			t.Errorf("%s widened its reach to everywhere", listed.Name)
		}
	}
	// Ni créer un jeton plus large : la route de création leur est fermée.
	created := bench.callPath(http.MethodPost, "/api/settings/api-tokens", `{"name":"large","scope":"write"}`, bearer)
	if created.Code != http.StatusForbidden || errorCode(t, created) != codeAPITokenRouteForbidden {
		t.Errorf("create by token: %d %s", created.Code, created.Body.String())
	}
}

// L'opérateur remplace la portée, et elle vaut à la requête suivante.
func TestReach_TheOperatorReplacesAReachAndItAppliesAtOnce(t *testing.T) {
	bench := newReachBench(t)
	bearer, token := bench.issueReachToken(t, "agent", []string{"agent.ping"}, apitoken.Reach{OnlyMachines: true, Machines: []string{reachA}})
	requireOutOfScope(t, "B before", bench.call(t, "agent.ping", reachB, bearer))

	var updated apiTokenJSON
	got := callAPI(bench.Server, http.MethodPut, "/api/settings/api-tokens/"+token.ID+"/reach", map[string]any{"machines": []string{reachB, reachA, reachB}})
	if got.Code != http.StatusOK {
		t.Fatalf("set reach: %d %s", got.Code, got.Body.String())
	}
	decodeAPI(t, got, &updated)
	if len(updated.Machines) != 2 || updated.Guests != nil {
		t.Fatalf("updated %+v: machines must be deduplicated, guests unset (everyone)", updated)
	}
	requireInScope(t, "B after", bench.call(t, "agent.ping", reachB, bearer))

	// Un corps vide redevient « partout ».
	if got := callAPI(bench.Server, http.MethodPut, "/api/settings/api-tokens/"+token.ID+"/reach", map[string]any{}); got.Code != http.StatusOK {
		t.Fatalf("clear reach: %d %s", got.Code, got.Body.String())
	}
	tokens, _ := bench.apiTokens.List(context.Background())
	for _, listed := range tokens {
		if listed.ID == token.ID && listed.Reach.IsLimited() {
			t.Fatalf("reach still limited: %+v", listed.Reach)
		}
	}
	var changed []audit.Event
	for _, event := range bench.auditSince(t, 0) {
		if event.Action == audit.APITokenUpdate {
			changed = append(changed, event)
		}
	}
	if len(changed) != 2 || changed[0].ObjectID != token.ID || changed[0].Details["reach"] == "" {
		t.Fatalf("update events %+v", changed)
	}
}

func TestReach_TheOperatorIsRefusedWhatIsInvalid(t *testing.T) {
	bench := newReachBench(t)
	_, writer := bench.issueReachToken(t, "écrivain", []string{"agent.ping"}, apitoken.Reach{})
	reader := bench.issueAPIToken(t, "lecteur")
	_ = reader
	tokens, _ := bench.apiTokens.List(context.Background())
	var readerID string
	for _, listed := range tokens {
		if listed.Name == "lecteur" {
			readerID = listed.ID
		}
	}
	cases := []struct {
		name, id string
		body     map[string]any
		status   int
		code     string
	}{
		{"unknown machine", writer.ID, map[string]any{"machines": []string{"fantôme"}}, http.StatusUnprocessableEntity, "api_token.machine_unknown"},
		{"guest zero", writer.ID, map[string]any{"guests": []int{0}}, http.StatusUnprocessableEntity, "api_token.guest_invalid"},
		{"guest too large", writer.ID, map[string]any{"guests": []int{1000000000}}, http.StatusUnprocessableEntity, "api_token.guest_invalid"},
		{"read token", readerID, map[string]any{"machines": []string{reachA}}, http.StatusUnprocessableEntity, "api_token.reach_needs_write"},
		{"unknown token", "inconnu", map[string]any{"machines": []string{reachA}}, http.StatusNotFound, ""},
	}
	for _, c := range cases {
		got := callAPI(bench.Server, http.MethodPut, "/api/settings/api-tokens/"+c.id+"/reach", c.body)
		if got.Code != c.status || (c.code != "" && errorCode(t, got) != c.code) {
			t.Errorf("%s: %d %s", c.name, got.Code, got.Body.String())
		}
	}
}

// La création par l'API pose la portée ; la liste la rend, avec les machines
// connues pour la liste à cocher.
func TestReach_CreationAndListCarryTheReach(t *testing.T) {
	bench := newReachBench(t)
	got := callAPI(bench.Server, http.MethodPost, "/api/settings/api-tokens", map[string]any{
		"name": "limité", "scope": "write", "verbs": []string{"proxmox.vm.start"}, "machines": []string{reachA}, "guests": []int{5012, 200},
	})
	if got.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", got.Code, got.Body.String())
	}
	var created apiTokenCreated
	decodeAPI(t, got, &created)
	if len(created.Item.Machines) != 1 || len(created.Item.Guests) != 2 || created.Item.Guests[0] != 200 {
		t.Fatalf("created %+v", created.Item)
	}
	var listed apiTokensResponse
	decodeAPI(t, callAPI(bench.Server, http.MethodGet, "/api/settings/api-tokens", nil), &listed)
	if len(listed.Machines) < 2 {
		t.Fatalf("machines offered: %+v", listed.Machines)
	}
	found := false
	for _, item := range listed.Tokens {
		if item.ID == created.Item.ID {
			found = len(item.Machines) == 1 && item.Machines[0] == reachA && len(item.Guests) == 2
		}
	}
	if !found {
		t.Fatalf("list lost the reach: %+v", listed.Tokens)
	}
	// Une portée sur un jeton en lecture est dite, pas ignorée.
	refused := callAPI(bench.Server, http.MethodPost, "/api/settings/api-tokens", map[string]any{"name": "lecteur", "machines": []string{reachA}})
	if refused.Code != http.StatusUnprocessableEntity || errorCode(t, refused) != "api_token.reach_needs_write" {
		t.Fatalf("read token with a reach: %d %s", refused.Code, refused.Body.String())
	}
}

// GET /api/token/verbs rend la portée du jeton appelant, pour l'outil MCP :
// null pour « toutes », une liste (vide comprise) sinon.
func TestReach_TheVerbsRouteTellsTheReach(t *testing.T) {
	bench := newReachBench(t)
	read := func(reach apitoken.Reach) tokenReachJSON {
		bearer, _ := bench.issueReachToken(t, "lit"+strconv.Itoa(len(reach.Machines))+strconv.FormatBool(reach.OnlyMachines)+strconv.FormatBool(reach.OnlyGuests), []string{"proxmox.vm.start"}, reach)
		var response tokenVerbsResponse
		got := callBearer(bench.Server, "/api/token/verbs", bearer)
		decodeAPI(t, got, &response)
		return response.Reach
	}
	if everywhere := read(apitoken.Reach{}); everywhere.Machines != nil || everywhere.Guests != nil {
		t.Errorf("no reach: %+v", everywhere)
	}
	if none := read(apitoken.Reach{OnlyMachines: true}); none.Machines == nil || len(none.Machines) != 0 || none.Guests != nil {
		t.Errorf("empty machines: %+v", none)
	}
	some := read(apitoken.Reach{OnlyMachines: true, Machines: []string{reachA}, OnlyGuests: true, Guests: []int{200}})
	if len(some.Machines) != 1 || some.Machines[0] != reachA || len(some.Guests) != 1 || some.Guests[0] != 200 {
		t.Errorf("limited: %+v", some)
	}
}
