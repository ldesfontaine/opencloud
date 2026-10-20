package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/auth"
)

// Le passage de l'interface ouverte à l'interface gardée met fin à tout ce
// que l'ouverture avait accordé : une écriture autorisée sous l'état d'avant
// ne prend pas effet sous celui d'après.

// signalingBody dit que le serveur a commencé à lire le corps.
type signalingBody struct {
	io.ReadCloser
	once    sync.Once
	reading chan struct{}
}

func (b *signalingBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.reading) })
	return b.ReadCloser.Read(p)
}

// heldWrite est une écriture anonyme dont le corps n'est pas encore parti.
type heldWrite struct {
	t    *testing.T
	conn net.Conn
	body string
}

// holdWrite envoie les en-têtes d'une écriture JSON sur une vraie connexion
// TCP, serveur configuré comme celui de serve (délai sur les en-têtes, aucun
// sur le corps), et retient le corps. Elle ne rend la main qu'une fois le
// serveur parti lire ce corps : l'attente est un signal, pas un délai.
func holdWrite(t *testing.T, server *testServer, method, path, body string) *heldWrite {
	t.Helper()
	reading := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == path {
			r.Body = &signalingBody{ReadCloser: r.Body, reading: reading}
		}
		server.ServeHTTP(w, r)
	})
	listener := httptest.NewUnstartedServer(handler)
	listener.Config.ReadHeaderTimeout = 10 * time.Second
	listener.Start()
	t.Cleanup(listener.Close)
	conn, err := net.Dial("tcp", listener.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	headers := fmt.Sprintf("%s %s HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\nSec-Fetch-Site: same-origin\r\nContent-Length: %d\r\n\r\n", method, path, len(body))
	if _, err := conn.Write([]byte(headers)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reading:
	case <-time.After(5 * time.Second):
		t.Fatal("the server never started reading the held body")
	}
	return &heldWrite{t: t, conn: conn, body: body}
}

// release envoie le corps et rend le statut de la réponse.
func (h *heldWrite) release() int {
	h.t.Helper()
	if _, err := h.conn.Write([]byte(h.body)); err != nil {
		h.t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(h.conn), nil)
	if err != nil {
		h.t.Fatal(err)
	}
	defer response.Body.Close()
	return response.StatusCode
}

