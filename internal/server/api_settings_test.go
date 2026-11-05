package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/machine"
)

func ruleOf(t *testing.T, rules rulesJSON, resource string) ruleJSON {
	t.Helper()
	for _, rule := range rules.Rules {
		if rule.Resource == resource {
			return rule
		}
	}
	t.Fatalf("no %s rule in %+v", resource, rules)
	return ruleJSON{}
}

// Le réglage s'enregistre sans toucher la langue, se relit, et la session
// sert aussitôt les nouveaux seuils au navigateur.
func TestAPI_Thresholds_SaveKeepTheLanguageAndReachTheSession(t *testing.T) {
	server := newTestServer(t)
	if got := callAPI(server.Server, http.MethodPut, "/api/session/language", languageRequest{Language: "en"}); got.Code != http.StatusNoContent {
		t.Fatalf("language: %d", got.Code)
	}
	var before rulesJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/settings/thresholds", nil), &before)
	disk := ruleOf(t, before, "disk")
	if disk.Attention != 85 || disk.Danger != 95 || disk.Recovery != 80 || disk.MinAttention != 50 || disk.MaxDanger != 99 || disk.Origin != "default" || disk.WindowMinutes != 0 {
		t.Fatalf("disk defaults %+v", disk)
	}
	cpu := ruleOf(t, before, "cpu")
	if cpu.Attention != 95 || cpu.WindowMinutes != 15 || cpu.MinWindowMinutes != 1 || cpu.MaxWindowMinutes != 120 {
		t.Fatalf("cpu defaults %+v", cpu)
	}
	if memory := ruleOf(t, before, "memory"); memory.Attention != 90 || memory.WindowMinutes != 15 {
		t.Fatalf("memory defaults %+v", memory)
	}

	got := callAPI(server.Server, http.MethodPut, "/api/settings/thresholds/disk", ruleRequest{Attention: 70, Danger: 90})
	if got.Code != http.StatusOK {
		t.Fatalf("status %d: %s", got.Code, got.Body.String())
	}
	var after rulesJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/settings/thresholds", nil), &after)
	if disk := ruleOf(t, after, "disk"); disk.Attention != 70 || disk.Danger != 90 || disk.Recovery != 65 || disk.Origin != "global" {
		t.Fatalf("saved %+v", disk)
	}
	if saved, err := server.db.LoadSettings(context.Background()); err != nil || saved.Language != "en" {
		t.Fatalf("the language must stay: %+v %v", saved, err)
	}
	var session sessionResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/session", nil), &session)
	if got := session.Thresholds["disk"]; got.Attention != 70 || got.Danger != 90 {
		t.Fatalf("session %+v", session.Thresholds)
	}
	if got := session.Thresholds["cpu"]; got.Attention != 95 || got.Danger != 99 {
		t.Fatalf("session cpu %+v", session.Thresholds)
	}
}

func TestAPI_Thresholds_RefuseWhatMakesNoSense_AndKeepThePrevious(t *testing.T) {
	server := newTestServer(t)
	refused := map[string]ruleRequest{
		"disk":    {Attention: 49, Danger: 95},
		"memory":  {Attention: 90, Danger: 95},
		"cpu":     {Attention: 95, Danger: 101, WindowMinutes: 15},
		"network": {Attention: 80, Danger: 90},
	}
	for resource, request := range refused {
		got := callAPI(server.Server, http.MethodPut, "/api/settings/thresholds/"+resource, request)
		if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "settings.threshold_invalid" {
			t.Fatalf("%s %+v: %d %s", resource, request, got.Code, got.Body.String())
		}
	}
	var rules rulesJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/settings/thresholds", nil), &rules)
	for _, rule := range rules.Rules {
		if rule.Origin != "default" {
			t.Fatalf("a refused write must not be applied: %+v", rule)
		}
	}
}

