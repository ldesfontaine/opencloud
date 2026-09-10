package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/zone"
)

// Les jetons des tests sont fabriqués, jamais écrits en dur : un scanner de
// secrets ne doit pas les prendre pour de vrais (packaging/test-action.sh fait
// de même pour son mot de passe).
func madeUpToken(what string) string {
	return "jetable-" + what + "-pour-le-test"
}

var webTestToken = madeUpToken("zone")

// fakeZones joue le gardien : il garde ce qu'on lui donne, et rend ce qu'on
// lui a posé. Le jeton n'en ressort par aucune méthode — comme le vrai.
type fakeZones struct {
	zones  []zone.Zone
	added  []string
	failed error
}

func (f *fakeZones) Zones(context.Context) ([]zone.Zone, error) {
	return f.zones, nil
}

func (f *fakeZones) Names(context.Context) ([]string, error) {
	names := make([]string, 0, len(f.zones))
	for _, registered := range f.zones {
		names = append(names, registered.Name)
	}
	return names, nil
}

func (f *fakeZones) Add(_ context.Context, name, token string) error {
	if f.failed != nil {
		return f.failed
	}
	f.added = append(f.added, name+" "+token)
	f.zones = append(f.zones, zone.Zone{
		Name: name, CloudflareID: "023e", Fingerprint: "abcdef012345",
		AddedAt: time.Now(), RotatedAt: time.Now(),
	})
	return nil
}

func (f *fakeZones) Rotate(_ context.Context, name, token string) error {
	if f.failed != nil {
		return f.failed
	}
	f.added = append(f.added, "rotate "+name+" "+token)
	return nil
}

func (f *fakeZones) Remove(_ context.Context, name string) error {
	if f.failed != nil {
		return f.failed
	}
	f.added = append(f.added, "remove "+name)
	return nil
}

// L'action de pose, telle que le catalogue la déclare.
var placeToken = catalog.Definition{
	Kind:       catalog.KindDNSToken,
	Label:      "Poser le jeton DNS",
	Scope:      catalog.ScopeDomain,
	Place:      catalog.PlaceTarget,
	Reversible: true,
	Interrupts: true,
	Timeout:    5 * time.Minute,
	Params: []catalog.ParamSpec{
		{Name: "zone", Label: "zone Cloudflare", Type: catalog.ParamDomain, Required: true},
	},
}

func newZonesServer(t *testing.T, zones *fakeZones, domains *fakeDomains) *Server {
	t.Helper()
	return newServerWith(t, Dependencies{
		Auth:      newTestAuth(t),
		Machines:  &fakeMachines{machines: []store.Machine{localMachine}},
		Domains:   domains,
		Zones:     zones,
		Enrolment: &fakeEnrolment{status: EnrolmentStatus{Enrolled: true, Since: time.Now()}},
		Actions:   newFakeActions(),
		Catalog:   &fakeCatalog{definitions: []catalog.Definition{diagnose, publish, unpublish, placeToken}},
	})
}

func TestGetDomains_WithoutAnyZone_SaysTheSectionIsEmpty(t *testing.T) {
	visitor := signedIn(t, newZonesServer(t, &fakeZones{}, &fakeDomains{}))

	page := visitor.get("/domains").Body.String()

	if !strings.Contains(page, "Zones Cloudflare") {
		t.Fatalf("la vue Domaines ne montre pas la section des zones :\n%s", page)
	}
	if !strings.Contains(page, "Aucune zone enregistrée") {
		t.Errorf("la section ne dit pas qu'elle est vide :\n%s", page)
	}
	if !strings.Contains(page, `action="/zones"`) {
		t.Errorf("la section n'offre pas d'ajouter une zone :\n%s", page)
	}
}

// La ligne dit la zone, ses dates, et l'état du jeton sur chaque machine.
func TestGetDomains_PopulatedZones_ShowEachMachineAndItsMark(t *testing.T) {
	registered := zone.Zone{
		Name:        "exemple.fr",
		AddedAt:     time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC),
		RotatedAt:   time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC),
		Fingerprint: "abcdef012345",
		Machines: []zone.Placement{{
			MachineID: store.LocalMachineID, PlacedAt: time.Now(),
			Fingerprint: "abcdef012345", Current: true,
		}},
	}
	visitor := signedIn(t, newZonesServer(t, &fakeZones{zones: []zone.Zone{registered}}, &fakeDomains{}))

	page := visitor.get("/domains").Body.String()

	for _, expected := range []string{"exemple.fr", "abcdef012345", markCurrent, labelTokenCurrent} {
		if !strings.Contains(page, expected) {
			t.Errorf("la page ne montre pas %q :\n%s", expected, page)
		}
	}
}

