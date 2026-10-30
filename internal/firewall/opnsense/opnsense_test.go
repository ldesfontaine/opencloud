package opnsense

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/firewall"
	"github.com/ldesfontaine/opencloud/internal/firewall/opnsense/opnsensetest"
)

const (
	testKey    = opnsensetest.Key
	testSecret = opnsensetest.Secret
)

// fakeOPNsense est le faux partagé, avec un client prêt à l'emploi.
type fakeOPNsense struct{ *opnsensetest.Server }

func newFakeOPNsense(t *testing.T) *fakeOPNsense {
	t.Helper()
	return &fakeOPNsense{opnsensetest.New(t)}
}

func (fake *fakeOPNsense) answer(path string, status int, body string) {
	fake.Answer(path, status, body)
}
func (fake *fakeOPNsense) seen() []string { return fake.Requests() }
func (fake *fakeOPNsense) pin() string    { return fake.Pin() }

func (fake *fakeOPNsense) client(pin string) firewall.Connector {
	return New(firewall.Credentials{
		Firewall:  firewall.Firewall{ID: "fw1", Kind: firewall.KindOPNsense, APIURL: fake.URL, APIKey: testKey, Pin: pin},
		APISecret: testSecret,
	})
}

func TestRead_AgainstARealisticOPNsense_RendersTheVendorNeutralSnapshot(t *testing.T) {
	fake := newFakeOPNsense(t)
	snapshot, err := firewall.Read(context.Background(), fake.client(fake.pin()))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != "OPNsense 25.1.6-amd64" {
		t.Errorf("version %q", snapshot.Version)
	}
	if len(snapshot.Interfaces) != 2 || snapshot.Interfaces[0] != (firewall.Interface{Name: "wan", Device: "igb0", Role: "WAN", Address: "203.0.113.2/24", Status: "up"}) ||
		snapshot.Interfaces[1].Address != "192.168.1.1/24" {
		t.Errorf("interfaces (the unassigned one is left out): %+v", snapshot.Interfaces)
	}
	if len(snapshot.Rules) != 3 {
		t.Fatalf("rules: %+v", snapshot.Rules)
	}
	web := snapshot.Rules[0]
	if web.Action != "pass" || web.Direction != "in" || web.Protocol != "TCP" || web.Source != "192.168.1.0/24" || web.Destination != "any" ||
		web.DestinationPort != "443" || web.Description != "Web sortant" || !web.Enabled || web.Interface != "lan" {
		t.Errorf("first rule: %+v", web)
	}
	if snapshot.Rules[1].Enabled || !snapshot.Rules[2].Enabled {
		t.Errorf("enabled flags (\"0\" is off, an absent field is on): %+v", snapshot.Rules)
	}
	if len(snapshot.Leases) != 3 {
		t.Fatalf("leases from the three DHCP servers: %+v", snapshot.Leases)
	}
	dnsmasq, kea, isc := snapshot.Leases[0], snapshot.Leases[1], snapshot.Leases[2]
	if dnsmasq.Server != "dnsmasq" || dnsmasq.MAC != "aa:bb:cc:dd:ee:ff" || dnsmasq.Hostname != "nas" || dnsmasq.Interface != "LAN" ||
		!dnsmasq.ExpiresAt.Equal(time.Unix(1800003600, 0)) || dnsmasq.IsStatic {
		t.Errorf("dnsmasq lease: %+v", dnsmasq)
	}
	if kea.Server != "kea" || !kea.IsStatic || !kea.ExpiresAt.IsZero() {
		t.Errorf("kea lease (expire 0 means none): %+v", kea)
	}
	if isc.Server != "isc" || isc.MAC != "de:ad:be:ef:00:01" || !isc.ExpiresAt.Equal(time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("isc lease: %+v", isc)
	}
	if len(snapshot.Gateways) != 3 {
		t.Fatalf("gateways: %+v", snapshot.Gateways)
	}
	online, offline, unmonitored := snapshot.Gateways[0], snapshot.Gateways[1], snapshot.Gateways[2]
	if online.Status != firewall.GatewayOnline || !online.IsMonitored || online.DelayMillis != 1.4 || online.LossPercent != 0 {
		t.Errorf("online gateway: %+v", online)
	}
	if offline.Status != firewall.GatewayOffline || offline.LossPercent != 100 {
		t.Errorf("offline gateway: %+v", offline)
	}
	if unmonitored.IsMonitored {
		t.Errorf("a gateway the firewall does not probe has no measure: %+v", unmonitored)
	}
}

