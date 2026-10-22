package alert_test

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/lang"
)

const auditTestSecret = "secret de canal pour le test" // gitleaks:allow

// journal branche un Recorder sur la fixture : il écrit sous l'horloge de la
// fixture et confie chaque événement au moteur. Appelé après la création des
// canaux, pour que « canal créé » ne soit pas lui-même un événement livré.
func (f *fixture) journal(t *testing.T) (context.Context, *audit.Recorder) {
	t.Helper()
	recorder := audit.New(f.db, discardLogger())
	recorder.SetClock(func() time.Time { return f.clock })
	f.SetAuditor(recorder)
	recorder.SetForwarder(f.Engine)
	return audit.WithActor(context.Background(), audit.Operator("lucas")), recorder
}

// auditChannel crée un canal JSON actif qui reçoit le journal.
func (f *fixture) auditChannel(t *testing.T, url string) alert.Channel {
	t.Helper()
	channel, err := f.CreateChannel(context.Background(), alert.ChannelDefinition{
		Name: "journal", URL: url, Format: alert.FormatJSON, Secret: auditTestSecret,
		MinSeverity: alert.SeverityAttention, ReceivesAudit: true, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return channel
}

func waitUntil(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type auditBody struct {
	Event string `json:"event"`
	Title string `json:"title"`
	Audit struct {
		ID        int64             `json:"id"`
		ActorKind string            `json:"actor_kind"`
		ActorName string            `json:"actor_name"`
		Action    string            `json:"action"`
		Object    string            `json:"object_name"`
		Details   map[string]string `json:"details"`
	} `json:"audit"`
}

func decodeAuditBody(t *testing.T, body []byte) auditBody {
	t.Helper()
	var decoded auditBody
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

// Bout en bout : un geste du journal arrive, signé, sur le canal qui reçoit
// le journal, et sa ligne est notée livrée dans audit_deliveries. Créer le
// canal est lui-même un geste du journal : le canal reçoit les deux, ce qui
// prouve aussi qu'aucun verrou du moteur n'est tenu pendant l'envoi.
func TestForwardAuditEvent_DeliversToTheReceivingChannel(t *testing.T) {
	f := newFixture(t)
	f.notifier(t, lang.French)
	target := newReceiver(http.StatusNoContent)
	defer target.Close()
	ctx, recorder := f.journal(t)
	f.auditChannel(t, target.URL+"/hook")

	recorder.Record(ctx, audit.Event{Action: audit.ProbePause, ObjectID: "p1", ObjectName: "api", Details: audit.Details{"kind": "http"}})
	waitUntil(t, "the two audit events", func() bool { return target.count() == 2 })

	target.mu.Lock()
	requests := append([]received(nil), target.requests...)
	target.mu.Unlock()
	var paused *received
	for index, request := range requests {
		if decodeAuditBody(t, request.body).Audit.Action == "probe.pause" {
			paused = &requests[index]
		}
	}
	if paused == nil {
		t.Fatalf("no probe.pause among the requests: %d", len(requests))
	}
	body := decodeAuditBody(t, paused.body)
	if body.Event != "audit" || body.Audit.ActorKind != "operator" || body.Audit.ActorName != "lucas" ||
		body.Audit.Object != "api" || body.Audit.Details["kind"] != "http" || !strings.HasPrefix(body.Title, "Journal · ") {
		t.Fatalf("body %+v", body)
	}
	if !alert.Verify(auditTestSecret, paused.body, paused.headers.Get("OpenCloud-Signature")) {
		t.Fatalf("signature %q does not match the body", paused.headers.Get("OpenCloud-Signature"))
	}
	if paused.headers.Get("OpenCloud-Event") != "audit" {
		t.Fatalf("headers %v", paused.headers)
	}
	waitUntil(t, "the delivery to be noted delivered", func() bool {
		deliveries, err := f.db.ListAuditDeliveries(context.Background(), body.Audit.ID)
		return err == nil && len(deliveries) == 1 && deliveries[0].Status == alert.DeliveryDelivered
	})
	deliveries, _ := f.db.ListAuditDeliveries(context.Background(), body.Audit.ID)
	if deliveries[0].Attempts != 1 || deliveries[0].Code != http.StatusNoContent || deliveries[0].Event != alert.EventAudit {
		t.Fatalf("delivery %+v", deliveries[0])
	}
}

// Un canal en panne ne retient pas les ouvriers : un seul essai y est fait,
// les suivants passent par la base, 5 s plus tard puis par le balayage.
func TestForwardAuditEvent_AFailureIsRetriedFromTheBaseWithoutWaitingInTheWorker(t *testing.T) {
	f := newFixture(t)
	notifier := f.notifier(t, lang.French)
	notifier.SetBackoffs(alert.Backoffs)
	target := newReceiver(http.StatusInternalServerError)
	defer target.Close()
	f.auditChannel(t, target.URL)
	ctx, recorder := f.journal(t)

	recorder.Record(ctx, audit.Event{Action: audit.ProbePause, ObjectID: "p1", ObjectName: "api"})
	var failed alert.Delivery
	waitUntil(t, "the first attempt to fail", func() bool {
		pending, _ := f.db.ListPendingAuditDeliveries(context.Background())
		due, _ := f.db.ListDueAuditDeliveries(context.Background(), f.clock.Add(time.Hour))
		if len(pending) == 0 && len(due) == 1 {
			failed = due[0]
			return true
		}
		return false
	})
	if failed.Attempts != 1 || failed.Reason != alert.ReasonStatus || failed.Code != http.StatusInternalServerError ||
		!failed.NextAttemptAt.Equal(f.clock.Add(5*time.Second)) {
		t.Fatalf("failed delivery %+v", failed)
	}
	time.Sleep(50 * time.Millisecond)
	if target.count() != 1 {
		t.Fatalf("the worker must try once: %d requests", target.count())
	}

	target.mu.Lock()
	target.status = http.StatusNoContent
	target.mu.Unlock()
	f.Sweep(context.Background())
	time.Sleep(20 * time.Millisecond)
	if target.count() != 1 {
		t.Fatalf("nothing is due yet: %d requests", target.count())
	}
	f.advance(5 * time.Second)
	f.Sweep(context.Background())
	waitUntil(t, "the retry to be delivered", func() bool {
		deliveries, err := f.db.ListAuditDeliveries(context.Background(), failed.AuditID)
		return err == nil && len(deliveries) == 1 && deliveries[0].Status == alert.DeliveryDelivered
	})
	if target.count() != 2 {
		t.Fatalf("one retry: %d requests", target.count())
	}
}

// Sans notifieur (la commande mcp), l'événement et ses livraisons sont
// écrits ; le démarrage suivant les envoie, une seule fois.
func TestRequeue_ReplaysPendingAuditDeliveries(t *testing.T) {
	f := newFixture(t)
	target := newReceiver(http.StatusOK)
	defer target.Close()
	f.auditChannel(t, target.URL)
	f.SetSender(nil)
	ctx, recorder := f.journal(t)
	recorder.Record(ctx, audit.Event{Action: audit.ProbePause, ObjectID: "p1", ObjectName: "api"})
	pending, err := f.db.ListPendingAuditDeliveries(context.Background())
	if err != nil || len(pending) != 1 || target.count() != 0 {
		t.Fatalf("pending %v, %v; requests %d", pending, err, target.count())
	}

	f.notifier(t, lang.French)
	if err := f.Requeue(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the delivery", func() bool {
		deliveries, _ := f.db.ListAuditDeliveries(context.Background(), pending[0].AuditID)
		return len(deliveries) == 1 && deliveries[0].Status == alert.DeliveryDelivered
	})
	if err := f.Requeue(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if target.count() != 1 {
		t.Fatalf("a delivered line must not be sent again: %d requests", target.count())
	}
}

// Les six formats disent l'acteur, l'action et l'objet, et rien de ce que le
// Recorder a retiré des détails.
func TestRenderAudit_EveryFormatSaysActorActionAndObject(t *testing.T) {
	f := newFixture(t)
	ctx, recorder := f.journal(t)
	recorder.Record(ctx, audit.Event{
		Action: audit.ChannelCreate, ObjectID: "7", ObjectName: "salon ops",
		Details: audit.Details{"format": "discord", "webhook_url": "https://hooks.example.org/leak"},
	})
	events, err := f.db.ListAuditEvents(context.Background(), audit.Query{Limit: 1})
	if err != nil || len(events) != 1 {
		t.Fatalf("events %v, %v", events, err)
	}
	catalogs, err := lang.Load()
	if err != nil {
		t.Fatal(err)
	}
	catalog := catalogs.For(lang.French)
	for _, format := range alert.Formats() {
		t.Run(string(format), func(t *testing.T) {
			body, err := alert.RenderAudit(catalog, format, events[0], f.clock, "https://oc.example.fr/journal")
			if err != nil {
				t.Fatal(err)
			}
			whole := string(body.Content) + body.HTML + body.Title
			for _, want := range []string{"Création", "Canal", "salon ops", "lucas", "2026-09-22T10:00:00Z"} {
				if !strings.Contains(whole, want) {
					t.Errorf("%s: %q missing from %q", format, want, whole)
				}
			}
			if strings.Contains(whole, "hooks.example.org") || strings.Contains(whole, "leak") {
				t.Errorf("%s: a secret detail leaked: %q", format, whole)
			}
			if !strings.HasPrefix(body.Title, "Journal · Création · Canal") {
				t.Errorf("%s: title %q", format, body.Title)
			}
		})
	}
}

// Un nom choisi par un agent ne devient pas du balisage chez Discord, Slack
// ni dans un HTML : même protection que pour les alertes.
func TestRenderAudit_EscapesTheObjectNameLikeAlertsDo(t *testing.T) {
	catalogs, err := lang.Load()
	if err != nil {
		t.Fatal(err)
	}
	catalog := catalogs.For(lang.English)
	event := audit.Event{
		ID: 1, At: testNow, Actor: audit.Agent("m1"), Action: audit.ActionOrder, ObjectKind: audit.ObjectAction,
		ObjectID: "r1", ObjectName: "<@everyone> & co",
	}
	// Les corps JSON re-échappent le texte : on cherche sa forme encodée.
	jsonText := func(text string) string {
		encoded, _ := json.Marshal(text)
		return strings.Trim(string(encoded), `"`)
	}
	cases := map[alert.Format]string{
		alert.FormatDiscord: jsonText(`\<\@everyone\> & co`),
		alert.FormatSlack:   jsonText(`&lt;@everyone&gt; &amp; co`),
		alert.FormatEmail:   html.EscapeString("<@everyone> & co"),
		alert.FormatMatrix:  jsonText(html.EscapeString("<@everyone> & co")),
	}
	for format, want := range cases {
		body, err := alert.RenderAudit(catalog, format, event, testNow, "")
		if err != nil {
			t.Fatal(err)
		}
		whole := string(body.Content) + body.HTML
		if !strings.Contains(whole, want) {
			t.Errorf("%s: %q missing from %q", format, want, whole)
		}
	}
	// Un acteur sans nom (une règle, un inconnu) se dit par sa sorte seule.
	body, err := alert.RenderAudit(catalog, alert.FormatText, audit.Event{At: testNow, Actor: audit.Actor{Kind: audit.ActorRule}, Action: audit.ProbePause}, testNow, "")
	if err != nil || !strings.Contains(string(body.Content), "By Rule, 2026-09-22T10:00:00Z.") || strings.Contains(string(body.Content), "«") {
		t.Errorf("body %q, %v", body.Content, err)
	}
}

// Une livraison d'alerte et une livraison du journal peuvent avoir le même
// identifiant et la même date (deux tables) : Matrix jetterait la seconde
// comme un doublon si leurs identifiants de transaction étaient égaux.
func TestMatrixTransactionID_AuditNeverEqualsAnAlertDelivery(t *testing.T) {
	f := newFixture(t)
	f.notifier(t, lang.French)
	server := newHomeserver()
	defer server.Close()
	id, err := f.db.InsertChannel(context.Background(), alert.Channel{
		Name: "salon", URL: server.URL, Format: alert.FormatMatrix, Destination: roomID, Secret: "syt_bot_token",
		MinSeverity: alert.SeverityAttention, ReceivesAudit: true, Enabled: true, CreatedAt: f.clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, recorder := f.journal(t)
	recorder.Record(ctx, audit.Event{Action: audit.ProbePause, ObjectID: "p1", ObjectName: "api"})
	f.Open(ctx, volumeFact(alert.SeverityAttention, 86))
	waitUntil(t, "both messages", func() bool { return len(server.all()) == 2 })

	sends := server.all()
	if sends[0].path == sends[1].path {
		t.Fatalf("same transaction for an alert and an audit delivery: %s", sends[0].path)
	}
	alertDeliveries, _ := f.db.ListDeliveries(context.Background(), f.only(t, alert.StatusOpen).ID)
	if len(alertDeliveries) != 1 || alertDeliveries[0].ID != 1 || alertDeliveries[0].ChannelID != id {
		t.Fatalf("the test needs both deliveries to share id 1: %+v", alertDeliveries)
	}
	audits := 0
	for _, send := range sends {
		if strings.Contains(send.path, "/opencloud-audit-1-") {
			audits++
		}
	}
	if audits != 1 {
		t.Fatalf("one path must carry the audit- prefix: %+v", sends)
	}
}

// Au démarrage, Requeue et ForwardAuditEvent peuvent voir la même livraison
// en attente (un événement écrit pendant que Requeue liste) : chacune ne
// part qu'une fois, quel que soit celui qui la prend le premier.
func TestRequeue_NeverQueuesAnAuditDeliveryTwiceAtStartup(t *testing.T) {
	f := newFixture(t)
	f.auditChannel(t, "http://127.0.0.1:9/hook")
	ctx, recorder := f.journal(t)

	// ForwardAuditEvent d'abord : Requeue saute ce qu'il a confié.
	recorder.Record(ctx, audit.Event{Action: audit.ProbePause, ObjectID: "p1", ObjectName: "api"})
	if got := f.sender.events(); got != "audit:journal" {
		t.Fatalf("forwarded: %q", got)
	}
	if err := f.Requeue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.sender.events(); got != "audit:journal" {
		t.Fatalf("Requeue queued it again: %q", got)
	}

	// Requeue d'abord : ForwardAuditEvent, appelé après lui pour un événement
	// qu'il a déjà pris, le saute.
	f2 := newFixture(t)
	f2.auditChannel(t, "http://127.0.0.1:9/hook")
	f2.SetSender(nil)
	ctx2, recorder2 := f2.journal(t)
	recorder2.Record(ctx2, audit.Event{Action: audit.ProbePause, ObjectID: "p2", ObjectName: "api"})
	f2.SetSender(f2.sender)
	if err := f2.Requeue(context.Background()); err != nil {
		t.Fatal(err)
	}
	events, err := f2.db.ListAuditEvents(context.Background(), audit.Query{Limit: 1})
	if err != nil || len(events) != 1 {
		t.Fatalf("events %v, %v", events, err)
	}
	f2.ForwardAuditEvent(context.Background(), events[0])
	if got := f2.sender.events(); got != "audit:journal" {
		t.Fatalf("queued twice: %q", got)
	}
}
