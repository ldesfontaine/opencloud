package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/sshkeys"
)

const (
	bastionID    = "66666666-7777-4888-8999-aaaaaaaaaaaa"
	sshKeyOne    = "SHA256:nThbg6kXUpJWGl7E1IGOCspRomTxdCARLviKw6E5SY8"
	sshKeyTwo    = "SHA256:Qa1b2c3d4e5f6g7h8i9j0KLMNOPQRSTUVWXYZabcdef"
	rejectedBody = `{"rejected":{"ssh_keys":"bad_ssh_keys"}}`
)

func sshKey(file, fingerprint, keyType string, bits int, comment string, options ...string) sshkeys.Key {
	if options == nil {
		options = []string{}
	}
	return sshkeys.Key{File: file, Fingerprint: fingerprint, Type: keyType, Bits: bits, Comment: comment, Options: options}
}

// Le signal de l'agent écrit le relevé de sa machine, que l'API rend par
// machine et pour toutes : des faits sur les clés, jamais le contenu d'un
// fichier. Une machine qui n'a rien relevé est là, sans date.
func TestSSHKeys_TheSignalWritesTheReportAndTheAPIReadsIt(t *testing.T) {
	server := newTestServer(t)
	enrolled, _ := server.enroll(t, "vps-paris-1", remoteID)
	server.enroll(t, "bastion", bastionID)
	session, err := server.machines.Connect(context.Background(), enrolled, "192.0.2.1", "v0.0.1", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	report := sshkeys.Report{Accounts: []sshkeys.Account{
		{Name: "root", UID: 0, State: sshkeys.StateRead, Ignored: 1, Keys: []sshkeys.Key{
			sshKey("authorized_keys", sshKeyOne, "ssh-ed25519", 256, "lucas@portable", "command", "from"),
			sshKey("authorized_keys", sshKeyTwo, "ssh-rsa", 2048, "vieux-pc"),
		}},
		{Name: "deploy", UID: 1001, State: sshkeys.StateDenied, Keys: []sshkeys.Key{}},
		{Name: "lucas", UID: 1000, State: sshkeys.StateUnsafe, Truncated: true, Keys: []sshkeys.Key{sshKey("authorized_keys2", sshKeyOne, "ssh-ed25519", 256, "")}},
	}, Truncated: true}

	if got := server.agentPost(session.Token, "/agent/signal", agentwire.SignalRequest{SSHKeys: &report}); got.Code != http.StatusNoContent {
		t.Fatalf("signal: %d %s", got.Code, got.Body.String())
	}

	one := callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID+"/ssh-keys", nil)
	if one.Code != http.StatusOK {
		t.Fatalf("machine: %d %s", one.Code, one.Body.String())
	}
	compareGolden(t, filepath.Join("testdata", "ssh_keys_machine.golden.json"), normalize(one.Body.String()))
	matrix := callAPI(server.Server, http.MethodGet, "/api/ssh-keys", nil)
	if matrix.Code != http.StatusOK {
		t.Fatalf("matrice: %d %s", matrix.Code, matrix.Body.String())
	}
	compareGolden(t, filepath.Join("testdata", "ssh_keys.golden.json"), normalize(matrix.Body.String()))
}

func TestSSHKeys_AMachineThatReportedNothingYetReadsEmptyAndUndated(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)

	got := callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID+"/ssh-keys", nil)

	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"reported_at":null`) || !strings.Contains(got.Body.String(), `"accounts":[]`) {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
}

func TestSSHKeys_AnUnknownMachineIsNotFound(t *testing.T) {
	server := newTestServer(t)
	if got := callAPI(server.Server, http.MethodGet, "/api/machines/inconnue/ssh-keys", nil); got.Code != http.StatusNotFound {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
}

// Un relevé hors bornes est refusé en entier, comme une autre section : le
// signal vit, les autres sections s'écrivent, l'ancien relevé reste.
func TestSSHKeys_AnInvalidReportIsRefusedAloneAndTheOldOneStays(t *testing.T) {
	server := newTestServer(t)
	enrolled, _ := server.enroll(t, "vps-paris-1", remoteID)
	session, err := server.machines.Connect(context.Background(), enrolled, "192.0.2.1", "v0.0.1", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	good := sshkeys.Report{Accounts: []sshkeys.Account{{Name: "root", State: sshkeys.StateRead, Keys: []sshkeys.Key{sshKey("authorized_keys", sshKeyOne, "ssh-ed25519", 256, "bonne")}}}}
	if got := server.agentPost(session.Token, "/agent/signal", agentwire.SignalRequest{SSHKeys: &good}); got.Code != http.StatusNoContent {
		t.Fatalf("signal: %d %s", got.Code, got.Body.String())
	}
	hostile := map[string]sshkeys.Report{
		"empreinte":   {Accounts: []sshkeys.Account{{Name: "root", State: sshkeys.StateRead, Keys: []sshkeys.Key{sshKey("authorized_keys", "../../etc/passwd", "ssh-ed25519", 256, "")}}}},
		"état":        {Accounts: []sshkeys.Account{{Name: "root", State: "ok"}}},
		"option":      {Accounts: []sshkeys.Account{{Name: "root", State: sshkeys.StateRead, Keys: []sshkeys.Key{sshKey("authorized_keys", sshKeyTwo, "ssh-ed25519", 256, "", `command="cat /etc/shadow"`)}}}},
		"commentaire": {Accounts: []sshkeys.Account{{Name: "root", State: sshkeys.StateRead, Keys: []sshkeys.Key{sshKey("authorized_keys", sshKeyTwo, "ssh-ed25519", 256, "\x1b[31m")}}}},
	}
	for name, report := range hostile {
		t.Run(name, func(t *testing.T) {
			got := server.agentPost(session.Token, "/agent/signal", agentwire.SignalRequest{SSHKeys: &report})
			if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "bad_ssh_keys") {
				t.Fatalf("relevé hostile accepté : %d %s", got.Code, got.Body.String())
			}
		})
	}

	one := callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID+"/ssh-keys", nil)
	if !strings.Contains(one.Body.String(), `"comment":"bonne"`) || strings.Contains(one.Body.String(), "shadow") {
		t.Fatalf("le relevé a changé : %s", one.Body.String())
	}
}

// Un agent qui sait lire un refus par section l'annonce : le serveur répond
// 200 avec la section refusée, les autres sont écrites.
func TestSSHKeys_AnAgentThatReadsSectionRefusalsGetsTheKeyOfTheRefusedSection(t *testing.T) {
	server := newTestServer(t)
	enrolled, _ := server.enroll(t, "vps-paris-1", remoteID)
	session, err := server.machines.Connect(context.Background(), enrolled, "192.0.2.1", "v0.0.1", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	report := sshkeys.Report{Accounts: []sshkeys.Account{{Name: "root", State: "ok"}}}

	got := signalAnnouncingSections(server, session.Token, agentwire.SignalRequest{SSHKeys: &report})

	if got.Code != http.StatusOK || strings.TrimSpace(got.Body.String()) != rejectedBody {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
}

// signalAnnouncingSections envoie un signal comme un agent qui lit le refus
// d'une section.
func signalAnnouncingSections(server *testServer, session string, payload agentwire.SignalRequest) *httptest.ResponseRecorder {
	var body bytes.Buffer
	_ = json.NewEncoder(&body).Encode(payload)
	request := httptest.NewRequest(http.MethodPost, "/agent/signal", &body)
	request.Header.Set("Authorization", "Bearer "+session)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(agentwire.HeaderSignalSections, "1")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}
