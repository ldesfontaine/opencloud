package server

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/goldentest"
	"github.com/ldesfontaine/opencloud/internal/verb"
)

// La parité UI / MCP a deux versants. Celui du front est
// web/src/api/parity.test.ts : chaque écriture que l'écran déclare existe, et
// chaque verbe a son geste. Celui-ci : chaque route d'écriture du serveur est
// appelée par l'écran, ou dit pourquoi elle ne l'est pas. Une route sans
// appelant est du code mort.

// Les routes d'écriture que l'écran n'appelle pas, chacune avec sa raison.
var writesWithoutScreen = map[string]string{
	"POST /api/manifest/plan":            "la commande `apply`, par la socket de commande ; l'écran ne montre que les passages",
	"POST /api/manifest/apply":           "la commande `apply`, par la socket de commande",
	"POST /agent/enroll":                 "l'agent, à l'enrôlement",
	"POST /agent/signal":                 "l'agent, à chaque signal",
	"POST /agent/logs/{request}":         "l'agent, la réponse à une lecture de journaux",
	"PUT /agent/parcels/{parcel}/{name}": "l'agent, un colis qu'il produit",
	"POST /agent/relay/response/{id}":    "l'agent d'une cible restic, la réponse de rest-server à une requête relayée",
	"POST /ping/{token}":                 "le ping d'une tâche, appelé par la tâche",
	"POST /ping/{token}/start":           "le ping d'une tâche, appelé par la tâche",
	"POST /ping/{token}/{code}":          "le ping d'une tâche, appelé par la tâche",
}

var frontWritePattern = regexp.MustCompile(`"((?:POST|PUT|DELETE) /[^"]+)"`)

func TestRoutes_EveryWriteHasAScreenCallerOrAReason(t *testing.T) {
	source, err := os.ReadFile("../../web/src/api/routes.ts")
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]bool{}
	for _, match := range frontWritePattern.FindAllStringSubmatch(string(source), -1) {
		declared[match[1]] = true
	}
	if len(declared) == 0 {
		t.Fatal("routes.ts ne déclare aucune écriture : motif illisible ?")
	}

	registered := map[string]bool{}
	for _, route := range newTestServer(t).routes() {
		if route.method == http.MethodGet || route.method == anyMethod {
			continue
		}
		pattern := route.method + " " + route.pattern
		registered[pattern] = true
		_, withoutScreen := writesWithoutScreen[pattern]
		switch {
		case declared[pattern] && withoutScreen:
			t.Errorf("%s : appelée par l'écran, retirez-la de writesWithoutScreen", pattern)
		case !declared[pattern] && !withoutScreen:
			t.Errorf("%s : aucun appelant dans web/src/api/routes.ts, et aucune raison dans writesWithoutScreen", pattern)
		}
	}
	for pattern := range declared {
		if !registered[pattern] {
			t.Errorf("%s : déclarée par routes.ts, absente des routes du serveur", pattern)
		}
	}
	for pattern := range writesWithoutScreen {
		if !registered[pattern] {
			t.Errorf("%s : listée sans écran, mais absente des routes du serveur", pattern)
		}
	}
}

// Le registre des verbes est figé dans testdata/verbs.golden.json : le front
// y lit les verbes (dans l'ordre du registre) pour prouver que chacun a son geste d'écran.
func TestVerbs_RegistryMatchesGoldenFile(t *testing.T) {
	type entry struct {
		ID      string `json:"id"`
		Route   string `json:"route"`
		Reverse string `json:"reverse"`
	}
	entries := []entry{}
	for _, offered := range verb.Default.All() {
		entries = append(entries, entry{offered.ID, offered.Route, offered.Reverse})
	}
	body, err := json.Marshal(map[string][]entry{"verbs": entries})
	if err != nil {
		t.Fatal(err)
	}
	goldentest.Compare(t, "testdata/verbs.golden.json", string(body)+"\n")
}
