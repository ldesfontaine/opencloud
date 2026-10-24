package proxmox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testTokenID     = "lecteur@pve!lecture"
	testTokenSecret = "0a1b2c3d-4e5f-6071-8293-a4b5c6d7e8f9" // gitleaks:allow -- faux secret de test
)

// fakeAPI joue l'API d'un Proxmox : un serveur HTTPS local, jamais un vrai
// hôte. Il compte les requêtes et garde l'en-tête Authorization de la dernière.
type fakeAPI struct {
	*httptest.Server
	mu            sync.Mutex
	authorization string
	paths         []string
	handler       func(w http.ResponseWriter, r *http.Request) bool
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	api := &fakeAPI{}
	api.Server = httptest.NewTLSServer(http.HandlerFunc(api.serve))
	t.Cleanup(api.Close)
	return api
}

func (api *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	api.mu.Lock()
	api.authorization = r.Header.Get("Authorization")
	api.paths = append(api.paths, r.URL.RequestURI())
	handler := api.handler
	api.mu.Unlock()
	if handler != nil && handler(w, r) {
		return
	}
	if r.Header.Get("Authorization") != "PVEAPIToken="+testTokenID+"="+testTokenSecret {
		http.Error(w, `{"data":null}`, http.StatusUnauthorized)
		return
	}
	var body string
	switch {
	case r.URL.Path == "/api2/json/version":
		body = `{"data":{"version":"8.2.4","release":"8.2","repoid":"abc"}}`
	case r.URL.Path == "/api2/json/cluster/resources" && r.URL.Query().Get("type") == "vm":
		body = `{"data":[
			{"type":"qemu","vmid":5012,"name":"docker1","node":"pve","status":"running","template":0,"maxcpu":2,"maxmem":2147483648,"maxdisk":34359738368},
			{"type":"lxc","vmid":200,"name":"dns","node":"pve","status":"stopped","template":0,"maxcpu":1,"maxmem":536870912,"maxdisk":8589934592},
			{"type":"qemu","vmid":9000,"name":"debian-12","node":"pve","status":"stopped","template":1,"maxcpu":2,"maxmem":1073741824,"maxdisk":4294967296}]}`
	case r.URL.Path == "/api2/json/cluster/resources" && r.URL.Query().Get("type") == "storage":
		body = `{"data":[
			{"type":"storage","id":"storage/pve/local","storage":"local","node":"pve","plugintype":"dir","status":"available","content":"iso,backup,vztmpl","maxdisk":1000,"disk":400},
			{"type":"storage","id":"storage/pve/nfs","storage":"nfs","node":"pve","plugintype":"nfs","status":"unknown","content":"backup","maxdisk":0,"disk":0},
			{"type":"storage","id":"storage/pve/zfs","storage":"zfs","node":"pve","plugintype":"zfspool","status":"available","content":"images,rootdir","maxdisk":500,"disk":500}]}`
	case r.URL.Path == "/api2/json/nodes/pve/storage/local/content" && r.URL.Query().Get("content") == "backup":
		body = `{"data":[
			{"vmid":5012,"ctime":1790000000,"size":100,"volid":"local:backup/old"},
			{"vmid":5012,"ctime":1790086400,"size":244318208,"volid":"local:backup/new"},
			{"vmid":0,"ctime":1790086400,"size":1,"volid":"local:backup/orphan"}]}`
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

func (api *fakeAPI) lastAuthorization() string {
	api.mu.Lock()
	defer api.mu.Unlock()
	return api.authorization
}

func (api *fakeAPI) requested() []string {
	api.mu.Lock()
	defer api.mu.Unlock()
	return append([]string(nil), api.paths...)
}

func (api *fakeAPI) pin() string {
	sum := sha256.Sum256(api.Certificate().Raw)
	return hex.EncodeToString(sum[:])
}

func (api *fakeAPI) credentials(pin string) Credentials {
	return Credentials{
		Host:        Host{MachineID: "m1", APIURL: api.URL, TokenID: testTokenID, Pin: pin},
		TokenSecret: testTokenSecret,
	}
}

func refusalOf(t *testing.T, err error, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

func TestClient_ReadsTheHostWithThePinnedCertificate(t *testing.T) {
	api := newFakeAPI(t)
	reading, err := NewClient(api.credentials(api.pin())).Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reading.Version != "8.2.4" {
		t.Errorf("version %q", reading.Version)
	}
	if want := "PVEAPIToken=" + testTokenID + "=" + testTokenSecret; api.lastAuthorization() != want {
		t.Errorf("authorization %q, want %q", api.lastAuthorization(), want)
	}
	if len(reading.Guests) != 3 {
		t.Fatalf("guests: %+v", reading.Guests)
	}
	docker := reading.Guests[0]
	if docker.VMID != 5012 || docker.Kind != KindQEMU || docker.Name != "docker1" || docker.Status != "running" || docker.CPUs != 2 || docker.MaxMem != 2<<30 || docker.Template {
		t.Errorf("first guest: %+v", docker)
	}
	if !reading.Guests[2].Template || reading.Guests[1].Kind != KindLXC {
		t.Errorf("template or lxc not read: %+v", reading.Guests)
	}
	// La plus récente des deux archives, et la taille de celle-là.
	if want := time.Unix(1790086400, 0).UTC(); !docker.LastBackupAt.Equal(want) || docker.LastBackupBytes != 244318208 {
		t.Errorf("last backup %v / %d", docker.LastBackupAt, docker.LastBackupBytes)
	}
	if !reading.Guests[1].LastBackupAt.IsZero() {
		t.Errorf("a guest without archive must have no backup: %v", reading.Guests[1].LastBackupAt)
	}
	if len(reading.Storages) != 3 {
		t.Fatalf("storages: %+v", reading.Storages)
	}
	local := reading.Storages[0]
	if local.Name != "local" || local.Node != "pve" || local.Type != "dir" || local.Total != 1000 || local.Used != 400 || local.Avail != 600 || !local.Active || !local.AcceptsBackups() {
		t.Errorf("local storage: %+v", local)
	}
	if reading.Storages[1].Active {
		t.Errorf("an unavailable storage must not be active: %+v", reading.Storages[1])
	}
	// Le stockage hors ligne et celui qui ne reçoit pas de sauvegardes ne sont pas interrogés.
	for _, path := range api.requested() {
		if strings.Contains(path, "/storage/nfs/") || strings.Contains(path, "/storage/zfs/") {
			t.Errorf("unexpected request %s", path)
		}
	}
}

func TestClient_RefusesAWrongToken(t *testing.T) {
	api := newFakeAPI(t)
	credentials := api.credentials(api.pin())
	credentials.TokenSecret = "wrong-secret-value"
	_, err := NewClient(credentials).Version(context.Background())
	refusalOf(t, err, ErrUnauthorized)
}

func TestClient_RefusesAnAnswerOverTheBound(t *testing.T) {
	api := newFakeAPI(t)
	api.handler = func(w http.ResponseWriter, _ *http.Request) bool {
		_, _ = w.Write([]byte(`{"data":{"version":"` + strings.Repeat("a", 9<<20) + `"}}`))
		return true
	}
	_, err := NewClient(api.credentials(api.pin())).Version(context.Background())
	refusalOf(t, err, ErrBadResponse)
}

func TestClient_RefusesAnAnswerThatIsNotTheAPI(t *testing.T) {
	for name, answer := range map[string]string{
		"not JSON":      `<html>login</html>`,
		"no data":       `{"errors":{}}`,
		"wrong type":    `{"data":"version"}`,
		"empty content": ``,
	} {
		t.Run(name, func(t *testing.T) {
			api := newFakeAPI(t)
			api.handler = func(w http.ResponseWriter, _ *http.Request) bool {
				_, _ = w.Write([]byte(answer))
				return true
			}
			_, err := NewClient(api.credentials(api.pin())).Version(context.Background())
			refusalOf(t, err, ErrBadResponse)
		})
	}
}

func TestClient_RefusesAnErrorStatus(t *testing.T) {
	api := newFakeAPI(t)
	api.handler = func(w http.ResponseWriter, _ *http.Request) bool {
		http.Error(w, "boom", http.StatusInternalServerError)
		return true
	}
	_, err := NewClient(api.credentials(api.pin())).Version(context.Background())
	refusalOf(t, err, ErrBadResponse)
}

func TestClient_DoesNotFollowARedirect(t *testing.T) {
	api := newFakeAPI(t)
	elsewhere := newFakeAPI(t)
	api.handler = func(w http.ResponseWriter, r *http.Request) bool {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusFound)
		return true
	}
	_, err := NewClient(api.credentials(api.pin())).Version(context.Background())
	refusalOf(t, err, ErrBadResponse)
	if len(elsewhere.requested()) != 0 {
		t.Fatalf("the redirect was followed: %v", elsewhere.requested())
	}
}

func TestClient_PinDecidesTheCertificate(t *testing.T) {
	api := newFakeAPI(t)

	// Sans empreinte, le magasin du système juge : le certificat de test n'y est pas.
	_, err := NewClient(api.credentials("")).Version(context.Background())
	refusalOf(t, err, ErrCertificate)

	// Une autre empreinte : refusé, et rien n'a été demandé à l'API.
	wrong := strings.Repeat("ab", 32)
	before := len(api.requested())
	_, err = NewClient(api.credentials(wrong)).Version(context.Background())
	refusalOf(t, err, ErrCertificate)
	if len(api.requested()) != before {
		t.Fatal("a request went out to a host whose certificate does not match the pin")
	}

	// La bonne : lu.
	version, err := NewClient(api.credentials(api.pin())).Version(context.Background())
	if err != nil || version != "8.2.4" {
		t.Fatalf("pinned read: %q, %v", version, err)
	}
}

func TestClient_AnUnreachableHostIsUnreachable(t *testing.T) {
	api := newFakeAPI(t)
	credentials := api.credentials(api.pin())
	api.Close()
	_, err := NewClient(credentials).Version(context.Background())
	refusalOf(t, err, ErrUnreachable)
}

func TestClient_TheSecretNeverAppearsInAnError(t *testing.T) {
	api := newFakeAPI(t)
	credentials := api.credentials(api.pin())
	credentials.TokenSecret = "wrong-secret-value"
	for name, run := range map[string]func() error{
		"unauthorized": func() error { _, err := NewClient(credentials).Version(context.Background()); return err },
		"certificate": func() error {
			other := credentials
			other.Pin = strings.Repeat("ab", 32)
			_, err := NewClient(other).Version(context.Background())
			return err
		},
	} {
		if err := run(); err == nil || strings.Contains(fmt.Sprint(err), credentials.TokenSecret) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestClient_EscapesTheNodeAndTheStorageInThePath(t *testing.T) {
	api := newFakeAPI(t)
	_, _ = NewClient(api.credentials(api.pin())).Backups(context.Background(), "pve/../x", "a b")
	for _, path := range api.requested() {
		if !strings.HasPrefix(path, "/api2/json/nodes/pve%2F..%2Fx/storage/a%20b/content") {
			t.Errorf("path not escaped: %s", path)
		}
	}
}
