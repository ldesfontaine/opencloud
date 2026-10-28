package server

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/auth"
	"github.com/ldesfontaine/opencloud/internal/lang"
)

// Ces tests n'ont besoin d'aucun front construit : les refus tombent avant
// lui, et l'API ne le sert pas.

const (
	// Une route de lecture de l'API, ouverte à l'opérateur et à lui seul.
	forwardUsersProbe = "/api/session"
	// Le refus du mode forward pour une identité qui n'est pas dans la liste.
	forwardForbiddenCode = "auth.forward_forbidden"
	// Le temps que les tests laissent à un flux avant de le couper.
	streamGuardTimeout = 2 * time.Second
)

// forwardWithList : le mode forward derrière 192.0.2.0/24, sans secret,
// restreint à users. Le journal du serveur est rendu au test.
func forwardWithList(t *testing.T, users ...string) (*testServer, *bytes.Buffer) {
	t.Helper()
	server := newFirstSetupServer(t)
	server.enableForward(t)
	server.forwardUsers = forwardUserSet(users)
	var logs bytes.Buffer
	server.logger = slog.New(slog.NewTextHandler(&logs, nil))
	return server, &logs
}

// callIdentityValues joue une requête qui porte exactement ces valeurs de
// Remote-User : aucune, une vide, plusieurs. callForwarded ne sait pas
// poser une valeur vide ni en doubler une.
func callIdentityValues(server *Server, path, peer string, values ...string) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	request.RemoteAddr = net.JoinHostPort(peer, "4000")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, value := range values {
		request.Header.Add(auth.DefaultForwardHeader, value)
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

// Liste absente ou vide : le comportement d'avant, aucune installation ne
// casse. C'est aussi ce que la configuration d'aujourd'hui produit.
func TestForwardUsers_NoListAdmitsEveryIdentity(t *testing.T) {
	for _, users := range [][]string{nil, {}} {
		server, logs := forwardWithList(t, users...)
		for _, identity := range []string{"lucas", "autre", "Lucas", "léa@example.org"} {
			if got := callForwarded(server.Server, http.MethodGet, forwardUsersProbe, trustedPeer, identity); got.Code != http.StatusOK {
				t.Errorf("no list, %q: status %d, want 200", identity, got.Code)
			}
		}
		if strings.Contains(logs.String(), "forward_users") {
			t.Errorf("no list, nothing to log: %q", logs.String())
		}
	}
}

func TestForwardUsers_OnlyTheListedIdentitiesPass(t *testing.T) {
	server, _ := forwardWithList(t, "lucas", "léa@example.org")
	cases := []struct {
		name     string
		identity string
		want     int
	}{
		{"listed", "lucas", http.StatusOK},
		{"listed, with an accent and an at sign", "léa@example.org", http.StatusOK},
		{"not listed", "autre", http.StatusForbidden},
		// La comparaison est exacte, casse comprise (décision à valider).
		{"capitalized", "Lucas", http.StatusForbidden},
		{"upper case", "LUCAS", http.StatusForbidden},
		{"upper case accent", "LÉA@example.org", http.StatusForbidden},
		// Le rognage de trustedIdentity existait avant la liste : un espace
		// que le mandataire n'a pas retiré ne ferme pas la porte à un admis.
		{"trailing space", "lucas ", http.StatusOK},
		{"leading space", " lucas", http.StatusOK},
		{"tabs around", "\tlucas\t", http.StatusOK},
		// Mais rien d'autre n'est rogné ni ignoré.
		{"space inside", "luc as", http.StatusForbidden},
		{"prefix of a listed identity", "luca", http.StatusForbidden},
		{"listed identity as a prefix", "lucas2", http.StatusForbidden},
		{"listed identity as a suffix", "xlucas", http.StatusForbidden},
		{"NUL after", "lucas\x00", http.StatusForbidden},
		{"NUL before", "\x00lucas", http.StatusForbidden},
		{"zero-width space after", "lucas\u200b", http.StatusForbidden},
		{"Cyrillic look-alike of the letter c", "lu\u0441as", http.StatusForbidden},
		{"two identities in one value", "lucas,autre", http.StatusForbidden},
		{"another listed identity after a comma", "autre,lucas", http.StatusForbidden},
		{"wildcard", "*", http.StatusForbidden},
		{"dot", ".", http.StatusForbidden},
	}
	for _, tc := range cases {
		got := callForwarded(server.Server, http.MethodGet, forwardUsersProbe, trustedPeer, tc.identity)
		if got.Code != tc.want {
			t.Errorf("%s (%q): status %d, want %d", tc.name, tc.identity, got.Code, tc.want)
		}
	}
}

// Les refus d'avant la liste restent des 401 : un 403 avant eux apprendrait
// à un pair hors trusted_proxies quelles identités sont admises.
func TestForwardUsers_EarlierRefusalsStayUnauthorized(t *testing.T) {
	server, _ := forwardWithList(t, "lucas")

	cases := []struct {
		name   string
		peer   string
		values []string
	}{
		{"header missing", trustedPeer, nil},
		{"header empty", trustedPeer, []string{""}},
		{"header only spaces", trustedPeer, []string{"   \t "}},
		{"untrusted peer, listed identity", untrustedPeer, []string{"lucas"}},
		{"untrusted peer, unlisted identity", untrustedPeer, []string{"autre"}},
		{"header sent twice, both listed", trustedPeer, []string{"lucas", "lucas"}},
		{"header sent twice, client value first", trustedPeer, []string{"autre", "lucas"}},
		{"header sent twice, client value last", trustedPeer, []string{"lucas", "autre"}},
		{"header sent twice, one empty", trustedPeer, []string{"lucas", ""}},
	}
	for _, tc := range cases {
		got := callIdentityValues(server.Server, forwardUsersProbe, tc.peer, tc.values...)
		if got.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", tc.name, got.Code)
		}
		if tc.peer == untrustedPeer && strings.Contains(got.Body.String(), "forward_users") {
			t.Errorf("%s: the refusal teaches about the list: %q", tc.name, got.Body.String())
		}
	}
}