// enableBasicByAPI active comme l'opérateur, depuis Paramètres : l'activation
// révoque aussi les jetons MCP.
func enableBasicByAPI(t *testing.T, server *testServer) {
	t.Helper()
	recorder := callAPI(server.Server, http.MethodPost, "/api/auth/actions/enable", authEnableRequest{Username: testUsername, Password: testPassword})
	if recorder.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestAuthTransition_AHeldPasswordChangeDoesNotOutliveTheActivation(t *testing.T) {
	server := newTestServer(t)
	attacker := "attacker chosen passphrase"
	held := holdWrite(t, server, http.MethodPut, "/api/auth/password", `{"password":"`+attacker+`"}`)
	enableBasicByAPI(t, server)
	if status := held.release(); status != http.StatusConflict {
		t.Fatalf("held password change: status %d, want 409", status)
	}
	if got := callAs(server.Server, http.MethodGet, "/api/counts", testUsername, testPassword); got.Code != http.StatusOK {
		t.Fatalf("the operator's password was replaced: %d", got.Code)
	}
	if got := callAs(server.Server, http.MethodGet, "/api/counts", testUsername, attacker); got.Code != http.StatusUnauthorized {
		t.Fatalf("the attacker's password works: %d", got.Code)
	}
}

func TestAuthTransition_AHeldDisableDoesNotReopenTheInterface(t *testing.T) {
	server := newTestServer(t)
	held := holdWrite(t, server, http.MethodPost, "/api/auth/actions/disable", `{}`)
	enableBasicByAPI(t, server)
	if status := held.release(); status != http.StatusConflict {
		t.Fatalf("held disable: status %d, want 409", status)
	}
	if got := callAs(server.Server, http.MethodGet, "/api/counts", "", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("the interface was reopened: %d", got.Code)
	}
}

func TestAuthTransition_AHeldTokenRequestMintsNothingAfterTheActivation(t *testing.T) {
	server := newTestServer(t)
	server.enableMCP(t)
	held := holdWrite(t, server, http.MethodPost, "/api/mcp/tokens", `{"name":"held","scope":"write","expires_in_days":0}`)
	enableBasicByAPI(t, server)
	if status := held.release(); status != http.StatusConflict {
		t.Fatalf("held token request: status %d, want 409", status)
	}
	tokens, err := server.access.APITokens(context.Background())
	if err != nil || len(tokens) != 0 {
		t.Fatalf("tokens after the activation: %+v %v", tokens, err)
	}
}

// Toute écriture, pas seulement celles qui accordent une autorité : admise
// sous l'interface ouverte, elle ne s'applique pas sous l'interface gardée.
func TestAuthTransition_AnyHeldWriteIsRefusedAfterTheActivation(t *testing.T) {
	server := newTestServer(t)
	held := holdWrite(t, server, http.MethodPut, "/api/session/language", `{"language":"en"}`)
	enableBasicByAPI(t, server)
	if status := held.release(); status != http.StatusConflict {
		t.Fatalf("held write: status %d, want 409", status)
	}
	var session sessionResponse
	decodeAPI(t, callAs(server.Server, http.MethodGet, "/api/session", testUsername, testPassword), &session)
	if session.Language == "en" {
		t.Fatal("the held write took effect")
	}
}

// Face à une interface gardée, un anonyme est refusé sur ses en-têtes :
// requireOperator ne lit pas son corps, et il ne peut pas faire tenir au
// serveur un Mio par connexion.
func TestRequireOperator_DoesNotReadTheBodyOfAnAnonymousWrite(t *testing.T) {
	server := newTestServer(t)
	server.enableBasic(t)
	reading := make(chan struct{})
	body := &signalingBody{ReadCloser: io.NopCloser(strings.NewReader(`{"password":"attacker chosen passphrase"}`)), reading: reading}
	request := httptest.NewRequest(http.MethodPut, "/api/auth/password", body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", recorder.Code)
	}
	select {
	case <-reading:
		t.Fatal("the body of an anonymous write was read")
	default:
	}
}

// Un corps trop gros n'est pas jugé par requireOperator : sans identifiants
// c'est toujours un 401, avec les bons c'est la réponse du handler.
func TestRequireOperator_LeavesAnOversizedBodyToTheHandler(t *testing.T) {
	server := newTestServer(t)
	server.enableBasic(t)
	put := func(authenticated bool) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPut, "/api/auth/password", strings.NewReader(strings.Repeat("a", maxRequestBody+1)))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		if authenticated {
			request.SetBasicAuth(testUsername, testPassword)
		}
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		return recorder
	}
	if got := put(false); got.Code != http.StatusUnauthorized {
		t.Fatalf("oversized body without credentials: %d %s", got.Code, got.Body.String())
	}
	if got := put(true); got.Code != http.StatusBadRequest || errorCode(t, got) != codeBadJSON {
		t.Fatalf("oversized body with credentials: %d %s", got.Code, got.Body.String())
	}
}

