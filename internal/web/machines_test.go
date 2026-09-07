package web

import (
	"context"
	"github.com/ldesfontaine/opencloud/internal/catalog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/store"
)

// La machine témoin : déclarée, jointe par SSH sur son port standard.
var remoteMachine = store.Machine{
	ID: "web-1", Name: "Web 1", Address: "192.0.2.10", Port: 22, Account: "opencloud",
	CreatedAt: time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC),
}

// Une empreinte bien formée : SHA256 en base64 sans remplissage.
var goodFingerprint = "SHA256:" + strings.Repeat("a", 43)

func (f *fakeMachines) Insert(_ context.Context, machine store.Machine) error {
	f.machines = append(f.machines, machine)
	return nil
}

func (f *fakeMachines) UpdateAccess(_ context.Context, id string, address string, port int) error {
	for index, machine := range f.machines {
		if machine.ID != id {
			continue
		}
		f.machines[index].Address = address
		f.machines[index].Port = port
		return nil
	}
	return store.ErrNotFound
}

// fakeEnroller garde ce qu'on lui a demandé : la clé engendrée, l'empreinte
// confirmée, l'accès changé.
type fakeEnroller struct {
	command     string
	prepared    []string
	confirmed   string
	fingerprint string
	access      *store.Machine
	err         error
}

func (f *fakeEnroller) Prepare(_ context.Context, machine store.Machine) (string, error) {
	f.prepared = append(f.prepared, machine.ID)
	return f.command, nil
}

func (f *fakeEnroller) Confirm(_ context.Context, machine store.Machine, fingerprint string) error {
	if f.err != nil {
		return f.err
	}
	f.confirmed = machine.ID
	f.fingerprint = fingerprint
	return nil
}

func (f *fakeEnroller) UpdateAccess(_ context.Context, machine store.Machine, fingerprint string) error {
	if f.err != nil {
		return f.err
	}
	f.access = &machine
	f.fingerprint = fingerprint
	return nil
}

type fakeProber struct {
	health MachineHealth
	probed []string
	err    error
}

func (f *fakeProber) Now(_ context.Context, machineID string) error {
	f.probed = append(f.probed, machineID)
	return f.err
}

func (f *fakeProber) Health(context.Context, string) (MachineHealth, error) {
	return f.health, nil
}

// enrolFakes rassemble les faux d'un serveur qui enrôle. Un faux laissé nul
// n'est pas branché : l'interface doit s'en passer.
type enrolFakes struct {
	machines  *fakeMachines
	actions   *fakeActions
	enrolment *fakeEnrolment
	enroller  *fakeEnroller
	prober    *fakeProber
}

func newEnrolServer(t *testing.T, fakes enrolFakes) *Server {
	t.Helper()
	if fakes.machines == nil {
		fakes.machines = &fakeMachines{}
	}
	if fakes.actions == nil {
		fakes.actions = newFakeActions()
	}

	deps := Dependencies{
		Auth:        newTestAuth(t),
		Machines:    fakes.machines,
		Declaration: fakes.machines,
		Actions:     fakes.actions,
		Catalog:     defaultCatalog(),
	}
	// Une interface qui porte un pointeur nul n'est pas nulle : on ne branche
	// que ce que le test a posé.
	if fakes.enrolment != nil {
		deps.Enrolment = fakes.enrolment
	}
	if fakes.enroller != nil {
		deps.Enroller = fakes.enroller
	}
	if fakes.prober != nil {
		deps.Prober = fakes.prober
	}
	return newServerWith(t, deps)
}

func enrolledSince(moment time.Time) *fakeEnrolment {
	return &fakeEnrolment{status: EnrolmentStatus{Enrolled: true, Since: moment}}
}