// La liste ne remplace ni trusted_proxies ni le secret partagé.
func TestForwardUsers_TheSecretIsJudgedBeforeTheList(t *testing.T) {
	server := newFirstSetupServer(t)
	server.enableForwardOnLoopback(t, proxySecret)
	server.forwardUsers = forwardUserSet([]string{"lucas"})

	cases := []struct {
		name     string
		identity string
		secrets  []string
		want     int
	}{
		{"listed, right secret", "lucas", []string{proxySecret}, http.StatusOK},
		{"unlisted, right secret", "autre", []string{proxySecret}, http.StatusForbidden},
		{"listed, no secret", "lucas", nil, http.StatusUnauthorized},
		{"unlisted, no secret", "autre", nil, http.StatusUnauthorized},
		{"unlisted, wrong secret", "autre", []string{wrongProxySecret}, http.StatusUnauthorized},
		{"listed, wrong secret", "lucas", []string{wrongProxySecret}, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		got := callProxied(server.Server, http.MethodGet, forwardUsersProbe, loopbackPeer, tc.identity, tc.secrets...)
		if got.Code != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, got.Code, tc.want)
		}
	}
}

func TestForwardUsers_ARefusalIsLoggedOnceWithTheIdentityReceived(t *testing.T) {
	server, logs := forwardWithList(t, "lucas")

	if got := callForwarded(server.Server, http.MethodGet, forwardUsersProbe, trustedPeer, "autre"); got.Code != http.StatusForbidden {
		t.Fatalf("status %d", got.Code)
	}
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("want one log line, got %d: %q", len(lines), logs.String())
	}
	for _, want := range []string{"level=WARN", "identity not in forward_users", "identity=autre", "peer=" + trustedPeer, "path=" + forwardUsersProbe} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("the log line lacks %q: %q", want, lines[0])
		}
	}
	// Un admis ne laisse aucune ligne.
	logs.Reset()
	callForwarded(server.Server, http.MethodGet, forwardUsersProbe, trustedPeer, "lucas")
	if logs.Len() != 0 {
		t.Fatalf("an admitted identity is not logged: %q", logs.String())
	}
}