// staleWrite prépare une écriture autorisée sous l'état d'authentification
// d'avant : change fait passer l'état d'après, puis le handler la reçoit,
// sans repasser par requireOperator, comme si elle avait traîné entre les
// deux.
func staleWrite(t *testing.T, server *testServer, handler http.HandlerFunc, method, path, body, id string, change func()) *httptest.ResponseRecorder {
	t.Helper()
	authorizing, err := server.operator.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(context.WithValue(request.Context(), authorizingStateKey{}, authorizing))
	if id != "" {
		request.SetPathValue("id", id)
	}
	change()
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

func requireStateChanged(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if recorder.Code != http.StatusConflict || errorCode(t, recorder) != codeAuthStateChanged {
		t.Fatalf("status %d %s, want 409 %s", recorder.Code, recorder.Body.String(), codeAuthStateChanged)
	}
}

func TestAuthTransition_WritesThatGrantLastingAuthorityAreRefusedUnderAChangedState(t *testing.T) {
	ctx := context.Background()

	t.Run("mcp token", func(t *testing.T) {
		server := newTestServer(t)
		server.enableMCP(t)
		requireStateChanged(t, staleWrite(t, server, server.grantsAuthority(server.createMCPToken), http.MethodPost, "/api/mcp/tokens", `{"name":"x","scope":"write"}`, "", func() { server.enableBasic(t) }))
		if tokens, _ := server.access.APITokens(ctx); len(tokens) != 0 {
			t.Fatalf("a token was minted: %+v", tokens)
		}
	})
	t.Run("mcp secret", func(t *testing.T) {
		server := newTestServer(t)
		secret := server.enableMCP(t)
		requireStateChanged(t, staleWrite(t, server, server.grantsAuthority(server.regenerateMCPSecret), http.MethodPost, "/api/mcp/actions/regenerate-secret", ``, "", func() { server.enableBasic(t) }))
		client, _, err := server.access.Client(ctx)
		if err != nil || !strings.HasPrefix(secret, client.SecretPrefix) {
			t.Fatalf("the secret was regenerated: %+v %v", client, err)
		}
	})
	t.Run("mcp redirect uris", func(t *testing.T) {
		server := newTestServer(t)
		server.enableMCP(t)
		requireStateChanged(t, staleWrite(t, server, server.grantsAuthority(server.setMCPRedirectURIs), http.MethodPut, "/api/mcp/redirect-uris", `{"redirect_uris":["http://localhost:1/cb"]}`, "", func() { server.enableBasic(t) }))
		if client, _, _ := server.access.Client(ctx); len(client.RedirectURIs) != 0 {
			t.Fatalf("a redirect uri was added: %+v", client.RedirectURIs)
		}
	})
	t.Run("mcp enable", func(t *testing.T) {
		server := newTestServer(t)
		requireStateChanged(t, staleWrite(t, server, server.grantsAuthority(server.Server.enableMCP), http.MethodPost, "/api/mcp/actions/enable", ``, "", func() { server.enableBasic(t) }))
		if _, enabled, _ := server.access.Client(ctx); enabled {
			t.Fatal("MCP was enabled")
		}
	})
	t.Run("machine token", func(t *testing.T) {
		server := newTestServer(t)
		requireStateChanged(t, staleWrite(t, server, server.grantsAuthority(server.createMachineToken), http.MethodPost, "/api/machines/tokens", `{"name":"web"}`, "", func() { server.enableBasic(t) }))
		if pending, _ := server.machines.PendingTokens(ctx); len(pending) != 0 {
			t.Fatalf("an enrollment token was created: %+v", pending)
		}
	})
	t.Run("oauth code", func(t *testing.T) {
		server := newTestServer(t)
		server.enableMCP(t)
		authorize := mcpAuthorizePath + "?response_type=code&client_id=opencloud&redirect_uri=" + localCallback + "&code_challenge=" + pkceChallenge + "&code_challenge_method=S256"
		requireStateChanged(t, staleWrite(t, server, server.grantsAuthority(server.oauthAuthorize), http.MethodGet, authorize, ``, "", func() { server.enableBasic(t) }))
	})
	t.Run("reenroll", func(t *testing.T) {
		server := newTestServer(t)
		server.enroll(t, "web", remoteID)
		requireStateChanged(t, staleWrite(t, server, server.grantsAuthority(server.reenrollMachine), http.MethodPost, "/api/machines/{id}/actions/reenroll", ``, remoteID, func() { server.enableBasic(t) }))
		if pending, _ := server.machines.PendingTokens(ctx); len(pending) != 0 {
			t.Fatalf("an enrollment token was created: %+v", pending)
		}
	})
}

func TestAuthTransition_PasswordDisableAndEnableAreRefusedUnderAChangedState(t *testing.T) {
	ctx := context.Background()
	changePassword := func(server *testServer) func() {
		return func() {
			current, err := server.operator.State(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := server.operator.ChangePassword(ctx, current, "the operator's new passphrase"); err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("password", func(t *testing.T) {
		server := newTestServer(t)
		server.enableBasic(t)
		requireStateChanged(t, staleWrite(t, server, server.changeAuthPassword, http.MethodPut, "/api/auth/password", `{"password":"attacker chosen passphrase"}`, "", changePassword(server)))
		if got := callAs(server.Server, http.MethodGet, "/api/counts", testUsername, "the operator's new passphrase"); got.Code != http.StatusOK {
			t.Fatalf("the operator's new password was replaced: %d", got.Code)
		}
	})
	t.Run("disable", func(t *testing.T) {
		server := newTestServer(t)
		server.enableBasic(t)
		requireStateChanged(t, staleWrite(t, server, server.disableAuth, http.MethodPost, "/api/auth/actions/disable", `{}`, "", changePassword(server)))
		if state, _ := server.operator.State(ctx); state.Mode != auth.ModeBasic {
			t.Fatalf("the interface was reopened: %+v", state)
		}
	})
	t.Run("enable", func(t *testing.T) {
		server := newTestServer(t)
		// Autorisée sous « none », arrivée sous forward : un autre état que le sien.
		recorder := staleWrite(t, server, server.enableAuth, http.MethodPost, "/api/auth/actions/enable", `{"username":"lucas","password":"correct horse battery"}`, "", func() {
			if _, err := server.operator.EnableForward(ctx, auth.State{Mode: auth.ModeNone}, auth.DefaultForwardHeader); err != nil {
				t.Fatal(err)
			}
		})
		if recorder.Code != http.StatusConflict {
			t.Fatalf("enable under a changed state: %d %s", recorder.Code, recorder.Body.String())
		}
		if state, _ := server.operator.State(ctx); state.Mode != auth.ModeForward {
			t.Fatalf("the refused activation replaced the state: %+v", state)
		}
	})
}

// Deux activations parties du même état : une seule gagne, l'autre est
// refusée, et l'état stocké est celui de la gagnante.
func TestAuthTransition_OnlyOneOfTwoConcurrentActivationsWins(t *testing.T) {
	server := newTestServer(t)
	ctx := context.Background()
	none := auth.State{Mode: auth.ModeNone}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, username := range []string{"first", "second"} {
		go func() {
			<-start
			_, err := server.operator.EnableBasic(ctx, none, username, testPassword)
			results <- err
		}()
	}
	close(start)
	var wins, refused int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			wins++
		case errors.Is(err, auth.ErrAlreadyEnabled) || errors.Is(err, auth.ErrStateChanged):
			refused++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if wins != 1 || refused != 1 {
		t.Fatalf("wins %d, refused %d, want 1 and 1", wins, refused)
	}
}

// L'activation attend qu'une écriture qui accorde une autorité, admise sous
// l'ancien état, ait fini d'écrire : la révocation qui suit l'emporte au lieu
// de la manquer.
func TestAuthTransition_TheActivationWaitsForAnAuthorityBeingGranted(t *testing.T) {
	server := newTestServer(t)
	server.enableMCP(t)
	ctx := context.Background()
	entered, proceed := make(chan struct{}), make(chan struct{})
	mint := server.grantsAuthority(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-proceed
		server.createMCPToken(w, r)
	})
	authorizing, err := server.operator.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/mcp/tokens", strings.NewReader(`{"name":"late","scope":"write"}`))
	request = request.WithContext(context.WithValue(request.Context(), authorizingStateKey{}, authorizing))
	minted := make(chan *httptest.ResponseRecorder)
	go func() {
		recorder := httptest.NewRecorder()
		mint(recorder, request)
		minted <- recorder
	}()
	<-entered

	enabled := make(chan int)
	go func() {
		enabled <- callAPI(server.Server, http.MethodPost, "/api/auth/actions/enable", authEnableRequest{Username: testUsername, Password: testPassword}).Code
	}()
	// L'activation est en attente du verrou dès qu'un lecteur de plus ne
	// peut plus le prendre.
	deadline := time.Now().Add(5 * time.Second)
	for server.authTransition.TryRLock() {
		server.authTransition.RUnlock()
		if time.Now().After(deadline) {
			t.Fatal("the activation did not wait for the authority being granted")
		}
		runtime.Gosched()
	}
	close(proceed)

	var created mcpTokenResponse
	decodeAPI(t, <-minted, &created)
	if code := <-enabled; code != http.StatusOK {
		t.Fatalf("enable: %d", code)
	}
	if _, err := server.access.Verify(ctx, created.Token); err == nil {
		t.Fatal("a token granted during the activation survived it")
	}
}

// --- OAuth : les codes en attente tombent avec les jetons ---

// pendingCode tire un code d'autorisation comme un client MCP, sans l'échanger.
func pendingCode(t *testing.T, server *testServer) string {
	t.Helper()
	got := get(server.Server, "/oauth/authorize?response_type=code&client_id=opencloud&redirect_uri="+localCallback+"&code_challenge="+pkceChallenge+"&code_challenge_method=S256")
	if got.Code != http.StatusFound {
		t.Fatalf("authorize: %d %s", got.Code, got.Body.String())
	}
	location := got.Header().Get("Location")
	code, _, _ := strings.Cut(location[strings.Index(location, "code=")+5:], "&")
	return code
}

func exchangeCode(server *testServer, secret, code string) *httptest.ResponseRecorder {
	return postForm(server.Server, "/oauth/token", map[string]string{
		"grant_type": "authorization_code", "client_id": "opencloud", "client_secret": secret,
		"code": code, "redirect_uri": localCallback, "code_verifier": pkceVerifier,
	}, [2]string{})
}

func TestAuthTransition_ACodeMintedWhileOpenIsNotExchangeableAfterTheActivation(t *testing.T) {
	server := newTestServer(t)
	secret := server.enableMCP(t)
	code := pendingCode(t, server)
	enableBasicByAPI(t, server)
	got := exchangeCode(server, secret, code)
	if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "invalid_grant") {
		t.Fatalf("exchange after the activation: %d %s", got.Code, got.Body.String())
	}
}

func TestAuthTransition_RegeneratingTheSecretCutsThePendingCodes(t *testing.T) {
	server := newTestServer(t)
	server.enableMCP(t)
	code := pendingCode(t, server)
	var renewed mcpSecretResponse
	decodeAPI(t, callAPI(server.Server, http.MethodPost, "/api/mcp/actions/regenerate-secret", nil), &renewed)
	got := exchangeCode(server, renewed.Secret, code)
	if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "invalid_grant") {
		t.Fatalf("exchange after the regeneration: %d %s", got.Code, got.Body.String())
	}
}

// --- Les flux ouverts avant l'activation ---

// Un flux de l'opérateur se ferme dès que l'état d'authentification n'est
// plus celui qui l'a autorisé ; ceux qui n'en ont pas (la page de statut) ne
// sont jamais concernés. La boucle des flux de l'administration appelle ce
// jugement à chaque ping ; celle de la page publique n'en a pas.
func TestAuthTransition_AStreamLapsesWhenTheAuthenticationChanges(t *testing.T) {
	server := newTestServer(t)
	opened := func() *http.Request {
		authorizing, err := server.operator.State(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodGet, "/api/events", nil)
		return request.WithContext(context.WithValue(request.Context(), authorizingStateKey{}, authorizing))
	}
	stream, publicStream := opened(), httptest.NewRequest(http.MethodGet, "/statut/api/events", nil)
	if server.authorizationLapsed(stream) || server.authorizationLapsed(publicStream) {
		t.Fatal("a stream lapsed with nothing changed")
	}
	server.enableBasic(t)
	if !server.authorizationLapsed(stream) {
		t.Fatal("a stream opened while the interface was open outlived the activation")
	}
	if server.authorizationLapsed(publicStream) {
		t.Fatal("the public stream is not the operator's")
	}
	if server.authorizationLapsed(opened()) {
		t.Fatal("a stream opened under the current state lapsed")
	}
}
