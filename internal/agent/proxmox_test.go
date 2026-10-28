package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/brand"
)

const (
	pveTestTokenID = "agent-test@pve!jeton"
	// Un faux secret de test.
	pveTestSecret = "0a1b2c3d-4e5f-6071-8293-a4b5c6d7e8f9" // gitleaks:allow
	pveTestNode   = "pve1"
)

var pveSnapshotName = regexp.MustCompile(`^oc-20261021-120000$`)

// fakeGuest est un invité du faux Proxmox.
type fakeGuest struct {
	vmid      int
	kind      string
	status    string
	template  int
	snapshots []string
}

// fakePVE joue l'API locale d'un hôte Proxmox en HTTPS (certificat de test,
// jugé par empreinte) : les invités, leurs états, les tâches UPID. Rien ne
// joint un vrai hôte.
type fakePVE struct {
	*httptest.Server
	mu     sync.Mutex
	guests []*fakeGuest
	// Comment les tâches finissent : « ok », « fail » (exitstatus d'erreur) ou
	// « hang » (elles courent toujours).
	taskMode string
	// Le statut HTTP forcé, 0 pour répondre normalement.
	forcedStatus int
	calls        []string
	forms        []url.Values
}

func newFakePVE(t *testing.T) *fakePVE {
	t.Helper()
	api := &fakePVE{taskMode: "ok", guests: []*fakeGuest{
		{vmid: 105, kind: "qemu", status: "stopped"},
		{vmid: 106, kind: "qemu", status: "running", snapshots: []string{"avant-migration", "oc-20261021-120000"}},
		{vmid: 107, kind: "qemu", status: "stopped", template: 1},
		{vmid: 200, kind: "lxc", status: "running"},
	}}
	api.Server = httptest.NewTLSServer(http.HandlerFunc(api.serve))
	t.Cleanup(api.Close)
	return api
}

func (api *fakePVE) fingerprint() string {
	sum := sha256.Sum256(api.Certificate().Raw)
	return hex.EncodeToString(sum[:])
}

func (api *fakePVE) callsMade() []string {
	api.mu.Lock()
	defer api.mu.Unlock()
	return append([]string(nil), api.calls...)
}

func (api *fakePVE) guest(vmid int) *fakeGuest {
	for _, guest := range api.guests {
		if guest.vmid == vmid {
			return guest
		}
	}
	return nil
}

var upidIn = regexp.MustCompile(`/nodes/pve1/(qemu|lxc)/(\d+)/(status/(start|shutdown)|snapshot(/[^/]+)?)$`)

