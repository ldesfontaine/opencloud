package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/goldentest"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

// Les deux machines du banc restic : la source (la machine distante des
// tests, nommée « vps ») et la cible « nas ». Des noms d'exemple, aucune
// adresse réelle : 192.0.2.10 est une adresse de documentation.
const (
	resticSourceID   = remoteID
	resticTargetID   = "22222222-3333-4444-8555-666666666666"
	resticTargetHost = "192.0.2.10"
	resticTargetPort = 8000
)

// resticBench : la source et la cible enrôlées et en ligne, avec leurs
// droits ; la cible posée et en marche (son rapport reçu), et un service
// Compose déclaré sur la source.
type resticBench struct {
	*testServer
	source    *machine.Session
	target    *machine.Session
	sourceKey ed25519.PrivateKey
	serviceID string
	// Les mots de passe du lien, tels que les ordres les ont portés.
	provisioned agentwire.ResticProvisionSecret
}

func newResticBench(t *testing.T, sourceRights, targetRights string) *resticBench {
	t.Helper()
	server := newTestServer(t)
	ctx := context.Background()
	sourceMachine, sourceKey := server.enroll(t, "vps", resticSourceID)
	targetMachine, _ := server.enroll(t, "nas", resticTargetID)
	source, err := server.machines.Connect(ctx, sourceMachine, "192.0.2.21", "v0.0.1", sourceRights, "", "")
	if err != nil {
		t.Fatal(err)
	}
	target, err := server.machines.Connect(ctx, targetMachine, "192.0.2.10", "v0.0.1", targetRights, "", "")
	if err != nil {
		t.Fatal(err)
	}
	bench := &resticBench{testServer: server, source: source, target: target, sourceKey: sourceKey}
	created := callAPI(server.Server, http.MethodPost, "/api/directory/services", nextcloudRequest(resticSourceID))
	if created.Code != http.StatusCreated {
		t.Fatalf("service: %d %s", created.Code, created.Body.String())
	}
	var service declaredServiceJSON
	decodeAPI(t, created, &service)
	bench.serviceID = service.ID
	return bench
}

// readyResticBench pose la cible par l'API et fait rapporter son rest-server
// en marche, avec son certificat public.
func readyResticBench(t *testing.T) *resticBench {
	t.Helper()
	return readyResticBenchRights(t, "observe,restic.target=/srv/restic")
}

// readyResticBenchRights fait de même avec les droits donnés à la cible.
func readyResticBenchRights(t *testing.T, targetRights string) *resticBench {
	t.Helper()
	bench := newResticBench(t, "observe,restic.produce", targetRights)
	got := callAPI(bench.Server, http.MethodPost, "/api/machines/"+resticTargetID+"/actions/restic-target-setup",
		map[string]any{"host": resticTargetHost, "port": resticTargetPort})
	if got.Code != http.StatusNoContent {
		t.Fatalf("setup: %d %s", got.Code, got.Body.String())
	}
	order := nextOrder(t, bench.target)
	var params agentwire.ResticTargetSetupParams
	if order.Kind != agentwire.KindResticTargetSetup || json.Unmarshal(order.Params, &params) != nil ||
		params.Port != resticTargetPort || len(params.Hosts) != 1 || params.Hosts[0] != resticTargetHost || params.Listen != "0.0.0.0" {
		t.Fatalf("setup order %s %s", order.Kind, order.Params)
	}
	certificate, fingerprint := testCertificate(t)
	report := agentwire.ResticReport{Target: &agentwire.ResticTargetReport{
		State: agentwire.ResticTargetRunning, Version: "rest-server 0.14.0", Host: resticTargetHost, Port: resticTargetPort,
		Certificate: certificate, Fingerprint: fingerprint, RepoBytes: 1 << 30, DiskTotal: 2 << 40, DiskFree: 600 << 30,
		RetentionFloor: &agentwire.ResticRetentionFloor{KeepDaily: 7},
	}}
	if got := signalAnnouncingSections(bench.testServer, bench.target.Token, agentwire.SignalRequest{Restic: &report}); got.Code != http.StatusNoContent {
		t.Fatalf("target report: %d %s", got.Code, got.Body.String())
	}
	return bench
}