func TestMachineForm_OffersTheThreeFieldsAndPort22(t *testing.T) {
	server := newEnrolServer(t, enrolFakes{enroller: &fakeEnroller{}})

	body := signedIn(t, server).get("/machines/new").Body.String()

	for _, expected := range []string{`name="name"`, `name="address"`, `name="port"`, `value="22"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("le formulaire ne porte pas %q :\n%s", expected, body)
		}
	}
}

func TestPostMachine_DerivesTheIdentifierAndAsksForTheCommand(t *testing.T) {
	machines := &fakeMachines{}
	enroller := &fakeEnroller{command: "sudo opencloud-enroll --machine serveur-ete"}
	server := newEnrolServer(t, enrolFakes{machines: machines, enroller: enroller})
	visitor := signedIn(t, server)
	csrfToken := visitor.csrfFrom(visitor.get("/machines/new"))

	response := visitor.post("/machines", url.Values{
		csrfFieldName:    {csrfToken},
		nameFieldName:    {"Serveur Été"},
		addressFieldName: {"192.0.2.10"},
		portFieldName:    {"22"},
	})

	expectRedirect(t, response, "/machines/serveur-ete")
	if len(machines.machines) != 1 {
		t.Fatalf("machines = %+v", machines.machines)
	}
	declared := machines.machines[0]
	if declared.ID != "serveur-ete" || declared.Name != "Serveur Été" {
		t.Fatalf("l'identifiant doit être dérivé du nom : %+v", declared)
	}
	if declared.Port != 22 || declared.Account != machineAccount {
		t.Fatalf("le port SSH et le compte fixé : %+v", declared)
	}
	if len(enroller.prepared) != 1 || enroller.prepared[0] != "serveur-ete" {
		t.Fatalf("la clé doit être engendrée juste après l'insertion : %v", enroller.prepared)
	}
}

func TestPostMachine_Hostname_IsAcceptedLikeAnAddress(t *testing.T) {
	machines := &fakeMachines{}
	server := newEnrolServer(t, enrolFakes{machines: machines, enroller: &fakeEnroller{}})
	visitor := signedIn(t, server)
	csrfToken := visitor.csrfFrom(visitor.get("/machines/new"))

	response := visitor.post("/machines", url.Values{
		csrfFieldName:    {csrfToken},
		nameFieldName:    {"Web 1"},
		addressFieldName: {"machine.exemple.fr"},
		portFieldName:    {"2222"},
	})

	expectRedirect(t, response, "/machines/web-1")
	if machines.machines[0].Address != "machine.exemple.fr" {
		t.Fatalf("machine = %+v", machines.machines[0])
	}
}

func TestPostMachine_EachRefusalNamesItsRemedy(t *testing.T) {
	cases := []struct {
		name    string
		form    url.Values
		expects []string
	}{
		{
			name:    "un nom qui ne donne pas d'identifiant",
			form:    url.Values{nameFieldName: {"!!!"}, addressFieldName: {"192.0.2.10"}, portFieldName: {"22"}},
			expects: []string{"ne donne aucun identifiant", "commence par une lettre"},
		},
		{
			name:    "l'identifiant de la machine openCloud",
			form:    url.Values{nameFieldName: {"Local"}, addressFieldName: {"192.0.2.10"}, portFieldName: {"22"}},
			expects: []string{"est celui de la machine openCloud", "autre nom"},
		},
		{
			name:    "un identifiant déjà pris",
			form:    url.Values{nameFieldName: {"Web 1"}, addressFieldName: {"192.0.2.10"}, portFieldName: {"22"}},
			expects: []string{"porte déjà", "web-1"},
		},
		{
			name:    "une adresse qui n'en est pas une",
			form:    url.Values{nameFieldName: {"Web 2"}, addressFieldName: {"MACHINE À MOI"}, portFieldName: {"22"}},
			expects: []string{"ni une adresse IP ni un nom", "IPv4 ou IPv6"},
		},
		{
			name:    "un port hors des bornes",
			form:    url.Values{nameFieldName: {"Web 2"}, addressFieldName: {"192.0.2.10"}, portFieldName: {"70000"}},
			expects: []string{"70000", "entre 1 et 65535"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			machines := &fakeMachines{machines: []store.Machine{remoteMachine}}
			server := newEnrolServer(t, enrolFakes{machines: machines, enroller: &fakeEnroller{}})
			visitor := signedIn(t, server)
			form := testCase.form
			form.Set(csrfFieldName, visitor.csrfFrom(visitor.get("/machines/new")))

			response := visitor.post("/machines", form)

			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("code = %d, attendu 422", response.Code)
			}
			body := response.Body.String()
			for _, expected := range testCase.expects {
				if !strings.Contains(body, expected) {
					t.Fatalf("le refus ne dit pas %q :\n%s", expected, body)
				}
			}
			if len(machines.machines) != 1 {
				t.Fatal("rien ne doit être déclaré sur un refus")
			}
		})
	}
}

func TestPostMachine_WithoutCSRF_IsForbidden(t *testing.T) {
	machines := &fakeMachines{}
	server := newEnrolServer(t, enrolFakes{machines: machines, enroller: &fakeEnroller{}})

	response := signedIn(t, server).post("/machines", url.Values{
		nameFieldName:    {"Web 1"},
		addressFieldName: {"192.0.2.10"},
		portFieldName:    {"22"},
	})

	if response.Code != http.StatusForbidden {
		t.Fatalf("code = %d, attendu 403", response.Code)
	}
	if len(machines.machines) != 0 {
		t.Fatal("rien ne doit être déclaré sans jeton")
	}
}

func TestMachine_NotEnrolled_ShowsTheCommandAndTheFingerprintForm(t *testing.T) {
	enroller := &fakeEnroller{command: "sudo opencloud-enroll --machine web-1"}
	server := newEnrolServer(t, enrolFakes{
		machines: &fakeMachines{machines: []store.Machine{remoteMachine}},
		enroller: enroller,
	})

	body := signedIn(t, server).get("/machines/web-1").Body.String()

	for _, expected := range []string{
		"sudo opencloud-enroll --machine web-1", `<textarea class="command" readonly`,
		"en root", "empreinte", `action="/machines/web-1/confirm"`, `name="fingerprint"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("la fiche ne montre pas %q :\n%s", expected, body)
		}
	}
}

