package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/dockerapi"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

// fakeContainer est un conteneur du faux démon : son environnement porte des
// secrets, que Docker rendrait à l'inspection.
type fakeContainer struct {
	name, image, state string
	env                []string
	// volumes : nom du volume → point de montage dans le conteneur.
	volumes map[string]string
}

// testContainers : une application qui monte un volume, des bases de chaque
// moteur, un cache (pas une base), une base arrêtée. Un volume n'est monté
// par personne.
func testContainers(secret string) ([]fakeContainer, []string) {
	containers := []fakeContainer{
		{name: "app-1", image: "nextcloud:29", state: "running", env: []string{"NEXTCLOUD_ADMIN_PASSWORD=" + secret + "-admin"},
			volumes: map[string]string{"app_data": "/var/www/html"}},
		{name: "db-1", image: "docker.io/library/postgres:16", state: "running", env: []string{"POSTGRES_PASSWORD=" + secret + "-pg"}},
		{name: "maria-1", image: "mariadb:11", state: "running", env: []string{"MARIADB_ROOT_PASSWORD=" + secret + "-maria"}},
		{name: "mongo-1", image: "mongo:7", state: "running", env: []string{"MONGO_INITDB_ROOT_USERNAME=root", "MONGO_INITDB_ROOT_PASSWORD=" + secret + "-mongo"}},
		{name: "cache-1", image: "redis:7", state: "running"},
		{name: "old-db", image: "postgres:15", state: "exited", env: []string{"POSTGRES_PASSWORD=" + secret + "-old"}},
	}
	return containers, []string{"app_data", "orphan"}
}

// fakeDaemon sert l'API Docker sur une socket unix : la liste, l'inspection
// (environnement compris, comme le vrai démon) et les volumes. Il compte les
// requêtes.
type fakeDaemon struct {
	mu       sync.Mutex
	requests int
}

func (d *fakeDaemon) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.requests
}