// createServiceBackup déclare la sauvegarde du service et lit les deux
// ordres du lien : restic.provision à la source (l'adresse, le certificat et
// les deux mots de passe dans le secret), puis restic.user.add à la cible (le
// seul mot de passe REST).
func (b *resticBench) createServiceBackup(t *testing.T) string {
	t.Helper()
	got := callAPI(b.Server, http.MethodPost, "/api/machines/"+resticSourceID+"/restic-backups", map[string]any{
		"name": "nextcloud", "scope": "service", "service_id": b.serviceID, "target_machine_id": resticTargetID,
		"retention": map[string]int{"keep_daily": 14, "keep_weekly": 8, "keep_monthly": 3},
	})
	if got.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", got.Code, got.Body.String())
	}
	var created resticBackupCreatedJSON
	decodeAPI(t, got, &created)
	provision := nextOrder(t, b.source)
	var params agentwire.ResticProvisionParams
	if provision.Kind != agentwire.KindResticProvision || json.Unmarshal(provision.Params, &params) != nil ||
		params.Target != resticTargetID || params.Host != resticTargetHost || params.Port != resticTargetPort || params.User != resticSourceID ||
		!strings.Contains(params.Certificate, "BEGIN CERTIFICATE") {
		t.Fatalf("provision %s %s", provision.Kind, provision.Params)
	}
	var secret agentwire.ResticProvisionSecret
	if json.Unmarshal(provision.Secret, &secret) != nil || secret.Validate() != nil || secret.RESTPassword == secret.RepoPassword {
		t.Fatal("the provision secret does not carry two drawn passwords")
	}
	b.provisioned = secret
	userAdd := nextOrder(t, b.target)
	var added agentwire.ResticUserAddParams
	if userAdd.Kind != agentwire.KindResticUserAdd || json.Unmarshal(userAdd.Params, &added) != nil || added.User != resticSourceID {
		t.Fatalf("user add %s %s", userAdd.Kind, userAdd.Params)
	}
	var userSecret map[string]string
	if json.Unmarshal(userAdd.Secret, &userSecret) != nil || len(userSecret) != 1 || userSecret["rest_password"] != secret.RESTPassword {
		t.Fatal("the target must receive the REST password, and only it")
	}
	return created.ID
}

// runNow lance la sauvegarde par l'API et rend l'ordre restic.backup lu sur
// le flux de la source.
func (b *resticBench) runNow(t *testing.T, backupID string) agentwire.OrderBody {
	t.Helper()
	got := callAPI(b.Server, http.MethodPost, "/api/machines/"+resticSourceID+"/restic-backups/"+backupID+"/actions/run", map[string]any{})
	if got.Code != http.StatusCreated {
		t.Fatalf("run: %d %s", got.Code, got.Body.String())
	}
	order := nextOrder(t, b.source)
	var params agentwire.ResticBackupParams
	if order.Kind != agentwire.KindResticBackup || json.Unmarshal(order.Params, &params) != nil ||
		params.Target != resticTargetID || params.Scope != agentwire.ResticScopeService || params.Service != "nextcloud" {
		t.Fatalf("backup order %s %s", order.Kind, order.Params)
	}
	return order
}

// reportRun fait rapporter à la source la fin de la sauvegarde.
func (b *resticBench) reportRun(t *testing.T, result agentwire.ActionResult) {
	t.Helper()
	report := agentwire.ActionReport{Results: []agentwire.ActionResult{result}}
	got := signalAnnouncingSections(b.testServer, b.source.Token, agentwire.SignalRequest{Actions: &report})
	if got.Code != http.StatusNoContent {
		t.Fatalf("run report: %d %s", got.Code, got.Body.String())
	}
}

// reportTargetRun fait rapporter à la cible la fin d'un de ses ordres.
func (b *resticBench) reportTargetRun(t *testing.T, result agentwire.ActionResult) {
	t.Helper()
	report := agentwire.ActionReport{Results: []agentwire.ActionResult{result}}
	got := signalAnnouncingSections(b.testServer, b.target.Token, agentwire.SignalRequest{Actions: &report})
	if got.Code != http.StatusNoContent {
		t.Fatalf("target run report: %d %s", got.Code, got.Body.String())
	}
}

func (b *resticBench) overview(t *testing.T) backupOverviewJSON {
	t.Helper()
	got := callAPI(b.Server, http.MethodGet, "/api/backups/overview", nil)
	if got.Code != http.StatusOK {
		t.Fatalf("overview: %d %s", got.Code, got.Body.String())
	}
	var overview backupOverviewJSON
	decodeAPI(t, got, &overview)
	return overview
}

