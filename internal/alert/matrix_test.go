package alert_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/lang"
)

// Un faux serveur d'accueil Matrix : il note chaque envoi, répond d'abord
// les codes qu'on lui donne, puis 200 avec un event_id.
type homeserver struct {
	*httptest.Server
	mu       sync.Mutex
	statuses []int
	sends    []matrixSend
}

type matrixSend struct {
	method        string
	path          string
	authorization string
	message       struct {
		MsgType       string `json:"msgtype"`
		Body          string `json:"body"`
		Format        string `json:"format"`
		FormattedBody string `json:"formatted_body"`
	}
}

func newHomeserver(statuses ...int) *homeserver {
	server := &homeserver{statuses: statuses}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		content, _ := io.ReadAll(request.Body)
		send := matrixSend{method: request.Method, path: request.URL.EscapedPath(), authorization: request.Header.Get("Authorization")}
		_ = json.Unmarshal(content, &send.message)
		server.mu.Lock()
		server.sends = append(server.sends, send)
		status := http.StatusOK
		if len(server.statuses) > 0 {
			status, server.statuses = server.statuses[0], server.statuses[1:]
		}
		server.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"event_id":"$abc"}`))
	}))
	return server
}

func (h *homeserver) all() []matrixSend {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]matrixSend(nil), h.sends...)
}

const roomID = "!AbCdEf:matrix.exemple.fr"

// matrixChannel écrit le canal par la base : la validation exige https,
// le faux serveur d'accueil parle http.
func (f *fixture) matrixChannel(t *testing.T, homeserverURL string) alert.Channel {
	t.Helper()
	id, err := f.db.InsertChannel(context.Background(), alert.Channel{
		Name: "salon ops", URL: homeserverURL, Format: alert.FormatMatrix, Destination: roomID, Secret: "syt_bot_token",
		MinSeverity: alert.SeverityAttention, NotifyResolve: true, Enabled: true, CreatedAt: f.clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := f.db.GetChannel(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return channel
}

// Un 502 puis un 200 : deux PUT, le même identifiant de transaction, pour
// que le serveur d'accueil n'écrive qu'un message si le premier avait passé.
func TestNotifier_Matrix_PutsAnHTMLMessageAndReplaysTheSameTransaction(t *testing.T) {
	f := newFixture(t)
	notifier := f.notifier(t, lang.French)
	notifier.SetPublicURL("https://oc.example.fr")
	server := newHomeserver(http.StatusBadGateway)
	defer server.Close()
	f.matrixChannel(t, server.URL+"/")
	f.Open(context.Background(), volumeFact(alert.SeverityAttention, 86))
	opened := f.only(t, alert.StatusOpen)
	delivery := f.waitDelivery(t, opened.ID)
	if delivery.Status != alert.DeliveryDelivered || delivery.Attempts != 2 {
		t.Fatalf("delivery %+v", delivery)
	}
	sends := server.all()
	if len(sends) != 2 || sends[0].path != sends[1].path {
		t.Fatalf("a retry must reuse its transaction id: %+v", sends)
	}
	prefix := "/_matrix/client/v3/rooms/%21AbCdEf:matrix.exemple.fr/send/m.room.message/opencloud-"
	got := sends[1]
	if got.method != http.MethodPut || !strings.HasPrefix(got.path, prefix) || got.authorization != "Bearer syt_bot_token" {
		t.Fatalf("request %s %s %q", got.method, got.path, got.authorization)
	}
	link := "https://oc.example.fr/alertes/alerte/"
	if got.message.MsgType != "m.text" || got.message.Format != "org.matrix.custom.html" || !strings.HasPrefix(got.message.Body, "Alerte · Disque à 86 %") {
		t.Fatalf("message %+v", got.message)
	}
	if !strings.Contains(got.message.Body, link) || !strings.Contains(got.message.FormattedBody, `<a href="`+link) || !strings.Contains(got.message.FormattedBody, "<strong>") {
		t.Fatalf("message lacks the link: %+v", got.message)
	}
}

// Deux tests sont deux messages : chacun tire son identifiant de transaction.
func TestNotifier_Matrix_EachTestIsANewTransaction(t *testing.T) {
	f := newFixture(t)
	notifier := f.notifier(t, lang.French)
	server := newHomeserver()
	defer server.Close()
	channel := f.matrixChannel(t, server.URL)
	for range 2 {
		if outcome, err := notifier.Test(context.Background(), channel); err != nil || !outcome.OK() || outcome.Code != http.StatusOK {
			t.Fatalf("outcome %+v, %v", outcome, err)
		}
	}
	sends := server.all()
	if len(sends) != 2 || sends[0].path == sends[1].path || !strings.Contains(sends[0].path, "/opencloud-test-") {
		t.Fatalf("sends %+v", sends)
	}
}

// Un jeton refusé se lit dans l'historique par son code HTTP.
func TestNotifier_Matrix_RefusedTokenIsAStatusFailure(t *testing.T) {
	f := newFixture(t)
	notifier := f.notifier(t, lang.French)
	server := newHomeserver(http.StatusUnauthorized)
	defer server.Close()
	outcome, err := notifier.Test(context.Background(), f.matrixChannel(t, server.URL))
	if err != nil || outcome.Reason != alert.ReasonStatus || outcome.Code != http.StatusUnauthorized {
		t.Fatalf("outcome %+v, %v", outcome, err)
	}
}

func TestChannelDefinition_Matrix_RefusesWhatCannotWork(t *testing.T) {
	f := newFixture(t)
	good := alert.ChannelDefinition{
		Name: "salon", URL: "https://matrix.exemple.fr", Format: alert.FormatMatrix, Destination: " " + roomID + " ", Secret: "syt_token",
		MinSeverity: alert.SeverityAttention, Enabled: true,
	}
	cases := map[string]struct {
		change func(*alert.ChannelDefinition)
		want   error
	}{
		"plain homeserver":    {func(d *alert.ChannelDefinition) { d.URL = "http://matrix.exemple.fr" }, alert.ErrChannelHomeserver},
		"room alias":          {func(d *alert.ChannelDefinition) { d.Destination = "#ops:matrix.exemple.fr" }, alert.ErrChannelRoomInvalid},
		"room without server": {func(d *alert.ChannelDefinition) { d.Destination = "!AbCdEf" }, alert.ErrChannelRoomInvalid},
		"room with a path":    {func(d *alert.ChannelDefinition) { d.Destination = "!a/../b:matrix.exemple.fr" }, alert.ErrChannelRoomInvalid},
		"no token":            {func(d *alert.ChannelDefinition) { d.Secret = "" }, alert.ErrChannelTokenMissing},
	}
	for name, test := range cases {
		definition := good
		test.change(&definition)
		if _, err := f.CreateChannel(context.Background(), definition); !errors.Is(err, test.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	created, err := f.CreateChannel(context.Background(), good)
	if err != nil || created.Destination != roomID || !created.HasSecret {
		t.Fatalf("created %+v, %v", created, err)
	}
	cleared := good
	cleared.Secret = ""
	cleared.ClearSecret = true
	if _, err := f.UpdateChannel(context.Background(), created.ID, cleared); !errors.Is(err, alert.ErrChannelTokenMissing) {
		t.Fatalf("clearing the token: %v", err)
	}
	kept := good
	kept.Secret = ""
	if updated, err := f.UpdateChannel(context.Background(), created.ID, kept); err != nil || updated.Secret != "syt_token" {
		t.Fatalf("an empty token keeps the saved one: %+v, %v", updated, err)
	}
}