// Une machine sans surcharge suit le global et le dit ; sa surcharge se pose,
// se relit avec ce qu'elle remplace, et se retire.
func TestAPI_MachineThresholds_OverrideTheGlobalAndSayWhichApplies(t *testing.T) {
	server := newTestServer(t)
	if got := callAPI(server.Server, http.MethodPut, "/api/settings/thresholds/cpu", ruleRequest{Attention: 80, Danger: 90, WindowMinutes: 10}); got.Code != http.StatusOK {
		t.Fatalf("global: %d %s", got.Code, got.Body.String())
	}
	path := "/api/machines/" + machine.LocalID + "/thresholds"
	var followed rulesJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, path, nil), &followed)
	if cpu := ruleOf(t, followed, "cpu"); cpu.Origin != "global" || cpu.Attention != 80 || cpu.Global == nil || cpu.Global.WindowMinutes != 10 {
		t.Fatalf("follows the global: %+v", cpu)
	}
	got := callAPI(server.Server, http.MethodPut, path+"/cpu", ruleRequest{Attention: 98, Danger: 100, WindowMinutes: 30})
	if got.Code != http.StatusOK {
		t.Fatalf("override: %d %s", got.Code, got.Body.String())
	}
	var overridden rulesJSON
	decodeAPI(t, got, &overridden)
	if cpu := ruleOf(t, overridden, "cpu"); cpu.Origin != "machine" || cpu.Attention != 98 || cpu.WindowMinutes != 30 || cpu.Global == nil || cpu.Global.Attention != 80 {
		t.Fatalf("overridden: %+v", cpu)
	}
	if disk := ruleOf(t, overridden, "disk"); disk.Origin != "default" {
		t.Fatalf("the other resources keep following: %+v", disk)
	}
	var global rulesJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/settings/thresholds", nil), &global)
	if cpu := ruleOf(t, global, "cpu"); cpu.Attention != 80 || cpu.Origin != "global" {
		t.Fatalf("an override must not touch the global: %+v", cpu)
	}
	cleared := callAPI(server.Server, http.MethodDelete, path+"/cpu", nil)
	if cleared.Code != http.StatusOK {
		t.Fatalf("clear: %d %s", cleared.Code, cleared.Body.String())
	}
	var back rulesJSON
	decodeAPI(t, cleared, &back)
	if cpu := ruleOf(t, back, "cpu"); cpu.Origin != "global" || cpu.Attention != 80 {
		t.Fatalf("cleared: %+v", cpu)
	}
}

func TestAPI_MachineThresholds_UnknownMachineIs404_BadRuleIs422(t *testing.T) {
	server := newTestServer(t)
	for _, call := range []struct{ method, path string }{
		{http.MethodGet, "/api/machines/nope/thresholds"},
		{http.MethodPut, "/api/machines/nope/thresholds/cpu"},
		{http.MethodDelete, "/api/machines/nope/thresholds/cpu"},
	} {
		got := callAPI(server.Server, call.method, call.path, ruleRequest{Attention: 90, Danger: 95, WindowMinutes: 15})
		if got.Code != http.StatusNotFound {
			t.Errorf("%s %s: %d %s", call.method, call.path, got.Code, got.Body.String())
		}
	}
	got := callAPI(server.Server, http.MethodPut, "/api/machines/"+machine.LocalID+"/thresholds/cpu", ruleRequest{Attention: 99, Danger: 98, WindowMinutes: 15})
	if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "settings.threshold_invalid" {
		t.Fatalf("bad rule: %d %s", got.Code, got.Body.String())
	}
}

func TestAPI_Reminder_DefaultsToTwelveHours_ZeroTurnsItOff_OutOfBoundsIsRefused(t *testing.T) {
	server := newTestServer(t)
	var reminder reminderSettingsJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/settings/reminder", nil), &reminder)
	if reminder.Hours != 12 || reminder.MaxHours != 168 {
		t.Fatalf("default %+v", reminder)
	}
	if got := callAPI(server.Server, http.MethodPut, "/api/settings/reminder", reminderSettingsRequest{Hours: 0}); got.Code != http.StatusOK {
		t.Fatalf("off: %d %s", got.Code, got.Body.String())
	}
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/settings/reminder", nil), &reminder)
	if reminder.Hours != 0 {
		t.Fatalf("off %+v", reminder)
	}
	got := callAPI(server.Server, http.MethodPut, "/api/settings/reminder", reminderSettingsRequest{Hours: 169})
	if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "settings.reminder_invalid" {
		t.Fatalf("too long: %d %s", got.Code, got.Body.String())
	}
}