// La machine openCloud s'amorce par enroll-local : aucune commande à coller,
// aucune empreinte à saisir.
func TestMachine_Local_KeepsEnrollLocalAndHasNoCommand(t *testing.T) {
	server := newEnrolServer(t, enrolFakes{
		machines: &fakeMachines{machines: []store.Machine{localMachine}},
		enroller: &fakeEnroller{command: "sudo opencloud-enroll --machine local"},
	})

	body := signedIn(t, server).get("/machines/local").Body.String()

	if !strings.Contains(body, "sudo opencloud enroll-local") {
		t.Fatalf("la fiche doit garder le geste d'amorçage :\n%s", body)
	}
	if strings.Contains(body, "opencloud-enroll --machine local") ||
		strings.Contains(body, "/machines/local/confirm") {
		t.Fatalf("la machine openCloud n'a pas de commande à coller :\n%s", body)
	}
}

func TestPostFingerprint_Malformed_Is400AndSaysTheForm(t *testing.T) {
	enroller := &fakeEnroller{command: "sudo opencloud-enroll --machine web-1"}
	server := newEnrolServer(t, enrolFakes{
		machines: &fakeMachines{machines: []store.Machine{remoteMachine}},
		enroller: enroller,
	})
	visitor := signedIn(t, server)
	csrfToken := visitor.csrfFrom(visitor.get("/machines/web-1"))

	response := visitor.post("/machines/web-1/confirm", url.Values{
		csrfFieldName:        {csrfToken},
		fingerprintFieldName: {"SHA256:trop-court"},
	})

	if response.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, attendu 400", response.Code)
	}
	if !strings.Contains(response.Body.String(), "suivi de 43 caractères") {
		t.Fatalf("le refus doit dire la forme attendue :\n%s", response.Body.String())
	}
	if enroller.confirmed != "" {
		t.Fatal("rien ne doit partir vers la machine")
	}
}

func TestPostFingerprint_Mismatch_IsRefusedOnTheMachinePage(t *testing.T) {
	enroller := &fakeEnroller{
		command: "sudo opencloud-enroll --machine web-1",
		err: refusal.Refusal{
			Cause:  "la clé d'hôte relevée ne correspond pas à l'empreinte saisie",
			Remedy: "rejouer la commande sur la machine et recopier l'empreinte qu'elle affiche",
		},
	}
	server := newEnrolServer(t, enrolFakes{
		machines: &fakeMachines{machines: []store.Machine{remoteMachine}},
		enroller: enroller,
	})
	visitor := signedIn(t, server)
	csrfToken := visitor.csrfFrom(visitor.get("/machines/web-1"))

	response := visitor.post("/machines/web-1/confirm", url.Values{
		csrfFieldName:        {csrfToken},
		fingerprintFieldName: {goodFingerprint},
	})

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d, attendu 422", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "ne correspond pas") || !strings.Contains(body, "rejouer la commande") {
		t.Fatalf("le refus doit dire la cause et le geste :\n%s", body)
	}
}

