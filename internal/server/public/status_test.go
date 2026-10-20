package public

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/server/servertest"
)

// Le public ne voit que le nom des composants et ce que l'opérateur a
// écrit : ni un nom de machine, de conteneur, d'image ou de sonde, ni une
// cible, une adresse, un identifiant d'objet, un jeton.
func TestStatusPublic_LeaksNothingOfTheObjects(t *testing.T) {
	server := newBench(t)
	server.seed(t)
	secrets := []string{
		"vps-paris-1", "51.15.20.114", servertest.RemoteID, `"local"`,
		"nextcloud", "cloud.exemple.fr", "sauvegarde", "hb_",
	}
	for _, path := range []string{"/statut/api/status", "/statut/api/i18n", "/statut"} {
		recorder := server.get(path)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: status %d", path, recorder.Code)
		}
		body := recorder.Body.String()
		for _, secret := range secrets {
			if strings.Contains(body, secret) {
				t.Errorf("%s leaks %q", path, secret)
			}
		}
	}
	body := server.get("/statut/api/status").Body.String()
	for _, expected := range []string{`"Nuage"`, `"Le nuage ne répond plus"`, `"Statut de la maison"`, `"global":"down"`} {
		if !strings.Contains(body, expected) {
			t.Errorf("public status lacks %s", expected)
		}
	}
}

// Les étiquettes JSON sont celles que le front lit ; un composant sans
// incident s'écrit [] et non null.
func TestStatusPublic_KeepsTheJSONShapeTheFrontReads(t *testing.T) {
	server := newBench(t)
	recorder := server.get("/statut/api/status")
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/json" || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %q %q", recorder.Code, recorder.Header().Get("Content-Type"), recorder.Header().Get("Cache-Control"))
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"title", "announcement", "language", "generated_at", "global", "components", "open", "history"} {
		if _, found := fields[name]; !found {
			t.Errorf("field %q missing from %s", name, recorder.Body.String())
		}
	}
	for _, name := range []string{"components", "open", "history"} {
		if string(fields[name]) != "[]" {
			t.Errorf("%s = %s, want []", name, fields[name])
		}
	}
}
