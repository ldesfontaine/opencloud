package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/proxmox"
)

const (
	pveID      = remoteID
	dockerVMID = "77777777-8888-4999-8aaa-bbbbbbbbbbbb"
	pveTokenID = "lecteur@pve!lecture"
	// Le jeton qui lit aussi les archives, pour le parcours navigateur : même secret fictif.
	pveFullTokenID  = "lecteur@pve!complet"
	pveTokenSecret  = "0a1b2c3d-4e5f-6071-8293-a4b5c6d7e8f9" // gitleaks:allow -- faux secret de test
	pveGoldenURL    = "https://pve.lan:8006"
	pveGoldenPinHex = "abababababababababababababababababababababababababababababababab"
)

// Les tâches vzdump de l'hôte, telles que /cluster/backup les rend : chaque
// nuit, tous les invités vers le partage du Pi, avec sa rétention.
const pveBackupJobs = `{"data":[{"id":"nuit-pi","type":"vzdump","schedule":"03:00","next-run":1793329200,
	"storage":"pi-sauvegardes","enabled":1,"all":1,"mode":"snapshot","prune-backups":"keep-daily=14,keep-weekly=8,keep-monthly=3"}]}`

// L'adresse de la carte de la VM 5012, telle que l'hôte la dit dans sa configuration.
const pveGuestMAC = "BC:24:11:AA:BB:CC"

// fakeProxmox joue l'API d'un hôte Proxmox sur une adresse locale : aucun test
// ne joint un vrai hôte.
type fakeProxmox struct {
	*httptest.Server
	mu     sync.Mutex
	broken bool
	// auditor : le jeton n'a que PVEAuditor, comme celui de Lucas.
	auditor bool
	// rich : un hôte garni (plusieurs VM, un modèle, des stockages dont un NFS de
	// sauvegardes) pour le parcours navigateur et les captures.
	rich bool
	// Le nom de la VM 5012, que le rapprochement lie à une machine du même nom d'hôte.
	runningName string
}

func newFakeProxmox(t *testing.T) *fakeProxmox {
	t.Helper()
	api := &fakeProxmox{}
	api.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		full := r.Header.Get("Authorization") == "PVEAPIToken="+pveFullTokenID+"="+pveTokenSecret
		if !full && r.Header.Get("Authorization") != "PVEAPIToken="+pveTokenID+"="+pveTokenSecret {
			http.Error(w, `{"data":null}`, http.StatusUnauthorized)
			return
		}
		api.mu.Lock()
		broken, auditor, rich := api.broken, api.auditor && !full, api.rich
		runningName := api.runningName
		api.mu.Unlock()
		if runningName == "" {
			runningName = "docker1"
		}
		var body string
		switch {
		case r.URL.Path == "/api2/json/version":
			body = `{"data":{"version":"8.2.4"}}`
		case broken:
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		case rich && r.URL.Path == "/api2/json/cluster/resources" && r.URL.Query().Get("type") == "vm":
			body = richGuests(runningName)
		case rich && r.URL.Path == "/api2/json/cluster/resources" && r.URL.Query().Get("type") == "storage":
			body = richStorages
		case rich && r.URL.Path == "/api2/json/nodes/pve/storage/pi-sauvegardes/content":
			body = richArchives()
		case r.URL.Path == "/api2/json/cluster/resources" && r.URL.Query().Get("type") == "vm":
			body = `{"data":[
				{"type":"qemu","vmid":5012,"name":"` + runningName + `","node":"pve","status":"running","template":0,"maxcpu":2,"maxmem":2147483648,"maxdisk":34359738368},
				{"type":"lxc","vmid":200,"name":"dns","node":"pve","status":"stopped","template":0,"maxcpu":1,"maxmem":536870912,"maxdisk":8589934592}]}`
		case r.URL.Path == "/api2/json/cluster/resources" && r.URL.Query().Get("type") == "storage":
			body = `{"data":[{"type":"storage","storage":"local","node":"pve","plugintype":"dir","status":"available","content":"iso,backup","maxdisk":1000,"disk":400}]}`
		case r.URL.Path == "/api2/json/cluster/status":
			body = `{"data":[{"type":"cluster","name":"homelab","nodes":1},{"type":"node","name":"pve","local":1}]}`
		case r.URL.Path == "/api2/json/nodes/pve/qemu/5012/config":
			body = `{"data":{"name":"` + runningName + `","net0":"virtio=` + pveGuestMAC + `,bridge=vmbr0,firewall=1,tag=50"}}`
		case r.URL.Path == "/api2/json/nodes/pve/lxc/200/config":
			body = `{"data":{"net0":"name=eth0,bridge=vmbr0,hwaddr=BC:24:11:AA:BB:DD,tag=50,ip=dhcp"}}`
		case r.URL.Path == "/api2/json/access/permissions":
			privileges := `"Datastore.Audit":1,"Datastore.Allocate":1`
			if auditor {
				privileges = `"Datastore.Audit":1,"VM.Audit":1`
			}
			body = `{"data":{"` + r.URL.Query().Get("path") + `":{` + privileges + `}}}`
		case r.URL.Path == "/api2/json/nodes/pve/storage/local/content":
			body = `{"data":[{"vmid":5012,"ctime":1790086400,"size":244318208}]}`
		case r.URL.Path == "/api2/json/cluster/backup":
			body = pveBackupJobs
		default:
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(api.Close)
	return api
}

func (api *fakeProxmox) setBroken(broken bool) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.broken = broken
}