func TestPostFingerprint_Confirmed_GoesBackToTheMachine(t *testing.T) {
	enroller := &fakeEnroller{command: "sudo opencloud-enroll --machine web-1"}
	server := newEnrolServer(t, enrolFakes{
		machines: &fakeMachines{machines: []store.Machine{remoteMachine}},
		enroller: enroller,
	})
	visitor := signedIn(t, server)
	csrfToken := visitor.csrfFrom(visitor.get("/machines/web-1"))

	response := visitor.post("/machines/web-1/confirm", url.Values{
		csrfFieldName:        {csrfToken},
		fingerprintFieldName: {goodFingerprint},
	})

	expectRedirect(t, response, "/machines/web-1")
	if enroller.confirmed != "web-1" || enroller.fingerprint != goodFingerprint {
		t.Fatalf("empreinte confirmée = %q sur %q", enroller.fingerprint, enroller.confirmed)
	}
}

// Une fois enrôlée, la fiche dit depuis quand.
func TestMachine_Enrolled_SaysSinceWhen(t *testing.T) {
	since := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	server := newEnrolServer(t, enrolFakes{
		machines:  &fakeMachines{machines: []store.Machine{remoteMachine}},
		enrolment: enrolledSince(since),
		enroller:  &fakeEnroller{command: "sudo opencloud-enroll --machine web-1"},
	})

	body := signedIn(t, server).get("/machines/web-1").Body.String()

	if !strings.Contains(body, "enrôlée depuis le "+formatMoment(since)) {
		t.Fatalf("la fiche doit dater l'enrôlement :\n%s", body)
	}
	if strings.Contains(body, "/machines/web-1/confirm") {
		t.Fatal("une machine enrôlée n'a plus d'empreinte à saisir")
	}
}

func TestPostProbe_RunsItAndGoesBackToTheMachine(t *testing.T) {
	prober := &fakeProber{}
	server := newEnrolServer(t, enrolFakes{
		machines:  &fakeMachines{machines: []store.Machine{remoteMachine}},
		enrolment: enrolledSince(time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)),
		enroller:  &fakeEnroller{},
		prober:    prober,
	})
	visitor := signedIn(t, server)
	page := visitor.get("/machines/web-1")
	if !strings.Contains(page.Body.String(), "Tester l'accès") {
		t.Fatalf("la fiche doit porter le bouton :\n%s", page.Body.String())
	}

	response := visitor.post("/machines/web-1/probe", url.Values{csrfFieldName: {visitor.csrfFrom(page)}})

	expectRedirect(t, response, "/machines/web-1")
	if len(prober.probed) != 1 || prober.probed[0] != "web-1" {
		t.Fatalf("sondages = %v", prober.probed)
	}
}

func TestPostProbe_Refusal_IsShownOnTheMachinePage(t *testing.T) {
	prober := &fakeProber{err: refusal.Refusal{
		Cause:  "cette machine n'est pas enrôlée",
		Remedy: "confirmer une empreinte de clé avant de sonder",
	}}
	server := newEnrolServer(t, enrolFakes{
		machines: &fakeMachines{machines: []store.Machine{remoteMachine}},
		enroller: &fakeEnroller{},
		prober:   prober,
	})
	visitor := signedIn(t, server)
	csrfToken := visitor.csrfFrom(visitor.get("/machines/web-1"))

	response := visitor.post("/machines/web-1/probe", url.Values{csrfFieldName: {csrfToken}})

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d, attendu 422", response.Code)
	}
	if !strings.Contains(response.Body.String(), "confirmer une empreinte") {
		t.Fatalf("le refus doit dire le geste :\n%s", response.Body.String())
	}
}

// Sans sonde branchée, la fiche n'offre pas le bouton et la route ne sert rien.
func TestProbe_WithoutProber_IsAbsentAnd404(t *testing.T) {
	server := newEnrolServer(t, enrolFakes{
		machines:  &fakeMachines{machines: []store.Machine{remoteMachine}},
		enrolment: enrolledSince(time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)),
		enroller:  &fakeEnroller{},
	})
	visitor := signedIn(t, server)
	page := visitor.get("/machines/web-1")

	if strings.Contains(page.Body.String(), "Tester l'accès") {
		t.Fatal("sans sonde, la fiche n'offre pas le bouton")
	}
	if !strings.Contains(page.Body.String(), "jamais sondée") {
		t.Fatalf("sans sonde, le statut le dit :\n%s", page.Body.String())
	}

	response := visitor.post("/machines/web-1/probe", url.Values{csrfFieldName: {visitor.csrfFrom(page)}})
	if response.Code != http.StatusNotFound {
		t.Fatalf("code = %d, attendu 404", response.Code)
	}
}