// Après une rotation, la machine qui n'a pas été rejouée est signalée.
func TestGetDomains_MachineWithTheOldToken_IsMarkedStale(t *testing.T) {
	registered := zone.Zone{
		Name: "exemple.fr", Fingerprint: "aaaaaaaaaaaa",
		Machines: []zone.Placement{{
			MachineID: store.LocalMachineID, PlacedAt: time.Now(),
			Fingerprint: "bbbbbbbbbbbb", Current: false,
		}},
	}
	visitor := signedIn(t, newZonesServer(t, &fakeZones{zones: []zone.Zone{registered}}, &fakeDomains{}))

	page := visitor.get("/domains").Body.String()

	if !strings.Contains(page, labelTokenStale) {
		t.Fatalf("la page ne signale pas l'ancien jeton :\n%s", page)
	}
}

// « Poser le jeton » mène à l'écran « avant » de l'action, sur la machine
// choisie, la zone déjà remplie : rien n'est lancé par un lien.
func TestGetDomains_PlaceLink_LeadsToTheActionFormWithTheZoneFilledIn(t *testing.T) {
	registered := zone.Zone{Name: "exemple.fr", Fingerprint: "abcdef012345"}
	server := newZonesServer(t, &fakeZones{zones: []zone.Zone{registered}}, &fakeDomains{})
	visitor := signedIn(t, server)

	page := visitor.get("/domains").Body.String()

	expected := "/machines/local/actions/dns-token?zone=" + url.QueryEscape("exemple.fr")
	if !strings.Contains(page, expected) {
		t.Fatalf("la page ne porte pas le lien %q :\n%s", expected, page)
	}

	form := visitor.get(expected).Body.String()
	if !strings.Contains(form, `<option value="exemple.fr" selected>`) {
		t.Errorf("l'écran de l'action ne pré-remplit pas la zone :\n%s", form)
	}
}

// La zone se choisit dans une liste : une zone qu'openCloud ne tient pas n'a
// pas de jeton, il n'y a rien à taper.
func TestGetActionForm_DNSToken_OffersTheZonesInAList(t *testing.T) {
	registered := zone.Zone{Name: "exemple.fr", Fingerprint: "abcdef012345"}
	visitor := signedIn(t, newZonesServer(t, &fakeZones{zones: []zone.Zone{registered}}, &fakeDomains{}))

	form := visitor.get("/machines/local/actions/dns-token").Body.String()

	if !strings.Contains(form, `<select id="zone" name="zone"`) {
		t.Fatalf("la zone n'est pas proposée dans une liste :\n%s", form)
	}
	if strings.Contains(form, `<input id="zone"`) {
		t.Error("la zone reste saisissable à la main")
	}
}

// L'écran « avant » nomme le fichier du jeton, son mode et sa taille, et ne
// montre pas ce qu'il y a dedans.
func TestGetActionForm_DNSToken_NamesTheSecretFileWithoutShowingIt(t *testing.T) {
	registered := zone.Zone{Name: "exemple.fr", Fingerprint: "abcdef012345"}
	server := newZonesServer(t, &fakeZones{zones: []zone.Zone{registered}}, &fakeDomains{})
	server.catalog = &fakeCatalog{
		definitions: []catalog.Definition{placeToken},
		files: map[catalog.Kind][]catalog.File{
			catalog.KindDNSToken: {{
				Path: "cloudflare.token", Content: []byte(webTestToken + "\n"),
				Mode: 0o600, Secret: true,
			}},
		},
	}
	visitor := signedIn(t, server)

	form := visitor.get("/machines/local/actions/dns-token?zone=exemple.fr").Body.String()

	if strings.Contains(form, webTestToken) {
		t.Fatalf("l'écran « avant » montre le jeton :\n%s", form)
	}
	for _, expected := range []string{"cloudflare.token", "0600", "secret, non affiché"} {
		if !strings.Contains(form, expected) {
			t.Errorf("l'écran ne dit pas %q :\n%s", expected, form)
		}
	}
}

func TestPostZones_ValidToken_AddsTheZoneAndReturnsToDomains(t *testing.T) {
	zones := &fakeZones{}
	visitor := signedIn(t, newZonesServer(t, zones, &fakeDomains{}))
	csrfToken := visitor.csrfFrom(visitor.get("/domains"))

	response := visitor.post("/zones", url.Values{
		csrfFieldName: {csrfToken},
		"zone":        {"exemple.fr"},
		"token":       {webTestToken},
	})

	expectRedirect(t, response, "/domains")
	if len(zones.added) != 1 || !strings.HasPrefix(zones.added[0], "exemple.fr ") {
		t.Fatalf("le gardien a reçu %v", zones.added)
	}
}