func TestRead_OnlyEverSendsGETsToTheReadingEndpoints(t *testing.T) {
	fake := newFakeOPNsense(t)
	if _, err := firewall.Read(context.Background(), fake.client(fake.pin())); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		versionPath: true, interfacesPath: true, rulesPath: true, gatewaysPath: true,
		dnsmasqLeasesPath: true, keaLeasesPath: true, iscLeasesPath: true,
	}
	for _, request := range fake.seen() {
		method, path, _ := strings.Cut(request, " ")
		if method != http.MethodGet || !allowed[path] {
			t.Errorf("unexpected request %q", request)
		}
	}
}

func TestSource_HasNoWritingCallToOPNsense(t *testing.T) {
	forbidden := []*regexp.Regexp{
		regexp.MustCompile(`http\.Method(Post|Put|Patch|Delete)\b`),
		regexp.MustCompile(`"(POST|PUT|PATCH|DELETE)"`),
		regexp.MustCompile(`\.(Post|PostForm|Head)\(`),
		// Les gestes d'écriture de l'API d'OPNsense : ajouter, supprimer, activer,
		// appliquer, rétablir, redémarrer.
		regexp.MustCompile(`(?i)/(add|del|set|toggle|apply|revert|savepoint|reconfigure|restart|reboot|halt|upgrade|update)[A-Za-z_]*(Rule|Item|Lease)?["/?]`),
	}
	for _, pattern := range []string{"*.go", "../*.go"} {
		files, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			content, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			for _, pattern := range forbidden {
				if found := pattern.Find(content); found != nil {
					t.Errorf("%s: %q looks like a writing call", file, found)
				}
			}
		}
	}
}

func TestClient_NewRequestsAreAlwaysGET(t *testing.T) {
	// get est le seul émetteur : son code ne nomme que http.MethodGet.
	content, err := os.ReadFile("client.go")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(content), "http.NewRequestWithContext("); got != 1 {
		t.Fatalf("%d request builders in client.go, want exactly one", got)
	}
	if !strings.Contains(string(content), "http.NewRequestWithContext(ctx, http.MethodGet,") {
		t.Fatal("the only request builder must send a GET")
	}
}

func TestClient_AuthenticatesWithBasicAndNeverLeaksTheSecret(t *testing.T) {
	fake := newFakeOPNsense(t)
	wrong := New(firewall.Credentials{
		Firewall:  firewall.Firewall{APIURL: fake.URL, APIKey: testKey, Pin: fake.pin()},
		APISecret: "not-the-secret-at-all-1234567890",
	})
	_, err := wrong.Version(context.Background())
	if !errors.Is(err, firewall.ErrUnauthorized) {
		t.Fatalf("a wrong secret: %v", err)
	}
	for _, leaked := range []string{testSecret, "not-the-secret-at-all-1234567890", testKey} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("the error leaks a credential: %v", err)
		}
	}
	if _, err := fake.client(fake.pin()).Version(context.Background()); err != nil {
		t.Fatalf("the right pair: %v", err)
	}
}

func TestClient_ForbiddenIsUnauthorized(t *testing.T) {
	fake := newFakeOPNsense(t)
	fake.answer(rulesPath, http.StatusForbidden, `{"status":403,"message":"Forbidden"}`)
	_, err := fake.client(fake.pin()).Rules(context.Background())
	if !errors.Is(err, firewall.ErrUnauthorized) {
		t.Fatalf("a missing privilege: %v", err)
	}
}

func TestClient_CertificateIsJudgedByThePinOnly(t *testing.T) {
	fake := newFakeOPNsense(t)
	// Sans empreinte, le certificat auto-signé n'est pas reconnu.
	_, err := fake.client("").Version(context.Background())
	if !errors.Is(err, firewall.ErrCertificate) {
		t.Fatalf("self-signed without pin: %v", err)
	}
	_, err = fake.client(strings.Repeat("ab", 32)).Version(context.Background())
	if !errors.Is(err, firewall.ErrCertificate) {
		t.Fatalf("another pin: %v", err)
	}
	if _, err := fake.client(fake.pin()).Version(context.Background()); err != nil {
		t.Fatalf("the right pin: %v", err)
	}
}