func (api *fakeProxmox) setGuestName(name string) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.runningName = name
}

func (api *fakeProxmox) pin() string {
	sum := sha256.Sum256(api.Certificate().Raw)
	return hex.EncodeToString(sum[:])
}

func (api *fakeProxmox) request(secret string) proxmoxRequest {
	return proxmoxRequest{APIURL: api.URL, TokenID: pveTokenID, TokenSecret: secret, Pin: api.pin()}
}

// proxmoxBench dresse le serveur d'essai avec un composant Proxmox dont le
// journal est lu par le test, et deux machines : celle qui porte l'hôte et une
// VM qui porte le nom d'hôte d'une des siennes.
func proxmoxBench(t *testing.T) (*testServer, *fakeProxmox, *bytes.Buffer) {
	t.Helper()
	server, api, journal, _ := proxmoxBenchWithKey(t)
	return server, api, journal
}

// wireProxmox branche au serveur d'essai un vrai composant Proxmox.
func wireProxmox(t *testing.T, server *testServer, logger *slog.Logger) {
	t.Helper()
	component := proxmox.New(server.db, server.machines, logger)
	component.SetClock(func() time.Time { return testNow })
	component.SetWatcher(server.bus)
	component.SetAuditor(server.journal)
	component.SetOrders(server.actions)
	component.SetViewer(server.db)
	// Le suivi d'un ordre suit la vie du serveur : on l'arrête et on l'attend
	// avant que l'état de test soit effacé.
	lifetime, stopServer := context.WithCancel(context.Background())
	component.SetLifetime(lifetime)
	t.Cleanup(func() {
		stopServer()
		component.WaitBackground()
	})
	server.Server.proxmox = component
}

// proxmoxBenchWithKey rend aussi la clé de l'agent de l'hôte, pour ouvrir son
// flux : un ordre n'est écrit que pour une machine en ligne.
func proxmoxBenchWithKey(t *testing.T) (*testServer, *fakeProxmox, *bytes.Buffer, ed25519.PrivateKey) {
	t.Helper()
	server := newTestServer(t)
	var journal bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&journal, &slog.HandlerOptions{Level: slog.LevelDebug}))
	server.logger = logger
	wireProxmox(t, server, logger)
	_, hostKey := server.enroll(t, "pve", pveID)
	server.enroll(t, "docker1", dockerVMID)
	return server, newFakeProxmox(t), &journal, hostKey
}

func putProxmox(server *testServer, id string, request proxmoxRequest) *httptest.ResponseRecorder {
	return callAPI(server.Server, http.MethodPut, "/api/machines/"+id+"/proxmox", request)
}

