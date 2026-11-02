package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/apitoken"
	"github.com/ldesfontaine/opencloud/internal/verb"
)

// Les tests hostiles de la non-suppression, couche 1 : un jeton à qui
// l'opérateur a coché TOUS les verbes du registre n'écrit ni plan, ni cible,
// ni rétention, ni sauvegarde. Le cadrage #366 ne lui laisse que lancer,
// vérifier, parcourir et restaurer à côté. Chaque route d'écriture du serveur
// est essayée (la liste vient des routes réelles : une route ajoutée plus
// tard est essayée sans qu'on y pense) ; ce qui n'est pas un verbe doit
// répondre 403, sans qu'un ordre parte ni qu'une ligne de l'aperçu change.

// Les routes d'écriture que ce jeton peut appeler, et rien d'autre : les
// verbes du registre. Les verbes restic sont les quatre du ticket.
var hostileAllowedResticVerbs = []string{"restic.backup.run", "restic.backup.verify", "restic.backup.restore", "restic.backup.browse"}

func (b *resticBench) hostileToken(t *testing.T) string {
	t.Helper()
	var every []string
	for _, offered := range verb.Default.All() {
		every = append(every, offered.ID)
	}
	cleartext, _, err := b.apiTokens.Create(context.Background(), apitoken.Request{Name: "hostile", Scope: apitoken.ScopeWrite, Verbs: every})
	if err != nil {
		t.Fatal(err)
	}
	return cleartext
}

func (b *resticBench) bearerCall(method, path, body, bearer string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	recorder := httptest.NewRecorder()
	b.ServeHTTP(recorder, request)
	return recorder
}

func TestHostileToken_EveryWriteRouteExceptTheVerbsIsRefusedAndNothingChanges(t *testing.T) {
	bench := readyResticBench(t)
	backupID := bench.createServiceBackup(t)
	token := bench.hostileToken(t)
	source, target := tapOrders(t, bench.source), tapOrders(t, bench.target)
	before := callAPI(bench.Server, http.MethodGet, "/api/backups/overview", nil).Body.String()

	tried := 0
	for _, route := range bench.routes() {
		if route.method == http.MethodGet || route.method == anyMethod || !strings.HasPrefix(route.pattern, "/api/") {
			continue
		}
		if offered, isVerb := verb.Default.ByRoute(route.method + " " + route.pattern); isVerb {
			if strings.HasPrefix(offered.ID, "restic.") && !slices.Contains(hostileAllowedResticVerbs, offered.ID) {
				t.Errorf("%s : un verbe restic hors de la liste du ticket", offered.ID)
			}
			continue
		}
		path := route.pattern
		for _, replacement := range [][2]string{{"{id}", resticSourceID}, {"{backup}", backupID}} {
			path = strings.ReplaceAll(path, replacement[0], replacement[1])
		}
		for _, segment := range verb.PathParams(path) {
			path = strings.ReplaceAll(path, "{"+segment+"}", "x")
		}
		body := `{"name":"autre","confirm_name":"nextcloud","retention":{"keep_daily":1},"host":"192.0.2.10","port":8000}`
		got := bench.bearerCall(route.method, path, body, token)
		if got.Code != http.StatusForbidden {
			t.Errorf("%s %s : %d, attendu 403 (%s)", route.method, route.pattern, got.Code, got.Body.String())
		}
		tried++
	}
	if tried < 20 {
		t.Fatalf("%d routes essayées seulement : la liste des routes est illisible ?", tried)
	}
	// Les routes de l'écran restic, nommées : elles doivent être dans la liste.
	for _, pattern := range []string{
		"PUT /api/machines/{id}/restic-backups/{backup}", "POST /api/machines/{id}/restic-backups",
		"DELETE /api/machines/{id}/restic-backups/{backup}",
		"POST /api/machines/{id}/actions/restic-target-setup", "POST /api/machines/{id}/actions/restic-target-remove",
	} {
		if _, isVerb := verb.Default.ByRoute(pattern); isVerb {
			t.Errorf("%s est un verbe", pattern)
		}
	}

	source.none(t, "a hostile token")
	target.none(t, "a hostile token")
	if after := callAPI(bench.Server, http.MethodGet, "/api/backups/overview", nil).Body.String(); after != before {
		t.Errorf("the overview changed under a hostile token:\n%s\n%s", before, after)
	}
}