func TestPostAccess_ChangesTheAddressThenTheEnrolment(t *testing.T) {
	machines := &fakeMachines{machines: []store.Machine{remoteMachine}}
	enroller := &fakeEnroller{}
	server := newEnrolServer(t, enrolFakes{
		machines:  machines,
		enrolment: enrolledSince(time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)),
		enroller:  enroller,
	})
	visitor := signedIn(t, server)
	page := visitor.get("/machines/web-1")
	if !strings.Contains(page.Body.String(), `action="/machines/web-1/access"`) {
		t.Fatalf("la fiche doit porter le formulaire :\n%s", page.Body.String())
	}

	response := visitor.post("/machines/web-1/access", url.Values{
		csrfFieldName:        {visitor.csrfFrom(page)},
		addressFieldName:     {"192.0.2.20"},
		portFieldName:        {"2222"},
		fingerprintFieldName: {goodFingerprint},
	})

	expectRedirect(t, response, "/machines/web-1")
	if machines.machines[0].Address != "192.0.2.20" || machines.machines[0].Port != 2222 {
		t.Fatalf("le store doit porter le nouvel accès : %+v", machines.machines[0])
	}
	if enroller.access == nil || enroller.access.Address != "192.0.2.20" || enroller.fingerprint != goodFingerprint {
		t.Fatalf("l'enrôlement doit relever la clé à la nouvelle adresse : %+v", enroller.access)
	}
}

func TestPostAccess_Refusal_IsShownOnTheMachinePage(t *testing.T) {
	enroller := &fakeEnroller{err: refusal.Refusal{
		Cause:  "la clé d'hôte relevée ne correspond pas à l'empreinte saisie",
		Remedy: "recopier une empreinte affichée par la machine",
	}}
	server := newEnrolServer(t, enrolFakes{
		machines:  &fakeMachines{machines: []store.Machine{remoteMachine}},
		enrolment: enrolledSince(time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)),
		enroller:  enroller,
	})
	visitor := signedIn(t, server)
	csrfToken := visitor.csrfFrom(visitor.get("/machines/web-1"))

	response := visitor.post("/machines/web-1/access", url.Values{
		csrfFieldName:        {csrfToken},
		addressFieldName:     {"192.0.2.20"},
		portFieldName:        {"2222"},
		fingerprintFieldName: {goodFingerprint},
	})

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d, attendu 422", response.Code)
	}
	if !strings.Contains(response.Body.String(), "recopier une empreinte") {
		t.Fatalf("le refus doit être affiché :\n%s", response.Body.String())
	}
}

func TestPostAccess_MalformedFingerprint_Is400(t *testing.T) {
	machines := &fakeMachines{machines: []store.Machine{remoteMachine}}
	server := newEnrolServer(t, enrolFakes{
		machines:  machines,
		enrolment: enrolledSince(time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)),
		enroller:  &fakeEnroller{},
	})
	visitor := signedIn(t, server)
	csrfToken := visitor.csrfFrom(visitor.get("/machines/web-1"))

	response := visitor.post("/machines/web-1/access", url.Values{
		csrfFieldName:        {csrfToken},
		addressFieldName:     {"192.0.2.20"},
		portFieldName:        {"2222"},
		fingerprintFieldName: {"SHA1:autre-chose"},
	})

	if response.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, attendu 400", response.Code)
	}
	if machines.machines[0].Address != remoteMachine.Address {
		t.Fatal("rien ne doit changer avant que l'empreinte soit lisible")
	}
}

