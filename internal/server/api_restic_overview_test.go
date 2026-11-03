package server

import (
	"bytes"
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/backup"
	"github.com/ldesfontaine/opencloud/internal/restic"
	"github.com/ldesfontaine/opencloud/internal/server/agentapi"
)

// Ce que Proxmox sauvegarde de lui-même se voit dès la déclaration de
// l'hôte, sans aucun plan d'openCloud : la tâche vzdump qui couvre chaque
// invité, son stockage, sa prochaine exécution et sa rétention, telles que
// /cluster/backup les dit. L'opérateur et un jeton de lecture lisent le même
// aperçu.
func TestOverview_ShowsWhatProxmoxBacksUpWithoutAnyPlan(t *testing.T) {
	server, api, _ := proxmoxBench(t)
	if got := putProxmox(server, pveID, api.request(pveTokenSecret)); got.Code != http.StatusOK {
		t.Fatalf("declare: %d %s", got.Code, got.Body.String())
	}
	bearer := server.issueAPIToken(t, "mcp-maison")
	byOperator := callAPI(server.Server, http.MethodGet, "/api/backups/overview", nil)
	byToken := callBearer(server.Server, "/api/backups/overview", bearer)
	if byOperator.Code != http.StatusOK || byToken.Code != http.StatusOK || byOperator.Body.String() != byToken.Body.String() {
		t.Fatalf("operator %d, token %d:\n%s\n%s", byOperator.Code, byToken.Code, byOperator.Body.String(), byToken.Body.String())
	}
	var overview backupOverviewJSON
	decodeAPI(t, byToken, &overview)
	sources := map[string]overviewSourceJSON{}
	for _, source := range overview.Sources {
		sources[source.Key] = source
	}
	retention := overviewRetentionJSON{Known: true, Applied: true, KeepDaily: 14, KeepWeekly: 8, KeepMonthly: 3}
	next := time.Unix(1793329200, 0).UTC()
	for _, vmid := range []int{5012, 200} {
		source := sources[backup.GuestSourceKey(pveID, vmid)]
		if len(source.Protections) != 1 {
			t.Fatalf("VM %d : %+v", vmid, source)
		}
		observed := source.Protections[0]
		if observed.Origin != restic.OriginObserved || observed.ID != "nuit-pi" || observed.Storage != "pi-sauvegardes" ||
			observed.Retention != retention || observed.NextAt == nil || !observed.NextAt.Equal(next) || observed.Schedule != "03:00" {
			t.Errorf("VM %d : %+v", vmid, observed)
		}
	}
	// La VM 5012 a une archive sur « local » : sa taille et sa date ; le
	// conteneur 200 n'en a aucune encore.
	withArchive := sources[backup.GuestSourceKey(pveID, 5012)].Protections[0]
	if withArchive.State != restic.ProtectionOK || withArchive.SizeBytes == nil || *withArchive.SizeBytes != 244318208 || withArchive.Copies == nil || *withArchive.Copies != 1 {
		t.Errorf("5012 : %+v", withArchive)
	}
	if never := sources[backup.GuestSourceKey(pveID, 200)].Protections[0]; never.State != restic.ProtectionNever || never.LastAt != nil {
		t.Errorf("200 : %+v", never)
	}
	if database := sources[backup.SourceDatabaseKey]; database.Kind != "database" || len(database.Protections) != 0 {
		t.Errorf("la base n'a aucun plan : %+v", database)
	}
	storages := 0
	for _, target := range overview.Targets {
		if target.Kind == restic.TargetKindStorage && target.Name == "local" && target.State == "active" && len(target.Holds) == 1 {
			storages++
		}
	}
	if storages != 1 {
		t.Errorf("le stockage « local » et ce qu'il garde : %+v", overview.Targets)
	}
}

// L'aperçu et l'historique se lisent, rien de plus : un jeton n'y écrit pas,
// et l'ancienne route de l'inventaire n'existe plus.
func TestOverview_IsAReadAndTheInventoryRouteIsGone(t *testing.T) {
	server := newTestServer(t)
	bearer := server.issueAPIToken(t, "mcp-maison")
	for _, path := range []string{"/api/backups/overview", "/api/backups/runs"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			if got := callBearerWith(server.Server, method, path, bearer, ""); got.Code != http.StatusForbidden {
				t.Errorf("token %s %s: %d, want 403", method, path, got.Code)
			}
		}
	}
	if got := callAPI(server.Server, http.MethodGet, "/api/backups/inventory", nil); got.Code != http.StatusNotFound {
		t.Errorf("the inventory route answers: %d", got.Code)
	}
}

