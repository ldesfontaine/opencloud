package server

import (
	"net/http"
	"testing"
)

// Le réglage s'enregistre sans toucher la langue, se relit, et la session
// sert aussitôt les nouveaux seuils au navigateur.
func TestAPI_DiskSettings_SaveKeepTheLanguageAndReachTheSession(t *testing.T) {
	server := newTestServer(t)
	if got := callAPI(server.Server, http.MethodPut, "/api/session/language", languageRequest{Language: "en"}); got.Code != http.StatusNoContent {
		t.Fatalf("language: %d", got.Code)
	}
	var before diskSettingsJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/settings/disk", nil), &before)
	if before.Attention != 85 || before.Danger != 95 || before.Recovery != 80 || before.MinAttention != 50 || before.MaxDanger != 99 {
		t.Fatalf("defaults %+v", before)
	}
	got := callAPI(server.Server, http.MethodPut, "/api/settings/disk", diskSettingsRequest{Attention: 70, Danger: 90})
	if got.Code != http.StatusOK {
		t.Fatalf("status %d: %s", got.Code, got.Body.String())
	}
	var after diskSettingsJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/settings/disk", nil), &after)
	if after.Attention != 70 || after.Danger != 90 || after.Recovery != 65 {
		t.Fatalf("saved %+v", after)
	}
	saved, err := server.settings.Load()
	if err != nil || saved.Language != "en" || saved.DiskAttentionPercent != 70 || saved.DiskDangerPercent != 90 {
		t.Fatalf("file %+v %v", saved, err)
	}
	var session sessionResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/session", nil), &session)
	if session.Disks.Attention != 70 || session.Disks.Danger != 90 {
		t.Fatalf("session %+v", session.Disks)
	}
}

func TestAPI_DiskSettings_RefuseWhatMakesNoSense_AndKeepThePrevious(t *testing.T) {
	server := newTestServer(t)
	for _, request := range []diskSettingsRequest{{Attention: 49, Danger: 95}, {Attention: 85, Danger: 100}, {Attention: 90, Danger: 90}, {Attention: 95, Danger: 85}} {
		got := callAPI(server.Server, http.MethodPut, "/api/settings/disk", request)
		if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "settings.disk_invalid" {
			t.Fatalf("%+v: %d %s", request, got.Code, got.Body.String())
		}
	}
	if saved, _ := server.settings.Load(); saved.DiskAttentionPercent != 0 {
		t.Fatalf("a refused write must not be applied: %+v", saved)
	}
}