func TestInfrastructure_ShowsTheStatusAndOffersToEnrol(t *testing.T) {
	actions := newFakeActions()
	server := newEnrolServer(t, enrolFakes{
		machines:  &fakeMachines{machines: []store.Machine{remoteMachine}},
		actions:   actions,
		enrolment: enrolledSince(time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)),
		enroller:  &fakeEnroller{},
		prober: &fakeProber{health: MachineHealth{
			ProbedAt: time.Now().Add(-2 * time.Minute), ProbeState: probeReachable,
		}},
	})

	body := signedIn(t, server).get("/").Body.String()

	for _, expected := range []string{"Enrôler une machine", `href="/machines/new"`, "joignable, vue il y a 2 min"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("l'Infrastructure ne montre pas %q :\n%s", expected, body)
		}
	}
	if strings.Contains(body, "en ligne") {
		t.Fatal("l'interface ne dit jamais « en ligne »")
	}
}

// Sans l'enrôlement branché, l'interface est celle du squelette : les routes
// existent, elles ne servent rien.
func TestEnrolmentPages_WithoutEnroller_Are404(t *testing.T) {
	server := newEnrolServer(t, enrolFakes{machines: &fakeMachines{machines: []store.Machine{remoteMachine}}})
	visitor := signedIn(t, server)

	if response := visitor.get("/machines/new"); response.Code != http.StatusNotFound {
		t.Fatalf("GET /machines/new : code = %d, attendu 404", response.Code)
	}
	for _, path := range []string{"/machines", "/machines/web-1/confirm", "/machines/web-1/access"} {
		if response := visitor.post(path, url.Values{}); response.Code != http.StatusNotFound {
			t.Fatalf("POST %s : code = %d, attendu 404", path, response.Code)
		}
	}
	if strings.Contains(visitor.get("/").Body.String(), "Enrôler une machine") {
		t.Fatal("sans enrôlement branché, l'Infrastructure ne le propose pas")
	}
}

func TestMachineStatus_SaysWhatWasSeenAndWhen(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	oneHourAgo := now.Add(-time.Hour)
	running := []store.Action{{State: store.StateApplied}, {State: store.StateRunning}}
	prepared := []store.Action{{State: store.StatePrepared}}
	concluded := []store.Action{{State: store.StateApplied}}

	cases := []struct {
		name     string
		enrolled bool
		health   MachineHealth
		recent   []store.Action
		state    string
		label    string
		note     string
	}{
		{
			name:   "non enrôlée prime sur tout le reste",
			health: MachineHealth{ProbedAt: now, ProbeState: probeReachable},
			recent: running,
			state:  statusNotEnrolled,
			label:  "non enrôlée",
		},
		{
			name:     "une action qui tourne passe devant le sondage",
			enrolled: true,
			health:   MachineHealth{ProbedAt: oneHourAgo, ProbeState: probeSSHFailed},
			recent:   running,
			state:    statusRunning,
			label:    "action en cours",
		},
		{
			name:     "une action préparée compte comme en cours",
			enrolled: true,
			recent:   prepared,
			state:    statusRunning,
			label:    "action en cours",
		},
		{
			name:     "un sondage réussi et frais dit la joignabilité et son âge",
			enrolled: true,
			health:   MachineHealth{ProbedAt: now.Add(-3 * time.Minute), ProbeState: probeReachable},
			recent:   concluded,
			state:    statusReachable,
			label:    "joignable, vue il y a 3 min",
		},
		{
			name:     "un sondage réussi mais vieux ne dit plus qu'une date",
			enrolled: true,
			health:   MachineHealth{ProbedAt: oneHourAgo, ProbeState: probeReachable},
			state:    statusStale,
			label:    "dernière remontée le " + formatMoment(oneHourAgo),
		},
		{
			name:     "SSH en échec porte sa date, la note vit à côté du badge",
			enrolled: true,
			health:   MachineHealth{ProbedAt: oneHourAgo, ProbeState: probeSSHFailed, ProbeNote: "connexion refusée"},
			state:    statusSSHFailed,
			label:    "SSH en échec depuis le " + formatMoment(oneHourAgo),
			note:     "connexion refusée",
		},
		{
			name:     "le lanceur en échec se distingue de SSH",
			enrolled: true,
			health:   MachineHealth{ProbedAt: oneHourAgo, ProbeState: probeLauncherFailed},
			state:    statusLauncherFailed,
			label:    "lanceur en échec depuis le " + formatMoment(oneHourAgo),
		},
		{
			name:     "jamais sondée tant qu'aucun sondage n'a eu lieu",
			enrolled: true,
			state:    statusNeverProbed,
			label:    "jamais sondée",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			status := newMachineStatus(testCase.enrolled, testCase.health, testCase.recent, now)

			if status.State != testCase.state || status.Label != testCase.label || status.Note != testCase.note {
				t.Fatalf("statut = %+v, attendu %q / %q / %q", status, testCase.state, testCase.label, testCase.note)
			}
			if strings.Contains(status.Label, "en ligne") {
				t.Fatal("le statut d'une machine ne dit jamais « en ligne »")
			}
		})
	}
}