// Une identité de 10 000 caractères, ou pleine de caractères de contrôle,
// est refusée, et ni le journal ni la page n'en recopient plus que la borne.
func TestForwardUsers_AHostileIdentityIsRefusedAndTruncatedInTheLogAndThePage(t *testing.T) {
	server, logs := forwardWithList(t, "lucas")
	huge := strings.Repeat("x", 10_000)

	for _, path := range []string{forwardUsersProbe, "/machines"} {
		logs.Reset()
		got := callForwarded(server.Server, http.MethodGet, path, trustedPeer, huge)
		if got.Code != http.StatusForbidden {
			t.Fatalf("%s: status %d, want 403", path, got.Code)
		}
		if strings.Contains(logs.String(), strings.Repeat("x", auth.MaxUsernameLength+1)) {
			t.Errorf("%s: the log keeps more than %d characters of the identity", path, auth.MaxUsernameLength)
		}
		if !strings.Contains(logs.String(), strings.Repeat("x", auth.MaxUsernameLength)) {
			t.Errorf("%s: the log must name the start of the identity: %q", path, logs.String())
		}
		if strings.Contains(got.Body.String(), strings.Repeat("x", auth.MaxUsernameLength+1)) {
			t.Errorf("%s: the answer echoes more than %d characters of the identity", path, auth.MaxUsernameLength)
		}
	}

	// Les caractères de contrôle ne passent ni dans le journal ni dans la page.
	logs.Reset()
	got := callForwarded(server.Server, http.MethodGet, "/machines", trustedPeer, "au\x1b[31mtre\x07\x00")
	if got.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", got.Code)
	}
	for _, text := range []string{logs.String(), got.Body.String()} {
		if strings.ContainsAny(text, "\x1b\x07\x00") {
			t.Errorf("a control character got through: %q", text)
		}
	}
}

// Les trois formes du refus : le JSON pour l'API, du texte pour une page,
// la langue du navigateur, jamais de cache.
func TestForwardUsers_TheRefusalIsAForbiddenWithItsOwnCode(t *testing.T) {
	server, _ := forwardWithList(t, "lucas")

	api := callForwarded(server.Server, http.MethodGet, forwardUsersProbe, trustedPeer, "autre")
	if api.Code != http.StatusForbidden || errorCode(t, api) != forwardForbiddenCode {
		t.Fatalf("API: %d %s", api.Code, api.Body.String())
	}
	// Le front traduit la clé sans argument : son texte n'a pas de verbe,
	// sinon il afficherait « %s » brut.
	for _, language := range []lang.Code{lang.French, lang.English} {
		if text := server.catalogs.For(language).Get(forwardForbiddenCode); strings.Contains(text, "%") {
			t.Errorf("%s: the API refusal %q carries a verb the front cannot fill", language, text)
		}
	}

	for acceptLanguage, want := range map[string]string{
		"fr-FR,fr;q=0.9": "Votre identité (autre) est transmise par le SSO mais n'est pas admise par",
		"en-US,en;q=0.9": "Your identity (autre) is sent by the SSO but is not admitted by",
		"":               "Your identity (autre) is sent by the SSO but is not admitted by",
	} {
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/machines", nil)
		request.RemoteAddr = net.JoinHostPort(trustedPeer, "4000")
		request.Header.Set(auth.DefaultForwardHeader, "autre")
		request.Header.Set("Accept-Language", acceptLanguage)
		page := httptest.NewRecorder()
		server.ServeHTTP(page, request)
		if page.Code != http.StatusForbidden {
			t.Fatalf("page %q: status %d, want 403", acceptLanguage, page.Code)
		}
		if !strings.Contains(page.Body.String(), want) || strings.Contains(page.Body.String(), "[auth.") {
			t.Errorf("page %q: body %q, want %q", acceptLanguage, page.Body.String(), want)
		}
		if got := page.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("page %q: Cache-Control %q, want no-store", acceptLanguage, got)
		}
		if !strings.HasPrefix(page.Header().Get("Content-Type"), "text/plain") {
			t.Errorf("page %q: Content-Type %q, the identity must not be read as HTML", acceptLanguage, page.Header().Get("Content-Type"))
		}
	}
}