// Le jeton répond à l'API avant que rien ne s'écrive : un jeton refusé laisse
// la machine sans hôte.
func TestProxmox_PutChecksTheTokenBeforeWriting(t *testing.T) {
	server, api, _ := proxmoxBench(t)

	got := putProxmox(server, pveID, api.request("wrong-secret-value"))

	if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "proxmox.unauthorized" {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	if read := callAPI(server.Server, http.MethodGet, "/api/machines/"+pveID+"/proxmox", nil); read.Code != http.StatusNotFound {
		t.Fatalf("a refused token must leave no host: %d %s", read.Code, read.Body.String())
	}
	if ids, err := server.db.ListProxmoxHostIDs(context.Background()); err != nil || len(ids) != 0 {
		t.Fatalf("hosts %v, %v", ids, err)
	}
}

func TestProxmox_PutRefusesWhatTheOperatorGotWrong(t *testing.T) {
	server, api, _ := proxmoxBench(t)
	good := api.request(pveTokenSecret)
	for name, tc := range map[string]struct {
		change func(*proxmoxRequest)
		want   string
	}{
		"address":     {func(r *proxmoxRequest) { r.APIURL = "http://pve.lan:8006" }, "proxmox.url_invalid"},
		"token id":    {func(r *proxmoxRequest) { r.TokenID = "lecteur" }, "proxmox.token_id_invalid"},
		"secret":      {func(r *proxmoxRequest) { r.TokenSecret = "court" }, "proxmox.secret_invalid"},
		"pin":         {func(r *proxmoxRequest) { r.Pin = "zz" }, "proxmox.pin_invalid"},
		"other pin":   {func(r *proxmoxRequest) { r.Pin = strings.Repeat("ab", 32) }, "proxmox.certificate"},
		"no pin":      {func(r *proxmoxRequest) { r.Pin = "" }, "proxmox.certificate"},
		"nobody home": {func(r *proxmoxRequest) { r.APIURL = "https://127.0.0.1:1" }, "proxmox.unreachable"},
	} {
		request := good
		tc.change(&request)
		got := putProxmox(server, pveID, request)
		if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != tc.want {
			t.Errorf("%s: %d %s, want %s", name, got.Code, got.Body.String(), tc.want)
		}
	}
	if got := putProxmox(server, "inconnue", good); got.Code != http.StatusNotFound {
		t.Errorf("unknown machine: %d", got.Code)
	}
	malformed := httptest.NewRecorder()
	server.ServeHTTP(malformed, httptest.NewRequest(http.MethodPut, "/api/machines/"+pveID+"/proxmox", strings.NewReader("{")))
	if malformed.Code == http.StatusOK {
		t.Errorf("malformed body accepted")
	}
}

// Le cycle entier : déclarer lit tout de suite, la fiche et les deux listes de
// machines le disent, le retrait remet tout à zéro. Les réponses sont figées.
func TestProxmox_DeclareReadAndRemove(t *testing.T) {
	server, api, _ := proxmoxBench(t)

	put := putProxmox(server, pveID, api.request(pveTokenSecret))
	if put.Code != http.StatusOK {
		t.Fatalf("put: %d %s", put.Code, put.Body.String())
	}
	if got := callAPI(server.Server, http.MethodGet, "/api/machines/"+pveID+"/proxmox", nil); got.Body.String() != put.Body.String() {
		t.Fatalf("the put answers with the host card:\n%s\n%s", put.Body.String(), got.Body.String())
	}
	golden := strings.NewReplacer(api.URL, pveGoldenURL, api.pin(), pveGoldenPinHex).Replace(normalize(put.Body.String()))
	compareGolden(t, filepath.Join("testdata", "machine_proxmox.golden.json"), golden)

	var host proxmoxHostJSON
	decodeAPI(t, put, &host)
	if host.LastError != "" || host.LastReadAt == nil || host.Version != "8.2.4" || host.Node != "pve" || len(host.Guests) != 2 || len(host.Storages) != 1 {
		t.Fatalf("host: %+v", host)
	}
	if host.Guests[1].VMID != 5012 || host.Guests[1].LinkedMachineID == nil || *host.Guests[1].LinkedMachineID != dockerVMID || host.Guests[1].LastBackupAt == nil {
		t.Fatalf("the VM named like the machine must be linked and carry its backup: %+v", host.Guests)
	}
	if host.Guests[0].LinkedMachineID != nil {
		t.Fatalf("a VM nobody enrolled is not linked: %+v", host.Guests[0])
	}

	// La machine de l'hôte le porte ; celle de la VM le sait de l'autre côté.
	carrier := callAPI(server.Server, http.MethodGet, "/api/machines/"+pveID, nil)
	compareGolden(t, filepath.Join("testdata", "machine_hosting_proxmox.golden.json"), normalize(carrier.Body.String()))
	guest := callAPI(server.Server, http.MethodGet, "/api/machines/"+dockerVMID, nil)
	compareGolden(t, filepath.Join("testdata", "machine_hosted.golden.json"), normalize(guest.Body.String()))
	var listed machinesResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/machines", nil), &listed)
	for _, entry := range listed.Machines {
		switch entry.ID {
		case pveID:
			if !entry.Proxmox.Declared || entry.Proxmox.Guests != 2 || entry.Proxmox.Hosted != nil {
				t.Errorf("carrier: %+v", entry.Proxmox)
			}
		case dockerVMID:
			if entry.Proxmox.Declared || entry.Proxmox.Hosted == nil || entry.Proxmox.Hosted.HostMachineName != "pve" || entry.Proxmox.Hosted.VMID != 5012 {
				t.Errorf("guest: %+v", entry.Proxmox)
			}
		}
	}

	if got := callAPI(server.Server, http.MethodPost, "/api/machines/"+pveID+"/actions/remove-proxmox", nil); got.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodGet, "/api/machines/"+pveID+"/proxmox", nil); got.Code != http.StatusNotFound {
		t.Fatalf("get after delete: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/"+pveID+"/actions/remove-proxmox", nil); got.Code != http.StatusNotFound {
		t.Fatalf("second delete: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/inconnue/actions/remove-proxmox", nil); got.Code != http.StatusNotFound {
		t.Fatalf("delete on an unknown machine: %d", got.Code)
	}
	after := callAPI(server.Server, http.MethodGet, "/api/machines/"+dockerVMID, nil)
	if !strings.Contains(after.Body.String(), `"hosted":null`) {
		t.Fatalf("the guest keeps no link to a removed host: %s", after.Body.String())
	}
}

// Le secret n'est écrit nulle part où l'on peut le relire : ni dans une
// réponse, ni dans l'audit, ni dans le journal, même quand tout va mal.
func TestProxmox_TheSecretNeverLeaves(t *testing.T) {
	server, api, journal := proxmoxBench(t)
	responses := []string{}
	record := func(recorder *httptest.ResponseRecorder) { responses = append(responses, recorder.Body.String()) }

	record(putProxmox(server, pveID, api.request("wrong-secret-value")))
	record(putProxmox(server, pveID, api.request(pveTokenSecret)))
	record(callAPI(server.Server, http.MethodGet, "/api/machines/"+pveID+"/proxmox", nil))
	record(callAPI(server.Server, http.MethodGet, "/api/machines", nil))
	record(callAPI(server.Server, http.MethodGet, "/api/machines/"+pveID, nil))
	// Un relevé qui échoue : la raison est gardée, jamais le jeton.
	api.setBroken(true)
	if err := server.Server.proxmox.(*proxmox.Service).Refresh(context.Background(), pveID); err == nil {
		t.Fatal("the broken API must fail the reading")
	}
	record(callAPI(server.Server, http.MethodGet, "/api/machines/"+pveID+"/proxmox", nil))
	record(putProxmox(server, pveID, api.request(pveTokenSecret)))
	record(callAPI(server.Server, http.MethodPost, "/api/machines/"+pveID+"/actions/remove-proxmox", nil))

	events, err := server.db.ListAuditEvents(context.Background(), audit.Query{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var actions []audit.Action
	for _, event := range events {
		actions = append(actions, event.Action)
		for key, value := range event.Details {
			responses = append(responses, key+"="+value)
		}
	}
	responses = append(responses, journal.String())

	for index, text := range responses {
		if strings.Contains(text, pveTokenSecret) || strings.Contains(text, "wrong-secret-value") {
			t.Errorf("text %d carries a token secret: %s", index, text)
		}
	}
	if !strings.Contains(journal.String(), "proxmox") {
		t.Errorf("the journal must say a reading failed:\n%s", journal.String())
	}
	var set, unset int
	for _, action := range actions {
		switch action {
		case audit.MachineProxmoxSet:
			set++
		case audit.MachineProxmoxUnset:
			unset++
		}
	}
	if set != 2 || unset != 1 {
		t.Errorf("audit: %d declarations and %d removals, want 2 and 1 (%v)", set, unset, actions)
	}
}

// Un relevé qui échoue garde ce que le dernier a montré, et dit pourquoi.
func TestProxmox_AFailedReadingKeepsTheLastOneAndSaysWhy(t *testing.T) {
	server, api, _ := proxmoxBench(t)
	if got := putProxmox(server, pveID, api.request(pveTokenSecret)); got.Code != http.StatusOK {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	api.setBroken(true)
	refresh := server.Server.proxmox.(*proxmox.Service)
	if err := refresh.Refresh(context.Background(), pveID); err == nil {
		t.Fatal("expected a refusal")
	}

	var host proxmoxHostJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/machines/"+pveID+"/proxmox", nil), &host)
	if host.LastError != "proxmox.bad_response" || len(host.Guests) != 2 || host.LastReadAt == nil {
		t.Fatalf("host: %+v", host)
	}
	var carrier machineJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/machines/"+pveID, nil), &carrier)
	if carrier.Proxmox.LastError != "proxmox.bad_response" || carrier.Proxmox.Guests != 2 {
		t.Fatalf("summary: %+v", carrier.Proxmox)
	}

	api.setBroken(false)
	if err := refresh.Refresh(context.Background(), pveID); err != nil {
		t.Fatal(err)
	}
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/machines/"+pveID+"/proxmox", nil), &host)
	if host.LastError != "" {
		t.Fatalf("a good reading clears the failure: %+v", host)
	}
}

// Un jeton d'API lit la fiche de l'hôte, jamais ne la déclare ni ne la retire.
func TestProxmox_AnAPITokenReadsButNeverWrites(t *testing.T) {
	server, api, _ := proxmoxBench(t)
	if got := putProxmox(server, pveID, api.request(pveTokenSecret)); got.Code != http.StatusOK {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	bearer := server.issueAPIToken(t, "mcp-maison")

	read := callBearer(server.Server, "/api/machines/"+pveID+"/proxmox", bearer)
	if read.Code != http.StatusOK || strings.Contains(read.Body.String(), pveTokenSecret) {
		t.Fatalf("read: %d %s", read.Code, read.Body.String())
	}
	for method, path := range map[string]string{http.MethodPut: "/api/machines/" + pveID + "/proxmox", http.MethodPost: "/api/machines/" + pveID + "/actions/remove-proxmox"} {
		got := callBearerWith(server.Server, method, path, bearer, "")
		if got.Code != http.StatusForbidden || errorCode(t, got) != codeAPITokenRouteForbidden {
			t.Errorf("%s %s: %d %s", method, path, got.Code, got.Body.String())
		}
	}
	if after := callAPI(server.Server, http.MethodGet, "/api/machines/"+pveID+"/proxmox", nil); after.Code != http.StatusOK {
		t.Errorf("the host must still be declared: %d", after.Code)
	}
}

// Retirer la machine annonce ce qui part avec elle : l'hôte et ses relevés.
func TestProxmox_TheDeletePlanOfAMachineCountsTheHostAndTheLinks(t *testing.T) {
	server, api, _ := proxmoxBench(t)
	if got := putProxmox(server, pveID, api.request(pveTokenSecret)); got.Code != http.StatusOK {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	host := callAPI(server.Server, http.MethodGet, "/api/machines/"+pveID+"/delete-plan", nil)
	if !strings.Contains(host.Body.String(), `"kind":"proxmox_hosts","count":1`) {
		t.Errorf("host plan: %s", host.Body.String())
	}
	guest := callAPI(server.Server, http.MethodGet, "/api/machines/"+dockerVMID+"/delete-plan", nil)
	if !strings.Contains(guest.Body.String(), `"kind":"proxmox_links","count":1`) {
		t.Errorf("guest plan: %s", guest.Body.String())
	}
}

func postGuestAction(server *testServer, id, vmid, verb, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/machines/"+id+"/proxmox/guests/"+vmid+"/actions/"+verb, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

// Le serveur refuse avant d'envoyer, avec la clé du refus : un geste inconnu,
// un hôte non déclaré, un invité absent du relevé, un état incohérent, un
// instantané qui n'est pas de ce produit. Aucun run n'est écrit.
func TestProxmox_GuestActionRefusesBeforeSending(t *testing.T) {
	server, api, _ := proxmoxBench(t)
	if got := postGuestAction(server, pveID, "5012", "shutdown", "{}"); got.Code != http.StatusNotFound {
		t.Fatalf("undeclared host: %d %s", got.Code, got.Body.String())
	}
	if got := putProxmox(server, pveID, api.request(pveTokenSecret)); got.Code != http.StatusOK {
		t.Fatalf("declare: %d %s", got.Code, got.Body.String())
	}
	cases := map[string]struct {
		vmid, verb, body string
		status           int
		key              string
	}{
		"unknown verb":       {"5012", "reboot", "{}", http.StatusNotFound, "not_found"},
		"forced stop":        {"5012", "stop", "{}", http.StatusNotFound, "not_found"},
		"vmid not a number":  {"abc", "start", "{}", http.StatusNotFound, "not_found"},
		"unknown guest":      {"9999", "start", "{}", http.StatusConflict, "proxmox.guest_unknown"},
		"start a running":    {"5012", "start", "{}", http.StatusConflict, "proxmox.state_mismatch"},
		"shutdown a stopped": {"200", "shutdown", "{}", http.StatusConflict, "proxmox.state_mismatch"},
		"not our snapshot":   {"200", "snapshot-delete", `{"snapshot":"avant-migration"}`, http.StatusUnprocessableEntity, "proxmox.snapshot_invalid"},
		"bad body":           {"200", "start", "{", http.StatusBadRequest, "bad_json"},
	}
	for name, c := range cases {
		got := postGuestAction(server, pveID, c.vmid, c.verb, c.body)
		if got.Code != c.status || errorCode(t, got) != c.key {
			t.Errorf("%s: %d %s", name, got.Code, got.Body.String())
		}
	}
	runs, err := server.db.ListActionRuns(context.Background(), action.Query{MachineID: pveID, Limit: 10})
	if err != nil || len(runs) != 0 {
		t.Fatalf("a refusal wrote runs: %v %v", runs, err)
	}
}

// La cause du « Aucune » : avec PVEAuditor, l'hôte liste zéro archive sans
// erreur. La fiche le dit en clair, avec les commandes exactes, et ne montre
// jamais le secret.
func TestProxmox_AnAuditorTokenIsToldItCannotSeeTheArchives(t *testing.T) {
	server, api, journal := proxmoxBench(t)
	api.auditor = true

	put := putProxmox(server, pveID, api.request(pveTokenSecret))
	if put.Code != http.StatusOK {
		t.Fatalf("put: %d %s", put.Code, put.Body.String())
	}
	var host proxmoxHostJSON
	decodeAPI(t, put, &host)
	if host.LastError != "" || !slices.Equal(host.BlindStorages, []string{"local"}) || host.BackupGrant == nil {
		t.Fatalf("host: %+v", host)
	}
	want := "pveum acl modify /storage/local --roles LectureSauvegardes --tokens '" + pveTokenID + "'"
	if !slices.Contains(host.BackupGrant.Commands, want) {
		t.Errorf("commands: %v", host.BackupGrant.Commands)
	}
	for _, guest := range host.Guests {
		if guest.LastBackupAt != nil || !guest.BackupUnknown {
			t.Errorf("no archive was read: it is unknown, not none: %+v", guest)
		}
	}
	listed := callAPI(server.Server, http.MethodGet, "/api/machines", nil)
	if !strings.Contains(listed.Body.String(), `"backup_blind":true`) {
		t.Errorf("the machine list must say it: %s", listed.Body.String())
	}
	for name, text := range map[string]string{"card": put.Body.String(), "list": listed.Body.String(), "journal": journal.String()} {
		if strings.Contains(text, pveTokenSecret) {
			t.Errorf("the token secret leaked in the %s", name)
		}
	}
}

// « Relire maintenant » : la nouvelle lecture est rendue avec la fiche, un hôte
// qui refuse n'est pas une erreur de la demande, et la route est fermée aux jetons.
func TestProxmox_RefreshNowReadsAgainAndKeepsARefusalOnTheCard(t *testing.T) {
	server, api, _ := proxmoxBench(t)
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/"+pveID+"/actions/refresh-proxmox", nil); got.Code != http.StatusNotFound {
		t.Fatalf("an undeclared host: %d", got.Code)
	}
	if put := putProxmox(server, pveID, api.request(pveTokenSecret)); put.Code != http.StatusOK {
		t.Fatalf("put: %d %s", put.Code, put.Body.String())
	}
	api.auditor = true
	got := callAPI(server.Server, http.MethodPost, "/api/machines/"+pveID+"/actions/refresh-proxmox", nil)
	var host proxmoxHostJSON
	decodeAPI(t, got, &host)
	if got.Code != http.StatusOK || !slices.Equal(host.BlindStorages, []string{"local"}) {
		t.Fatalf("refresh: %d %+v", got.Code, host)
	}
	api.setBroken(true)
	got = callAPI(server.Server, http.MethodPost, "/api/machines/"+pveID+"/actions/refresh-proxmox", nil)
	decodeAPI(t, got, &host)
	if got.Code != http.StatusOK || host.LastError != "proxmox.bad_response" || !slices.Equal(host.BlindStorages, []string{"local"}) {
		t.Fatalf("a refusal must stay on the card: %d %+v", got.Code, host)
	}
}

// Un hôte garni, tel qu'une petite installation : quatre VM et conteneurs en
// marche ou arrêtés, un modèle, trois stockages dont un NFS de sauvegardes.
func richGuests(linkedName string) string {
	return `{"data":[
		{"type":"qemu","vmid":5012,"name":"` + linkedName + `","node":"pve","status":"running","template":0,"maxcpu":4,"maxmem":8589934592,"maxdisk":68719476736},
		{"type":"qemu","vmid":102,"name":"vpn","node":"pve","status":"running","template":0,"maxcpu":2,"maxmem":2147483648,"maxdisk":17179869184},
		{"type":"lxc","vmid":104,"name":"dns","node":"pve","status":"running","template":0,"maxcpu":1,"maxmem":536870912,"maxdisk":8589934592},
		{"type":"qemu","vmid":105,"name":"immich","node":"pve","status":"running","template":0,"maxcpu":4,"maxmem":17179869184,"maxdisk":274877906944},
		{"type":"qemu","vmid":201,"name":"windows-test","node":"pve","status":"stopped","template":0,"maxcpu":4,"maxmem":8589934592,"maxdisk":85899345920},
		{"type":"lxc","vmid":202,"name":"vieux-wiki","node":"pve","status":"stopped","template":0,"maxcpu":1,"maxmem":1073741824,"maxdisk":12884901888},
		{"type":"qemu","vmid":9000,"name":"debian-12","node":"pve","status":"stopped","template":1,"maxcpu":2,"maxmem":1073741824,"maxdisk":4294967296}]}`
}

const richStorages = `{"data":[
	{"type":"storage","storage":"local","node":"pve","plugintype":"dir","status":"available","content":"iso,vztmpl","maxdisk":107374182400,"disk":23622320128},
	{"type":"storage","storage":"local-lvm","node":"pve","plugintype":"lvmthin","status":"available","content":"images,rootdir","maxdisk":1099511627776,"disk":670000000000},
	{"type":"storage","storage":"pi-sauvegardes","node":"pve","plugintype":"nfs","status":"available","content":"backup","maxdisk":2199023255552,"disk":1759218604441}]}`

// Les archives de la nuit, datées d'après l'heure réelle : l'écran juge leur
// âge à l'horloge du navigateur. 9 h pour les VM sauvegardées chaque nuit, 3 jours
// pour immich, 20 jours pour le vieux wiki, aucune pour windows-test.
func richArchives() string {
	at := func(age time.Duration) int64 { return time.Now().Add(-age).Unix() }
	return fmt.Sprintf(`{"data":[
		{"vmid":5012,"ctime":%d,"size":13300000000,"volid":"pi-sauvegardes:backup/vzdump-qemu-5012-a"},
		{"vmid":102,"ctime":%d,"size":3300000000,"volid":"pi-sauvegardes:backup/vzdump-qemu-102-a"},
		{"vmid":104,"ctime":%d,"size":640000000,"volid":"pi-sauvegardes:backup/vzdump-lxc-104-a"},
		{"vmid":105,"ctime":%d,"size":44000000000,"volid":"pi-sauvegardes:backup/vzdump-qemu-105-a"},
		{"vmid":202,"ctime":%d,"size":1300000000,"volid":"pi-sauvegardes:backup/vzdump-lxc-202-a"}]}`,
		at(9*time.Hour), at(9*time.Hour), at(9*time.Hour), at(72*time.Hour), at(20*24*time.Hour))
}