// resticProtection rend la protection restic de la source du service.
func (b *resticBench) resticProtection(t *testing.T, overview backupOverviewJSON) overviewProtectionJSON {
	t.Helper()
	for _, source := range overview.Sources {
		if source.Key != "service:"+b.serviceID {
			continue
		}
		for _, protection := range source.Protections {
			if protection.Origin == restic.OriginRestic {
				return protection
			}
		}
	}
	t.Fatalf("no restic protection on the service: %+v", overview.Sources)
	return overviewProtectionJSON{}
}

// nextOrder lit le prochain ordre poussé sur le flux d'une session.
func nextOrder(t *testing.T, session *machine.Session) agentwire.OrderBody {
	t.Helper()
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case event := <-session.Events():
			if event.Name != agentwire.EventOrder {
				continue
			}
			signed, err := agentwire.DecodeSignedOrder(event.Data)
			if err != nil {
				t.Fatal(err)
			}
			var body agentwire.OrderBody
			if err := json.Unmarshal(signed.Body, &body); err != nil {
				t.Fatal(err)
			}
			return body
		case <-timeout.C:
			t.Fatal("no order pushed")
		}
	}
}

// testCertificate tire un certificat auto-signé, comme celui que la cible
// génère pour rest-server, et rend son PEM public et son empreinte.
func testCertificate(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "nas"},
		NotBefore: testNow.Add(-time.Hour), NotAfter: testNow.Add(365 * 24 * time.Hour),
		IPAddresses: []net.IP{net.ParseIP(resticTargetHost)},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	return certificate, agentwire.CertificateFingerprint(der)
}

var (
	resticRunID       = regexp.MustCompile(`"run_id":"[0-9a-f]{16}"`)
	resticServiceKey  = regexp.MustCompile(`"(service:)?svc_[0-9a-f]{16}"`)
	resticFingerprint = regexp.MustCompile(`"fingerprint":"[0-9a-f]{64}"`)
)

// normalizeRestic fige ce qui est tiré à chaque passage : les identifiants,
// l'empreinte du certificat de test.
func normalizeRestic(body string) string {
	body = normalizeBackups(body)
	body = resticRunID.ReplaceAllString(body, `"run_id":"ID"`)
	body = resticServiceKey.ReplaceAllString(body, `"${1}svc_ID"`)
	return resticFingerprint.ReplaceAllString(body, `"fingerprint":"FINGERPRINT"`)
}

// Le chemin direct : la source a joint la cible, le résultat le dit, et
// l'aperçu le rend avec la taille ajoutée, la rétention (appliquée par la
// cible, aucune purge encore) et la prochaine nuit.
func TestRestic_ADirectRunIsRecordedWithItsPath(t *testing.T) {
	bench := readyResticBench(t)
	backupID := bench.createServiceBackup(t)
	order := bench.runNow(t, backupID)
	bench.reportRun(t, agentwire.ActionResult{RunID: order.RunID, State: agentwire.ActionAccepted})
	snapshot := strings.Repeat("a", 64)
	bench.reportRun(t, agentwire.ActionResult{RunID: order.RunID, State: agentwire.ActionSucceeded, Restic: &agentwire.ResticRunReport{
		Path: agentwire.ResticPathDirect, Reason: agentwire.ResticReachable, TargetHost: resticTargetHost, TargetPort: resticTargetPort,
		SnapshotID: snapshot, BytesAdded: 42 << 20, DurationSeconds: 7,
	}})

	protection := bench.resticProtection(t, bench.overview(t))
	if protection.State != restic.ProtectionOK || protection.Path == nil || protection.Path.Path != "direct" || protection.Path.Reason != "reachable" {
		t.Fatalf("protection %+v path %+v", protection, protection.Path)
	}
	if protection.AddedBytes == nil || *protection.AddedBytes != 42<<20 || protection.TargetMachineName != "nas" || protection.Scope != "service" {
		t.Fatalf("protection %+v", protection)
	}
	if retention := protection.Retention; !retention.Known || !retention.Applied || retention.LastForget != nil || retention.KeepDaily != 14 || retention.KeepWeekly != 8 || retention.KeepMonthly != 3 {
		t.Fatalf("retention %+v", retention)
	}
	if len(protection.Runs) != 1 || protection.Runs[0].Path != "direct" || protection.Runs[0].State != "succeeded" || protection.NextAt == nil {
		t.Fatalf("runs %+v next %v", protection.Runs, protection.NextAt)
	}
	stored, err := bench.db.GetResticBackup(context.Background(), backupID)
	if err != nil || stored.Last.Report.SnapshotID != snapshot || stored.LastSuccessAt.IsZero() {
		t.Fatalf("stored %+v %v", stored, err)
	}
}