// Les compteurs de l'Infrastructure disent ce qui a été constaté : une machine
// dont le dernier sondage a vieilli, ou qui n'a jamais été sondée, n'entre dans
// aucun d'eux.
func TestMachineCounters_CountEachStateAndLeaveOutWhatIsUnknown(t *testing.T) {
	rows := []machineRow{
		{Status: machineStatus{State: statusReachable}},
		{Status: machineStatus{State: statusReachable}},
		{Status: machineStatus{State: statusRunning}},
		{Status: machineStatus{State: statusSSHFailed}},
		{Status: machineStatus{State: statusLauncherFailed}},
		{Status: machineStatus{State: statusNotEnrolled}},
		{Status: machineStatus{State: statusStale}},
		{Status: machineStatus{State: statusNeverProbed}},
	}

	counters := newMachineCounters(rows)

	expected := map[string]int{
		counterReachable:   2,
		counterRunning:     1,
		counterFailed:      2,
		counterNotEnrolled: 1,
	}
	if len(counters) != len(expected) {
		t.Fatalf("%d compteurs, attendu %d", len(counters), len(expected))
	}
	for _, counter := range counters {
		if counter.Count != expected[counter.Kind] {
			t.Fatalf("compteur %s = %d, attendu %d", counter.Kind, counter.Count, expected[counter.Kind])
		}
	}
}

func TestIdentifierOf_ReducesTheNameToASlug(t *testing.T) {
	cases := map[string]string{
		"Web 1":         "web-1",
		"Serveur Été":   "serveur-ete",
		"  web--1  ":    "web-1",
		"MACHINE_À_MOI": "machine-a-moi",
		"!!!":           "",
	}

	for name, expected := range cases {
		if got := identifierOf(name); got != expected {
			t.Fatalf("identifierOf(%q) = %q, attendu %q", name, got, expected)
		}
	}
}

func TestMachineScopedActions_LeaveOutEnroler_ItIsPlayedByTheCommand(t *testing.T) {
	enrol := catalog.Definition{Kind: catalog.KindEnroler, Label: "Enrôler", Scope: catalog.ScopeMachine, Place: catalog.PlaceTarget}
	server := &Server{catalog: &fakeCatalog{definitions: []catalog.Definition{diagnose, enrol}}}

	available := server.machineScopedActions()

	if len(available) != 1 || available[0].Kind != string(catalog.KindDiagnostiquer) {
		t.Fatalf("actions disponibles = %+v : Enrôler se joue par la commande collée, pas depuis la fiche", available)
	}
}

// Dans le tableau, l'enrôlement est une clé et son title ; la phrase entière
// reste sur la fiche de la machine.
func TestInfrastructure_TheEnrolmentColumn_IsAMarkWithItsSentence(t *testing.T) {
	since := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	server := newEnrolServer(t, enrolFakes{
		machines:  &fakeMachines{machines: []store.Machine{remoteMachine}},
		enrolment: enrolledSince(since),
		enroller:  &fakeEnroller{},
	})

	body := signedIn(t, server).get("/").Body.String()

	if !strings.Contains(body, `<span class="mark ok" title="enrôlée depuis le `+formatMoment(since)+`">`) {
		t.Fatalf("la colonne porte une clé verte et sa phrase en title :\n%s", body)
	}
}

func TestInfrastructure_NotEnrolled_TheMarkStaysGreyAndSaysTheGesture(t *testing.T) {
	server := newEnrolServer(t, enrolFakes{
		machines: &fakeMachines{machines: []store.Machine{remoteMachine}},
		enroller: &fakeEnroller{},
	})

	body := signedIn(t, server).get("/").Body.String()

	if !strings.Contains(body, `<span class="mark" title="non enrôlée : jouer la commande`) {
		t.Fatalf("la clé reste grise et dit le geste :\n%s", body)
	}
}