func (api *fakePVE) serve(w http.ResponseWriter, r *http.Request) {
	api.mu.Lock()
	defer api.mu.Unlock()
	if r.Header.Get("Authorization") != "PVEAPIToken="+pveTestTokenID+"="+pveTestSecret {
		http.Error(w, `{"data":null}`, http.StatusUnauthorized)
		return
	}
	_ = r.ParseForm()
	api.calls = append(api.calls, r.Method+" "+r.URL.RequestURI())
	api.forms = append(api.forms, r.PostForm)
	if api.forcedStatus != 0 {
		http.Error(w, `{"data":null,"message":"forced"}`, api.forcedStatus)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api2/json")
	switch {
	case r.Method == http.MethodGet && path == "/cluster/resources":
		api.writeResources(w)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/status/current"):
		api.writeCurrent(w, path)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/snapshot"):
		api.writeSnapshots(w, path)
	case r.Method == http.MethodGet && strings.Contains(path, "/tasks/"):
		api.writeTask(w)
	case upidIn.MatchString(path) && (r.Method == http.MethodPost || r.Method == http.MethodDelete):
		api.startTask(w, r, path)
	default:
		http.NotFound(w, r)
	}
}

func (api *fakePVE) writeResources(w http.ResponseWriter) {
	rows := []map[string]any{}
	for _, guest := range api.guests {
		rows = append(rows, map[string]any{"type": guest.kind, "vmid": guest.vmid, "node": pveTestNode, "status": guest.status, "template": guest.template})
	}
	writeData(w, rows)
}

func (api *fakePVE) writeCurrent(w http.ResponseWriter, path string) {
	var kind string
	var vmid int
	if _, err := fmt.Sscanf(path, "/nodes/pve1/%2s/%d/status/current", &kind, &vmid); err != nil && api.guestOf(path) == nil {
		http.NotFound(w, nil)
		return
	}
	writeData(w, map[string]any{"status": api.guestOf(path).status})
}

// guestOf retrouve l'invité d'un chemin /nodes/pve1/<sorte>/<numéro>/…
func (api *fakePVE) guestOf(path string) *fakeGuest {
	fields := strings.Split(strings.TrimPrefix(path, "/nodes/pve1/"), "/")
	if len(fields) < 2 {
		return nil
	}
	var vmid int
	if _, err := fmt.Sscanf(fields[1], "%d", &vmid); err != nil {
		return nil
	}
	return api.guest(vmid)
}

func (api *fakePVE) writeSnapshots(w http.ResponseWriter, path string) {
	rows := []map[string]any{{"name": "current"}}
	for _, name := range api.guestOf(path).snapshots {
		rows = append(rows, map[string]any{"name": name})
	}
	writeData(w, rows)
}

// startTask répond par un UPID ; la tâche, elle, se juge à sa lecture.
func (api *fakePVE) startTask(w http.ResponseWriter, r *http.Request, path string) {
	guest := api.guestOf(path)
	switch {
	case strings.HasSuffix(path, "/status/start"):
		guest.status = "running"
	case strings.HasSuffix(path, "/status/shutdown"):
		guest.status = "stopped"
	case r.Method == http.MethodPost:
		guest.snapshots = append(guest.snapshots, r.PostForm.Get("snapname"))
	}
	writeData(w, "UPID:pve1:0000ABCD:0001F2E3:6540A1B2:qmtask:105:"+pveTestTokenID+":")
}

func (api *fakePVE) writeTask(w http.ResponseWriter) {
	switch api.taskMode {
	case "hang":
		writeData(w, map[string]any{"status": "running"})
	case "fail":
		writeData(w, map[string]any{"status": "stopped", "exitstatus": "VM is locked (backup)"})
	default:
		writeData(w, map[string]any{"status": "stopped", "exitstatus": "OK"})
	}
}

func writeData(w http.ResponseWriter, data any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

// proxmoxRig est un agent qui a ces droits, dont le worker tourne, sur un
// faux Proxmox dont le jeton local est posé dans son état. Le journal de
// l'agent est gardé : aucun test ne doit y lire le secret.
type proxmoxRig struct {
	*orderBench
	pve  *fakePVE
	logs *bytes.Buffer
	seq  int64
}

func newProxmoxRig(t *testing.T, rights ...string) *proxmoxRig {
	t.Helper()
	b := newOrderBenchWithRights(t, append([]string{"observe"}, rights...))
	pve := newFakePVE(t)
	logs := &bytes.Buffer{}
	b.runner.logger = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	b.runner.proxmox.apiURL = pve.URL
	b.runner.proxmox.pollPeriod = time.Millisecond
	b.runner.proxmox.timeout = 5 * time.Second
	b.runner.proxmox.now = func() time.Time { return time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC) }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.runner.worker.run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	rig := &proxmoxRig{orderBench: b, pve: pve, logs: logs}
	rig.writeToken(t, pveTestTokenID, pveTestSecret, pve.fingerprint(), 0o600)
	return rig
}

func (r *proxmoxRig) writeToken(t *testing.T, id, secret, fingerprint string, mode os.FileMode) {
	t.Helper()
	content, err := json.Marshal(map[string]string{"token_id": id, "secret": secret, "fingerprint": fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.stateDir.Name(), brand.AgentProxmoxFile)
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile garde le mode d'un fichier qui existe déjà.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// run envoie l'ordre et rend ses résultats : « accepté » puis la fin, ou le
// refus seul.
func (r *proxmoxRig) run(t *testing.T, kind agentwire.Kind, params any) []agentwire.ActionResult {
	t.Helper()
	r.seq++
	body := longOrder(r.seq, kind, params)
	r.runner.receive(r.signed(t, body))
	return r.until(t, body.RunID)
}

func guestParams(vmid int, kind string) agentwire.ProxmoxGuestParams {
	return agentwire.ProxmoxGuestParams{VMID: vmid, Type: kind}
}

func finalOf(t *testing.T, results []agentwire.ActionResult) agentwire.ActionResult {
	t.Helper()
	if len(results) == 0 {
		t.Fatal("no result")
	}
	return results[len(results)-1]
}

func expectResult(t *testing.T, results []agentwire.ActionResult, state agentwire.ActionState, reason string) agentwire.ActionResult {
	t.Helper()
	final := finalOf(t, results)
	if final.State != state || final.Reason != reason {
		t.Fatalf("result %+v, want %s %q", results, state, reason)
	}
	return final
}

// writes ne garde que les appels qui changent quelque chose.
func writes(calls []string) []string {
	out := []string{}
	for _, call := range calls {
		if !strings.HasPrefix(call, "GET ") {
			out = append(out, call)
		}
	}
	return out
}

func TestProxmoxStart_StartsAStoppedGuestAndReportsTheTaskIssue(t *testing.T) {
	rig := newProxmoxRig(t, "proxmox.power")
	results := rig.run(t, agentwire.KindProxmoxStart, guestParams(105, "qemu"))
	if len(results) != 2 || results[0].State != agentwire.ActionAccepted {
		t.Fatalf("results %+v", results)
	}
	final := expectResult(t, results, agentwire.ActionSucceeded, "")
	if got := lines(final.Output); len(got) != 2 || !strings.HasPrefix(got[0], "task UPID:pve1:") || got[1] != "started qemu/105" {
		t.Fatalf("output %q", got)
	}
	if got := writes(rig.pve.callsMade()); len(got) != 1 || got[0] != "POST /api2/json/nodes/pve1/qemu/105/status/start" {
		t.Fatalf("writes %q", got)
	}
}

func TestProxmoxShutdown_AsksForACleanShutdownNeverAForcedOne(t *testing.T) {
	rig := newProxmoxRig(t, "proxmox.power")
	final := expectResult(t, rig.run(t, agentwire.KindProxmoxShutdown, guestParams(106, "qemu")), agentwire.ActionSucceeded, "")
	if got := lines(final.Output); got[len(got)-1] != "shutdown qemu/106" {
		t.Fatalf("output %q", got)
	}
	rig.pve.mu.Lock()
	defer rig.pve.mu.Unlock()
	for index, call := range rig.pve.calls {
		if strings.HasSuffix(call, "/status/shutdown") && len(rig.pve.forms[index]) != 0 {
			t.Errorf("the shutdown carried parameters %v: no forceStop, no timeout", rig.pve.forms[index])
		}
	}
}

func TestProxmoxSnapshot_NamesItItselfWithoutMemoryNorDescription(t *testing.T) {
	rig := newProxmoxRig(t, "proxmox.snapshot")
	final := expectResult(t, rig.run(t, agentwire.KindProxmoxSnapshot, guestParams(200, "lxc")), agentwire.ActionSucceeded, "")
	if got := lines(final.Output); got[len(got)-1] != "snapshot lxc/200 oc-20261021-120000" {
		t.Fatalf("output %q", got)
	}
	rig.pve.mu.Lock()
	defer rig.pve.mu.Unlock()
	for index, call := range rig.pve.calls {
		if !strings.HasSuffix(call, "/lxc/200/snapshot") {
			continue
		}
		form := rig.pve.forms[index]
		if len(form) != 1 || !pveSnapshotName.MatchString(form.Get("snapname")) {
			t.Errorf("the snapshot form is %v: only a generated snapname", form)
		}
	}
}

func TestProxmoxSnapshotDelete_DeletesItsOwnSnapshotOnly(t *testing.T) {
	rig := newProxmoxRig(t, "proxmox.snapshot")
	params := guestParams(106, "qemu")
	params.Snapshot = "oc-20261021-120000"
	final := expectResult(t, rig.run(t, agentwire.KindProxmoxSnapshotDelete, params), agentwire.ActionSucceeded, "")
	if got := lines(final.Output); got[len(got)-1] != "snapshot_deleted qemu/106 oc-20261021-120000" {
		t.Fatalf("output %q", got)
	}
	if got := writes(rig.pve.callsMade()); len(got) != 1 || got[0] != "DELETE /api2/json/nodes/pve1/qemu/106/snapshot/oc-20261021-120000" {
		t.Fatalf("writes %q", got)
	}
}

func TestProxmoxSnapshotDelete_RefusesAnySnapshotItDidNotCreate(t *testing.T) {
	for _, name := range []string{"avant-migration", "oc-manuel", "oc-20261021-120000/../../x", "OC-20261021-120000", "", "current"} {
		rig := newProxmoxRig(t, "proxmox.snapshot")
		params := guestParams(106, "qemu")
		params.Snapshot = name
		expectResult(t, rig.run(t, agentwire.KindProxmoxSnapshotDelete, params), agentwire.ActionRefused, agentwire.ReasonProxmoxSnapshotNotOurs)
		if calls := rig.pve.callsMade(); len(calls) != 0 {
			t.Errorf("%q: the agent called Proxmox before refusing: %q", name, calls)
		}
	}
}

func TestProxmoxSnapshotDelete_RefusesAnOwnSnapshotThatIsNotThere(t *testing.T) {
	rig := newProxmoxRig(t, "proxmox.snapshot")
	params := guestParams(105, "qemu")
	params.Snapshot = "oc-20261021-120000"
	expectResult(t, rig.run(t, agentwire.KindProxmoxSnapshotDelete, params), agentwire.ActionRefused, agentwire.ReasonProxmoxSnapshotMissing)
	if got := writes(rig.pve.callsMade()); len(got) != 0 {
		t.Fatalf("writes %q", got)
	}
}

func TestProxmoxOrders_RefuseAnInconsistentStateWithoutWriting(t *testing.T) {
	cases := map[string]struct {
		kind   agentwire.Kind
		params agentwire.ProxmoxGuestParams
	}{
		"start a running guest":     {agentwire.KindProxmoxStart, guestParams(106, "qemu")},
		"shutdown a stopped guest":  {agentwire.KindProxmoxShutdown, guestParams(105, "qemu")},
		"start a running container": {agentwire.KindProxmoxStart, guestParams(200, "lxc")},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rig := newProxmoxRig(t, "proxmox.power")
			expectResult(t, rig.run(t, c.kind, c.params), agentwire.ActionRefused, agentwire.ReasonProxmoxStateMismatch)
			if got := writes(rig.pve.callsMade()); len(got) != 0 {
				t.Fatalf("writes %q", got)
			}
		})
	}
}

func TestProxmoxOrders_RefuseATemplateAndAGuestThatIsNotThere(t *testing.T) {
	rig := newProxmoxRig(t, "proxmox.power", "proxmox.snapshot")
	expectResult(t, rig.run(t, agentwire.KindProxmoxStart, guestParams(107, "qemu")), agentwire.ActionRefused, agentwire.ReasonProxmoxTemplate)
	expectResult(t, rig.run(t, agentwire.KindProxmoxSnapshot, guestParams(107, "qemu")), agentwire.ActionRefused, agentwire.ReasonProxmoxTemplate)
	expectResult(t, rig.run(t, agentwire.KindProxmoxStart, guestParams(999, "qemu")), agentwire.ActionRefused, agentwire.ReasonProxmoxGuestMissing)
	// Le numéro existe, pas de cette sorte.
	expectResult(t, rig.run(t, agentwire.KindProxmoxStart, guestParams(105, "lxc")), agentwire.ActionRefused, agentwire.ReasonProxmoxGuestMissing)
	if got := writes(rig.pve.callsMade()); len(got) != 0 {
		t.Fatalf("writes %q", got)
	}
}

func TestProxmoxOrders_TheRightOfEachKindIsEnforced(t *testing.T) {
	power := newProxmoxRig(t, "proxmox.power")
	expectResult(t, power.run(t, agentwire.KindProxmoxSnapshot, guestParams(105, "qemu")), agentwire.ActionRefused, agentwire.RightMissing("proxmox.snapshot"))
	snapshot := newProxmoxRig(t, "proxmox.snapshot")
	expectResult(t, snapshot.run(t, agentwire.KindProxmoxStart, guestParams(105, "qemu")), agentwire.ActionRefused, agentwire.RightMissing("proxmox.power"))
	expectResult(t, snapshot.run(t, agentwire.KindProxmoxShutdown, guestParams(106, "qemu")), agentwire.ActionRefused, agentwire.RightMissing("proxmox.power"))
	none := newProxmoxRig(t)
	expectResult(t, none.run(t, agentwire.KindProxmoxStart, guestParams(105, "qemu")), agentwire.ActionRefused, agentwire.RightMissing("proxmox.power"))
	for _, rig := range []*proxmoxRig{power, snapshot, none} {
		if calls := rig.pve.callsMade(); len(calls) != 0 {
			t.Errorf("a refused right still called Proxmox: %q", calls)
		}
	}
}

func TestProxmoxOrders_RefuseParametersThatAreNotStrict(t *testing.T) {
	rig := newProxmoxRig(t, "proxmox.power", "proxmox.snapshot")
	cases := map[string]struct {
		kind   agentwire.Kind
		params any
	}{
		"an unknown field":            {agentwire.KindProxmoxStart, map[string]any{"vmid": 105, "type": "qemu", "force": true}},
		"a vmid below 100":            {agentwire.KindProxmoxStart, guestParams(5, "qemu")},
		"a vmid as text":              {agentwire.KindProxmoxStart, map[string]any{"vmid": "105", "type": "qemu"}},
		"an unknown type":             {agentwire.KindProxmoxStart, guestParams(105, "kvm")},
		"a snapshot name to a start":  {agentwire.KindProxmoxStart, agentwire.ProxmoxGuestParams{VMID: 105, Type: "qemu", Snapshot: "oc-20261021-120000"}},
		"a snapshot name to creation": {agentwire.KindProxmoxSnapshot, agentwire.ProxmoxGuestParams{VMID: 105, Type: "qemu", Snapshot: "oc-20261021-120000"}},
	}
	for name, c := range cases {
		results := rig.run(t, c.kind, c.params)
		if final := finalOf(t, results); final.Reason != agentwire.ReasonBadOrder {
			t.Errorf("%s: %+v", name, results)
		}
	}
	if calls := rig.pve.callsMade(); len(calls) != 0 {
		t.Fatalf("calls %q", calls)
	}
}

func TestProxmoxOrders_ATaskThatFailsIsAFailureWithItsRealExitStatus(t *testing.T) {
	rig := newProxmoxRig(t, "proxmox.power")
	rig.pve.taskMode = "fail"
	final := expectResult(t, rig.run(t, agentwire.KindProxmoxStart, guestParams(105, "qemu")), agentwire.ActionFailed, agentwire.ReasonProxmoxTaskFailed)
	if got := strings.Join(lines(final.Output), "\n"); !strings.Contains(got, "VM is locked (backup)") {
		t.Fatalf("the exit status of the task is not reported: %q", got)
	}
}

func TestProxmoxOrders_ATaskThatDragsTimesOutWithoutForcingAnything(t *testing.T) {
	rig := newProxmoxRig(t, "proxmox.power")
	rig.pve.taskMode = "hang"
	rig.runner.proxmox.timeout = 150 * time.Millisecond
	expectResult(t, rig.run(t, agentwire.KindProxmoxShutdown, guestParams(106, "qemu")), agentwire.ActionFailed, agentwire.ReasonProxmoxTaskTimeout)
	if got := writes(rig.pve.callsMade()); len(got) != 1 || !strings.HasSuffix(got[0], "/status/shutdown") {
		t.Fatalf("after the timeout the only write must be the shutdown, got %q", got)
	}
}

func TestProxmoxOrders_AWrongFingerprintSendsNothingNotEvenTheToken(t *testing.T) {
	rig := newProxmoxRig(t, "proxmox.power")
	rig.writeToken(t, pveTestTokenID, pveTestSecret, strings.Repeat("ab", 32), 0o600)
	expectResult(t, rig.run(t, agentwire.KindProxmoxStart, guestParams(105, "qemu")), agentwire.ActionRefused, agentwire.ReasonProxmoxCertificate)
	if calls := rig.pve.callsMade(); len(calls) != 0 {
		t.Fatalf("the request reached the server: %q", calls)
	}
}

func TestProxmoxOrders_NeverConnectWithoutAFingerprint(t *testing.T) {
	cases := map[string]string{"empty": "", "not hexadecimal": "zz:zz", "too short": "abcd"}
	for name, fingerprint := range cases {
		t.Run(name, func(t *testing.T) {
			rig := newProxmoxRig(t, "proxmox.power")
			rig.writeToken(t, pveTestTokenID, pveTestSecret, fingerprint, 0o600)
			expectResult(t, rig.run(t, agentwire.KindProxmoxStart, guestParams(105, "qemu")), agentwire.ActionRefused, agentwire.ReasonProxmoxPinMissing)
			if calls := rig.pve.callsMade(); len(calls) != 0 {
				t.Fatalf("calls %q", calls)
			}
		})
	}
}

func TestProxmoxOrders_TheFingerprintMayBeWrittenAsOpensslDoes(t *testing.T) {
	rig := newProxmoxRig(t, "proxmox.power")
	fingerprint := strings.ToUpper(rig.pve.fingerprint())
	var colons []string
	for index := 0; index < len(fingerprint); index += 2 {
		colons = append(colons, fingerprint[index:index+2])
	}
	rig.writeToken(t, pveTestTokenID, pveTestSecret, strings.Join(colons, ":"), 0o600)
	expectResult(t, rig.run(t, agentwire.KindProxmoxStart, guestParams(105, "qemu")), agentwire.ActionSucceeded, "")
}

func TestProxmoxOrders_RefuseWithoutAUsableTokenFile(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		rig := newProxmoxRig(t, "proxmox.power")
		if err := os.Remove(filepath.Join(rig.stateDir.Name(), brand.AgentProxmoxFile)); err != nil {
			t.Fatal(err)
		}
		expectResult(t, rig.run(t, agentwire.KindProxmoxStart, guestParams(105, "qemu")), agentwire.ActionRefused, agentwire.ReasonProxmoxTokenMissing)
	})
	t.Run("readable by others", func(t *testing.T) {
		rig := newProxmoxRig(t, "proxmox.power")
		rig.writeToken(t, pveTestTokenID, pveTestSecret, rig.pve.fingerprint(), 0o640)
		expectResult(t, rig.run(t, agentwire.KindProxmoxStart, guestParams(105, "qemu")), agentwire.ActionRefused, agentwire.ReasonProxmoxTokenUnsafe)
	})
	t.Run("malformed", func(t *testing.T) {
		rig := newProxmoxRig(t, "proxmox.power")
		path := filepath.Join(rig.stateDir.Name(), brand.AgentProxmoxFile)
		if err := os.WriteFile(path, []byte(`{"token_id":"x","secret":"y"`), 0o600); err != nil {
			t.Fatal(err)
		}
		expectResult(t, rig.run(t, agentwire.KindProxmoxStart, guestParams(105, "qemu")), agentwire.ActionRefused, agentwire.ReasonProxmoxTokenMissing)
	})
	t.Run("an unknown field", func(t *testing.T) {
		rig := newProxmoxRig(t, "proxmox.power")
		path := filepath.Join(rig.stateDir.Name(), brand.AgentProxmoxFile)
		content := fmt.Sprintf(`{"token_id":%q,"secret":%q,"fingerprint":%q,"url":"https://elsewhere:8006"}`, pveTestTokenID, pveTestSecret, rig.pve.fingerprint())
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		expectResult(t, rig.run(t, agentwire.KindProxmoxStart, guestParams(105, "qemu")), agentwire.ActionRefused, agentwire.ReasonProxmoxTokenMissing)
	})
	t.Run("a link out of the state", func(t *testing.T) {
		rig := newProxmoxRig(t, "proxmox.power")
		outside := filepath.Join(t.TempDir(), "elsewhere.json")
		content := fmt.Sprintf(`{"token_id":%q,"secret":%q,"fingerprint":%q}`, pveTestTokenID, pveTestSecret, rig.pve.fingerprint())
		if err := os.WriteFile(outside, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(rig.stateDir.Name(), brand.AgentProxmoxFile)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, path); err != nil {
			t.Fatal(err)
		}
		expectResult(t, rig.run(t, agentwire.KindProxmoxStart, guestParams(105, "qemu")), agentwire.ActionRefused, agentwire.ReasonProxmoxTokenMissing)
	})
}