// La bascule sur le relais : la source n'a pas joint la cible en TCP. Le
// résultat garde la raison, l'hôte et le port de la cible : l'aperçu les rend
// avec les noms des deux machines, de quoi dire « pour passer en direct : vps
// doit joindre nas:8000 » sans que le serveur écrive la phrase.
func TestRestic_ARelayRunExposesWhatToOpenToGoDirect(t *testing.T) {
	bench := readyResticBench(t)
	backupID := bench.createServiceBackup(t)
	order := bench.runNow(t, backupID)
	bench.reportRun(t, agentwire.ActionResult{RunID: order.RunID, State: agentwire.ActionSucceeded, Restic: &agentwire.ResticRunReport{
		Path: agentwire.ResticPathRelay, Reason: agentwire.ResticTCPUnreachable, TargetHost: resticTargetHost, TargetPort: resticTargetPort,
		SnapshotID: strings.Repeat("b", 64), BytesAdded: 200 << 10, DurationSeconds: 12,
	}})

	body := callAPI(bench.Server, http.MethodGet, "/api/backups/overview", nil)
	var overview backupOverviewJSON
	decodeAPI(t, body, &overview)
	path := bench.resticProtection(t, overview).Path
	want := overviewPathJSON{Path: "relay", Reason: "tcp_unreachable", SourceName: "vps", TargetName: "nas", TargetHost: resticTargetHost, TargetPort: resticTargetPort}
	if path == nil || *path != want {
		t.Fatalf("path %+v, want %+v", path, want)
	}
	var target overviewTargetJSON
	for _, candidate := range overview.Targets {
		if candidate.Kind == restic.TargetKindRestic {
			target = candidate
		}
	}
	if target.MachineName != "nas" || target.State != "running" || target.Host != resticTargetHost || target.Port != resticTargetPort ||
		target.DiskFree != 600<<30 || target.DiskUsed != (2<<40)-(600<<30) || target.Dir != "/srv/restic" || len(target.Holds) != 1 {
		t.Fatalf("target %+v", target)
	}
	goldentest.Compare(t, filepath.Join("testdata", "backups_overview.golden.json"), normalizeRestic(body.Body.String()))
}

