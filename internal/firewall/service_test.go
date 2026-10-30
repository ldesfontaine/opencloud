package firewall_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/firewall"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

// scriptedConnector répond ce que le test lui donne, sans réseau.
type scriptedConnector struct {
	mu       sync.Mutex
	failWith error
	rules    []firewall.Rule
	calls    int
}

func (c *scriptedConnector) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failWith = err
}

func (c *scriptedConnector) answer() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return c.failWith
}

func (c *scriptedConnector) Version(context.Context) (string, error) {
	return "OPNsense 25.1", c.answer()
}
func (c *scriptedConnector) Interfaces(context.Context) ([]firewall.Interface, error) {
	return []firewall.Interface{{Name: "lan", Status: "up"}}, c.answer()
}
func (c *scriptedConnector) Rules(context.Context) ([]firewall.Rule, error) {
	return c.rules, c.answer()
}
func (c *scriptedConnector) Leases(context.Context) ([]firewall.Lease, error) { return nil, c.answer() }
func (c *scriptedConnector) Gateways(context.Context) ([]firewall.Gateway, error) {
	return nil, c.answer()
}

type recordingWatcher struct{ count int }

func (w *recordingWatcher) FirewallsChanged() { w.count++ }

type bench struct {
	service   *firewall.Service
	db        *store.DB
	connector *scriptedConnector
	watcher   *recordingWatcher
	given     []firewall.Credentials
}

