package agentapi

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

// Un colis ne se sert qu'à la machine pour laquelle il a été écrit, sous le
// jeton de sa session et à un agent du protocole courant.
func TestParcel_ServesItsMachineOnly(t *testing.T) {
	server := newBench(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	_, session, _, cancel := server.openStream(t, enrolled.ID, private)
	defer cancel()

	state, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	outbox, err := action.NewOutbox(state)
	if err != nil {
		t.Fatal(err)
	}
	server.actions.SetOutbox(outbox)
	writer, err := outbox.Create(enrolled.ID)
	if err != nil {
		t.Fatal(err)
	}
	file, err := writer.Create("opencloud-20261021-030000.tar.age")
	if err != nil {
		t.Fatal(err)
	}
	const content = "age-encryption.org/v1\nun contenu de test"
	if _, err := io.WriteString(file, content); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	parcel, files := writer.Seal()
	path := agentwire.ParcelPath(parcel, files[0].Name)

	get := func(path, token string, protocol bool) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		if protocol {
			request.Header.Set(agentwire.HeaderProtocol, agentwire.ProtocolVersion)
		}
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		return recorder
	}

	served := get(path, session, true)
	if served.Code != http.StatusOK || served.Body.String() != content || served.Header().Get("Cache-Control") != "no-store" ||
		served.Header().Get("Content-Type") != "application/octet-stream" || served.Header().Get("Content-Length") != "40" {
		t.Fatalf("served %d %q %v", served.Code, served.Body.String(), served.Header())
	}
	for name, got := range map[string]*httptest.ResponseRecorder{
		"no token":      get(path, "", true),
		"a wrong token": get(path, "not-a-session", true),
		"protocol 1":    get(path, session, false),
		"unknown file":  get(agentwire.ParcelPath(parcel, "autre.age"), session, true),
		"unknown":       get(agentwire.ParcelPath("colis-inconnu", files[0].Name), session, true),
	} {
		want := map[string]int{"no token": http.StatusUnauthorized, "a wrong token": http.StatusUnauthorized, "protocol 1": http.StatusUpgradeRequired}[name]
		if want == 0 {
			want = http.StatusNotFound
		}
		if got.Code != want {
			t.Errorf("%s: %d %s", name, got.Code, got.Body.String())
		}
	}

	// Un colis écrit pour une autre machine : 404, comme un colis inconnu.
	other, err := outbox.Create("another-machine")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := other.Create("opencloud-20261021-030000.tar.age")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(foreign, content)
	_ = foreign.Close()
	otherParcel, otherFiles := other.Seal()
	if got := get(agentwire.ParcelPath(otherParcel, otherFiles[0].Name), session, true); got.Code != http.StatusNotFound {
		t.Fatalf("a parcel of another machine: %d", got.Code)
	}
}

const (
	otherMachineID = "22222222-3333-4444-8555-666666666666"
	archiveName    = "pve-vm-5012-20261021-030000.tar.age"
	archiveContent = "age-encryption.org/v1\nune archive de test"
)

func sumOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// Le producteur téléverse dans le colis que le serveur lui a ouvert, la
// destination le lit ensuite, et nul autre ne touche ni à l'un ni à l'autre.
func TestParcelUpload_TheProducerFillsAndTheDestinationReads(t *testing.T) {
	server := newBench(t)
	producer, producerKey := server.enroll(t, "pve", remoteID)
	destination, destinationKey := server.enroll(t, "pi", otherMachineID)
	_, producerSession, _, cancelProducer := server.openStream(t, producer.ID, producerKey)
	defer cancelProducer()
	_, destinationSession, _, cancelDestination := server.openStream(t, destination.ID, destinationKey)
	defer cancelDestination()

	state, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	outbox, err := action.NewOutbox(state)
	if err != nil {
		t.Fatal(err)
	}
	server.actions.SetOutbox(outbox)
	upload, err := outbox.CreateForUpload(destination.ID, producer.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := agentwire.ParcelPath(upload.ID(), archiveName)

	call := func(method, path, token, content, sum string, protocol bool) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(content))
		if method == http.MethodGet {
			request = httptest.NewRequest(method, path, nil)
		}
		if protocol {
			request.Header.Set(agentwire.HeaderProtocol, agentwire.ProtocolVersion)
		}
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		if sum != "" {
			request.Header.Set(agentwire.HeaderParcelSHA256, sum)
		}
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		return recorder
	}
	put := func(token, content, sum string) *httptest.ResponseRecorder {
		return call(http.MethodPut, path, token, content, sum, true)
	}

	// Les refus d'avant le colis : pas de session, pas de protocole, et un colis
	// qui n'attend ni la destination ni une machine inconnue.
	for name, got := range map[string]*httptest.ResponseRecorder{
		"no token":      put("", archiveContent, sumOf(archiveContent)),
		"a wrong token": put("not-a-session", archiveContent, sumOf(archiveContent)),
		"protocol 1":    call(http.MethodPut, path, producerSession, archiveContent, sumOf(archiveContent), false),
	} {
		want := map[string]int{"no token": http.StatusUnauthorized, "a wrong token": http.StatusUnauthorized, "protocol 1": http.StatusUpgradeRequired}[name]
		if got.Code != want {
			t.Errorf("%s: %d %s", name, got.Code, got.Body.String())
		}
	}
	if got := put(destinationSession, archiveContent, sumOf(archiveContent)); got.Code != http.StatusNotFound {
		t.Fatalf("the destination fills its own parcel: %d %s", got.Code, got.Body.String())
	}
	if got := call(http.MethodPut, agentwire.ParcelPath("colis-inconnu", archiveName), producerSession, archiveContent, sumOf(archiveContent), true); got.Code != http.StatusNotFound {
		t.Fatalf("an unknown parcel: %d", got.Code)
	}

	// Les refus du fichier : somme absente ou fausse, nom, taille annoncée.
	missing := put(producerSession, archiveContent, "")
	wrong := put(producerSession, archiveContent, sumOf("autre"))
	badName := call(http.MethodPut, agentwire.ParcelPath(upload.ID(), "autre.txt"), producerSession, archiveContent, sumOf(archiveContent), true)
	for name, got := range map[string]*httptest.ResponseRecorder{"no sum": missing, "a wrong sum": wrong, "a bad name": badName} {
		if got.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", name, got.Code, got.Body.String())
		}
	}
	for name, want := range map[string]string{"bad_digest": missing.Body.String(), "bad_name": badName.Body.String()} {
		if !strings.Contains(want, name) {
			t.Errorf("answer %q lacks %q", want, name)
		}
	}
	chunked := httptest.NewRequest(http.MethodPut, path, strings.NewReader(archiveContent))
	chunked.ContentLength = -1
	chunked.Header.Set(agentwire.HeaderProtocol, agentwire.ProtocolVersion)
	chunked.Header.Set("Authorization", "Bearer "+producerSession)
	chunked.Header.Set(agentwire.HeaderParcelSHA256, sumOf(archiveContent))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, chunked)
	if recorder.Code != http.StatusLengthRequired {
		t.Errorf("no length: %d", recorder.Code)
	}
	oversized := httptest.NewRequest(http.MethodPut, path, strings.NewReader(archiveContent))
	oversized.ContentLength = action.MaxUploadBytes + 1
	oversized.Header.Set(agentwire.HeaderProtocol, agentwire.ProtocolVersion)
	oversized.Header.Set("Authorization", "Bearer "+producerSession)
	oversized.Header.Set(agentwire.HeaderParcelSHA256, sumOf(archiveContent))
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, oversized)
	if recorder.Code != http.StatusRequestEntityTooLarge || !strings.Contains(recorder.Body.String(), "parcel_too_large") {
		t.Errorf("too large: %d %s", recorder.Code, recorder.Body.String())
	}
	if files := upload.Files(); len(files) != 0 {
		t.Fatalf("a refused upload left %+v", files)
	}

	// Le bon envoi : écrit, puis un second du même nom refusé.
	if got := put(producerSession, archiveContent, sumOf(archiveContent)); got.Code != http.StatusCreated {
		t.Fatalf("the upload: %d %s", got.Code, got.Body.String())
	}
	if got := put(producerSession, archiveContent, sumOf(archiveContent)); got.Code != http.StatusConflict || !strings.Contains(got.Body.String(), "parcel_conflict") {
		t.Fatalf("the same name twice: %d %s", got.Code, got.Body.String())
	}
	read := call(http.MethodGet, path, destinationSession, "", "", true)
	if read.Code != http.StatusOK || read.Body.String() != archiveContent {
		t.Fatalf("the destination reads %d %q", read.Code, read.Body.String())
	}
	if got := call(http.MethodGet, path, producerSession, "", "", true); got.Code != http.StatusNotFound {
		t.Fatalf("the producer reads the parcel back: %d", got.Code)
	}

	// Un disque sans la place répond 413 disk_full, rien n'est écrit.
	outbox.SetFreeBytes(func(string) (uint64, error) { return 0, nil })
	second := "pve-vm-5013-20261021-030000.tar.age"
	request := httptest.NewRequest(http.MethodPut, agentwire.ParcelPath(upload.ID(), second), strings.NewReader(archiveContent))
	request.Header.Set(agentwire.HeaderProtocol, agentwire.ProtocolVersion)
	request.Header.Set("Authorization", "Bearer "+producerSession)
	request.Header.Set(agentwire.HeaderParcelSHA256, sumOf(archiveContent))
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge || !strings.Contains(recorder.Body.String(), "disk_full") {
		t.Fatalf("a full disk: %d %s", recorder.Code, recorder.Body.String())
	}
	if files := upload.Files(); len(files) != 1 {
		t.Fatalf("files %+v", files)
	}
}