// Un résultat hors des listes fermées ne s'écrit pas tel quel : un chemin
// inconnu est jeté (l'état de l'ordre reste), une raison inconnue devient
// « unknown ».
func TestRestic_AnAgentCannotStoreItsOwnWords(t *testing.T) {
	bench := readyResticBench(t)
	backupID := bench.createServiceBackup(t)
	order := bench.runNow(t, backupID)
	bench.reportRun(t, agentwire.ActionResult{RunID: order.RunID, State: agentwire.ActionFailed, Reason: "pare-feu", Restic: &agentwire.ResticRunReport{
		Path: "tunnel", Reason: "firewall", TargetHost: "nas:8000",
	}})
	stored, err := bench.db.GetResticBackup(context.Background(), backupID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Last.State != restic.StateFailed || stored.Last.Reason != "unknown" || stored.Last.Report.Path != "" || stored.Last.Report.TargetHost != "" {
		t.Fatalf("stored %+v", stored.Last)
	}
}

// Un droit qui manque se refuse avec sa clé, qui nomme le droit ; la machine
// est celle de la route : l'écran propose « Ré-enrôler avec ce droit ».
func TestRestic_AMissingRightIsRefusedWithTheRightAndTheMachine(t *testing.T) {
	bench := newResticBench(t, "observe", "observe")
	got := callAPI(bench.Server, http.MethodPost, "/api/machines/"+resticTargetID+"/actions/restic-target-setup",
		map[string]any{"host": resticTargetHost, "port": resticTargetPort})
	if got.Code != http.StatusConflict || errorCode(t, got) != "restic.target_right_missing" {
		t.Fatalf("target: %d %s", got.Code, got.Body.String())
	}

	ready := readyResticBench(t)
	ready.machines.Disconnect(ready.source)
	sourceMachine, err := ready.machines.Get(context.Background(), resticSourceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ready.machines.Connect(context.Background(), sourceMachine.Machine, "192.0.2.21", "v0.0.1", "observe", "", ""); err != nil {
		t.Fatal(err)
	}
	got = callAPI(ready.Server, http.MethodPost, "/api/machines/"+resticSourceID+"/restic-backups", map[string]any{
		"name": "nextcloud", "scope": "service", "service_id": ready.serviceID, "target_machine_id": resticTargetID,
	})
	if got.Code != http.StatusConflict || errorCode(t, got) != "restic.source_right_missing" {
		t.Fatalf("source: %d %s", got.Code, got.Body.String())
	}
	got = callAPI(ready.Server, http.MethodPost, "/api/machines/"+resticSourceID+"/restic-backups", map[string]any{
		"name": "vm", "scope": "vm", "vmid": 101, "target_machine_id": resticTargetID,
	})
	if got.Code != http.StatusConflict || errorCode(t, got) != "restic.source_right_missing" {
		t.Fatalf("vm source: %d %s", got.Code, got.Body.String())
	}

	// L'aperçu dit, machine par machine, le droit qui manque.
	for _, fit := range ready.overview(t).Machines {
		if fit.MachineID != resticSourceID {
			continue
		}
		for _, right := range fit.Rights {
			if right.Right == machine.CapabilityResticProduce && right.State != "missing" {
				t.Fatalf("restic.produce on vps: %+v", right)
			}
		}
		return
	}
	t.Fatal("the source is not in the machines of the overview")
}

// Le dossier demandé doit être celui du droit ; un dossier vide le prend.
func TestRestic_TheTargetFolderIsTheOneOfTheRight(t *testing.T) {
	bench := newResticBench(t, "observe,restic.produce", "observe,restic.target=/srv/restic")
	got := callAPI(bench.Server, http.MethodPost, "/api/machines/"+resticTargetID+"/actions/restic-target-setup",
		map[string]any{"host": resticTargetHost, "port": resticTargetPort, "dir": "/srv/ailleurs"})
	if got.Code != http.StatusConflict || errorCode(t, got) != "restic.target_dir_outside_right" {
		t.Fatalf("dir: %d %s", got.Code, got.Body.String())
	}
	for _, bad := range []map[string]any{{"host": "nas:8000", "port": 8000}, {"host": resticTargetHost, "port": 70000}} {
		if got := callAPI(bench.Server, http.MethodPost, "/api/machines/"+resticTargetID+"/actions/restic-target-setup", bad); got.Code != http.StatusUnprocessableEntity {
			t.Errorf("%v: %d %s", bad, got.Code, got.Body.String())
		}
	}
}

// Un dépôt ne reçoit qu'une sauvegarde à la fois ; une sauvegarde d'une autre
// machine ne se lance pas sous la route de celle-ci.
func TestRestic_OneRunPerRepositoryAndOnlyUnderItsSource(t *testing.T) {
	bench := readyResticBench(t)
	backupID := bench.createServiceBackup(t)
	bench.runNow(t, backupID)
	again := callAPI(bench.Server, http.MethodPost, "/api/machines/"+resticSourceID+"/restic-backups/"+backupID+"/actions/run", map[string]any{})
	if again.Code != http.StatusConflict || errorCode(t, again) != "restic.backup_running" {
		t.Fatalf("second run: %d %s", again.Code, again.Body.String())
	}
	elsewhere := callAPI(bench.Server, http.MethodPost, "/api/machines/"+resticTargetID+"/restic-backups/"+backupID+"/actions/run", map[string]any{})
	if elsewhere.Code != http.StatusNotFound {
		t.Fatalf("under another machine: %d %s", elsewhere.Code, elsewhere.Body.String())
	}
	if removed := callAPI(bench.Server, http.MethodPost, "/api/machines/"+resticTargetID+"/actions/restic-target-remove", map[string]any{}); removed.Code != http.StatusConflict ||
		errorCode(t, removed) != "restic.target_in_use" {
		t.Fatalf("remove a used target: %d %s", removed.Code, removed.Body.String())
	}
}
