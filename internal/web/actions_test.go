package web

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/auth"
	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/runner"
	"github.com/ldesfontaine/opencloud/internal/store"
)

var (
	diagnose = catalog.Definition{
		Kind:       catalog.KindDiagnostiquer,
		Label:      "Diagnostiquer",
		Summary:    "Lecture seule : services, disque, horloge, ports.",
		Scope:      catalog.ScopeMachine,
		Place:      catalog.PlaceTarget,
		Reversible: true,
		Timeout:    10 * time.Minute,
	}
	restart = catalog.Definition{
		Kind:       catalog.Kind("redemarrer"),
		Label:      "Redémarrer",
		Summary:    "Redémarre la machine.",
		Scope:      catalog.ScopeMachine,
		Place:      catalog.PlaceTarget,
		Reversible: true,
		Interrupts: true,
		Timeout:    10 * time.Minute,
	}
	localMachine = store.Machine{
		ID: store.LocalMachineID, Name: "machine openCloud",
		Address: "127.0.0.1", Port: 22, Account: "opencloud",
		CreatedAt: time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC),
	}
)

type fakeMachines struct {
	machines []store.Machine
}

func (f *fakeMachines) Machines(context.Context) ([]store.Machine, error) {
	return f.machines, nil
}

func (f *fakeMachines) Machine(_ context.Context, id string) (store.Machine, error) {
	for _, machine := range f.machines {
		if machine.ID == id {
			return machine, nil
		}
	}
	return store.Machine{}, store.ErrNotFound
}

type fakeEnrolment struct {
	status EnrolmentStatus
}

func (f *fakeEnrolment) Status(context.Context, string) (EnrolmentStatus, error) {
	return f.status, nil
}

type fakeCatalog struct {
	definitions []catalog.Definition
	files       map[catalog.Kind][]catalog.File
	err         error
}

func (c *fakeCatalog) Definitions() []catalog.Definition { return c.definitions }

func (c *fakeCatalog) Lookup(kind catalog.Kind) (catalog.Definition, bool) {
	for _, definition := range c.definitions {
		if definition.Kind == kind {
			return definition, true
		}
	}
	return catalog.Definition{}, false
}

func (c *fakeCatalog) Prepare(kind catalog.Kind, params map[string]string) (catalog.Prepared, error) {
	if c.err != nil {
		return catalog.Prepared{}, c.err
	}
	definition, found := c.Lookup(kind)
	if !found {
		return catalog.Prepared{}, store.ErrNotFound
	}
	return catalog.Prepared{Definition: definition, Params: params, Files: c.files[kind]}, nil
}

type enqueued struct {
	machineID string
	kind      catalog.Kind
	params    map[string]string
}

type fakeActions struct {
	mu       sync.Mutex
	actions  map[string]store.Action
	lines    map[string][]store.ActionLine
	events   chan runner.Event
	calls    []enqueued
	enqueued store.Action
	err      error
}

func newFakeActions() *fakeActions {
	return &fakeActions{
		actions: map[string]store.Action{},
		lines:   map[string][]store.ActionLine{},
		events:  make(chan runner.Event, 8),
	}
}

func (f *fakeActions) Enqueue(_ context.Context, machineID string, kind catalog.Kind, params map[string]string) (store.Action, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, enqueued{machineID: machineID, kind: kind, params: params})
	if f.err != nil {
		return store.Action{}, f.err
	}
	return f.enqueued, nil
}

func (f *fakeActions) Subscribe(string) (<-chan runner.Event, func()) {
	return f.events, func() {}
}

func (f *fakeActions) Action(_ context.Context, id string) (store.Action, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	action, found := f.actions[id]
	if !found {
		return store.Action{}, store.ErrNotFound
	}
	return action, nil
}

func (f *fakeActions) ActionsForMachine(_ context.Context, machineID string, limit int) ([]store.Action, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var found []store.Action
	for _, action := range f.actions {
		if action.MachineID == machineID && len(found) < limit {
			found = append(found, action)
		}
	}
	return found, nil
}

func (f *fakeActions) Lines(_ context.Context, actionID string, afterSeq int64) ([]store.ActionLine, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var kept []store.ActionLine
	for _, line := range f.lines[actionID] {
		if line.Seq > afterSeq {
			kept = append(kept, line)
		}
	}
	return kept, nil
}

