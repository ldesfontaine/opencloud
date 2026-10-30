package server

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/firewall"
	"github.com/ldesfontaine/opencloud/internal/firewall/opnsense"
	"github.com/ldesfontaine/opencloud/internal/firewall/opnsense/opnsensetest"
)

const (
	firewallGoldenURL    = "https://fw.lan"
	firewallGoldenPinHex = "abababababababababababababababababababababababababababababababab"
)

func firewallRequestFor(fake *opnsensetest.Server, secret string) firewallRequest {
	return firewallRequest{
		Name: "Bordure", Kind: firewall.KindOPNsense, APIURL: fake.URL,
		APIKey: opnsensetest.Key, APISecret: secret, Pin: fake.Pin(),
	}
}

// firewallBench dresse le serveur d'essai avec un faux OPNsense et un journal
// que le test relit.
func firewallBench(t *testing.T) (*testServer, *opnsensetest.Server, *bytes.Buffer) {
	t.Helper()
	server := newTestServer(t)
	var journal bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&journal, &slog.HandlerOptions{Level: slog.LevelDebug}))
	component := firewall.New(server.db, logger)
	component.SetClock(func() time.Time { return testNow })
	component.SetWatcher(server.bus)
	component.SetAuditor(server.journal)
	component.SetConnector(firewall.KindOPNsense, opnsense.New)
	server.Server.firewalls = component
	server.firewalls = component
	return server, opnsensetest.New(t), &journal
}

func declareFirewallThroughAPI(t *testing.T, server *testServer, fake *opnsensetest.Server) (string, *httptest.ResponseRecorder) {
	t.Helper()
	created := callAPI(server.Server, http.MethodPost, "/api/firewalls", firewallRequestFor(fake, opnsensetest.Secret))
	if created.Code != http.StatusCreated {
		t.Fatalf("declare: %d %s", created.Code, created.Body.String())
	}
	var detail firewallDetailJSON
	decodeAPI(t, created, &detail)
	return detail.ID, created
}

func goldenFirewall(fake *opnsensetest.Server, body string) string {
	return strings.NewReplacer(fake.URL, firewallGoldenURL, fake.Pin(), firewallGoldenPinHex).Replace(normalize(body))
}

