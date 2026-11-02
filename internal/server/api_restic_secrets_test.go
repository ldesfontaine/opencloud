package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/verb"
)

// orderTap lit tout ce que le serveur pousse sur le flux d'une machine : le
// texte de chaque événement est gardé, les ordres se lisent un par un.
type orderTap struct {
	mu     sync.Mutex
	events []string
	orders chan agentwire.OrderBody
}

func tapOrders(t *testing.T, session *machine.Session) *orderTap {
	t.Helper()
	tap := &orderTap{orders: make(chan agentwire.OrderBody, 32)}
	ctx := t.Context()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-session.Events():
				tap.record(event.Name + " " + string(event.Data))
				if event.Name != agentwire.EventOrder {
					continue
				}
				signed, err := agentwire.DecodeSignedOrder(event.Data)
				if err != nil {
					continue
				}
				// Le corps signé voyage encodé : on garde aussi son clair.
				tap.record(string(signed.Body))
				var body agentwire.OrderBody
				if json.Unmarshal(signed.Body, &body) == nil {
					tap.orders <- body
				}
			}
		}
	}()
	return tap
}

func (tap *orderTap) record(text string) {
	tap.mu.Lock()
	defer tap.mu.Unlock()
	tap.events = append(tap.events, text)
}

func (tap *orderTap) next(t *testing.T, kind agentwire.Kind) agentwire.OrderBody {
	t.Helper()
	select {
	case order := <-tap.orders:
		if order.Kind != kind {
			t.Fatalf("order %s, want %s", order.Kind, kind)
		}
		return order
	case <-time.After(30 * time.Second):
		t.Fatalf("no %s pushed", kind)
	}
	return agentwire.OrderBody{}
}

// none dit qu'aucun ordre n'arrive pendant un court instant.
func (tap *orderTap) none(t *testing.T, when string) {
	t.Helper()
	select {
	case order := <-tap.orders:
		t.Fatalf("%s: unexpected %s", when, order.Kind)
	case <-time.After(300 * time.Millisecond):
	}
}

func (tap *orderTap) text() string {
	tap.mu.Lock()
	defer tap.mu.Unlock()
	return strings.Join(tap.events, "\n")
}

