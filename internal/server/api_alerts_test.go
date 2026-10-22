package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/live"
)

// Les données de test ouvrent déjà des alertes par les composants : la
// base sur un crash, la tâche certbot en échec, le certificat du site à
// renouveler et non vérifié. On y ajoute un canal et une fenêtre pour taire.
func (ts *testServer) seedAlerts(t *testing.T) (channelID int64, alertID int64) {
	t.Helper()
	ctx := context.Background()
	channel, err := ts.alerts.CreateChannel(ctx, alert.ChannelDefinition{
		Name: "salon ops", URL: "https://discord.com/api/webhooks/1/x", Format: alert.FormatDiscord, Secret: "s3cret",
		MinSeverity: alert.SeverityAttention, NotifyResolve: true, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.alerts.CreateMute(ctx, alert.MuteDefinition{Kind: alert.KindJobLate, Reason: "cron désactivé ce week-end", Duration: 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	open, err := ts.alerts.List(ctx, alert.StatusOpen)
	if err != nil {
		t.Fatal(err)
	}
	for _, found := range open {
		if found.Kind == alert.KindJobFailed {
			alertID = found.ID
		}
	}
	if alertID == 0 {
		t.Fatalf("no job_failed alert among %+v", open)
	}
	return channel.ID, alertID
}

func TestAlertsAPI_ReadsMatchGoldenFiles(t *testing.T) {
	server := newTestServer(t)
	server.seedAll(t)
	_, alertID := server.seedAlerts(t)
	cases := map[string]string{
		"alerts":         "/api/alerts",
		"alert":          "/api/alerts/" + itoa(alertID),
		"alert_channels": "/api/alerts/channels",
		"alert_mutes":    "/api/alerts/mutes",
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			recorder := callAPI(server.Server, http.MethodGet, path, nil)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
			}
			body := recorder.Body.String()
			if strings.Contains(body, "s3cret") {
				t.Fatal("the secret must never leave the process")
			}
			compareGolden(t, filepath.Join("testdata", name+".golden.json"), normalize(body))
		})
	}
}

func itoa(id int64) string {
	return strings.TrimSpace(strings.Repeat(" ", 0) + json.Number(strings.TrimSpace(string(mustJSON(id)))).String())
}

func mustJSON(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}

// Acquitter sort du compteur rouge ; la résolution vient du composant,
// jamais de l'API, et réserve une livraison pour le canal qui la veut.
func TestAlertsAPI_AcknowledgeAndResolutionByTheComponent(t *testing.T) {
	server := newTestServer(t)
	server.seedAll(t)
	_, alertID := server.seedAlerts(t)
	var before countsResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/counts", nil), &before)
	recorder := callAPI(server.Server, http.MethodPost, "/api/alerts/"+itoa(alertID)+"/actions/acknowledge", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("acknowledge: %d %s", recorder.Code, recorder.Body.String())
	}
	var acknowledged alertJSON
	decodeAPI(t, recorder, &acknowledged)
	if acknowledged.AcknowledgedAt == nil || acknowledged.Status != "open" {
		t.Fatalf("acknowledged %+v", acknowledged)
	}
	var after countsResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/counts", nil), &after)
	if after.Alerts.Open != before.Alerts.Open || after.Alerts.Unacknowledged != before.Alerts.Unacknowledged-1 {
		t.Fatalf("counts before %+v, after %+v", before.Alerts, after.Alerts)
	}
	// Le prochain ping à l'heure de certbot résout l'alerte.
	jobs, err := server.db.ListHeartbeats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	for _, job := range jobs {
		if job.Name == "certbot renew" {
			if _, err := server.heartbeats.Receive(context.Background(), job.Token, heartbeat.Ping{Kind: heartbeat.KindExitCode, ExitCode: &zero}); err != nil {
				t.Fatal(err)
			}
		}
	}
	recorder = callAPI(server.Server, http.MethodGet, "/api/alerts/"+itoa(alertID), nil)
	var detail alertResponse
	decodeAPI(t, recorder, &detail)
	if detail.Alert.Status != "resolved" || len(detail.Deliveries) != 1 || detail.Deliveries[0].Event != "resolved" || detail.Deliveries[0].Status != "pending" {
		t.Fatalf("detail %+v", detail)
	}
	recorder = callAPI(server.Server, http.MethodPost, "/api/alerts/"+itoa(alertID)+"/actions/acknowledge", nil)
	if recorder.Code != http.StatusConflict || errorCode(t, recorder) != "alert.already_resolved" {
		t.Fatalf("acknowledge a resolved alert: %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := callAPI(server.Server, http.MethodGet, "/api/alerts/999", nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown alert: %d", recorder.Code)
	}
}

func TestAlertsAPI_ChannelsAndMutesGoThroughTheComponent(t *testing.T) {
	server := newTestServer(t)
	received := make(chan *http.Request, 1)
	var body []byte
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		received <- r
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	recorder := callAPI(server.Server, http.MethodPost, "/api/alerts/channels", map[string]any{
		"name": "ntfy maison", "url": target.URL + "/opencloud", "format": "text", "secret": "s3cret", "min_severity": "attention", "notify_resolve": true, "enabled": true,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", recorder.Code, recorder.Body.String())
	}
	var channel channelJSON
	decodeAPI(t, recorder, &channel)
	if !channel.HasSecret || !channel.Plain || strings.Contains(recorder.Body.String(), "s3cret") {
		t.Fatalf("channel %+v", channel)
	}
	recorder = callAPI(server.Server, http.MethodPost, "/api/alerts/channels/"+itoa(channel.ID)+"/actions/test", nil)
	var outcome channelTestResponse
	decodeAPI(t, recorder, &outcome)
	if recorder.Code != http.StatusOK || !outcome.OK || outcome.Code != http.StatusOK {
		t.Fatalf("test: %d %+v", recorder.Code, outcome)
	}
	request := <-received
	if request.Header.Get("OpenCloud-Event") != "test" || !alert.Verify("s3cret", body, request.Header.Get("OpenCloud-Signature")) || request.Header.Get("Title") == "" {
		t.Fatalf("test request headers %v", request.Header)
	}
	if !strings.Contains(string(body), "openCloud") {
		t.Fatalf("test body %q", body)
	}
	recorder = callAPI(server.Server, http.MethodPut, "/api/alerts/channels/"+itoa(channel.ID), map[string]any{
		"name": "ntfy maison", "url": target.URL + "/opencloud", "format": "text", "min_severity": "danger", "notify_resolve": false, "enabled": false,
	})
	decodeAPI(t, recorder, &channel)
	if recorder.Code != http.StatusOK || !channel.HasSecret || channel.Enabled || channel.MinSeverity != "danger" {
		t.Fatalf("update keeps the secret: %d %+v", recorder.Code, channel)
	}
	recorder = callAPI(server.Server, http.MethodPost, "/api/alerts/channels", map[string]any{"name": "meta", "url": "http://169.254.169.254/", "format": "json", "min_severity": "attention"})
	if recorder.Code != http.StatusUnprocessableEntity || errorCode(t, recorder) != "channel.url_forbidden" {
		t.Fatalf("link-local: %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = callAPI(server.Server, http.MethodPost, "/api/alerts/mutes", map[string]any{"target_kind": "machine", "duration_minutes": 60})
	if recorder.Code != http.StatusUnprocessableEntity || errorCode(t, recorder) != "silence.invalid" {
		t.Fatalf("a target kind without its object: %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = callAPI(server.Server, http.MethodPost, "/api/alerts/mutes", map[string]any{"target_kind": "probe", "target_id": "inconnue", "duration_minutes": 60})
	if recorder.Code != http.StatusUnprocessableEntity || errorCode(t, recorder) != "silence.target_unknown" {
		t.Fatalf("an unknown target: %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = callAPI(server.Server, http.MethodPost, "/api/alerts/mutes", map[string]any{"target_kind": "machine", "target_id": "local", "duration_minutes": 60, "reason": "mise à jour"})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("mute: %d %s", recorder.Code, recorder.Body.String())
	}
	var mute muteJSON
	decodeAPI(t, recorder, &mute)
	if !mute.Active || mute.Target.Name != "opencloud" || !mute.EndsAt.Equal(testNow.Add(time.Hour)) || mute.CreatedByKind != "operator" {
		t.Fatalf("mute %+v", mute)
	}
	// Planifiée : la fenêtre commence à l'heure dite, pas avant.
	recorder = callAPI(server.Server, http.MethodPost, "/api/alerts/mutes", map[string]any{"starts_at": testNow.Add(2 * time.Hour).Format(time.RFC3339), "duration_minutes": 30})
	var planned muteJSON
	decodeAPI(t, recorder, &planned)
	if recorder.Code != http.StatusCreated || planned.Active || !planned.EndsAt.Equal(testNow.Add(150*time.Minute)) || planned.Target.Kind != "" {
		t.Fatalf("planned: %d %+v", recorder.Code, planned)
	}
	if recorder := callAPI(server.Server, http.MethodDelete, "/api/alerts/mutes/"+itoa(mute.ID), nil); recorder.Code != http.StatusNoContent {
		t.Fatalf("delete mute: %d", recorder.Code)
	}
	if recorder := callAPI(server.Server, http.MethodDelete, "/api/alerts/channels/"+itoa(channel.ID), nil); recorder.Code != http.StatusNoContent {
		t.Fatalf("delete channel: %d", recorder.Code)
	}
	if recorder := callAPI(server.Server, http.MethodGet, "/api/alerts/channels/"+itoa(channel.ID), nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("deleted channel still answers: %d", recorder.Code)
	}
}

// receives_audit se crée, se relit, se modifie, comme le reste du canal.
func TestAlertsAPI_ChannelReceivesAuditRoundTrips(t *testing.T) {
	server := newTestServer(t)
	var channel channelJSON
	recorder := callAPI(server.Server, http.MethodPost, "/api/alerts/channels", map[string]any{
		"name": "journal", "url": "https://hooks.example.org/journal", "format": "json", "min_severity": "attention", "receives_audit": true, "enabled": true,
	})
	decodeAPI(t, recorder, &channel)
	if recorder.Code != http.StatusCreated || !channel.ReceivesAudit {
		t.Fatalf("create: %d %+v", recorder.Code, channel)
	}
	var read channelJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/alerts/channels/"+itoa(channel.ID), nil), &read)
	if !read.ReceivesAudit {
		t.Fatalf("read: %+v", read)
	}
	recorder = callAPI(server.Server, http.MethodPut, "/api/alerts/channels/"+itoa(channel.ID), map[string]any{
		"name": "journal", "url": "https://hooks.example.org/journal", "format": "json", "min_severity": "attention", "receives_audit": false, "enabled": true,
	})
	decodeAPI(t, recorder, &channel)
	if recorder.Code != http.StatusOK || channel.ReceivesAudit {
		t.Fatalf("update: %d %+v", recorder.Code, channel)
	}
}

// Toute écriture du moteur publie « alerts » : l'onglet relit.
func TestAlertsAPI_ChangesReachTheLive(t *testing.T) {
	server := newTestServer(t)
	server.seedAll(t)
	_, alertID := server.seedAlerts(t)
	subscription := server.bus.Subscribe()
	defer subscription.Close()
	if recorder := callAPI(server.Server, http.MethodPost, "/api/alerts/"+itoa(alertID)+"/actions/acknowledge", nil); recorder.Code != http.StatusOK {
		t.Fatalf("acknowledge: %d", recorder.Code)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	topics, err := subscription.Next(ctx)
	if err != nil || len(topics) != 1 || topics[0] != live.TopicAlerts {
		t.Fatalf("topics %v, %v", topics, err)
	}
}
