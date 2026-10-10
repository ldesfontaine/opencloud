package server

import (
	"net/http"
	"strings"
	"testing"
)

// Un canal Matrix rend son salon, jamais son jeton ; ce qui ne peut pas
// marcher est refusé par une clé du catalogue.
func TestAPI_MatrixChannel_ReturnsTheRoomNeverTheToken(t *testing.T) {
	server := newTestServer(t)
	request := channelRequest{
		Name: "salon ops", URL: "https://matrix.exemple.fr", Format: "matrix", Destination: "!AbCdEf:matrix.exemple.fr",
		Secret: "syt_bot_token", MinSeverity: "attention", NotifyResolve: true, Enabled: true,
	}
	got := callAPI(server.Server, http.MethodPost, "/api/alerts/channels", request)
	if got.Code != http.StatusCreated || strings.Contains(got.Body.String(), "syt_bot_token") {
		t.Fatalf("status %d: %s", got.Code, got.Body.String())
	}
	var created channelJSON
	decodeAPI(t, got, &created)
	if created.Destination != "!AbCdEf:matrix.exemple.fr" || !created.HasSecret || created.Format != "matrix" {
		t.Fatalf("created %+v", created)
	}
	refusals := map[string]func(*channelRequest){
		"channel.matrix_homeserver_invalid": func(r *channelRequest) { r.URL = "http://matrix.exemple.fr" },
		"channel.matrix_room_invalid":       func(r *channelRequest) { r.Destination = "#ops:matrix.exemple.fr" },
		"channel.matrix_token_missing":      func(r *channelRequest) { r.Secret = "" },
	}
	for want, change := range refusals {
		bad := request
		change(&bad)
		refused := callAPI(server.Server, http.MethodPost, "/api/alerts/channels", bad)
		if refused.Code != http.StatusUnprocessableEntity || errorCode(t, refused) != want {
			t.Errorf("%s: %d %s", want, refused.Code, refused.Body.String())
		}
	}
}