func newBench(t *testing.T) *bench {
	t.Helper()
	db := storetest.Open(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := &bench{
		db: db, watcher: &recordingWatcher{},
		connector: &scriptedConnector{rules: []firewall.Rule{{UUID: "r1", Action: "pass", Enabled: true}}},
	}
	b.service = firewall.New(db, logger)
	b.service.SetAuditor(audit.New(db, logger))
	b.service.SetWatcher(b.watcher)
	b.service.SetClock(func() time.Time { return time.Unix(1_800_000_000, 0) })
	b.service.SetConnector(firewall.KindOPNsense, func(credentials firewall.Credentials) firewall.Connector {
		b.given = append(b.given, credentials)
		return b.connector
	})
	return b
}

func definition() firewall.Definition {
	return firewall.Definition{
		Name: "Bordure", Kind: firewall.KindOPNsense, APIURL: "https://fw.lan",
		APIKey: "key-of-the-api-pair", APISecret: "secret-of-the-api-pair",
	}
}

func (b *bench) auditEvents(t *testing.T) []audit.Event {
	t.Helper()
	events, err := b.db.ListAuditEvents(context.Background(), audit.Query{Limit: 50, ObjectKind: audit.ObjectFirewall})
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestDeclare_ChecksThePairBeforeWritingAnything(t *testing.T) {
	b := newBench(t)
	b.connector.fail(firewall.ErrUnauthorized)
	_, err := b.service.Declare(context.Background(), definition())
	if !errors.Is(err, firewall.ErrUnauthorized) {
		t.Fatalf("got %v", err)
	}
	if ids, _ := b.db.ListFirewallIDs(context.Background()); len(ids) != 0 {
		t.Fatal("a refused pair must write nothing")
	}
	if b.watcher.count != 0 || len(b.auditEvents(t)) != 0 {
		t.Fatal("a refused declaration must not wake the live view nor reach the audit log")
	}
}

func TestDeclare_RefusesAnInvalidInputWithoutCallingTheFirewall(t *testing.T) {
	b := newBench(t)
	bad := definition()
	bad.APIURL = "http://fw.lan"
	if _, err := b.service.Declare(context.Background(), bad); !errors.Is(err, firewall.ErrURLInvalid) {
		t.Fatalf("got %v", err)
	}
	if b.connector.calls != 0 {
		t.Fatal("an invalid input reached the firewall")
	}
}

func TestDeclare_WritesReadsAtOnceAndAuditsWithoutAnySecret(t *testing.T) {
	b := newBench(t)
	id, err := b.service.Declare(context.Background(), definition())
	if err != nil {
		t.Fatal(err)
	}
	view, err := b.db.GetFirewallView(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if view.Version != "OPNsense 25.1" || len(view.Rules) != 1 || view.LastReadAt.IsZero() || view.LastError != "" {
		t.Errorf("first reading: %+v", view)
	}
	credentials, _ := b.db.GetFirewallCredentials(context.Background(), id)
	if credentials.APISecret != "secret-of-the-api-pair" {
		t.Errorf("the secret must be kept for the next readings")
	}
	if b.watcher.count < 2 {
		t.Errorf("the live view must hear the declaration and the reading, got %d", b.watcher.count)
	}
	events := b.auditEvents(t)
	if len(events) != 1 || events[0].Action != audit.FirewallCreate || events[0].ObjectName != "Bordure" {
		t.Fatalf("audit: %+v", events)
	}
	for key, value := range events[0].Details {
		for _, secret := range []string{"secret-of-the-api-pair", "key-of-the-api-pair"} {
			if strings.Contains(value, secret) {
				t.Errorf("the audit detail %q carries a credential", key)
			}
		}
	}
	if events[0].Details["host"] != "fw.lan" || events[0].Details["pinned"] != "no" {
		t.Errorf("details: %v", events[0].Details)
	}
}

func TestDeclare_ASecondFirewallWithTheSameNameIsRefused(t *testing.T) {
	b := newBench(t)
	if _, err := b.service.Declare(context.Background(), definition()); err != nil {
		t.Fatal(err)
	}
	_, err := b.service.Declare(context.Background(), definition())
	if !errors.Is(err, firewall.ErrNameTaken) || !refusal.IsConflict(err) {
		t.Fatalf("got %v", err)
	}
}

func TestDeclare_AFailingFirstReadingStillDeclaresAndSaysWhy(t *testing.T) {
	b := newBench(t)
	// La vérification passe, le relevé échoue ensuite sur les règles.
	b.connector.rules = nil
	b.service.SetConnector(firewall.KindOPNsense, func(firewall.Credentials) firewall.Connector {
		return failingAfterVersion{b.connector}
	})
	id, err := b.service.Declare(context.Background(), definition())
	if err != nil {
		t.Fatalf("the declaration itself must succeed: %v", err)
	}
	view, _ := b.db.GetFirewallView(context.Background(), id)
	if view.LastError != "firewall.unreachable" || !view.LastReadAt.IsZero() {
		t.Fatalf("the failure must be kept on the firewall: %+v", view)
	}
}

// failingAfterVersion laisse passer la vérification de la déclaration puis
// refuse tout le reste.
type failingAfterVersion struct{ *scriptedConnector }

func (f failingAfterVersion) Interfaces(context.Context) ([]firewall.Interface, error) {
	return nil, firewall.ErrUnreachable
}

func TestRefresh_AFailureKeepsTheLastReadingAndTheNextOneClearsTheFailure(t *testing.T) {
	b := newBench(t)
	id, err := b.service.Declare(context.Background(), definition())
	if err != nil {
		t.Fatal(err)
	}
	b.connector.fail(firewall.ErrCertificate)
	if err := b.service.Refresh(context.Background(), id); !errors.Is(err, firewall.ErrCertificate) {
		t.Fatalf("got %v", err)
	}
	view, _ := b.db.GetFirewallView(context.Background(), id)
	if view.LastError != "firewall.certificate" || len(view.Rules) != 1 || view.LastReadAt.IsZero() {
		t.Fatalf("a failure keeps the last reading: %+v", view)
	}

	b.connector.fail(nil)
	b.connector.rules = nil
	if err := b.service.Refresh(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	view, _ = b.db.GetFirewallView(context.Background(), id)
	if view.LastError != "" || len(view.Rules) != 0 {
		t.Fatalf("a good reading replaces the old one: %+v", view)
	}
}

func TestRefresh_AForgottenSecretIsAnUnauthorizedPair(t *testing.T) {
	b := newBench(t)
	id, err := b.service.Declare(context.Background(), definition())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.db.ForgetSecrets(context.Background(), func() (string, error) { return "t", nil }); err != nil {
		t.Fatal(err)
	}
	callsBefore := b.connector.calls
	if err := b.service.Refresh(context.Background(), id); !errors.Is(err, firewall.ErrUnauthorized) {
		t.Fatalf("got %v", err)
	}
	if b.connector.calls != callsBefore {
		t.Fatal("an empty secret must not be sent to the firewall")
	}
}

func TestRedeclare_ReplacesTheConnectionAndAudits(t *testing.T) {
	b := newBench(t)
	id, err := b.service.Declare(context.Background(), definition())
	if err != nil {
		t.Fatal(err)
	}
	changed := definition()
	changed.Name = "Bordure 2"
	changed.APISecret = "another-secret-of-the-pair"
	if err := b.service.Redeclare(context.Background(), id, changed); err != nil {
		t.Fatal(err)
	}
	credentials, _ := b.db.GetFirewallCredentials(context.Background(), id)
	if credentials.Name != "Bordure 2" || credentials.APISecret != "another-secret-of-the-pair" {
		t.Errorf("credentials: %+v", credentials.Firewall)
	}
	if err := b.service.Redeclare(context.Background(), "ghost", changed); !errors.Is(err, firewall.ErrNotFound) {
		t.Fatalf("unknown firewall: %v", err)
	}
	events := b.auditEvents(t)
	if len(events) != 2 || events[0].Action != audit.FirewallUpdate {
		t.Fatalf("audit: %+v", events)
	}
}

func TestRemove_DeletesTheFirewallAndAudits(t *testing.T) {
	b := newBench(t)
	id, err := b.service.Declare(context.Background(), definition())
	if err != nil {
		t.Fatal(err)
	}
	if err := b.service.Remove(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := b.service.Remove(context.Background(), id); !errors.Is(err, firewall.ErrNotFound) {
		t.Fatalf("second removal: %v", err)
	}
	events := b.auditEvents(t)
	if len(events) != 2 || events[0].Action != audit.FirewallDelete || events[0].ObjectName != "Bordure" {
		t.Fatalf("audit: %+v", events)
	}
}

func TestRefreshReadings_DoNotReachTheAuditLog(t *testing.T) {
	b := newBench(t)
	id, err := b.service.Declare(context.Background(), definition())
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := b.service.Refresh(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	b.connector.fail(firewall.ErrUnreachable)
	_ = b.service.Refresh(context.Background(), id)
	if events := b.auditEvents(t); len(events) != 1 {
		t.Fatalf("only the declaration is audited, got %d events", len(events))
	}
}

func TestDeclare_AnUnknownConnectorKindIsRefused(t *testing.T) {
	b := newBench(t)
	service := firewall.New(b.db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := service.Declare(context.Background(), definition()); !errors.Is(err, firewall.ErrKindInvalid) {
		t.Fatalf("a kind the wiring did not teach: %v", err)
	}
}