// dumpDatabase relit chaque table de la base, chaque valeur en texte : ce
// qu'un « sqlite3 .dump » montrerait.
func dumpDatabase(t *testing.T, bench *resticBench) string {
	t.Helper()
	path := filepath.Join(bench.state.Name(), brand.DatabaseFile)
	reader, err := sql.Open("sqlite", "file:"+path+"?"+url.Values{"mode": {"ro"}}.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	tables, err := reader.Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var name string
		if err := tables.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	_ = tables.Close()
	var dump strings.Builder
	for _, name := range names {
		rows, err := reader.Query(`SELECT * FROM "` + name + `"`)
		if err != nil {
			t.Fatal(err)
		}
		columns, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for index := range values {
				pointers[index] = &values[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			for _, value := range values {
				if raw, isBytes := value.([]byte); isBytes {
					value = string(raw)
				}
				fmt.Fprintf(&dump, "%s: %v\n", name, value)
			}
		}
		_ = rows.Close()
	}
	return dump.String()
}

// Le banc de non-fuite du serveur, sur de vrais mots de passe tirés par le
// service : ils ne partent que dans le secret de leurs ordres (les deux à la
// source, le REST seul à la cible pour l'ouvrir, celui du dépôt à la cible
// le temps d'une rétention), et nulle part ailleurs : ni l'API, ni le
// journal, ni la base en clair, ni le flux d'un autre agent.
func TestRestic_TheLinkPasswordsLeaveOnlyInTheirOrders(t *testing.T) {
	bench := readyResticBench(t)
	stranger := e2eConnect(t, bench.testServer, "inconnue", dockerVMID, "observe,restic.produce")
	source, target, third := tapOrders(t, bench.source), tapOrders(t, bench.target), tapOrders(t, stranger)
	bearer := bench.issueAPIToken(t, "mcp-maison")

	got := callAPI(bench.Server, http.MethodPost, "/api/machines/"+resticSourceID+"/restic-backups", map[string]any{
		"name": "nextcloud", "scope": "service", "service_id": bench.serviceID, "target_machine_id": resticTargetID,
	})
	if got.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", got.Code, got.Body.String())
	}
	var created resticBackupCreatedJSON
	decodeAPI(t, got, &created)
	var passwords agentwire.ResticProvisionSecret
	provision := source.next(t, agentwire.KindResticProvision)
	if json.Unmarshal(provision.Secret, &passwords) != nil || passwords.Validate() != nil || len(passwords.RESTPassword) != 43 {
		t.Fatal("the source did not receive two drawn passwords")
	}
	userAdd := target.next(t, agentwire.KindResticUserAdd)
	if !strings.Contains(string(userAdd.Secret), passwords.RESTPassword) || strings.Contains(string(userAdd.Secret), passwords.RepoPassword) {
		t.Fatal("restic.user.add carries the REST password, never the repository password")
	}
	if strings.Contains(target.text(), passwords.RepoPassword) {
		t.Fatal("the target saw the repository password before any retention")
	}

	// La nuit : la sauvegarde, puis la rétention qui seule porte le mot de
	// passe du dépôt à la cible.
	passed := make(chan struct{})
	go func() {
		bench.restic.RunIfDue(context.Background())
		close(passed)
	}()
	backup := source.next(t, agentwire.KindResticBackup)
	if len(backup.Secret) != 0 {
		t.Fatal("restic.backup carries a secret")
	}
	bench.reportRun(t, agentwire.ActionResult{RunID: backup.RunID, State: agentwire.ActionSucceeded, Restic: &agentwire.ResticRunReport{
		Path: agentwire.ResticPathDirect, Reason: agentwire.ResticReachable, SnapshotID: strings.Repeat("e", 64), BytesAdded: 3,
	}})
	retain := target.next(t, agentwire.KindResticRetain)
	var retainSecret map[string]string
	if json.Unmarshal(retain.Secret, &retainSecret) != nil || len(retainSecret) != 1 || retainSecret["repo_password"] != passwords.RepoPassword {
		t.Fatal("restic.retain carries the repository password, and only it")
	}
	bench.reportTargetRun(t, agentwire.ActionResult{RunID: retain.RunID, State: agentwire.ActionSucceeded})
	<-passed

	// La sauvegarde supprimée : le lien est retiré, ses mots de passe restent
	// scellés en base, et la base les tait toujours.
	if got := callAPI(bench.Server, http.MethodDelete, "/api/machines/"+resticSourceID+"/restic-backups/"+created.ID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", got.Code, got.Body.String())
	}
	if removed := target.next(t, agentwire.KindResticUserRemove); len(removed.Secret) != 0 {
		t.Fatal("restic.user.remove carries a secret")
	}
	if retired, err := bench.db.GetResticLink(context.Background(), resticSourceID, resticTargetID); err != nil || !retired.IsRetired() || retired.Secrets.Repo != passwords.RepoPassword {
		t.Fatalf("the retired link lost its passwords: %v", err)
	}

	if !strings.Contains(bench.logs.String(), "restic retention sent") {
		t.Fatal("the journal of the server is not captured")
	}
	places := map[string]string{
		"journal":         bench.logs.String(),
		"database dump":   dumpDatabase(t, bench),
		"stranger stream": third.text(),
	}
	for _, path := range []string{
		"/api/backups/overview", "/api/backups/runs", "/api/actions", "/api/audit?limit=500", "/api/machines/" + resticSourceID,
		"/api/machines/" + resticTargetID, "/api/settings/api-tokens",
	} {
		places[path] = callAPI(bench.Server, http.MethodGet, path, nil).Body.String()
	}
	places["token overview"] = callBearer(bench.Server, "/api/backups/overview", bearer).Body.String()
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
		for name, password := range map[string]string{"REST": passwords.RESTPassword, "repository": passwords.RepoPassword} {
			if strings.Contains(content, password) {
				t.Errorf("the %s password leaked into %s", name, place)
			}
		}
	}
	// La source n'a vu que son lien ; la cible, que ses deux ordres.
	if strings.Count(source.text(), passwords.RepoPassword) != 1 || strings.Count(target.text(), passwords.RepoPassword) != 1 {
		t.Error("the repository password left in more than its two orders")
	}
	if strings.Contains(third.text(), agentwire.EventOrder) {
		t.Errorf("a third agent received an order: %s", third.text())
	}
}