func TestProxmoxOrders_ARefusedTokenIsReportedAsSuch(t *testing.T) {
	rig := newProxmoxRig(t, "proxmox.power")
	rig.writeToken(t, pveTestTokenID, "00000000-0000-0000-0000-000000000000", rig.pve.fingerprint(), 0o600)
	expectResult(t, rig.run(t, agentwire.KindProxmoxStart, guestParams(105, "qemu")), agentwire.ActionRefused, agentwire.ReasonProxmoxUnauthorized)
	rig.writeToken(t, pveTestTokenID, pveTestSecret, rig.pve.fingerprint(), 0o600)
	rig.pve.forcedStatus = http.StatusForbidden
	expectResult(t, rig.run(t, agentwire.KindProxmoxStart, guestParams(105, "qemu")), agentwire.ActionRefused, agentwire.ReasonProxmoxUnauthorized)
}

func TestProxmoxOrders_AnApiThatAnswersAnErrorOrRedirectsIsABadResponse(t *testing.T) {
	rig := newProxmoxRig(t, "proxmox.power")
	rig.pve.forcedStatus = http.StatusInternalServerError
	expectResult(t, rig.run(t, agentwire.KindProxmoxStart, guestParams(105, "qemu")), agentwire.ActionRefused, agentwire.ReasonProxmoxBadResponse)
	rig.pve.forcedStatus = http.StatusFound
	expectResult(t, rig.run(t, agentwire.KindProxmoxStart, guestParams(105, "qemu")), agentwire.ActionRefused, agentwire.ReasonProxmoxBadResponse)
}

