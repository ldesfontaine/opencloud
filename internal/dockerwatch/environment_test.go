package dockerwatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/dockerapi"
)

// Le banc de non-fuite de l'environnement (étude services-modèle §2.4). Des
// valeurs secrètes sont semées dans l'environnement d'un conteneur de test : aucune,
// ni aucun nom de variable, ne doit se retrouver dans ce que l'agent livre
// (le rapport complet, en JSON), ni dans son journal. Le même banc existe côté
// serveur (internal/server, TestEnvironmentLeak_*) pour la base, l'API et le MCP.

const (
	idWeb      = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	idDB       = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	idCache    = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	idStranger = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	netShop    = "1111111111111111111111111111111111111111111111111111111111111111"
	netOther   = "2222222222222222222222222222222222222222222222222222222222222222"
)

// Les secrets semés, et les noms de variables qui les portent.
var (
	seededSecrets = []string{"hunter2-Zq83", "p4ss-Xk29", "svc-user", "tok_9f3a77c1d2e8", "qtok_77aa31", "s3cr3t-signing-key", "AKIAFAKEFAKEFAKE"}
	seededNames   = []string{"DB_PASSWORD", "API_TOKEN", "DATABASE_URL", "SIGNING_KEY", "AWS_ACCESS_KEY_ID", "BACKEND_HOST", "CACHE_URL"}
)

func seededEnvironment() []string {
	return []string{
		"DB_PASSWORD=hunter2-Zq83",
		"API_TOKEN=tok_9f3a77c1d2e8",
		"SIGNING_KEY=s3cr3t-signing-key",
		"AWS_ACCESS_KEY_ID=AKIAFAKEFAKEFAKE",
		"DATABASE_URL=postgres://svc-user:p4ss-Xk29@db:5432/app?token=qtok_77aa31",
		"CACHE_URL=redis://cache:6379/0",
		"BACKEND_HOST=stranger",
		"SELF=web",
		"PATH=/usr/local/bin:/usr/bin",
	}
}

type benchContainer struct {
	id, name, service string
	network           string
	ip                string
	aliases           []string
	env               []string
}

func benchInspect(c benchContainer, withEnv bool) map[string]any {
	labels := map[string]string{}
	if c.service != "" {
		labels["com.docker.compose.project"] = "shop"
		labels["com.docker.compose.service"] = c.service
	}
	config := map[string]any{"Image": "img", "Labels": labels}
	if withEnv {
		config["Env"] = c.env
	}
	return map[string]any{
		"Id": c.id, "Name": "/" + c.name, "Image": "sha256:img", "Created": "2026-10-01T10:00:00Z",
		"State":      map[string]any{"Status": "running"},
		"Config":     config,
		"HostConfig": map[string]any{"NetworkMode": "default"},
		"NetworkSettings": map[string]any{"Networks": map[string]any{
			"net": map[string]any{"NetworkID": c.network, "IPAddress": c.ip, "DNSNames": c.aliases},
		}},
	}
}

func benchDaemon(t *testing.T, requests *atomic.Int64) *http.ServeMux {
	t.Helper()
	benches := []benchContainer{
		{id: idWeb, name: "shop-web-1", service: "web", network: netShop, ip: "172.20.0.2", aliases: []string{"shop-web-1", "web"}, env: seededEnvironment()},
		{id: idDB, name: "shop-db-1", service: "db", network: netShop, ip: "172.20.0.3", aliases: []string{"shop-db-1", "db"}},
		{id: idCache, name: "shop-cache-1", service: "cache", network: netShop, ip: "172.20.0.4", aliases: []string{"shop-cache-1", "cache"}},
		{id: idStranger, name: "stranger", network: netOther, ip: "172.30.0.2", aliases: []string{"stranger"}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1.41/containers/json", func(w http.ResponseWriter, r *http.Request) {
		var list []map[string]any
		for _, c := range benches {
			list = append(list, map[string]any{"Id": c.id, "Names": []string{"/" + c.name}, "Labels": map[string]string{}})
		}
		_ = json.NewEncoder(w).Encode(list)
	})
	mux.HandleFunc("GET /v1.41/containers/{id}/json", func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		for _, c := range benches {
			if c.id == r.PathValue("id") {
				_ = json.NewEncoder(w).Encode(benchInspect(c, true))
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})
	return mux
}

func inventoryOf(t *testing.T, readEnvironment bool) (inventory []container.Observed, requests int64, journal string) {
	t.Helper()
	var count atomic.Int64
	var logged bytes.Buffer
	client := serve(t, benchDaemon(t, &count))
	watcher := New(client, newSink(), Options{Logger: slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug})), ReadEnvironment: readEnvironment})
	inventory, _, err := watcher.inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return inventory, count.Load(), logged.String()
}