// Ce que l'opérateur a saisi ne se réaffiche jamais : le jeton n'est ni dans
// la page qui suit l'ajout, ni dans le champ du formulaire.
func TestPostZones_AfterAdding_NoPageEverShowsTheToken(t *testing.T) {
	zones := &fakeZones{}
	visitor := signedIn(t, newZonesServer(t, zones, &fakeDomains{}))
	csrfToken := visitor.csrfFrom(visitor.get("/domains"))

	visitor.post("/zones", url.Values{
		csrfFieldName: {csrfToken},
		"zone":        {"exemple.fr"},
		"token":       {webTestToken},
	})

	for _, path := range []string{"/domains", "/machines/local/actions/dns-token?zone=exemple.fr"} {
		page := visitor.get(path).Body.String()
		if strings.Contains(page, webTestToken) {
			t.Fatalf("%s réaffiche le jeton :\n%s", path, page)
		}
	}
}

// Un refus du gardien se lit en deux temps, et le nom saisi revient — jamais
// le jeton.
func TestPostZones_Refused_ShowsTheCauseAndKeepsTheNameOnly(t *testing.T) {
	zones := &fakeZones{failed: refusal.Refusal{
		Cause:  "Cloudflare refuse ce jeton : il est invalide, révoqué ou expiré",
		Remedy: "créer un jeton d'API « Zone / DNS / Edit » sur cette zone, puis le coller ici",
	}}
	visitor := signedIn(t, newZonesServer(t, zones, &fakeDomains{}))
	csrfToken := visitor.csrfFrom(visitor.get("/domains"))

	response := visitor.post("/zones", url.Values{
		csrfFieldName: {csrfToken},
		"zone":        {"exemple.fr"},
		"token":       {webTestToken},
	})

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d, attendu 422", response.Code)
	}
	page := response.Body.String()
	if !strings.Contains(page, "Cloudflare refuse ce jeton") {
		t.Errorf("la page ne dit pas la cause :\n%s", page)
	}
	if !strings.Contains(page, `value="exemple.fr"`) {
		t.Errorf("la page ne garde pas le nom saisi :\n%s", page)
	}
	if strings.Contains(page, webTestToken) {
		t.Fatalf("la page réaffiche le jeton :\n%s", page)
	}
}

func TestPostZoneToken_RotatesTheZoneToken(t *testing.T) {
	registered := zone.Zone{Name: "exemple.fr", Fingerprint: "abcdef012345"}
	zones := &fakeZones{zones: []zone.Zone{registered}}
	visitor := signedIn(t, newZonesServer(t, zones, &fakeDomains{}))
	csrfToken := visitor.csrfFrom(visitor.get("/domains"))

	response := visitor.post("/zones/exemple.fr/token", url.Values{
		csrfFieldName: {csrfToken},
		"token":       {madeUpToken("zone-tournee")},
	})

	expectRedirect(t, response, "/domains")
	if len(zones.added) != 1 || !strings.HasPrefix(zones.added[0], "rotate exemple.fr ") {
		t.Fatalf("le gardien a reçu %v", zones.added)
	}
}

func TestPostZoneRemove_RemovesTheZone(t *testing.T) {
	registered := zone.Zone{Name: "exemple.fr", Fingerprint: "abcdef012345"}
	zones := &fakeZones{zones: []zone.Zone{registered}}
	visitor := signedIn(t, newZonesServer(t, zones, &fakeDomains{}))
	csrfToken := visitor.csrfFrom(visitor.get("/domains"))

	response := visitor.post("/zones/exemple.fr/remove", url.Values{csrfFieldName: {csrfToken}})

	expectRedirect(t, response, "/domains")
	if len(zones.added) != 1 || zones.added[0] != "remove exemple.fr" {
		t.Fatalf("le gardien a reçu %v", zones.added)
	}
}

// Sans le gardien branché, la section n'existe pas et ses gestes non plus.
func TestZones_WithoutTheKeeper_TheSectionAndItsGesturesAreGone(t *testing.T) {
	visitor := signedIn(t, newDomainsServer(t, &fakeDomains{}))

	page := visitor.get("/domains").Body.String()
	if strings.Contains(page, "Zones Cloudflare") {
		t.Errorf("la section s'affiche sans gardien :\n%s", page)
	}
	if code := visitor.post("/zones", url.Values{}).Code; code != http.StatusNotFound {
		t.Errorf("POST /zones = %d, attendu 404", code)
	}
}