func serveFakeDaemon(t *testing.T, containers []fakeContainer, volumes []string) (*dockerapi.Client, *fakeDaemon) {
	t.Helper()
	daemon := &fakeDaemon{}
	prefix := "/v" + dockerapi.APIVersion
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+prefix+"/containers/json", func(w http.ResponseWriter, _ *http.Request) {
		var list []map[string]any
		for _, c := range containers {
			list = append(list, map[string]any{"Id": "id-" + c.name, "Names": []string{"/" + c.name}, "Image": c.image, "State": c.state})
		}
		writeJSON(w, list)
	})
	mux.HandleFunc("GET "+prefix+"/containers/{id}/json", func(w http.ResponseWriter, r *http.Request) {
		for _, c := range containers {
			if "id-"+c.name == r.PathValue("id") {
				writeJSON(w, inspectionOf(c))
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("GET "+prefix+"/volumes", func(w http.ResponseWriter, _ *http.Request) {
		var list []map[string]any
		for _, name := range volumes {
			list = append(list, map[string]any{"Name": name, "Driver": "local", "Mountpoint": "/var/lib/docker/volumes/" + name + "/_data"})
		}
		writeJSON(w, map[string]any{"Volumes": list})
	})
	counted := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		daemon.mu.Lock()
		daemon.requests++
		daemon.mu.Unlock()
		mux.ServeHTTP(w, r)
	})
	dir, err := os.MkdirTemp("/tmp", "oc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(counted)
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return dockerapi.New(socket), daemon
}

func inspectionOf(c fakeContainer) map[string]any {
	var mounts []map[string]any
	for name, destination := range c.volumes {
		mounts = append(mounts, map[string]any{"Type": "volume", "Name": name, "Source": "/var/lib/docker/volumes/" + name + "/_data", "Destination": destination})
	}
	mounts = append(mounts, map[string]any{"Type": "bind", "Source": "/etc/localtime", "Destination": "/etc/localtime"})
	return map[string]any{
		"Id": "id-" + c.name, "Name": "/" + c.name, "Image": "sha256:abc",
		"Config": map[string]any{"Image": c.image, "Env": c.env},
		"State":  map[string]any{"Status": c.state}, "Mounts": mounts,
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

const testDockerProgram = "/usr/bin/docker"

// newServiceBench : une source liée (par le relais), un service avec son
// dossier, et le faux démon.
func newServiceBench(t *testing.T, rights ...string) (*resticBench, *fakeDaemon) {
	t.Helper()
	b := newResticBench(t, append([]string{"restic.produce"}, rights...)...)
	b.runner.restic.source.dialer = &refusingDialer{}
	b.provision(t, "nas", 8000, mustCertificate(t))
	b.service(t, "nextcloud")
	containers, volumes := testContainers("seed")
	client, daemon := serveFakeDaemon(t, containers, volumes)
	b.runner.restic.source.inventory = client
	b.runner.restic.source.dockerProgram = testDockerProgram
	return b, daemon
}

func dockerBackup(service string, volumes, databases []string) agentwire.ResticBackupParams {
	return agentwire.ResticBackupParams{Target: resticTargetID, Scope: agentwire.ResticScopeService, Service: service, Volumes: volumes, Databases: databases}
}

// backupCalls rend les arguments des seules sauvegardes lancées.
func backupCalls(fake *fakeRestic) []string {
	var calls []string
	for _, call := range fake.callsMade() {
		if call.args[0] == "backup" {
			calls = append(calls, strings.Join(call.args, " "))
		}
	}
	return calls
}

// Le dossier, le volume et la base sont trois instantanés : docker cp par
// le conteneur qui monte le volume, docker exec du script du moteur.
func TestResticBackup_ServiceSavesEachPartAsItsOwnSnapshot(t *testing.T) {
	b, _ := newServiceBench(t, "restic.docker")
	params := dockerBackup("nextcloud", []string{"app_data"}, []string{"db-1"})
	result := expectResult(t, b.run(t, agentwire.KindResticBackup, params), agentwire.ActionSucceeded, "")

	calls := backupCalls(b.restic)
	if len(calls) != 3 {
		t.Fatalf("restic ran %v", calls)
	}
	if !strings.HasSuffix(calls[0], filepath.Join(b.root, "nextcloud")) {
		t.Errorf("folder %s", calls[0])
	}
	if !strings.HasSuffix(calls[1], "--stdin-from-command --stdin-filename volume-app_data.tar -- /usr/bin/docker cp app-1:/var/www/html/. -") {
		t.Errorf("volume %s", calls[1])
	}
	if !strings.HasSuffix(calls[2], "--stdin-filename database-db-1.sql -- /usr/bin/docker exec db-1 sh -c "+restic.DumpScript(agentwire.EnginePostgres)) {
		t.Errorf("database %s", calls[2])
	}
	report := result.Restic
	want := []agentwire.ResticPartReport{
		{Kind: agentwire.ResticPartFolder, State: agentwire.ResticPartOK, BytesAdded: 4096},
		{Kind: agentwire.ResticPartVolume, Name: "app_data", State: agentwire.ResticPartOK, BytesAdded: 4096},
		{Kind: agentwire.ResticPartDatabase, Name: "db-1", State: agentwire.ResticPartOK, BytesAdded: 4096},
	}
	if report == nil || !slices.Equal(report.Parts, want) || report.BytesAdded != 3*4096 || report.SnapshotID != testSnapshotID {
		t.Fatalf("report %+v", report)
	}
	if err := report.Validate(); err != nil {
		t.Fatal(err)
	}
	if !hasLine(result.Output, "volume app_data snapshot "+testSnapshotID) || !hasLine(result.Output, "database db-1 snapshot") {
		t.Errorf("output %+v", result.Output)
	}
}

// Des volumes ou des bases sans restic.docker : refusé, rien ne part ni ne
// se lit. Le dossier seul n'en a pas besoin et ne touche pas Docker.
func TestResticBackup_DockerPartsNeedResticDocker(t *testing.T) {
	b, daemon := newServiceBench(t)
	for _, params := range []agentwire.ResticBackupParams{
		dockerBackup("nextcloud", []string{"app_data"}, nil),
		dockerBackup("", nil, []string{"db-1"}),
	} {
		expectResult(t, b.run(t, agentwire.KindResticBackup, params), agentwire.ActionRefused, agentwire.RightMissing(machine.CapabilityResticDocker))
	}
	if len(b.restic.callsMade()) != 0 || daemon.count() != 0 {
		t.Fatal("a refused order ran restic or read docker")
	}
	result := expectResult(t, b.run(t, agentwire.KindResticBackup, serviceBackup("nextcloud")), agentwire.ActionSucceeded, "")
	want := []agentwire.ResticPartReport{{Kind: agentwire.ResticPartFolder, State: agentwire.ResticPartOK, BytesAdded: 4096}}
	if !slices.Equal(result.Restic.Parts, want) || daemon.count() != 0 || len(backupCalls(b.restic)) != 1 {
		t.Fatalf("folder only: %+v, %d docker requests", result.Restic, daemon.count())
	}
}

// Docker fermé au compte de l'agent : refusé avant tout lancement, avec la
// raison du droit.
func TestResticBackup_DockerPartsAreRefusedWhenDockerIsClosed(t *testing.T) {
	b, daemon := newServiceBench(t, "restic.docker")
	b.runner.checks.docker = func(context.Context) string { return agentwire.ReasonDockerDenied }
	expectResult(t, b.run(t, agentwire.KindResticBackup, dockerBackup("nextcloud", []string{"app_data"}, nil)), agentwire.ActionRefused, agentwire.ReasonDockerDenied)
	if len(b.restic.callsMade()) != 0 || daemon.count() != 0 {
		t.Fatal("restic ran or docker was read")
	}
}

// Ce que la machine n'a pas est ignoré et rapporté, sans échec tant qu'une
// partie a sauvegardé ; tout ignoré, l'ordre échoue.
func TestResticBackup_SkipsWhatTheMachineDoesNotHave(t *testing.T) {
	b, _ := newServiceBench(t, "restic.docker")
	params := dockerBackup("nextcloud", []string{"orphan", "ghost"}, []string{"gone-db", "cache-1"})
	result := expectResult(t, b.run(t, agentwire.KindResticBackup, params), agentwire.ActionSucceeded, "")
	want := []agentwire.ResticPartReport{
		{Kind: agentwire.ResticPartFolder, State: agentwire.ResticPartOK, BytesAdded: 4096},
		{Kind: agentwire.ResticPartVolume, Name: "orphan", State: agentwire.ResticPartSkipped, Reason: agentwire.ReasonVolumeUnmounted},
		{Kind: agentwire.ResticPartVolume, Name: "ghost", State: agentwire.ResticPartSkipped, Reason: agentwire.ReasonContainerMissing},
		{Kind: agentwire.ResticPartDatabase, Name: "gone-db", State: agentwire.ResticPartSkipped, Reason: agentwire.ReasonContainerMissing},
		{Kind: agentwire.ResticPartDatabase, Name: "cache-1", State: agentwire.ResticPartSkipped, Reason: agentwire.ReasonEngineUnsupported},
	}
	if !slices.Equal(result.Restic.Parts, want) || len(backupCalls(b.restic)) != 1 {
		t.Fatalf("parts %+v, calls %v", result.Restic.Parts, backupCalls(b.restic))
	}

	allSkipped := expectResult(t, b.run(t, agentwire.KindResticBackup, dockerBackup("", []string{"orphan"}, []string{"cache-1"})), agentwire.ActionFailed, agentwire.ReasonVolumeUnmounted)
	if allSkipped.Restic == nil || len(allSkipped.Restic.Parts) != 2 || allSkipped.Restic.Validate() != nil {
		t.Fatalf("all skipped %+v", allSkipped.Restic)
	}
	if len(backupCalls(b.restic)) != 1 {
		t.Fatal("restic ran with nothing to save")
	}
}

// Un dump qui échoue n'arrête pas les parties suivantes ; il se rejoue une
// fois, seul. Une base arrêtée échoue sans rien lancer.
func TestResticBackup_AFailedDumpDoesNotStopTheOtherParts(t *testing.T) {
	b, _ := newServiceBench(t, "restic.docker")
	b.restic.answer = func(call fakeCall, _ int, stdout, stderr io.Writer) int {
		if slices.Contains(call.args, "db-1") {
			_, _ = io.WriteString(stderr, restic.DumpFailedMarker+"\nFatal: unable to save snapshot: command exited 1\n")
			return 1
		}
		return writeSummary(stdout)
	}
	params := dockerBackup("nextcloud", []string{"app_data"}, []string{"db-1", "old-db", "maria-1"})
	result := expectResult(t, b.run(t, agentwire.KindResticBackup, params), agentwire.ActionFailed, agentwire.ReasonDumpFailed)
	states := map[string]string{}
	for _, part := range result.Restic.Parts {
		states[part.Kind+" "+part.Name] = part.State + " " + part.Reason
	}
	want := map[string]string{
		"folder ": "ok ", "volume app_data": "ok ", "database db-1": "failed dump_failed",
		"database old-db": "failed dump_failed", "database maria-1": "ok ",
	}
	if len(states) != len(want) {
		t.Fatalf("parts %+v", result.Restic.Parts)
	}
	for part, state := range want {
		if states[part] != state {
			t.Errorf("%s: %q, want %q", part, states[part], state)
		}
	}
	calls := backupCalls(b.restic)
	if len(calls) != 5 || !strings.Contains(calls[4], "exec db-1 ") || slices.ContainsFunc(calls, func(c string) bool { return strings.Contains(c, "old-db") }) {
		t.Fatalf("restic ran %v", calls)
	}
	if result.Restic.SnapshotID != testSnapshotID || result.Restic.BytesAdded != 3*4096 || !hasLine(result.Output, "database db-1 failed dump_failed") {
		t.Fatalf("result %+v", result)
	}
	// Un échec de restic lui-même, sans la marque du script, reste restic_failed.
	b.restic.answer = func(_ fakeCall, _ int, _, stderr io.Writer) int {
		_, _ = io.WriteString(stderr, "Fatal: repository locked\n")
		return 1
	}
	expectResult(t, b.run(t, agentwire.KindResticBackup, dockerBackup("", nil, []string{"db-1"})), agentwire.ActionFailed, agentwire.ReasonResticFailed)
}

// Un nom hostile est refusé à la lecture de l'ordre : ni Docker ni restic.
func TestResticBackup_HostileNamesAreRefusedBeforeAnything(t *testing.T) {
	b, daemon := newServiceBench(t, "restic.docker")
	for _, params := range []agentwire.ResticBackupParams{
		dockerBackup("nextcloud", []string{"../x"}, nil),
		dockerBackup("nextcloud", []string{"a/b"}, nil),
		dockerBackup("", nil, []string{"-x"}),
		dockerBackup("", nil, []string{"db-1:/etc"}),
	} {
		expectResult(t, b.run(t, agentwire.KindResticBackup, params), agentwire.ActionFailed, agentwire.ReasonBadOrder)
	}
	if len(b.restic.callsMade()) != 0 || daemon.count() != 0 {
		t.Fatal("a hostile name reached docker or restic")
	}
}

// Le démon ne répond pas : les parties Docker échouent, le dossier part.
func TestResticBackup_UnreadableInventoryFailsTheDockerPartsOnly(t *testing.T) {
	b, _ := newServiceBench(t, "restic.docker")
	b.runner.restic.source.inventory = dockerapi.New(filepath.Join(t.TempDir(), "absent.sock"))
	result := expectResult(t, b.run(t, agentwire.KindResticBackup, dockerBackup("nextcloud", []string{"app_data"}, nil)), agentwire.ActionFailed, agentwire.ReasonDockerMissing)
	if len(backupCalls(b.restic)) != 1 || result.Restic.Parts[0].State != agentwire.ResticPartOK {
		t.Fatalf("result %+v", result.Restic)
	}
}

// restic.docker se juge comme les autres droits : Docker, puis restic.
func TestJudgeRights_ResticDockerNeedsDockerAndRestic(t *testing.T) {
	identity := Identity{Rights: []string{"observe", "restic.produce", "restic.docker"}}
	checks := allowAll()
	if unavailable := judgeRights(context.Background(), identity, checks); len(unavailable) != 0 {
		t.Fatalf("unavailable %v", unavailable)
	}
	checks.docker = func(context.Context) string { return agentwire.ReasonDockerMissing }
	if unavailable := judgeRights(context.Background(), identity, checks); unavailable[machine.CapabilityResticDocker] != agentwire.ReasonDockerMissing {
		t.Fatalf("unavailable %v", unavailable)
	}
	checks = allowAll()
	checks.findRestic = func() (string, error) { return "", errors.New("not found") }
	if unavailable := judgeRights(context.Background(), identity, checks); unavailable[machine.CapabilityResticDocker] != agentwire.ReasonResticMissing {
		t.Fatalf("unavailable %v", unavailable)
	}
}