func TestFirewalls_NoneDeclaredIsAnEmptyListAndNothingElseChanges(t *testing.T) {
	server := newTestServer(t)
	got := callAPI(server.Server, http.MethodGet, "/api/firewalls", nil)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"firewalls":[]`) {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	compareGolden(t, filepath.Join("testdata", "firewalls_empty.golden.json"), normalize(got.Body.String()))
	if got := callAPI(server.Server, http.MethodGet, "/api/firewalls/inconnu", nil); got.Code != http.StatusNotFound {
		t.Fatalf("unknown firewall: %d", got.Code)
	}
	for _, part := range []string{"rules", "interfaces", "leases"} {
		if got := callAPI(server.Server, http.MethodGet, "/api/firewalls/inconnu/"+part, nil); got.Code != http.StatusNotFound {
			t.Fatalf("%s of an unknown firewall: %d", part, got.Code)
		}
	}
}

// La paire répond à l'API avant que rien ne s'écrive.
func TestFirewalls_PostChecksThePairBeforeWriting(t *testing.T) {
	server, fake, _ := firewallBench(t)
	got := callAPI(server.Server, http.MethodPost, "/api/firewalls", firewallRequestFor(fake, "wrong-secret-of-the-pair"))
	if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "firewall.unauthorized" {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	if ids, err := server.db.ListFirewallIDs(context.Background()); err != nil || len(ids) != 0 {
		t.Fatalf("a refused pair must leave no firewall: %v %v", ids, err)
	}
}

func TestFirewalls_PostRefusesWhatTheOperatorGotWrong(t *testing.T) {
	server, fake, _ := firewallBench(t)
	good := firewallRequestFor(fake, opnsensetest.Secret)
	for name, tc := range map[string]struct {
		change func(*firewallRequest)
		want   string
	}{
		"name":        {func(r *firewallRequest) { r.Name = " " }, "firewall.name_invalid"},
		"kind":        {func(r *firewallRequest) { r.Kind = "pfsense" }, "firewall.kind_invalid"},
		"address":     {func(r *firewallRequest) { r.APIURL = "http://fw.lan" }, "firewall.url_invalid"},
		"key":         {func(r *firewallRequest) { r.APIKey = "court" }, "firewall.key_invalid"},
		"secret":      {func(r *firewallRequest) { r.APISecret = "court" }, "firewall.secret_invalid"},
		"pin":         {func(r *firewallRequest) { r.Pin = "zz" }, "firewall.pin_invalid"},
		"other pin":   {func(r *firewallRequest) { r.Pin = strings.Repeat("ab", 32) }, "firewall.certificate"},
		"no pin":      {func(r *firewallRequest) { r.Pin = "" }, "firewall.certificate"},
		"nobody home": {func(r *firewallRequest) { r.APIURL = "https://127.0.0.1:1" }, "firewall.unreachable"},
	} {
		request := good
		tc.change(&request)
		got := callAPI(server.Server, http.MethodPost, "/api/firewalls", request)
		if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != tc.want {
			t.Errorf("%s: %d %s, want %s", name, got.Code, got.Body.String(), tc.want)
		}
	}
	malformed := httptest.NewRecorder()
	server.ServeHTTP(malformed, httptest.NewRequest(http.MethodPost, "/api/firewalls", strings.NewReader("{")))
	if malformed.Code == http.StatusCreated {
		t.Errorf("malformed body accepted")
	}
}

// Le cycle entier : déclarer lit tout de suite, la liste et les trois relevés le
// disent, le retrait remet tout à zéro. Les réponses sont figées.
func TestFirewalls_DeclareReadRedeclareAndRemove(t *testing.T) {
	server, fake, _ := firewallBench(t)
	id, created := declareFirewallThroughAPI(t, server, fake)

	path := "/api/firewalls/" + id
	if got := callAPI(server.Server, http.MethodGet, path, nil); got.Body.String() != created.Body.String() {
		t.Fatalf("the post answers with the card:\n%s\n%s", created.Body.String(), got.Body.String())
	}
	compareGolden(t, filepath.Join("testdata", "firewall.golden.json"), goldenFirewall(fake, created.Body.String()))
	compareGolden(t, filepath.Join("testdata", "firewalls.golden.json"), goldenFirewall(fake, callAPI(server.Server, http.MethodGet, "/api/firewalls", nil).Body.String()))
	compareGolden(t, filepath.Join("testdata", "firewall_rules.golden.json"), goldenFirewall(fake, callAPI(server.Server, http.MethodGet, path+"/rules", nil).Body.String()))
	compareGolden(t, filepath.Join("testdata", "firewall_interfaces.golden.json"), goldenFirewall(fake, callAPI(server.Server, http.MethodGet, path+"/interfaces", nil).Body.String()))
	compareGolden(t, filepath.Join("testdata", "firewall_leases.golden.json"), goldenFirewall(fake, callAPI(server.Server, http.MethodGet, path+"/leases", nil).Body.String()))

	var detail firewallDetailJSON
	decodeAPI(t, created, &detail)
	if detail.LastError != "" || detail.LastReadAt == nil || detail.Version != "OPNsense 25.1.6-amd64" || !detail.IsPinned ||
		detail.Counts.Rules != 3 || detail.Counts.Interfaces != 2 || detail.Counts.Leases != 3 || len(detail.Gateways) != 3 {
		t.Fatalf("card: %+v", detail)
	}

	// Un second pare-feu de même nom est refusé, un autre nom passe.
	duplicate := callAPI(server.Server, http.MethodPost, "/api/firewalls", firewallRequestFor(fake, opnsensetest.Secret))
	if duplicate.Code != http.StatusConflict || errorCode(t, duplicate) != "firewall.name_taken" {
		t.Fatalf("duplicate: %d %s", duplicate.Code, duplicate.Body.String())
	}

	// Redéclarer : le secret se ressaisit, la fiche garde son identifiant.
	renamed := firewallRequestFor(fake, opnsensetest.Secret)
	renamed.Name = "Bordure principale"
	put := callAPI(server.Server, http.MethodPut, path, renamed)
	if put.Code != http.StatusOK {
		t.Fatalf("put: %d %s", put.Code, put.Body.String())
	}
	decodeAPI(t, put, &detail)
	if detail.ID != id || detail.Name != "Bordure principale" {
		t.Fatalf("redeclared card: %+v", detail)
	}
	if got := callAPI(server.Server, http.MethodPut, "/api/firewalls/inconnu", renamed); got.Code != http.StatusNotFound {
		t.Fatalf("put on an unknown firewall: %d", got.Code)
	}
	wrong := firewallRequestFor(fake, "wrong-secret-of-the-pair")
	if got := callAPI(server.Server, http.MethodPut, path, wrong); got.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a wrong pair must not replace the connection: %d", got.Code)
	}

	if got := callAPI(server.Server, http.MethodDelete, path, nil); got.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodGet, path, nil); got.Code != http.StatusNotFound {
		t.Fatalf("get after delete: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodDelete, path, nil); got.Code != http.StatusNotFound {
		t.Fatalf("second delete: %d", got.Code)
	}
}

// Le relevé qui échoue garde le précédent et dit pourquoi.
func TestFirewalls_AFailingReadingKeepsTheLastOneAndSaysWhy(t *testing.T) {
	server, fake, _ := firewallBench(t)
	id, _ := declareFirewallThroughAPI(t, server, fake)

	fake.Answer(opnsensetest.RulesPath, http.StatusInternalServerError, `{}`)
	if err := server.firewalls.Refresh(context.Background(), id); err == nil {
		t.Fatal("the broken API must fail the reading")
	}
	var detail firewallDetailJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/firewalls/"+id, nil), &detail)
	if detail.LastError != "firewall.bad_response" || detail.Counts.Rules != 3 || detail.LastReadAt == nil {
		t.Fatalf("a failure must keep the last reading: %+v", detail)
	}
}

// Un jeton d'API lit les cinq routes, sans la clé, ni l'empreinte, ni le secret,
// et n'en écrit aucune.
func TestFirewalls_ATokenReadsWithoutAnySecretAndWritesNothing(t *testing.T) {
	server, fake, _ := firewallBench(t)
	id, _ := declareFirewallThroughAPI(t, server, fake)
	bearer := server.issueAPIToken(t, "mcp-maison")

	reads := []string{"/api/firewalls", "/api/firewalls/" + id, "/api/firewalls/" + id + "/rules", "/api/firewalls/" + id + "/interfaces", "/api/firewalls/" + id + "/leases"}
	for _, path := range reads {
		got := callBearer(server.Server, path, bearer)
		if got.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, got.Code, got.Body.String())
		}
		for _, secret := range []string{opnsensetest.Secret, opnsensetest.Key, fake.Pin()} {
			if strings.Contains(got.Body.String(), secret) {
				t.Errorf("%s leaks %q to a token", path, secret[:8])
			}
		}
	}
	// L'opérateur, lui, lit la clé et l'empreinte de sa déclaration.
	operator := callAPI(server.Server, http.MethodGet, "/api/firewalls/"+id, nil).Body.String()
	if !strings.Contains(operator, opnsensetest.Key) || !strings.Contains(operator, fake.Pin()) {
		t.Fatalf("the operator should read the key and the pin: %s", operator)
	}
	if strings.Contains(operator, opnsensetest.Secret) {
		t.Fatalf("not even the operator reads the secret: %s", operator)
	}

	for _, attempt := range []struct{ method, path string }{
		{http.MethodPost, "/api/firewalls"},
		{http.MethodPut, "/api/firewalls/" + id},
		{http.MethodDelete, "/api/firewalls/" + id},
	} {
		got := callBearerWith(server.Server, attempt.method, attempt.path, bearer, "")
		if got.Code != http.StatusForbidden || errorCode(t, got) != codeAPITokenRouteForbidden {
			t.Errorf("%s %s by a token: %d %s", attempt.method, attempt.path, got.Code, got.Body.String())
		}
	}
	if got := callAPI(server.Server, http.MethodGet, "/api/firewalls/"+id, nil); got.Code != http.StatusOK {
		t.Fatalf("the token must not have removed the firewall: %d", got.Code)
	}
}

// Le secret n'est écrit nulle part où l'on peut le relire : ni dans une
// réponse, ni dans l'audit, ni dans le journal, même quand tout va mal.
func TestFirewalls_TheSecretNeverLeaves(t *testing.T) {
	server, fake, journal := firewallBench(t)
	responses := []string{}
	record := func(recorder *httptest.ResponseRecorder) { responses = append(responses, recorder.Body.String()) }

	record(callAPI(server.Server, http.MethodPost, "/api/firewalls", firewallRequestFor(fake, "wrong-secret-of-the-pair")))
	id, created := declareFirewallThroughAPI(t, server, fake)
	record(created)
	record(callAPI(server.Server, http.MethodGet, "/api/firewalls", nil))
	record(callAPI(server.Server, http.MethodGet, "/api/firewalls/"+id, nil))
	fake.Answer(opnsensetest.RulesPath, http.StatusInternalServerError, `{}`)
	_ = server.firewalls.Refresh(context.Background(), id)
	record(callAPI(server.Server, http.MethodGet, "/api/firewalls/"+id, nil))
	record(callAPI(server.Server, http.MethodPut, "/api/firewalls/"+id, firewallRequestFor(fake, opnsensetest.Secret)))
	record(callAPI(server.Server, http.MethodDelete, "/api/firewalls/"+id, nil))

	events, err := server.db.ListAuditEvents(context.Background(), audit.Query{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var seen []audit.Action
	for _, event := range events {
		seen = append(seen, event.Action)
		for key, value := range event.Details {
			responses = append(responses, key+"="+value)
		}
	}
	responses = append(responses, journal.String())
	for _, leaked := range []string{opnsensetest.Secret, "wrong-secret-of-the-pair"} {
		for _, text := range responses {
			if strings.Contains(text, leaked) {
				t.Fatalf("the secret %q leaked in: %s", leaked[:8], text)
			}
		}
	}
	// La déclaration, la redéclaration et le retrait sont au journal ; les
	// relevés (réussis ou non) n'y sont pas.
	want := []audit.Action{audit.FirewallDelete, audit.FirewallUpdate, audit.FirewallCreate}
	var firewallActions []audit.Action
	for _, action := range seen {
		if action.ObjectKind() == audit.ObjectFirewall {
			firewallActions = append(firewallActions, action)
		}
	}
	if len(firewallActions) != len(want) {
		t.Fatalf("audit actions %v, want %v", firewallActions, want)
	}
	for index := range want {
		if firewallActions[index] != want[index] {
			t.Fatalf("audit actions %v, want %v", firewallActions, want)
		}
	}
}