// Une écriture de l'API refusée à une identité hors liste n'écrit rien.
func TestForwardUsers_ARefusedIdentityWritesNothing(t *testing.T) {
	server, _ := forwardWithList(t, "lucas")
	mark := server.latestAuditID(t)

	refused := callForwardedJSON(server.Server, http.MethodPost, "/api/directory/projects", `{"name":"client-a"}`, trustedPeer, auth.DefaultForwardHeader, "autre")
	if refused.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", refused.Code, refused.Body.String())
	}
	if events := server.auditSince(t, mark); len(events) != 0 {
		t.Fatalf("a refused write was journaled: %+v", events)
	}

	// Ce que l'API ne ferait pas pour « autre », elle le fait pour « lucas », sous son nom.
	allowed := callForwardedJSON(server.Server, http.MethodPost, "/api/directory/projects", `{"name":"client-a"}`, trustedPeer, auth.DefaultForwardHeader, "lucas")
	if allowed.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201: %s", allowed.Code, allowed.Body.String())
	}
	events := server.auditSince(t, mark)
	if len(events) != 1 || events[0].Actor != audit.Operator("lucas") {
		t.Fatalf("the journal must name the forwarded identity: %+v", events)
	}
}

// callUnlisted joue une requête de l'identité « autre », hors liste, depuis
// un pair fiable, avec un corps JSON. Un flux admis à tort ne finirait
// jamais : le délai fait échouer le test au lieu de le suspendre.
func callUnlisted(server *Server, method, path string) *httptest.ResponseRecorder {
	ctx, cancel := context.WithTimeout(context.Background(), streamGuardTimeout)
	defer cancel()
	request := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader("{}"))
	request.RemoteAddr = net.JoinHostPort(trustedPeer, "4000")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(auth.DefaultForwardHeader, "autre")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

// Toutes les routes réservées à l'opérateur, l'API, ses écritures, ses flux,
// ses routes de première configuration, /api/auth lui-même et les pages du
// front, passent par la même porte : un 403 du refus de la liste, rendu
// avant le handler (aucune trace au journal d'audit), jamais un 401 (le pair
// est fiable) ni un 2xx. Le parcours lit routes() : une route ajoutée plus
// tard y entre seule.
func TestForwardUsers_EveryOperatorRouteRefusesAnUnlistedIdentity(t *testing.T) {
	server, _ := forwardWithList(t, "lucas")
	mark := server.latestAuditID(t)
	placeholders := strings.NewReplacer("{id}", "x", "{code}", "fr", "{name}", "x", "{kind}", "x", "{subject}", "x", "{vmid}", "105", "{verb}", "start")
	checked := 0
	for _, route := range server.routes() {
		if !isOperatorRoute(route.pattern) {
			// Une route sortie de la porte ne sortirait pas du parcours en
			// silence : les seules ouvertes sont celles qui ont leur propre
			// preuve (agent, pings) ou n'en ont pas besoin (statut).
			if !strings.HasPrefix(route.pattern, "/agent/") && !strings.HasPrefix(route.pattern, "/ping/") && !strings.HasPrefix(route.pattern, "/statut") {
				t.Errorf("%s %s: open route outside the agent, pings and status page", route.method, route.pattern)
			}
			continue
		}
		path := placeholders.Replace(route.pattern)
		if strings.Contains(path, "{") {
			t.Errorf("%s %s: add its placeholder to the test", route.method, route.pattern)
			continue
		}
		methods := []string{route.method}
		switch route.method {
		case anyMethod:
			methods = []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodDelete}
		case http.MethodGet:
			methods = append(methods, http.MethodHead)
		}
		for _, method := range methods {
			got := callUnlisted(server.Server, method, path)
			if got.Code != http.StatusForbidden {
				t.Errorf("%s %s: status %d, want 403", method, path, got.Code)
				continue
			}
			if strings.HasPrefix(path, "/api/") && method != http.MethodHead && errorCode(t, got) != forwardForbiddenCode {
				t.Errorf("%s %s: %s, want the list's refusal", method, path, got.Body.String())
			}
			checked++
		}
	}
	if checked < 100 {
		t.Fatalf("only %d operator calls checked: the walk lost its routes", checked)
	}
	// Des chemins qu'aucun motif ne nomme tels quels tombent sur un
	// fourre-tout gardé, jamais sur une route ouverte.
	for _, path := range []string{"/API/session", "/api/session/", "/api/nope", "/machines", "/parametres", "/index.html", "/statut-x"} {
		if got := callUnlisted(server.Server, http.MethodGet, path); got.Code != http.StatusForbidden {
			t.Errorf("GET %s: status %d, want 403", path, got.Code)
		}
	}
	// Un chemin à nettoyer est redirigé par le mux avant toute garde, sans
	// réponse de handler ; sa cible est refusée ci-dessus.
	for _, path := range []string{"/api/./auth", "//api/auth", "/statut/../api/auth", "/ping/../api/session"} {
		if got := callUnlisted(server.Server, http.MethodGet, path); got.Code < 300 || got.Code >= 400 {
			t.Errorf("GET %s: status %d, want a redirect", path, got.Code)
		}
	}
	if events := server.auditSince(t, mark); len(events) != 0 {
		t.Fatalf("a refused identity reached a handler that journals: %+v", events)
	}
}