// Un producteur ne peut ni effacer ni purger : aucun verbe, aucune route ne
// lance restic.retain, l'ordre ne part qu'à une machine qui tient
// restic.target, et un DELETE relayé est refusé (voir aussi
// TestRestic_ARelayedSourceReachesItsRepositoryAndCannotDelete).
func TestRestic_AProducerCannotDeleteNorForget(t *testing.T) {
	bench := readyResticBench(t)
	bench.createServiceBackup(t)
	for _, registered := range verb.Default.All() {
		route := strings.ToLower(registered.ID + " " + registered.Route)
		if strings.Contains(route, "retain") || strings.Contains(route, "forget") || strings.Contains(route, "prune") {
			t.Errorf("a verb purges: %s", registered.ID)
		}
	}
	for _, path := range []string{"/actions/restic-retain", "/actions/restic-forget", "/restic-backups/actions/retain"} {
		got := callAPI(bench.Server, http.MethodPost, "/api/machines/"+resticSourceID+path, map[string]any{})
		if got.Code != http.StatusNotFound && got.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d", path, got.Code)
		}
	}
	params, _ := json.Marshal(agentwire.ResticRetainParams{User: resticSourceID, KeepDaily: 1})
	if _, err := bench.actions.SendWithSecret(context.Background(), resticSourceID, agentwire.KindResticRetain, params, json.RawMessage(`{}`), "passwords"); err == nil {
		t.Fatal("restic.retain left for a machine without restic.target")
	}
}

// Le rapport de parties est une liste blanche : un agent hostile qui glisse
// une valeur semée dans le nom d'une partie (avec un chemin) ou dans sa
// raison (un texte libre) n'en laisse rien : le rapport entier est jeté,
// l'état de l'ordre reste, et la valeur n'est ni dans l'API, ni dans
// l'audit, ni dans le journal, ni dans la base. (La raison de l'ordre
// lui-même est celle du composant action : un texte ASCII court, gardé tel
// quel ; restic n'en retient qu'un mot de sa liste fermée.)
func TestRestic_AHostilePartReportLeavesNoTrace(t *testing.T) {
	const seed = "hunter2-graine-secrete" // gitleaks:allow -- valeur semée par le test
	bench := readyResticBench(t)
	backupID := bench.createServiceBackup(t)
	order := bench.runNow(t, backupID)
	bench.reportRun(t, agentwire.ActionResult{RunID: order.RunID, State: agentwire.ActionFailed, Reason: agentwire.ReasonDumpFailed,
		Restic: &agentwire.ResticRunReport{Path: agentwire.ResticPathDirect, Reason: agentwire.ResticReachable, BytesAdded: 1, Parts: []agentwire.ResticPartReport{
			{Kind: agentwire.ResticPartFolder, State: agentwire.ResticPartOK, BytesAdded: 1},
			{Kind: agentwire.ResticPartVolume, Name: "../" + seed + "/_data", State: agentwire.ResticPartOK},
			{Kind: agentwire.ResticPartDatabase, Name: "nextcloud-db-1", State: agentwire.ResticPartFailed, Reason: "pg_dump: password " + seed + " refused"},
		}}})

	stored, err := bench.db.GetResticBackup(context.Background(), backupID)
	if err != nil || stored.Last.State != "failed" || stored.Last.Reason != agentwire.ReasonDumpFailed || stored.Last.Report.Path != "" || len(stored.Last.Report.Parts) != 0 {
		t.Fatalf("stored %+v %v", stored.Last, err)
	}
	places := map[string]string{"journal": bench.logs.String(), "database dump": dumpDatabase(t, bench)}
	for _, path := range []string{"/api/backups/overview", "/api/backups/runs", "/api/actions", "/api/audit?limit=500", "/api/machines/" + resticSourceID} {
		places[path] = callAPI(bench.Server, http.MethodGet, path, nil).Body.String()
	}
	for place, content := range places {
		if strings.Contains(content, seed) {
			t.Errorf("the seeded value leaked into %s", place)
		}
	}
}