// Un jeton ne peut plus recevoir les anciens verbes d'écriture : le registre
// ne les connaît plus, il les refuse à la création.
func TestHostileToken_TheWritingResticVerbsCannotBeGranted(t *testing.T) {
	bench := readyResticBench(t)
	for _, id := range []string{"restic.target.setup", "restic.target.remove", "restic.backup.create", "restic.backup.delete", "restic.backup.update", "restic.retain", "restic.forget"} {
		_, _, err := bench.apiTokens.Create(context.Background(), apitoken.Request{Name: "essai " + id, Scope: apitoken.ScopeWrite, Verbs: []string{id}})
		if err == nil || err.Error() != apitoken.ErrVerbUnknown.Error() {
			t.Errorf("%s : %v", id, err)
		}
	}
}

// Les mêmes routes, avec un jeton en lecture : 403 aussi, y compris pour les
// verbes (la portée read n'en porte aucun).
func TestHostileToken_AReadTokenWritesNothingAtAll(t *testing.T) {
	bench := readyResticBench(t)
	backupID := bench.createServiceBackup(t)
	cleartext, _, err := bench.apiTokens.Create(context.Background(), apitoken.Request{Name: "lecture", Scope: apitoken.ScopeRead})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/machines/" + resticSourceID + "/restic-backups"
	for _, call := range []struct{ method, path string }{
		{http.MethodPut, base + "/" + backupID}, {http.MethodDelete, base + "/" + backupID}, {http.MethodPost, base},
		{http.MethodPost, base + "/" + backupID + "/actions/run"}, {http.MethodPost, base + "/" + backupID + "/actions/restore"},
		{http.MethodPost, "/api/machines/" + resticTargetID + "/actions/restic-target-remove"},
	} {
		if got := bench.bearerCall(call.method, call.path, "{}", cleartext); got.Code != http.StatusForbidden {
			t.Errorf("%s %s : %d", call.method, call.path, got.Code)
		}
	}
}

// Le banc de non-fuite du chemin d'un jeton : les mots de passe du lien (celui
// du dépôt est la clé qui déchiffre tout), tirés par le serveur et remis par
// ordre signé, ne sont ni dans ce que le jeton reçoit, ni dans l'aperçu, ni
// dans l'audit, les ordres, les demandes de confirmation, le journal, la base.
func TestHostileToken_NoPasswordOfTheLinkLeaksOnAnyOutput(t *testing.T) {
	bench := readyResticBench(t)
	backupID := bench.createServiceBackup(t)
	token := bench.hostileToken(t)
	base := "/api/machines/" + resticSourceID + "/restic-backups/" + backupID + "/actions/"
	replies := []string{
		bench.bearerCall(http.MethodPost, base+"run", "{}", token).Body.String(),
		bench.bearerCall(http.MethodPost, base+"verify", "{}", token).Body.String(),
		bench.bearerCall(http.MethodPost, base+"restore", `{"snapshot":"`+strings.Repeat("a", 64)+`","path":"/srv/data/a","stamp":"20260912-140000"}`, token).Body.String(),
		bench.bearerCall(http.MethodPut, "/api/machines/"+resticSourceID+"/restic-backups/"+backupID, `{"name":"x"}`, token).Body.String(),
		bench.bearerCall(http.MethodGet, "/api/backups/overview", "", token).Body.String(),
		bench.bearerCall(http.MethodGet, "/api/audit?limit=500", "", token).Body.String(),
		bench.bearerCall(http.MethodGet, "/api/actions", "", token).Body.String(),
	}
	places := map[string]string{
		"replies":       strings.Join(replies, "\n"),
		"journal":       bench.logs.String(),
		"database dump": dumpDatabase(t, bench),
		"operator view": callAPI(bench.Server, http.MethodGet, "/api/backups/overview", nil).Body.String() +
			callAPI(bench.Server, http.MethodGet, "/api/audit?limit=500", nil).Body.String() +
			callAPI(bench.Server, http.MethodGet, "/api/verb-requests", nil).Body.String(),
	}
	secrets := []string{bench.provisioned.RepoPassword, bench.provisioned.RESTPassword}
	for _, secret := range secrets {
		if len(secret) < 32 {
			t.Fatalf("le mot de passe semé est trop court pour prouver quoi que ce soit : %d", len(secret))
		}
	}
	for place, content := range places {
		for _, secret := range secrets {
			if strings.Contains(content, secret) {
				t.Errorf("un mot de passe du lien fuit dans %s", place)
			}
		}
	}
}