// newActionsServer monte l'interface complète : machines, catalogue, runner.
func newActionsServer(t *testing.T, machines *fakeMachines, actions *fakeActions, actionCatalog *fakeCatalog, enrolment Enrolment) *Server {
	t.Helper()
	return newServerWith(t, Dependencies{
		Auth:      newTestAuth(t),
		Machines:  machines,
		Enrolment: enrolment,
		Actions:   actions,
		Catalog:   actionCatalog,
	})
}

// signedIn ouvre une session utilisable : le mot de passe par défaut doit être
// changé avant d'atteindre quoi que ce soit d'autre.
func signedIn(t *testing.T, server *Server) *browser {
	t.Helper()
	visitor := newBrowserOf(t, server)
	visitor.login(auth.DefaultUsername, auth.DefaultPassword)
	visitor.changePassword(auth.DefaultPassword, "brand-new-password", "brand-new-password")
	return visitor
}

func defaultCatalog() *fakeCatalog {
	return &fakeCatalog{definitions: []catalog.Definition{diagnose, restart}}
}

func TestInfrastructure_ListsEachMachineWithItsEnrolmentAndLastAction(t *testing.T) {
	actions := newFakeActions()
	actions.actions["diagnostiquer-1"] = store.Action{
		ID: "diagnostiquer-1", MachineID: store.LocalMachineID,
		Kind: string(catalog.KindDiagnostiquer), State: store.StateApplied,
		CreatedAt: time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC),
	}
	enrolled := &fakeEnrolment{status: EnrolmentStatus{Enrolled: true, Since: time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)}}
	server := newActionsServer(t, &fakeMachines{machines: []store.Machine{localMachine}}, actions, defaultCatalog(), enrolled)

	body := signedIn(t, server).get("/").Body.String()

	for _, expected := range []string{"machine openCloud", "127.0.0.1", "enrôlée depuis le", "Diagnostiquer", "Appliquée"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("la page Infrastructure ne montre pas %q :\n%s", expected, body)
		}
	}
}

func TestInfrastructure_MachineNotEnrolled_NamesTheCommandToPlay(t *testing.T) {
	server := newActionsServer(t, &fakeMachines{machines: []store.Machine{localMachine}},
		newFakeActions(), defaultCatalog(), nil)

	body := signedIn(t, server).get("/").Body.String()

	if !strings.Contains(body, "non enrôlée") || !strings.Contains(body, "sudo opencloud enroll-local") {
		t.Fatalf("la fiche doit dire le geste qui lève le blocage :\n%s", body)
	}
}

func TestMachine_ShowsTheAvailableActionsAndTheHistory(t *testing.T) {
	actions := newFakeActions()
	actions.actions["diagnostiquer-1"] = store.Action{
		ID: "diagnostiquer-1", MachineID: store.LocalMachineID,
		Kind: string(catalog.KindDiagnostiquer), State: store.StateFailed,
		Result: "trois écarts", CreatedAt: time.Now().UTC(),
	}
	server := newActionsServer(t, &fakeMachines{machines: []store.Machine{localMachine}},
		actions, defaultCatalog(), nil)

	body := signedIn(t, server).get("/machines/local").Body.String()

	for _, expected := range []string{"Diagnostiquer", "Redémarrer", "Échouée", "trois écarts", "/machines/local/actions/diagnostiquer"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("la fiche machine ne montre pas %q :\n%s", expected, body)
		}
	}
}

func TestMachine_Unknown_Is404(t *testing.T) {
	server := newActionsServer(t, &fakeMachines{}, newFakeActions(), defaultCatalog(), nil)

	response := signedIn(t, server).get("/machines/absente")

	if response.Code != http.StatusNotFound {
		t.Fatalf("code = %d, attendu 404", response.Code)
	}
}

// Sans runner ni catalogue branchés, l'interface est celle du squelette : les
// routes existent, elles ne servent rien.
func TestMachineAndActionPages_WithoutRunner_Are404(t *testing.T) {
	visitor := signedIn(t, newServerWith(t, Dependencies{Auth: newTestAuth(t)}))

	for _, path := range []string{"/machines/local", "/machines/local/actions/diagnostiquer",
		"/actions/diagnostiquer-1", "/actions/diagnostiquer-1/stream"} {
		if response := visitor.get(path); response.Code != http.StatusNotFound {
			t.Fatalf("%s : code = %d, attendu 404", path, response.Code)
		}
	}
}