func TestInventory_WithTheRightResolvesHostsAgainstTheMachineOnly(t *testing.T) {
	inventory, _, _ := inventoryOf(t, true)
	var web container.Observed
	for _, item := range inventory {
		if item.ContainerID == idWeb {
			web = item
		}
	}
	want := []container.EnvTarget{{Name: "cache", Group: "shop", Port: 6379}, {Name: "db", Group: "shop", Port: 5432}}
	if fmt.Sprint(web.EnvTargets) != fmt.Sprint(want) {
		t.Errorf("targets %+v, want %+v (a host on no shared network, itself, a secret, a path: none of them)", web.EnvTargets, want)
	}
	for _, item := range inventory {
		if item.ContainerID != idWeb && len(item.EnvTargets) != 0 {
			t.Errorf("%s has no environment, got %+v", item.Name, item.EnvTargets)
		}
	}
}

func TestInventory_NoSeededSecretNorVariableNameLeavesTheAgent(t *testing.T) {
	inventory, _, journal := inventoryOf(t, true)
	report := container.Report{Complete: true, Inventory: inventory}
	delivered, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	output := string(delivered) + "\n" + journal + "\n" + fmt.Sprintf("%v %+v %#v", inventory, report, report)
	for _, leak := range append(append([]string{}, seededSecrets...), seededNames...) {
		if strings.Contains(output, leak) {
			t.Errorf("%q left the agent: %s", leak, output)
		}
	}
}

func TestInventory_WithoutTheRightTheEnvironmentIsNeverAskedFor(t *testing.T) {
	inventory, requests, journal := inventoryOf(t, false)
	if requests != int64(len(inventory)) {
		t.Errorf("%d inspections for %d containers: without observe.env each container is inspected once, and its environment is not read", requests, len(inventory))
	}
	for _, item := range inventory {
		if len(item.EnvTargets) != 0 {
			t.Errorf("%s carries %+v without the right", item.Name, item.EnvTargets)
		}
	}
	for _, leak := range seededSecrets {
		if strings.Contains(journal, leak) {
			t.Errorf("%q in the journal", leak)
		}
	}
}

func TestEnvironmentTargets_AreBoundedAndSorted(t *testing.T) {
	source := container.Observed{ContainerID: "s", Networks: []container.Attachment{{NetworkID: "n"}}}
	var inventory []container.Observed
	inventory = append(inventory, source)
	var entries []string
	for index := range container.MaxEnvTargets + 8 {
		name := fmt.Sprintf("svc%02d", index)
		inventory = append(inventory, container.Observed{ContainerID: name, Name: name, Networks: []container.Attachment{{NetworkID: "n"}}})
		entries = append(entries, fmt.Sprintf("H%d=%s", index, name))
	}
	encoded, _ := json.Marshal(entries)
	var hosts dockerapi.EnvHosts
	if err := json.Unmarshal(encoded, &hosts); err != nil {
		t.Fatal(err)
	}
	targets := environmentTargets(source, hosts, inventory)
	if len(targets) != container.MaxEnvTargets || targets[0].Name != "svc00" {
		t.Errorf("%d targets, first %q", len(targets), targets[0].Name)
	}
}