func TestForwardUsers_TheLogStreamsAreGuarded(t *testing.T) {
	server, _ := forwardWithList(t, "lucas")
	for _, path := range []string{"/api/events", "/api/containers/x/logs/stream", "/api/containers/x/logs"} {
		ctx, cancel := context.WithTimeout(context.Background(), streamGuardTimeout)
		got := callForwardedWithin(ctx, server.Server, http.MethodGet, path, trustedPeer, "autre")
		cancel()
		if got.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", path, got.Code)
		}
	}
}

// Une liste mal construite ne rouvre jamais la porte : [""] n'est pas « vide »
// (la configuration la refuse, mais le serveur ne le suppose pas).
func TestForwardUsers_AListHoldingOnlyAnEmptyEntryAdmitsNobody(t *testing.T) {
	server, _ := forwardWithList(t, "")
	for _, identity := range []string{"lucas", "autre", " ", "\x00"} {
		got := callForwarded(server.Server, http.MethodGet, forwardUsersProbe, trustedPeer, identity)
		if got.Code == http.StatusOK {
			t.Errorf("%q passed through a list of one empty entry", identity)
		}
	}
	// L'en-tête vide reste un 401, jamais un laissez-passer.
	if got := callIdentityValues(server.Server, forwardUsersProbe, trustedPeer, ""); got.Code != http.StatusUnauthorized {
		t.Errorf("empty header: status %d, want 401", got.Code)
	}
}

// Des doublons (qu'une configuration ne laisse pas passer) ne changent rien :
// l'ensemble est le même, et l'API rend chaque identité une fois.
func TestForwardUsers_DuplicatesCollapse(t *testing.T) {
	server, _ := forwardWithList(t, "lucas", "lucas", "léa", "lucas")
	if got := callForwarded(server.Server, http.MethodGet, forwardUsersProbe, trustedPeer, "lucas"); got.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", got.Code)
	}
	var state authResponse
	decodeAPI(t, callForwarded(server.Server, http.MethodGet, "/api/auth", trustedPeer, "lucas"), &state)
	if strings.Join(state.AllowedUsers, "|") != "lucas|léa" {
		t.Fatalf("allowed_users %v, want each identity once, sorted by bytes", state.AllowedUsers)
	}
}

// L'activation ne s'enferme pas dehors : même garde-fou que l'en-tête
// absent, avec sa propre clé pour que la page dise quoi faire.
func TestForwardUsers_EnablingFromAnUnlistedIdentityIsRefused(t *testing.T) {
	const enable = "/api/auth/actions/enable"
	server := newFirstSetupServer(t)
	server.trustedProxies = parsePrefixes(t, "192.0.2.0/24")
	server.forwardUsers = forwardUserSet([]string{"lucas"})

	for _, identity := range []string{"autre", "Lucas"} {
		refused := callForwardedJSON(server.Server, http.MethodPost, enable, `{"mode":"forward"}`, trustedPeer, auth.DefaultForwardHeader, identity)
		if refused.Code != http.StatusUnprocessableEntity || errorCode(t, refused) != "auth.forward_user_not_listed" {
			t.Fatalf("%q: %d %s", identity, refused.Code, refused.Body.String())
		}
	}
	state, err := server.operator.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Mode != auth.ModeNone {
		t.Fatalf("a refused activation changed the mode to %q", state.Mode)
	}

	// Les refus d'avant restent ceux d'avant : l'en-tête manquant d'un pair
	// non fiable n'apprend rien sur la liste.
	forged := callForwardedJSON(server.Server, http.MethodPost, enable, `{"mode":"forward"}`, untrustedPeer, auth.DefaultForwardHeader, "autre")
	if forged.Code != http.StatusUnprocessableEntity || errorCode(t, forged) != "auth.forward_header_missing" {
		t.Fatalf("untrusted peer: %d %s", forged.Code, forged.Body.String())
	}

	// Depuis un admis, l'activation passe et la réponse rend la liste.
	allowed := callForwardedJSON(server.Server, http.MethodPost, enable, `{"mode":"forward"}`, trustedPeer, auth.DefaultForwardHeader, "lucas")
	if allowed.Code != http.StatusOK {
		t.Fatalf("listed identity: %d %s", allowed.Code, allowed.Body.String())
	}
	var enabled authEnableResponse
	decodeAPI(t, allowed, &enabled)
	if enabled.Mode != "forward" || strings.Join(enabled.AllowedUsers, "|") != "lucas" {
		t.Fatalf("enabled: %+v", enabled)
	}
}