func TestProxmoxOrders_AnApiThatDoesNotAnswerIsUnreachable(t *testing.T) {
	rig := newProxmoxRig(t, "proxmox.power")
	rig.pve.Close()
	expectResult(t, rig.run(t, agentwire.KindProxmoxStart, guestParams(105, "qemu")), agentwire.ActionRefused, agentwire.ReasonProxmoxUnreachable)
}

// Ce que l'agent appelle est une liste fermée : jamais un arrêt forcé, un
// redémarrage, un retour en arrière, une suppression d'invité, un changement de
// configuration. Tous les ordres et tous les refus passent ici.
func TestProxmoxOrders_OnlyTheFourGesturesEverReachProxmox(t *testing.T) {
	rig := newProxmoxRig(t, "proxmox.power", "proxmox.snapshot")
	deleteParams := guestParams(106, "qemu")
	deleteParams.Snapshot = "oc-20261021-120000"
	rig.run(t, agentwire.KindProxmoxStart, guestParams(105, "qemu"))
	rig.run(t, agentwire.KindProxmoxShutdown, guestParams(106, "qemu"))
	rig.run(t, agentwire.KindProxmoxSnapshot, guestParams(200, "lxc"))
	rig.run(t, agentwire.KindProxmoxSnapshotDelete, deleteParams)
	rig.pve.taskMode = "fail"
	rig.run(t, agentwire.KindProxmoxStart, guestParams(105, "qemu"))
	allowed := regexp.MustCompile(`^(GET /api2/json/(cluster/resources\?type=vm|nodes/pve1/(qemu|lxc)/\d+/(status/current|snapshot)|nodes/pve1/tasks/[^/]+/status)` +
		`|POST /api2/json/nodes/pve1/(qemu|lxc)/\d+/(status/(start|shutdown)|snapshot)` +
		`|DELETE /api2/json/nodes/pve1/(qemu|lxc)/\d+/snapshot/oc-\d{8}-\d{6})$`)
	for _, call := range rig.pve.callsMade() {
		if !allowed.MatchString(call) {
			t.Errorf("a call outside the four gestures: %s", call)
		}
		for _, forbidden := range []string{"forceStop", "stop", "reset", "reboot", "rollback", "vmstate", "description", "force"} {
			if strings.Contains(strings.ToLower(call), strings.ToLower(forbidden)) && !strings.Contains(call, "snapshot/") {
				t.Errorf("%s: %q must never be asked", call, forbidden)
			}
		}
	}
	rig.pve.mu.Lock()
	defer rig.pve.mu.Unlock()
	for _, form := range rig.pve.forms {
		for key := range form {
			if key != "snapname" {
				t.Errorf("a parameter %q was sent: only snapname is", key)
			}
		}
	}
}

