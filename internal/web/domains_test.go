package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/store"
)

type fakeDomains struct {
	domains []store.Domain
}

func (f *fakeDomains) Domains(context.Context) ([]store.Domain, error) {
	return f.domains, nil
}

// Les deux actions d'hôte virtuel, telles que le catalogue les déclare : le
// port n'est pas un paramètre, il est lu sur la machine.
var (
	publish = catalog.Definition{
		Kind:       catalog.KindVhost,
		Label:      "Créer un hôte virtuel",
		Scope:      catalog.ScopeDomain,
		Place:      catalog.PlaceTarget,
		Reversible: true,
		Timeout:    5 * time.Minute,
		Params: []catalog.ParamSpec{
			{Name: "domain", Label: "nom de domaine", Type: catalog.ParamDomain, Required: true},
			{Name: "environment", Label: "environnement", Type: catalog.ParamSlug, Required: true},
			{Name: "service", Label: "service", Type: catalog.ParamSlug, Required: true},
		},
	}
	unpublish = catalog.Definition{
		Kind:       catalog.KindVhostRemove,
		Label:      "Supprimer un hôte virtuel",
		Scope:      catalog.ScopeDomain,
		Place:      catalog.PlaceTarget,
		Reversible: true,
		Interrupts: true,
		Timeout:    5 * time.Minute,
		Params: []catalog.ParamSpec{
			{Name: "domain", Label: "nom de domaine", Type: catalog.ParamDomain, Required: true},
		},
	}
)

var publishedTemoin = store.Domain{
	Name:        "temoin.exemple.fr",
	MachineID:   store.LocalMachineID,
	Environment: "prod",
	Service:     "temoin",
	Port:        80,
	CreatedAt:   time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC),
	UpdatedAt:   time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC),
}

// newDomainsServer monte l'interface avec la table des hôtes virtuels.
func newDomainsServer(t *testing.T, domains *fakeDomains) *Server {
	t.Helper()
	return newServerWith(t, Dependencies{
		Auth:      newTestAuth(t),
		Machines:  &fakeMachines{machines: []store.Machine{localMachine}},
		Domains:   domains,
		Enrolment: &fakeEnrolment{status: EnrolmentStatus{Enrolled: true, Since: time.Now()}},
		Actions:   newFakeActions(),
		Catalog:   &fakeCatalog{definitions: []catalog.Definition{diagnose, publish, unpublish}},
	})
}

func TestGetDomains_Empty_SaysSoAndOffersToPublish(t *testing.T) {
	visitor := signedIn(t, newDomainsServer(t, &fakeDomains{}))

	page := visitor.get("/domains").Body.String()

	if !strings.Contains(page, "Aucun nom publié") {
		t.Fatalf("la page ne dit pas qu'elle est vide :\n%s", page)
	}
	if !strings.Contains(page, `href="/domains/new"`) {
		t.Error("la page vide ne mène pas à la publication")
	}
}

// La ligne dit tout ce que la machine porte : le nom, où, quoi, et le port
// constaté — celui que personne n'a saisi.
func TestGetDomains_ListsWhatTheMachinesCarry(t *testing.T) {
	visitor := signedIn(t, newDomainsServer(t, &fakeDomains{domains: []store.Domain{publishedTemoin}}))

	page := visitor.get("/domains").Body.String()

	for _, expected := range []string{
		"temoin.exemple.fr",
		"machine openCloud",
		">prod<",
		">temoin<",
		">80<",
	} {
		if !strings.Contains(page, expected) {
			t.Errorf("la page ne montre pas %q :\n%s", expected, page)
		}
	}
}

// « Supprimer » mène à l'écran « avant » de l'action, sur la machine qui porte
// le nom, avec le nom déjà posé : rien n'est lancé par un lien.
func TestGetDomains_RemoveLink_LeadsToTheActionFormWithTheNameFilledIn(t *testing.T) {
	server := newDomainsServer(t, &fakeDomains{domains: []store.Domain{publishedTemoin}})
	visitor := signedIn(t, server)

	page := visitor.get("/domains").Body.String()

	expected := "/machines/local/actions/vhost-remove?domain=" + url.QueryEscape(publishedTemoin.Name)
	if !strings.Contains(page, expected) {
		t.Fatalf("la page ne porte pas le lien %q :\n%s", expected, page)
	}

	form := visitor.get(expected).Body.String()
	if !strings.Contains(form, `value="`+publishedTemoin.Name+`"`) {
		t.Errorf("l'écran de l'action ne reprend pas le nom :\n%s", form)
	}
}

// Sans la table branchée, la vue n'existe pas : l'interface ne montre pas une
// page vide qui mentirait.
func TestGetDomains_WithoutTheTable_IsNotFound(t *testing.T) {
	visitor := signedIn(t, newActionsServer(t,
		&fakeMachines{machines: []store.Machine{localMachine}},
		newFakeActions(),
		&fakeCatalog{definitions: []catalog.Definition{diagnose}},
		&fakeEnrolment{}))

	if code := visitor.get("/domains").Code; code != http.StatusNotFound {
		t.Fatalf("code = %d, attendu 404", code)
	}
}

// L'onglet Domaines est marqué courant quand on y est, et lui seul.
func TestGetDomains_MarksItsOwnTab(t *testing.T) {
	visitor := signedIn(t, newDomainsServer(t, &fakeDomains{}))

	page := visitor.get("/domains").Body.String()

	if !strings.Contains(page, `class="nav-item current" href="/domains"`) {
		t.Errorf("l'onglet Domaines n'est pas marqué courant :\n%s", page)
	}
	if strings.Contains(page, `class="nav-item current" href="/"`) {
		t.Error("l'onglet Infrastructure est marqué courant sur la vue Domaines")
	}
}

// Le premier écran de la publication mène au formulaire de l'action, par
// machine : aucun mécanisme de plus.
func TestGetDomainsNew_LeadsToTheActionFormOfEachMachine(t *testing.T) {
	visitor := signedIn(t, newDomainsServer(t, &fakeDomains{}))

	page := visitor.get("/domains/new").Body.String()

	if !strings.Contains(page, `href="/machines/local/actions/vhost"`) {
		t.Fatalf("la page ne mène pas au formulaire de l'action :\n%s", page)
	}
}