func TestActionForm_ShowsTheFourAttributesAndWhatWillBeWritten(t *testing.T) {
	actionCatalog := defaultCatalog()
	actionCatalog.files = map[catalog.Kind][]catalog.File{
		catalog.KindDiagnostiquer: {{Path: "traefik/site.yml", Content: []byte("http: {}"), Mode: 0o644}},
	}
	server := newActionsServer(t, &fakeMachines{machines: []store.Machine{localMachine}},
		newFakeActions(), actionCatalog, nil)

	body := signedIn(t, server).get("/machines/local/actions/diagnostiquer").Body.String()

	for _, expected := range []string{"Portée", "la machine", "Lieu", "sur la machine cible",
		"Réversibilité", "réversible", "Interruption", "traefik/site.yml", "0644", "8 octets"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("l'écran « avant » ne montre pas %q :\n%s", expected, body)
		}
	}
	if !strings.Contains(body, "viendra plus tard") {
		t.Fatal("l'écran doit dire que la comparaison des fichiers n'existe pas encore")
	}
}

func TestActionForm_NoFile_SaysSo(t *testing.T) {
	server := newActionsServer(t, &fakeMachines{machines: []store.Machine{localMachine}},
		newFakeActions(), defaultCatalog(), nil)

	body := signedIn(t, server).get("/machines/local/actions/diagnostiquer").Body.String()

	if !strings.Contains(body, "Aucun fichier à poser") {
		t.Fatalf("l'écran doit le dire :\n%s", body)
	}
}

func TestActionForm_UnknownAction_Is404(t *testing.T) {
	server := newActionsServer(t, &fakeMachines{machines: []store.Machine{localMachine}},
		newFakeActions(), defaultCatalog(), nil)

	response := signedIn(t, server).get("/machines/local/actions/inventee")

	if response.Code != http.StatusNotFound {
		t.Fatalf("code = %d, attendu 404", response.Code)
	}
}

func TestPostAction_Reversible_EnqueuesAndGoesToTheAction(t *testing.T) {
	actions := newFakeActions()
	actions.enqueued = store.Action{ID: "diagnostiquer-260906-abcd", MachineID: store.LocalMachineID}
	server := newActionsServer(t, &fakeMachines{machines: []store.Machine{localMachine}},
		actions, defaultCatalog(), nil)
	visitor := signedIn(t, server)
	csrfToken := visitor.csrfFrom(visitor.get("/machines/local/actions/diagnostiquer"))

	response := visitor.post("/machines/local/actions/diagnostiquer", url.Values{csrfFieldName: {csrfToken}})

	expectRedirect(t, response, "/actions/diagnostiquer-260906-abcd")
	if len(actions.calls) != 1 || actions.calls[0].kind != catalog.KindDiagnostiquer {
		t.Fatalf("dépôts = %+v", actions.calls)
	}
}

// Une action qui coupe un service ne part pas sans la case cochée.
func TestPostAction_Interrupting_WithoutConfirmation_Is400(t *testing.T) {
	actions := newFakeActions()
	server := newActionsServer(t, &fakeMachines{machines: []store.Machine{localMachine}},
		actions, defaultCatalog(), nil)
	visitor := signedIn(t, server)
	csrfToken := visitor.csrfFrom(visitor.get("/machines/local/actions/redemarrer"))

	response := visitor.post("/machines/local/actions/redemarrer", url.Values{csrfFieldName: {csrfToken}})

	if response.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, attendu 400", response.Code)
	}
	if !strings.Contains(response.Body.String(), messageConfirmationRequired.Cause) {
		t.Fatalf("le refus doit être nommé :\n%s", response.Body.String())
	}
	if len(actions.calls) != 0 {
		t.Fatal("rien ne doit être déposé sans confirmation")
	}
}

func TestPostAction_Interrupting_Confirmed_IsEnqueued(t *testing.T) {
	actions := newFakeActions()
	actions.enqueued = store.Action{ID: "redemarrer-260906-abcd", MachineID: store.LocalMachineID}
	server := newActionsServer(t, &fakeMachines{machines: []store.Machine{localMachine}},
		actions, defaultCatalog(), nil)
	visitor := signedIn(t, server)
	csrfToken := visitor.csrfFrom(visitor.get("/machines/local/actions/redemarrer"))

	response := visitor.post("/machines/local/actions/redemarrer",
		url.Values{csrfFieldName: {csrfToken}, confirmFieldName: {"oui"}})

	expectRedirect(t, response, "/actions/redemarrer-260906-abcd")
}