func TestForwardUsers_NoListNeverBlocksTheActivation(t *testing.T) {
	server := newFirstSetupServer(t)
	server.trustedProxies = parsePrefixes(t, "192.0.2.0/24")
	got := callForwardedJSON(server.Server, http.MethodPost, "/api/auth/actions/enable", `{"mode":"forward"}`, trustedPeer, auth.DefaultForwardHeader, "autre")
	if got.Code != http.StatusOK {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	var enabled authEnableResponse
	decodeAPI(t, got, &enabled)
	if enabled.AllowedUsers != nil {
		t.Fatalf("allowed_users %v, want null", enabled.AllowedUsers)
	}
}

// GET /api/auth : la liste en forward seulement, null sinon ; le JSON dit
// null, pas [].
func TestForwardUsers_AuthStateShowsTheListInForwardModeOnly(t *testing.T) {
	forward, _ := forwardWithList(t, "lucas", "Léa")
	got := callForwarded(forward.Server, http.MethodGet, "/api/auth", trustedPeer, "lucas")
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"allowed_users":["Léa","lucas"]`) {
		t.Fatalf("forward with a list: %d %s", got.Code, got.Body.String())
	}

	empty, _ := forwardWithList(t)
	got = callForwarded(empty.Server, http.MethodGet, "/api/auth", trustedPeer, "autre")
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"allowed_users":null`) {
		t.Fatalf("forward without a list: %d %s", got.Code, got.Body.String())
	}

	// Basic : la liste configurée ne garde rien, la page ne la montre pas.
	basic := newTestServer(t)
	basic.forwardUsers = forwardUserSet([]string{"lucas"})
	got = callAPI(basic.Server, http.MethodGet, "/api/auth", nil)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"allowed_users":null`) || strings.Contains(got.Body.String(), "lucas\"]") {
		t.Fatalf("basic: %d %s", got.Code, got.Body.String())
	}

	// None : idem.
	none := newFirstSetupServer(t)
	none.forwardUsers = forwardUserSet([]string{"lucas"})
	got = callAPI(none.Server, http.MethodGet, "/api/auth", nil)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"allowed_users":null`) {
		t.Fatalf("none: %d %s", got.Code, got.Body.String())
	}
}

// En basic, forward_users ne garde rien : un Basic juste passe, quelle que
// soit la liste (elle ne vaut qu'en forward).
func TestForwardUsers_TheListDoesNotTouchBasicMode(t *testing.T) {
	server := newTestServer(t)
	server.forwardUsers = forwardUserSet([]string{"autre"})
	if got := callAPI(server.Server, http.MethodGet, forwardUsersProbe, nil); got.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", got.Code)
	}
}

// Un jeton d'API n'est pas une identité du mandataire : il passe avant le
// mode, la liste ne le juge pas. Il a été tiré par un opérateur admis.
func TestForwardUsers_AnAPITokenIsNotJudgedByTheList(t *testing.T) {
	server, _ := forwardWithList(t, "lucas")
	bearer := server.issueAPIToken(t, "inspecteur")
	if got := callBearerWith(server.Server, http.MethodGet, "/api/counts", bearer, untrustedPeer); got.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", got.Code)
	}
}