func TestProxmoxOrders_TheSecretIsNowhereInTheLogsTheResultsOrTheErrors(t *testing.T) {
	rig := newProxmoxRig(t, "proxmox.power", "proxmox.snapshot")
	var everything []string
	collect := func(results []agentwire.ActionResult) {
		for _, result := range results {
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			everything = append(everything, string(encoded))
		}
	}
	collect(rig.run(t, agentwire.KindProxmoxStart, guestParams(105, "qemu")))
	rig.pve.taskMode = "fail"
	collect(rig.run(t, agentwire.KindProxmoxShutdown, guestParams(106, "qemu")))
	rig.pve.taskMode = "ok"
	rig.pve.forcedStatus = http.StatusInternalServerError
	collect(rig.run(t, agentwire.KindProxmoxSnapshot, guestParams(200, "lxc")))
	rig.pve.forcedStatus = 0
	rig.writeToken(t, pveTestTokenID, pveTestSecret, strings.Repeat("ab", 32), 0o600)
	collect(rig.run(t, agentwire.KindProxmoxStart, guestParams(105, "qemu")))
	rig.pve.Close()
	rig.writeToken(t, pveTestTokenID, pveTestSecret, "ab", 0o600)
	collect(rig.run(t, agentwire.KindProxmoxStart, guestParams(105, "qemu")))
	everything = append(everything, rig.logs.String())
	for _, text := range everything {
		if strings.Contains(text, pveTestSecret) {
			t.Fatalf("the token secret leaked: %q", text)
		}
	}
	if rig.logs.Len() == 0 {
		t.Fatal("the agent logged nothing: the test would prove nothing")
	}
	saved, err := os.ReadFile(filepath.Join(rig.stateDir.Name(), "identity.json"))
	if err != nil || bytes.Contains(saved, []byte(pveTestSecret)) {
		t.Fatalf("the identity file holds the secret: %v", err)
	}
}

