package agentapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