func TestPostAction_Refusal_IsShownOnTheBeforeScreen(t *testing.T) {
	actions := newFakeActions()
	actions.err = refusal.Refusal{Cause: "le domaine ne résout pas", Remedy: "créer l'enregistrement DNS d'abord"}
	server := newActionsServer(t, &fakeMachines{machines: []store.Machine{localMachine}},
		actions, defaultCatalog(), nil)
	visitor := signedIn(t, server)
	csrfToken := visitor.csrfFrom(visitor.get("/machines/local/actions/diagnostiquer"))

	response := visitor.post("/machines/local/actions/diagnostiquer", url.Values{csrfFieldName: {csrfToken}})

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d, attendu 422", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "le domaine ne résout pas") || !strings.Contains(body, "enregistrement DNS d") {
		t.Fatalf("le refus doit dire la cause et le geste :\n%s", body)
	}
}

func TestPostAction_WithoutCSRF_IsForbidden(t *testing.T) {
	actions := newFakeActions()
	server := newActionsServer(t, &fakeMachines{machines: []store.Machine{localMachine}},
		actions, defaultCatalog(), nil)
	visitor := signedIn(t, server)

	response := visitor.post("/machines/local/actions/diagnostiquer", url.Values{})

	if response.Code != http.StatusForbidden {
		t.Fatalf("code = %d, attendu 403", response.Code)
	}
	if len(actions.calls) != 0 {
		t.Fatal("rien ne doit être déposé sans jeton")
	}
}

func TestAction_ShowsTheStoredOutputAndTheAttributes(t *testing.T) {
	actions := newFakeActions()
	code := 0
	actions.actions["diagnostiquer-1"] = store.Action{
		ID: "diagnostiquer-1", MachineID: store.LocalMachineID,
		Kind: string(catalog.KindDiagnostiquer), State: store.StateApplied,
		Result: "rien à signaler", ExitCode: &code, CreatedAt: time.Now().UTC(),
	}
	actions.lines["diagnostiquer-1"] = []store.ActionLine{
		{Seq: 1, At: time.Now().UTC(), Text: "étape: disque"},
		{Seq: 2, At: time.Now().UTC(), Text: "résultat: rien à signaler"},
	}
	server := newActionsServer(t, &fakeMachines{machines: []store.Machine{localMachine}},
		actions, defaultCatalog(), nil)

	body := signedIn(t, server).get("/actions/diagnostiquer-1").Body.String()

	for _, expected := range []string{"Diagnostiquer", "Appliquée", "étape: disque", "rien à signaler", "0 — fait", "Portée"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("la page action ne montre pas %q :\n%s", expected, body)
		}
	}
	if strings.Contains(body, "actions.js") {
		t.Fatal("une action conclue n'a rien à suivre en direct")
	}
}

func TestAction_Running_LoadsTheLiveScript(t *testing.T) {
	actions := newFakeActions()
	actions.actions["diagnostiquer-1"] = store.Action{
		ID: "diagnostiquer-1", MachineID: store.LocalMachineID,
		Kind: string(catalog.KindDiagnostiquer), State: store.StateRunning, CreatedAt: time.Now().UTC(),
	}
	actions.lines["diagnostiquer-1"] = []store.ActionLine{{Seq: 1, At: time.Now().UTC(), Text: "étape: disque"}}
	server := newActionsServer(t, &fakeMachines{machines: []store.Machine{localMachine}},
		actions, defaultCatalog(), nil)

	body := signedIn(t, server).get("/actions/diagnostiquer-1").Body.String()

	if !strings.Contains(body, `<script src="/static/actions.js" defer>`) {
		t.Fatalf("la page doit charger le direct :\n%s", body)
	}
	if !strings.Contains(body, "/actions/diagnostiquer-1/stream?after=1") {
		t.Fatal("le direct doit reprendre après la dernière ligne affichée")
	}
}

func TestAction_Unknown_Is404(t *testing.T) {
	server := newActionsServer(t, &fakeMachines{machines: []store.Machine{localMachine}},
		newFakeActions(), defaultCatalog(), nil)

	response := signedIn(t, server).get("/actions/absente")

	if response.Code != http.StatusNotFound {
		t.Fatalf("code = %d, attendu 404", response.Code)
	}
}

