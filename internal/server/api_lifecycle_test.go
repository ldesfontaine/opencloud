package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/lifecycle"
	"github.com/ldesfontaine/opencloud/internal/settings"
)

// La source des tests : PostgreSQL et Debian, et le compte de ce qu'on lui
// a demandé. Aucun test ne joint endoflife.date.
type supportSource struct {
	asked int
}

func newSupportSource() *supportSource {
	return &supportSource{}
}

func (s *supportSource) Repositories(context.Context) (map[string]string, error) {
	s.asked++
	return map[string]string{"library/postgres": "postgresql"}, nil
}

func (s *supportSource) Product(_ context.Context, name string) (lifecycle.Product, error) {
	s.asked++
	switch name {
	case "postgresql":
		return lifecycle.Product{Name: name, Label: "PostgreSQL", Cycles: []lifecycle.Cycle{
			{Name: "18", IsMaintained: true, EOLFrom: "2030-11-14", Latest: "18.6"},
			{Name: "17", IsMaintained: true, EOLFrom: "2029-11-08", Latest: "17.11"},
			{Name: "16", IsMaintained: true, EOLFrom: "2028-11-09", Latest: "16.15"},
			{Name: "12", IsEOL: true, EOLFrom: "2024-11-21", Latest: "12.22"},
		}}, nil
	case "debian":
		return lifecycle.Product{Name: name, Label: "Debian", Cycles: []lifecycle.Cycle{
			{Name: "13", IsMaintained: true, EOLFrom: "2030-06-30", Latest: "13.7"},
			{Name: "12", IsMaintained: true, EOLFrom: "2028-06-30", Latest: "12.15"},
		}}, nil
	}
	return lifecycle.Product{}, lifecycle.ErrNoProduct
}

func lifecycleSetting(preferences *settings.Store) func() bool {
	return func() bool {
		current, err := preferences.Load()
		return err == nil && current.LifecycleEnabled
	}
}

// Coupé par défaut : la fiche ne dit rien du support, et rien ne sort
// vers la source, même si on la presse.
func TestAPI_Lifecycle_OffByDefault_ShowsNothingAndReadsNothing(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris", remoteID)
	serviceID := server.seedServices(t)
	var state lifecycleSettingsJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/settings/lifecycle", nil), &state)
	if state.Enabled || state.Source != "endoflife.date" || state.FetchedAt != nil || state.LastAttemptAt != nil || state.Products != 0 {
		t.Fatalf("defaults %+v", state)
	}
	if err := server.lifecycle.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if server.support.asked != 0 {
		t.Fatalf("the source was read %d times while the setting is off", server.support.asked)
	}
	got := callAPI(server.Server, http.MethodGet, "/api/services/"+serviceID, nil)
	if got.Code != http.StatusOK || strings.Contains(got.Body.String(), `"lifecycle"`) {
		t.Fatalf("status %d: %s", got.Code, got.Body.String())
	}
}