func TestClient_UnreachableHost(t *testing.T) {
	fake := newFakeOPNsense(t)
	client := fake.client(fake.pin())
	fake.Close()
	if _, err := client.Version(context.Background()); !errors.Is(err, firewall.ErrUnreachable) {
		t.Fatalf("a closed server: %v", err)
	}
}

func TestClient_DoesNotFollowRedirects(t *testing.T) {
	var elsewhere int
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere++
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	fake := newFakeOPNsense(t)
	fake.answer(versionPath, http.StatusFound, "")
	redirecting := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/steal", http.StatusFound)
	}))
	defer redirecting.Close()
	sum := sha256.Sum256(redirecting.Certificate().Raw)
	client := New(firewall.Credentials{
		Firewall:  firewall.Firewall{APIURL: redirecting.URL, APIKey: testKey, Pin: hex.EncodeToString(sum[:])},
		APISecret: testSecret,
	})
	_, err := client.Version(context.Background())
	if !errors.Is(err, firewall.ErrBadResponse) {
		t.Fatalf("a redirection: %v", err)
	}
	if elsewhere != 0 {
		t.Fatal("the redirection was followed and the credentials went elsewhere")
	}
}

func TestClient_BadAnswersAreRefused(t *testing.T) {
	cases := map[string]struct {
		path   string
		status int
		body   string
		call   func(firewall.Connector) error
	}{
		"not json":       {versionPath, 200, `<html>login</html>`, func(c firewall.Connector) error { _, err := c.Version(context.Background()); return err }},
		"no version":     {versionPath, 200, `{"versions":[]}`, func(c firewall.Connector) error { _, err := c.Version(context.Background()); return err }},
		"server error":   {rulesPath, 500, `{}`, func(c firewall.Connector) error { _, err := c.Rules(context.Background()); return err }},
		"truncated list": {rulesPath, 200, fmt.Sprintf(`{"total":%d,"rows":[]}`, maxRules+1), func(c firewall.Connector) error { _, err := c.Rules(context.Background()); return err }},
		"too big":        {interfacesPath, 200, `{"rows":[{"description":"` + strings.Repeat("x", maxResponseBytes) + `"}]}`, func(c firewall.Connector) error { _, err := c.Interfaces(context.Background()); return err }},
		"no route here":  {gatewaysPath, 404, `{}`, func(c firewall.Connector) error { _, err := c.Gateways(context.Background()); return err }},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			fake := newFakeOPNsense(t)
			fake.answer(test.path, test.status, test.body)
			if err := test.call(fake.client(fake.pin())); !errors.Is(err, firewall.ErrBadResponse) {
				t.Fatalf("got %v, want a bad response", err)
			}
		})
	}
}

func TestLeases_AServerThatIsAbsentOrForbiddenIsSkippedUnlessNoneAnswers(t *testing.T) {
	fake := newFakeOPNsense(t)
	fake.answer(keaLeasesPath, http.StatusNotFound, `{}`)
	fake.answer(iscLeasesPath, http.StatusForbidden, `{}`)
	leases, err := fake.client(fake.pin()).Leases(context.Background())
	if err != nil || len(leases) != 1 || leases[0].Server != "dnsmasq" {
		t.Fatalf("one server answers: %+v %v", leases, err)
	}

	fake.answer(dnsmasqLeasesPath, http.StatusNotFound, `{}`)
	leases, err = fake.client(fake.pin()).Leases(context.Background())
	if !errors.Is(err, firewall.ErrUnauthorized) {
		t.Fatalf("none answers and one is forbidden: %+v %v", leases, err)
	}

	fake.answer(iscLeasesPath, http.StatusNotFound, `{}`)
	leases, err = fake.client(fake.pin()).Leases(context.Background())
	if err != nil || len(leases) != 0 {
		t.Fatalf("no DHCP server at all is no lease, not an error: %+v %v", leases, err)
	}
}

func TestBoundedText_NeverCutsACharacterInHalf(t *testing.T) {
	long := strings.Repeat("é", maxTextBytes)
	cut := boundedText(long)
	if len(cut) > maxTextBytes || !strings.HasSuffix(long, cut[len(cut)-2:]) {
		t.Fatalf("%d bytes", len(cut))
	}
}