// La nuit : une file par source, les sauvegardes d'une source l'une après
// l'autre, un seul passage à la fois. Pendant qu'il court, le même dépôt ne
// reçoit pas une seconde sauvegarde ; la rétention part après, et le passage
// suivant ne rejoue pas la nuit.
func TestRestic_TheNightRunsEachRepositoryOnce(t *testing.T) {
	bench := readyResticBench(t)
	backupID := bench.createServiceBackup(t)
	passed := make(chan struct{})
	go func() {
		bench.restic.RunIfDue(context.Background())
		close(passed)
	}()
	order := nextOrder(t, bench.source)
	if order.Kind != agentwire.KindResticBackup {
		t.Fatalf("order %s", order.Kind)
	}
	busy := callAPI(bench.Server, http.MethodPost, "/api/machines/"+resticSourceID+"/restic-backups/"+backupID+"/actions/run", map[string]any{})
	if busy.Code != http.StatusConflict || errorCode(t, busy) != "restic.backup_running" {
		t.Fatalf("run during the night: %d %s", busy.Code, busy.Body.String())
	}
	// Un second passage pendant le premier ne fait rien.
	bench.restic.RunIfDue(context.Background())
	bench.reportRun(t, agentwire.ActionResult{RunID: order.RunID, State: agentwire.ActionSucceeded, Restic: &agentwire.ResticRunReport{
		Path: agentwire.ResticPathDirect, Reason: agentwire.ResticReachable, SnapshotID: strings.Repeat("d", 64), BytesAdded: 1,
	}})
	retain := nextOrder(t, bench.target)
	if retain.Kind != agentwire.KindResticRetain {
		t.Fatalf("after the night: %s", retain.Kind)
	}
	bench.reportTargetRun(t, agentwire.ActionResult{RunID: retain.RunID, State: agentwire.ActionSucceeded})
	select {
	case <-passed:
	case <-time.After(30 * time.Second):
		t.Fatal("the night pass did not end")
	}
	bench.restic.RunIfDue(context.Background())
	select {
	case event := <-bench.source.Events():
		t.Fatalf("the night ran twice: %s", event.Name)
	default:
	}
}

// Le banc de non-fuite : un agent falsifié glisse un secret dans tout ce
// qu'il peut envoyer hors de la liste blanche (mots de passe, URL du dépôt,
// clé privée) et là où une forme fermée l'attend (une empreinte). Le secret
// n'est ni dans le journal, ni dans l'audit, ni dans les réponses de l'API,
// ni dans la base.
func TestRestic_ASecretSentByAForgedAgentLeavesNoTrace(t *testing.T) {
	const secret = "S3cret-0f-the-rest-password-9d71" // gitleaks:allow -- valeur semée par le test
	bench := readyResticBench(t)
	backupID := bench.createServiceBackup(t)
	order := bench.runNow(t, backupID)

	var journal bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&journal, &slog.HandlerOptions{Level: slog.LevelDebug}))
	component := restic.New(bench.db, bench.machines, logger)
	component.SetClock(func() time.Time { return testNow })
	component.SetSender(bench.actions)
	component.SetInventory(bench.backups)
	component.SetAuditor(bench.journal)
	handler := agentapi.New(agentapi.Options{
		Logger: logger, Machines: bench.machines, Resources: bench.resources, Containers: bench.containers,
		Probes: bench.probes, Updates: bench.updates, Actions: bench.actions, Restic: component,
		ClientAddress: func(*http.Request) string { return "192.0.2.21" }, Clock: func() time.Time { return testNow },
	})
	mux := http.NewServeMux()
	handler.Routes(func(method, pattern string, route http.HandlerFunc) {
		if method != "*" {
			pattern = method + " " + pattern
		}
		mux.HandleFunc(pattern, route)
	})
	forged := []struct {
		session string
		body    string
	}{
		{bench.source.Token, `{"restic":{"source":{"version":"restic 0.18.1","password":"` + secret + `","repository_password":"` + secret +
			`","targets":["` + resticTargetID + `"],"links":[{"target":"` + resticTargetID + `","password":"` + secret + `"}]}}}`},
		{bench.source.Token, `{"restic":{"source":{"links":[{"target":"` + resticTargetID + `","hash":"` + secret + `"}]}}}`},
		{bench.target.Token, `{"restic":{"target":{"state":"running","host":"` + resticTargetHost + `","port":8000,"repo_bytes":1,"disk_total":2,"disk_free":1,` +
			`"private_key":"` + secret + `","htpasswd":"` + secret + `","users":["` + resticSourceID + `"]}}}`},
		{bench.source.Token, `{"actions":{"results":[{"run_id":"` + order.RunID + `","state":"succeeded","restic":{"path":"direct","reason":"reachable",` +
			`"password":"` + secret + `","repository":"rest:https://u:` + secret + `@` + resticTargetHost + `:8000/","bytes_added":5,"duration_seconds":1}}]}}`},
	}
	for _, signal := range forged {
		request := httptest.NewRequest(http.MethodPost, "/agent/signal", strings.NewReader(signal.body))
		request.Header.Set("Authorization", "Bearer "+signal.session)
		request.Header.Set(agentwire.HeaderSignalSections, "1")
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent && recorder.Code != http.StatusOK {
			t.Fatalf("signal: %d %s", recorder.Code, recorder.Body.String())
		}
	}

	places := map[string]string{"journal": journal.String()}
	for _, path := range []string{"/api/backups/overview", "/api/actions", "/api/audit?limit=200", "/api/machines/" + resticSourceID} {
		got := callAPI(bench.Server, http.MethodGet, path, nil)
		places[path] = got.Body.String()
	}
	err := fs.WalkDir(bench.state.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := fs.ReadFile(bench.state.FS(), path)
		places["state:"+path] = string(content)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for place, content := range places {
		if strings.Contains(content, secret) {
			t.Errorf("the secret leaked into %s", place)
		}
	}
	stored, err := bench.db.GetResticBackup(context.Background(), backupID)
	if err != nil || stored.Last.State != restic.StateSucceeded {
		t.Fatalf("the whitelisted fields still pass: %+v %v", stored, err)
	}
}
