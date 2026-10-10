package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/alert"
)

// L'acquittement groupé passe les ouvertes non acquittées, ignore le reste
// sans refuser, et sort la sélection du compteur rouge d'un coup.
func TestAlertsAPI_AcknowledgeMany_CountsWhatChangedAndEmptiesTheRedCounter(t *testing.T) {
	server := newTestServer(t)
	server.seedAll(t)
	_, alertID := server.seedAlerts(t)
	open, err := server.alerts.List(context.Background(), alert.StatusOpen)
	if err != nil || len(open) < 2 {
		t.Fatalf("seed: %d open, %v", len(open), err)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/alerts/"+itoa(alertID)+"/actions/acknowledge", nil); got.Code != http.StatusOK {
		t.Fatalf("single: %d", got.Code)
	}
	ids := []int64{999999}
	for _, found := range open {
		ids = append(ids, found.ID, found.ID)
	}
	recorder := callAPI(server.Server, http.MethodPost, "/api/alerts/actions/acknowledge", acknowledgeAlertsRequest{IDs: ids})
	if recorder.Code != http.StatusOK {
		t.Fatalf("bulk: %d %s", recorder.Code, recorder.Body.String())
	}
	var response acknowledgeAlertsResponse
	decodeAPI(t, recorder, &response)
	if response.Acknowledged != len(open)-1 {
		t.Fatalf("acknowledged %d, want %d", response.Acknowledged, len(open)-1)
	}
	var counts countsResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/counts", nil), &counts)
	if counts.Alerts.Unacknowledged != 0 || counts.Alerts.Open != len(open) {
		t.Fatalf("counts %+v", counts.Alerts)
	}
	again := callAPI(server.Server, http.MethodPost, "/api/alerts/actions/acknowledge", acknowledgeAlertsRequest{IDs: ids})
	decodeAPI(t, again, &response)
	if again.Code != http.StatusOK || response.Acknowledged != 0 {
		t.Fatalf("twice: %d %+v", again.Code, response)
	}
}

func TestAlertsAPI_AcknowledgeMany_RefusesAnEmptyOrOversizedSelection(t *testing.T) {
	server := newTestServer(t)
	tooMany := make([]int64, alert.MaxAcknowledgedAtOnce+1)
	for index := range tooMany {
		tooMany[index] = int64(index + 1)
	}
	cases := map[string]acknowledgeAlertsRequest{
		"alert.selection_empty":     {IDs: []int64{}},
		"alert.selection_too_large": {IDs: tooMany},
	}
	for key, request := range cases {
		got := callAPI(server.Server, http.MethodPost, "/api/alerts/actions/acknowledge", request)
		if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != key {
			t.Fatalf("%s: %d %s", key, got.Code, got.Body.String())
		}
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/alerts/actions/acknowledge", acknowledgeAlertsRequest{IDs: []int64{0, -3}}); errorCode(t, got) != "alert.selection_empty" {
		t.Fatalf("invalid ids: %s", got.Body.String())
	}
}