func TestStream_ConcludedAction_ReplaysThenEnds(t *testing.T) {
	actions := newFakeActions()
	actions.actions["diagnostiquer-1"] = store.Action{
		ID: "diagnostiquer-1", MachineID: store.LocalMachineID,
		Kind: string(catalog.KindDiagnostiquer), State: store.StateApplied, Result: "rien à signaler",
	}
	actions.lines["diagnostiquer-1"] = []store.ActionLine{
		{Seq: 1, At: time.Now().UTC(), Text: "étape: disque"},
		{Seq: 2, At: time.Now().UTC(), Text: "résultat: rien à signaler"},
	}
	server := newActionsServer(t, &fakeMachines{machines: []store.Machine{localMachine}},
		actions, defaultCatalog(), nil)

	response := signedIn(t, server).get("/actions/diagnostiquer-1/stream")

	if response.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("type = %q", response.Header().Get("Content-Type"))
	}
	body := response.Body.String()
	for _, expected := range []string{"id: 1", "event: line", "étape: disque", "event: done", `"state":"applied"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("le flux ne porte pas %q :\n%s", expected, body)
		}
	}
}

func TestStream_After_SkipsWhatThePageAlreadyShows(t *testing.T) {
	actions := newFakeActions()
	actions.actions["diagnostiquer-1"] = store.Action{
		ID: "diagnostiquer-1", MachineID: store.LocalMachineID, State: store.StateApplied,
	}
	actions.lines["diagnostiquer-1"] = []store.ActionLine{
		{Seq: 1, At: time.Now().UTC(), Text: "étape: disque"},
		{Seq: 2, At: time.Now().UTC(), Text: "étape: horloge"},
	}
	server := newActionsServer(t, &fakeMachines{machines: []store.Machine{localMachine}},
		actions, defaultCatalog(), nil)

	body := signedIn(t, server).get("/actions/diagnostiquer-1/stream?after=1").Body.String()

	if strings.Contains(body, "disque") || !strings.Contains(body, "horloge") {
		t.Fatalf("le rejeu doit repartir après la ligne 1 :\n%s", body)
	}
}

func TestStream_Anonymous_RedirectsToLogin(t *testing.T) {
	expectRedirect(t, newBrowser(t).get("/actions/diagnostiquer-1/stream"), "/login")
}

// Le direct de bout en bout : rejeu de ce qui est stocké, puis les lignes qui
// arrivent, puis la fin — sur une vraie connexion HTTP.
func TestStream_RunningAction_ReplaysThenStreamsLiveThenDone(t *testing.T) {
	actions := newFakeActions()
	actions.actions["diagnostiquer-1"] = store.Action{
		ID: "diagnostiquer-1", MachineID: store.LocalMachineID, State: store.StateRunning,
	}
	actions.lines["diagnostiquer-1"] = []store.ActionLine{{Seq: 1, At: time.Now().UTC(), Text: "étape: disque"}}
	server := newActionsServer(t, &fakeMachines{machines: []store.Machine{localMachine}},
		actions, defaultCatalog(), nil)
	visitor := signedIn(t, server)

	live := httptest.NewServer(server.Handler())
	defer live.Close()

	request, err := http.NewRequest(http.MethodGet, live.URL+"/actions/diagnostiquer-1/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range visitor.cookies {
		request.AddCookie(cookie)
	}
	response, err := live.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	reader := bufio.NewReader(response.Body)
	replayed := readUntil(t, reader, "étape: disque")
	if !strings.Contains(replayed, "id: 1") {
		t.Fatalf("le rejeu doit numéroter les lignes :\n%s", replayed)
	}

	actions.events <- runner.Event{Seq: 2, At: time.Now().UTC(), Text: "étape: horloge"}
	if live := readUntil(t, reader, "étape: horloge"); !strings.Contains(live, "id: 2") {
		t.Fatalf("la ligne en direct doit porter son seq :\n%s", live)
	}

	actions.events <- runner.Event{Done: true, State: store.StateApplied, Result: "rien à signaler"}
	if end := readUntil(t, reader, "rien à signaler"); !strings.Contains(end, "event: done") {
		t.Fatalf("la fin doit porter l'état et le résultat :\n%s", end)
	}
}

// readUntil lit le flux jusqu'à la ligne attendue et rend tout ce qui a été lu.
func readUntil(t *testing.T, reader *bufio.Reader, expected string) string {
	t.Helper()
	var read strings.Builder
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		read.WriteString(line)
		if strings.Contains(line, expected) {
			return read.String()
		}
		if err != nil {
			break
		}
	}
	t.Fatalf("%q n'est jamais arrivé dans le flux :\n%s", expected, read.String())
	return ""
}

// La page d'une action Diagnostiquer conclue montre un rapport, pas un
// journal : la sortie brute reste là, repliée dessous.
func TestAction_ConcludedDiagnostiquer_ShowsAReportAndFoldsTheRawOutput(t *testing.T) {
	actions := newFakeActions()
	code := 0
	actions.actions["diagnostiquer-1"] = store.Action{
		ID: "diagnostiquer-1", MachineID: store.LocalMachineID,
		Kind: string(catalog.KindDiagnostiquer), State: store.StateApplied,
		Result: "inchangé", ExitCode: &code, CreatedAt: time.Now().UTC(),
	}
	actions.lines["diagnostiquer-1"] = storedLines(enrolledDiagnosticOutput)
	server := newActionsServer(t, &fakeMachines{machines: []store.Machine{localMachine}},
		actions, defaultCatalog(), nil)

	body := signedIn(t, server).get("/actions/diagnostiquer-1").Body.String()

	for _, expected := range []string{"Identité", "Horloge synchronisée", "oui",
		"Unités en échec", "aucune", "Port 80", "tenu", "Propriétaire du lanceur"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("le rapport ne montre pas %q :\n%s", expected, body)
		}
	}
	if !strings.Contains(body, "port publié sur toutes les interfaces") {
		t.Fatal("les avertissements se lisent en tête du rapport")
	}
	if !strings.Contains(body, "<summary>") || !strings.Contains(body, "Sortie brute") {
		t.Fatalf("la sortie brute reste disponible, repliée :\n%s", body)
	}
	if !strings.Contains(body, "info: horloge_synchronisee=yes") {
		t.Fatal("la sortie brute garde les lignes telles que le script les a écrites")
	}
}

// Une action qui tourne n'a pas de rapport : le journal arrive au fil de
// l'eau, le rapport se construit à la fin.
func TestAction_RunningDiagnostiquer_KeepsTheLiveJournal(t *testing.T) {
	actions := newFakeActions()
	actions.actions["diagnostiquer-1"] = store.Action{
		ID: "diagnostiquer-1", MachineID: store.LocalMachineID,
		Kind: string(catalog.KindDiagnostiquer), State: store.StateRunning, CreatedAt: time.Now().UTC(),
	}
	actions.lines["diagnostiquer-1"] = storedLines(enrolledDiagnosticOutput)
	server := newActionsServer(t, &fakeMachines{machines: []store.Machine{localMachine}},
		actions, defaultCatalog(), nil)

	body := signedIn(t, server).get("/actions/diagnostiquer-1").Body.String()

	if strings.Contains(body, "Sortie brute") {
		t.Fatalf("pas de rapport tant que l'action tourne :\n%s", body)
	}
	if !strings.Contains(body, `id="action-output"`) {
		t.Fatal("le direct garde sa sortie")
	}
}

// L'écran « avant » dit ce que l'action va lire, en plus des quatre attributs.
func TestActionForm_Diagnostiquer_SaysWhatWillBeRead(t *testing.T) {
	server := newActionsServer(t, &fakeMachines{machines: []store.Machine{localMachine}},
		newFakeActions(), defaultCatalog(), nil)

	body := signedIn(t, server).get("/machines/local/actions/diagnostiquer").Body.String()

	for _, expected := range []string{"de la machine sans rien changer",
		"Ce qui va être lu", "les unités systemd en échec", "les ports 80 et 443", "le lanceur"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("l'écran « avant » ne dit pas %q :\n%s", expected, body)
		}
	}
}

// La carte de l'action, sur la fiche machine, dit la même ligne.
func TestMachine_TheDiagnoseCard_SaysWhatTheActionDoes(t *testing.T) {
	server := newActionsServer(t, &fakeMachines{machines: []store.Machine{localMachine}},
		newFakeActions(), defaultCatalog(), nil)

	body := signedIn(t, server).get("/machines/local").Body.String()

	if !strings.Contains(body, "de la machine sans rien changer") {
		t.Fatalf("la carte Diagnostiquer dit ce que l'action fait :\n%s", body)
	}
}
