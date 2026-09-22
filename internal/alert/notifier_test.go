package alert_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Un récepteur de test : il note chaque requête et répond ce qu'on lui dit.
type receiver struct {
	*httptest.Server
	mu       sync.Mutex
	status   int
	requests []received
}

type received struct {
	headers http.Header
	body    []byte
}

func newReceiver(status int) *receiver {
	r := &receiver{status: status}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		r.mu.Lock()
		r.requests = append(r.requests, received{headers: request.Header.Clone(), body: body})
		status := r.status
		r.mu.Unlock()
		if status >= 300 && status < 400 {
			w.Header().Set("Location", "/elsewhere")
		}
		w.WriteHeader(status)
	}))
	return r
}

func (r *receiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *receiver) last() received {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requests[len(r.requests)-1]
}

// notifier fait un vrai notifieur sur la base de la fixture, avec des
// attentes d'une milliseconde, et le fait tourner jusqu'à la fin du test.
func (f *fixture) notifier(t *testing.T, code lang.Code) *alert.Notifier {
	t.Helper()
	catalogs, err := lang.Load()
	if err != nil {
		t.Fatal(err)
	}
	notifier := alert.NewNotifier(f.db, catalogs, func() lang.Code { return code }, discardLogger())
	notifier.SetBackoffs([]time.Duration{time.Millisecond, time.Millisecond})
	notifier.SetClock(func() time.Time { return f.clock })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { notifier.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	f.SetSender(notifier)
	return notifier
}

// waitDelivery attend que la livraison de l'alerte quitte l'attente.
func (f *fixture) waitDelivery(t *testing.T, alertID int64) alert.Delivery {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		deliveries, err := f.Deliveries(context.Background(), alertID)
		if err != nil {
			t.Fatal(err)
		}
		if len(deliveries) > 0 && deliveries[len(deliveries)-1].Status != alert.DeliveryPending {
			return deliveries[len(deliveries)-1]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("delivery still pending")
	return alert.Delivery{}
}

func (f *fixture) webhook(t *testing.T, url string, format alert.Format, secret string) alert.Channel {
	t.Helper()
	channel, err := f.CreateChannel(context.Background(), alert.ChannelDefinition{
		Name: "ops", URL: url, Format: format, Secret: secret, MinSeverity: alert.SeverityAttention, NotifyResolve: true, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return channel
}

func TestNotifier_DeliversASignedBodyInTheInterfaceLanguage(t *testing.T) {
	f := newFixture(t)
	f.notifier(t, lang.French)
	target := newReceiver(http.StatusNoContent)
	defer target.Close()
	f.webhook(t, target.URL+"/hook", alert.FormatJSON, "s3cret")
	f.Open(context.Background(), volumeFact(alert.SeverityAttention, 86))
	opened := f.only(t, alert.StatusOpen)
	delivery := f.waitDelivery(t, opened.ID)
	if delivery.Status != alert.DeliveryDelivered || delivery.Attempts != 1 || delivery.Code != http.StatusNoContent {
		t.Fatalf("delivery %+v", delivery)
	}
	got := target.last()
	if !alert.Verify("s3cret", got.body, got.headers.Get("OpenCloud-Signature")) {
		t.Fatalf("signature %q does not match the body", got.headers.Get("OpenCloud-Signature"))
	}
	if got.headers.Get("OpenCloud-Event") != "opened" || got.headers.Get("Content-Type") != "application/json" || got.headers.Get("User-Agent") != "openCloud" {
		t.Fatalf("headers %v", got.headers)
	}
	var payload struct {
		Event    string `json:"event"`
		Language string `json:"language"`
		Title    string `json:"title"`
		Text     struct{ Fact, Cause, Action string }
		Alert    struct {
			Kind    string `json:"kind"`
			Details struct {
				Percent int `json:"percent"`
			} `json:"details"`
		} `json:"alert"`
	}
	if err := json.Unmarshal(got.body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Event != "opened" || payload.Language != "fr" || payload.Alert.Kind != "disk_full" || payload.Alert.Details.Percent != 86 {
		t.Fatalf("payload %+v", payload)
	}
	if payload.Text.Fact != "Disque à 86 %" || !strings.Contains(payload.Text.Cause, "/data sur opencloud") || !strings.Contains(payload.Text.Cause, "85 %") || payload.Text.Action == "" {
		t.Fatalf("text %+v", payload.Text)
	}
	if !strings.HasPrefix(payload.Title, "Alerte · ") {
		t.Fatalf("title %q", payload.Title)
	}
}

func TestNotifier_RetriesThreeTimesThenFails(t *testing.T) {
	f := newFixture(t)
	f.notifier(t, lang.English)
	target := newReceiver(http.StatusInternalServerError)
	defer target.Close()
	f.webhook(t, target.URL, alert.FormatText, "")
	f.Open(context.Background(), volumeFact(alert.SeverityDanger, 97))
	delivery := f.waitDelivery(t, f.only(t, alert.StatusOpen).ID)
	if delivery.Status != alert.DeliveryFailed || delivery.Attempts != alert.MaxAttempts || delivery.Reason != alert.ReasonStatus || delivery.Code != http.StatusInternalServerError {
		t.Fatalf("delivery %+v", delivery)
	}
	if target.count() != alert.MaxAttempts {
		t.Fatalf("requests %d", target.count())
	}
	got := target.last()
	if got.headers.Get("OpenCloud-Signature") != "" || got.headers.Get("Content-Type") != "text/plain; charset=utf-8" || got.headers.Get("Priority") != "high" {
		t.Fatalf("headers %v", got.headers)
	}
	if !strings.HasPrefix(string(got.body), "Alert · Disk at 97 %\n") || got.headers.Get("Title") != "Alert · Disk at 97 %" {
		t.Fatalf("body %q, title %q", got.body, got.headers.Get("Title"))
	}
}

func TestNotifier_NeverFollowsARedirect(t *testing.T) {
	f := newFixture(t)
	notifier := f.notifier(t, lang.French)
	target := newReceiver(http.StatusFound)
	defer target.Close()
	channel := f.webhook(t, target.URL, alert.FormatDiscord, "")
	outcome, err := notifier.Test(context.Background(), channel)
	if err != nil || outcome.OK() || outcome.Reason != alert.ReasonRedirect || outcome.Code != http.StatusFound || target.count() != 1 {
		t.Fatalf("outcome %+v, %v, requests %d", outcome, err, target.count())
	}
}

func TestNotifier_Test_SendsTheFormatOfTheChannel(t *testing.T) {
	f := newFixture(t)
	notifier := f.notifier(t, lang.French)
	target := newReceiver(http.StatusOK)
	defer target.Close()
	for _, format := range []alert.Format{alert.FormatDiscord, alert.FormatSlack} {
		channel := f.webhook(t, target.URL, format, "")
		outcome, err := notifier.Test(context.Background(), channel)
		if err != nil || !outcome.OK() {
			t.Fatalf("%s: %+v, %v", format, outcome, err)
		}
		got := target.last()
		if got.headers.Get("OpenCloud-Event") != "test" {
			t.Fatalf("%s: event %q", format, got.headers.Get("OpenCloud-Event"))
		}
		var payload map[string]any
		if err := json.Unmarshal(got.body, &payload); err != nil {
			t.Fatal(err)
		}
		key := "embeds"
		if format == alert.FormatSlack {
			key = "attachments"
		}
		if _, ok := payload[key]; !ok || !strings.Contains(string(got.body), "Test") {
			t.Fatalf("%s: body %s", format, got.body)
		}
	}
	outcome, err := notifier.Test(context.Background(), alert.Channel{URL: "http://169.254.169.254/", Format: alert.FormatJSON})
	if err != nil || outcome.Reason != alert.ReasonForbidden {
		t.Fatalf("link-local: %+v, %v", outcome, err)
	}
	outcome, err = notifier.Test(context.Background(), alert.Channel{URL: "http://127.0.0.1:9/", Format: alert.FormatJSON})
	if err != nil || outcome.Reason != alert.ReasonRefused {
		t.Fatalf("closed port: %+v, %v", outcome, err)
	}
}

func TestNotifier_RequeueAfterARestartDoesNotSendTwice(t *testing.T) {
	f := newFixture(t)
	target := newReceiver(http.StatusOK)
	defer target.Close()
	f.webhook(t, target.URL, alert.FormatJSON, "")
	// Ouverte pendant que le faux notifieur écoute : la livraison reste réservée.
	f.Open(context.Background(), volumeFact(alert.SeverityAttention, 86))
	f.notifier(t, lang.French)
	if err := f.Requeue(context.Background()); err != nil {
		t.Fatal(err)
	}
	delivery := f.waitDelivery(t, f.only(t, alert.StatusOpen).ID)
	if delivery.Status != alert.DeliveryDelivered || target.count() != 1 {
		t.Fatalf("delivery %+v, requests %d", delivery, target.count())
	}
	if err := f.Requeue(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if target.count() != 1 {
		t.Fatalf("a delivered line must not be sent again: %d", target.count())
	}
	_ = machine.LocalID
}