func TestJudgeRights_ProxmoxRightsFollowTheTokenFile(t *testing.T) {
	rig := newProxmoxRig(t, "proxmox.power", "proxmox.snapshot")
	// Le vrai juge du jeton, pas celui des bancs.
	checks := allowAll()
	checks.proxmoxToken = func() string {
		_, reason := readProxmoxToken(rig.stateDir)
		return reason
	}
	identity := rig.runner.identity
	if got := judgeRights(context.Background(), identity, checks); len(got) != 0 {
		t.Fatalf("a good token is not unavailable: %v", got)
	}
	rig.writeToken(t, pveTestTokenID, pveTestSecret, rig.pve.fingerprint(), 0o644)
	got := judgeRights(context.Background(), identity, checks)
	if got["proxmox.power"] != agentwire.ReasonProxmoxTokenUnsafe || got["proxmox.snapshot"] != agentwire.ReasonProxmoxTokenUnsafe {
		t.Fatalf("unsafe: %v", got)
	}
	if err := os.Remove(filepath.Join(rig.stateDir.Name(), brand.AgentProxmoxFile)); err != nil {
		t.Fatal(err)
	}
	got = judgeRights(context.Background(), identity, checks)
	if got["proxmox.power"] != agentwire.ReasonProxmoxTokenMissing {
		t.Fatalf("missing: %v", got)
	}
}