// Activé : le réglage s'écrit sans toucher la langue, la lecture suivante
// renseigne la fiche, et le couper la vide aussitôt.
func TestAPI_Lifecycle_Enabled_TheServiceSaysItsSupport(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris", remoteID)
	serviceID := server.seedServices(t)
	if got := callAPI(server.Server, http.MethodPut, "/api/session/language", languageRequest{Language: "en"}); got.Code != http.StatusNoContent {
		t.Fatalf("language: %d", got.Code)
	}
	got := callAPI(server.Server, http.MethodPut, "/api/settings/lifecycle", lifecycleSettingsRequest{Enabled: true})
	var state lifecycleSettingsJSON
	decodeAPI(t, got, &state)
	if got.Code != http.StatusOK || !state.Enabled || state.FetchedAt != nil {
		t.Fatalf("status %d: %+v", got.Code, state)
	}
	saved, err := server.settings.Load()
	if err != nil || !saved.LifecycleEnabled || saved.Language != "en" {
		t.Fatalf("file %+v %v", saved, err)
	}

	// Avant la première lecture, la fiche dit qu'elle attend.
	var pending serviceResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/services/"+serviceID, nil), &pending)
	if pending.Service.Lifecycle == nil || pending.Service.Lifecycle.Status != string(lifecycle.StatusPending) || pending.Service.Lifecycle.FetchedAt != nil {
		t.Fatalf("before the first reading: %+v", pending.Service.Lifecycle)
	}

	if err := server.lifecycle.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	var response serviceResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/services/"+serviceID, nil), &response)
	support := response.Service.Lifecycle
	if support == nil || support.Status != "maintained" || support.Product != "PostgreSQL" || support.Cycle != "16" || support.EOLFrom != "2028-11-09" || support.Latest != "16.15" {
		t.Fatalf("postgres:16.4: %+v", support)
	}
	if support.ProductURL != "https://endoflife.date/postgresql" || support.IsStale || support.FetchedAt == nil || !support.FetchedAt.Equal(testNow) {
		t.Fatalf("postgres:16.4: %+v", support)
	}
	if len(support.Newer) != 2 || support.Newer[0] != (newerCycleJSON{Cycle: "17", Latest: "17.11", EOLFrom: "2029-11-08"}) || support.Newer[1].Cycle != "18" {
		t.Fatalf("newer %+v", support.Newer)
	}
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/settings/lifecycle", nil), &state)
	if !state.Enabled || state.Products != 1 || state.IsFailing || state.FetchedAt == nil || !state.FetchedAt.Equal(testNow) {
		t.Fatalf("after the reading: %+v", state)
	}

	// Les listes ne le portent pas : la fiche seulement.
	if list := callAPI(server.Server, http.MethodGet, "/api/services", nil); strings.Contains(list.Body.String(), `"lifecycle"`) {
		t.Fatalf("the list carries the support: %s", list.Body.String())
	}

	asked := server.support.asked
	got = callAPI(server.Server, http.MethodPut, "/api/settings/lifecycle", lifecycleSettingsRequest{Enabled: false})
	decodeAPI(t, got, &state)
	if got.Code != http.StatusOK || state.Enabled {
		t.Fatalf("status %d: %+v", got.Code, state)
	}
	if err := server.lifecycle.Refresh(context.Background()); err != nil || server.support.asked != asked {
		t.Fatalf("the source was read after the setting was turned off: %v", err)
	}
	if off := callAPI(server.Server, http.MethodGet, "/api/services/"+serviceID, nil); strings.Contains(off.Body.String(), `"lifecycle"`) {
		t.Fatalf("turned off, the service still says its support: %s", off.Body.String())
	}
}

// Le système d'une machine dit son support comme l'image d'un service :
// rien tant que le réglage est coupé, puis la branche et jusqu'à quand.
// Un système que la table ne connaît pas reste « support inconnu ».
func TestAPI_Lifecycle_Enabled_TheMachineSaysItsSystemSupport(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris", remoteID)
	// L'agent redit son système à la connexion, sous le nom d'os-release.
	if err := server.machines.NoteSystem(context.Background(), remoteID, "Debian GNU/Linux 12"); err != nil {
		t.Fatal(err)
	}
	if off := callAPI(server.Server, http.MethodGet, "/api/machines", nil); off.Code != http.StatusOK || strings.Contains(off.Body.String(), `"lifecycle"`) {
		t.Fatalf("turned off: %d %s", off.Code, off.Body.String())
	}
	if got := callAPI(server.Server, http.MethodPut, "/api/settings/lifecycle", lifecycleSettingsRequest{Enabled: true}); got.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", got.Code, got.Body.String())
	}
	if err := server.lifecycle.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	var remote machineJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID, nil), &remote)
	support := remote.Lifecycle
	if remote.OS != "Debian GNU/Linux 12" || support == nil || support.Status != "maintained" || support.Product != "Debian" || support.Cycle != "12" || support.EOLFrom != "2028-06-30" {
		t.Fatalf("remote machine: %q %+v", remote.OS, support)
	}
	if support.ProductURL != "https://endoflife.date/debian" || len(support.Newer) != 1 || support.Newer[0].Cycle != "13" {
		t.Fatalf("remote machine: %+v", support)
	}

	// La machine openCloud du test se nomme « Debian 12 », pas comme
	// os-release : rien n'est deviné.
	var list machinesResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/machines", nil), &list)
	for _, item := range list.Machines {
		if item.Lifecycle == nil {
			t.Fatalf("%s: no support while the setting is on", item.Name)
		}
		if item.Local && item.Lifecycle.Status != string(lifecycle.StatusUnknownProduct) {
			t.Fatalf("local machine %q: %+v", item.OS, item.Lifecycle)
		}
	}
	var state lifecycleSettingsJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/settings/lifecycle", nil), &state)
	if state.Products != 1 {
		t.Fatalf("one product in use, debian: %+v", state)
	}
}
