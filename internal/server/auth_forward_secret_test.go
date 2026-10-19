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

	"github.com/ldesfontaine/opencloud/internal/auth"
	"github.com/ldesfontaine/opencloud/internal/brand"
)

const (
	loopbackPeer = "127.0.0.1"
	// Le secret que Traefik poserait, et un autre de même taille et de taille
	// différente pour les refus.
	proxySecret      = "9f2c61d0b7a84e35c1f06d98ab3e7742d5c8e1f0a6b93d247e5c0f81a2b6d4c3" // gitleaks:allow -- faux secret de test
	wrongProxySecret = "0000000000000000000000000000000000000000000000000000000000000000" // gitleaks:allow -- faux secret de test
	shortProxySecret = "9f2c61d0b7a84e35"                                                 // gitleaks:allow -- faux secret de test
)

// enableForwardOnLoopback : Traefik en mode host, tel que la doc le montre.
func (ts *testServer) enableForwardOnLoopback(t *testing.T, secret string) {
	t.Helper()
	ts.trustedProxies = parsePrefixes(t, "127.0.0.1/32", "::1/128")
	ts.forwardSecret = secret
	if _, err := ts.operator.EnableForward(context.Background(), auth.DefaultForwardHeader); err != nil {
		t.Fatal(err)
	}
}

// callProxied joue une requête depuis peer avec l'identité et les valeurs du
// secret données (aucune, une ou plusieurs).
func callProxied(server *Server, method, path, peer, identity string, secrets ...string) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(context.Background(), method, path, nil)
	request.RemoteAddr = net.JoinHostPort(peer, "4000")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	if identity != "" {
		request.Header.Set(auth.DefaultForwardHeader, identity)
	}
	for _, secret := range secrets {
		request.Header.Add(brand.ProxySecretHeader, secret)
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

func TestAuthForwardSecret_ALoopbackPeerMustProveItWentThroughTheProxy(t *testing.T) {
	server := newTestServer(t)
	server.enableForwardOnLoopback(t, proxySecret)

	cases := []struct {
		name    string
		peer    string
		secrets []string
		want    int
	}{
		{"loopback, identity, no secret", loopbackPeer, nil, http.StatusUnauthorized},
		{"loopback, identity, wrong secret", loopbackPeer, []string{wrongProxySecret}, http.StatusUnauthorized},
		{"loopback, identity, secret of another size", loopbackPeer, []string{shortProxySecret}, http.StatusUnauthorized},
		{"loopback, identity, empty secret", loopbackPeer, []string{""}, http.StatusUnauthorized},
		{"loopback, identity, secret sent twice", loopbackPeer, []string{proxySecret, proxySecret}, http.StatusUnauthorized},
		{"loopback, identity, right secret after a wrong one", loopbackPeer, []string{wrongProxySecret, proxySecret}, http.StatusUnauthorized},
		{"loopback, identity, right secret", loopbackPeer, []string{proxySecret}, http.StatusOK},
		{"IPv6 loopback, identity, right secret", "::1", []string{proxySecret}, http.StatusOK},
		{"IPv6 loopback, identity, no secret", "::1", nil, http.StatusUnauthorized},
		// Une socket double pile rend 127.0.0.1 sous sa forme IPv6.
		{"IPv4-mapped loopback, identity, no secret", "::ffff:127.0.0.1", nil, http.StatusUnauthorized},
		{"IPv4-mapped loopback, identity, right secret", "::ffff:127.0.0.1", []string{proxySecret}, http.StatusOK},
		{"untrusted peer, identity, right secret", untrustedPeer, []string{proxySecret}, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		got := callProxied(server.Server, http.MethodGet, "/api/counts", tc.peer, testUsername, tc.secrets...)
		if got.Code != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, got.Code, tc.want)
		}
	}
	// Le bon secret ne tient pas lieu d'identité.
	if got := callProxied(server.Server, http.MethodGet, "/api/counts", loopbackPeer, "", proxySecret); got.Code != http.StatusUnauthorized {
		t.Errorf("right secret without identity: status %d, want 401", got.Code)
	}
	// Les pages du front passent par la même garde.
	if got := callProxied(server.Server, http.MethodGet, "/machines", loopbackPeer, testUsername); got.Code != http.StatusUnauthorized {
		t.Errorf("page without secret: status %d, want 401", got.Code)
	}
	if got := callProxied(server.Server, http.MethodGet, "/machines", loopbackPeer, testUsername, proxySecret); got.Code != http.StatusOK {
		t.Errorf("page with the secret: status %d, want 200", got.Code)
	}
}

func TestAuthForwardSecret_ARefusalNeverLogsTheValueReceived(t *testing.T) {
	server := newTestServer(t)
	server.enableForwardOnLoopback(t, proxySecret)
	var logs bytes.Buffer
	server.logger = slog.New(slog.NewTextHandler(&logs, nil))

	callProxied(server.Server, http.MethodGet, "/api/counts", loopbackPeer, testUsername, wrongProxySecret)
	callProxied(server.Server, http.MethodGet, "/api/counts", loopbackPeer, testUsername, proxySecret[:len(proxySecret)-1])

	if !strings.Contains(logs.String(), "proxy secret missing or wrong") {
		t.Fatalf("a refusal must be logged: %q", logs.String())
	}
	for _, leaked := range []string{wrongProxySecret, proxySecret, proxySecret[:len(proxySecret)-1]} {
		if strings.Contains(logs.String(), leaked) {
			t.Fatalf("the log holds a secret value: %q", logs.String())
		}
	}
}

// Le secret a servi à la porte : aucun gestionnaire ne le voit, pas plus que
// la réponse.
func TestAuthForwardSecret_IsRemovedFromTheRequestBeforeTheHandlers(t *testing.T) {
	server := newTestServer(t)
	server.enableForwardOnLoopback(t, proxySecret)

	var seen []string
	reached := false
	handler := server.requireOperator(func(_ http.ResponseWriter, r *http.Request) {
		reached = true
		seen = r.Header.Values(brand.ProxySecretHeader)
	})
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/counts", nil)
	request.RemoteAddr = net.JoinHostPort(loopbackPeer, "4000")
	request.Header.Set(auth.DefaultForwardHeader, testUsername)
	request.Header.Set(brand.ProxySecretHeader, proxySecret)
	recorder := httptest.NewRecorder()
	handler(recorder, request)

	if !reached {
		t.Fatalf("the request must pass: %d", recorder.Code)
	}
	if len(seen) != 0 {
		t.Fatalf("the handler still sees the secret header")
	}
	if got := recorder.Header().Get(brand.ProxySecretHeader); got != "" {
		t.Fatalf("the response carries the secret header: %q", got)
	}
}

func TestAuthForwardSecret_WithoutASecretANonLoopbackProxyKeepsItsBehavior(t *testing.T) {
	server := newTestServer(t)
	server.enableForward(t) // 192.0.2.0/24, aucun secret
	if got := callForwarded(server.Server, http.MethodGet, "/api/counts", trustedPeer, testUsername); got.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", got.Code)
	}
	// Un secret envoyé pour rien n'est pas une erreur.
	if got := callProxied(server.Server, http.MethodGet, "/api/counts", trustedPeer, testUsername, wrongProxySecret); got.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", got.Code)
	}
}

func TestAuthForwardSecret_AConfiguredSecretIsRequiredFromAnyTrustedProxy(t *testing.T) {
	server := newTestServer(t)
	server.enableForward(t)
	server.forwardSecret = proxySecret
	if got := callForwarded(server.Server, http.MethodGet, "/api/counts", trustedPeer, testUsername); got.Code != http.StatusUnauthorized {
		t.Fatalf("without the secret: status %d, want 401", got.Code)
	}
	if got := callProxied(server.Server, http.MethodGet, "/api/counts", trustedPeer, testUsername, proxySecret); got.Code != http.StatusOK {
		t.Fatalf("with the secret: status %d, want 200", got.Code)
	}
}

func TestAuthAPI_EnableForwardRefusedOnALoopbackProxyWithoutASecret(t *testing.T) {
	const enable = "/api/auth/actions/enable"
	for _, trusted := range [][]string{
		{"127.0.0.1/32"},
		{"::1/128"},
		{"127.0.0.0/8"},
		{"10.0.0.0/8", "127.0.0.1/32"},
		{"0.0.0.0/0"},
		{"::/0"},
	} {
		server := newTestServer(t)
		server.trustedProxies = parsePrefixes(t, trusted...)
		got := callForwardedJSON(server.Server, http.MethodPost, enable, `{"mode":"forward"}`, loopbackPeer, auth.DefaultForwardHeader, testUsername)
		if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "auth.forward_secret_required" {
			t.Errorf("%v: %d %s", trusted, got.Code, got.Body.String())
		}
		state, err := server.operator.State(context.Background())
		if err != nil || state.Mode != auth.ModeNone {
			t.Errorf("%v: the mode must stay none, got %q (%v)", trusted, state.Mode, err)
		}
	}
}

func TestAuthAPI_EnableForwardOnALoopbackProxyWorksWithTheSecret(t *testing.T) {
	server := newTestServer(t)
	server.trustedProxies = parsePrefixes(t, "127.0.0.1/32", "::1/128")
	server.forwardSecret = proxySecret
	const enable = "/api/auth/actions/enable"

	post := func(secret string) *httptest.ResponseRecorder {
		request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, enable, strings.NewReader(`{"mode":"forward"}`))
		request.RemoteAddr = net.JoinHostPort(loopbackPeer, "4000")
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(auth.DefaultForwardHeader, testUsername)
		if secret != "" {
			request.Header.Set(brand.ProxySecretHeader, secret)
		}
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		return recorder
	}
	if got := post(""); got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "auth.forward_header_missing" {
		t.Fatalf("without the secret: %d %s", got.Code, got.Body.String())
	}
	if got := post(wrongProxySecret); got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "auth.forward_header_missing" {
		t.Fatalf("with a wrong secret: %d %s", got.Code, got.Body.String())
	}
	if got := post(proxySecret); got.Code != http.StatusOK {
		t.Fatalf("with the secret: %d %s", got.Code, got.Body.String())
	}
}

// Une installation qui tourne déjà en forward sur la boucle locale, sans
// secret : l'accès est gardé, la page des réglages le dit.
func TestAuthAPI_ForwardAlreadyActiveWithoutASecretKeepsAccessAndSaysItIsInsecure(t *testing.T) {
	server := newTestServer(t)
	server.enableForwardOnLoopback(t, "")

	if got := callProxied(server.Server, http.MethodGet, "/api/counts", loopbackPeer, testUsername); got.Code != http.StatusOK {
		t.Fatalf("access must be kept: %d", got.Code)
	}
	read := callProxied(server.Server, http.MethodGet, "/api/auth", loopbackPeer, testUsername)
	if read.Code != http.StatusOK {
		t.Fatalf("read auth: %d", read.Code)
	}
	var state struct {
		Mode            string `json:"mode"`
		ForwardInsecure bool   `json:"forward_insecure"`
	}
	decodeAPI(t, read, &state)
	if state.Mode != "forward" || !state.ForwardInsecure {
		t.Fatalf("want forward_insecure, got %+v (%s)", state, read.Body.String())
	}
}

func TestAuthAPI_ForwardInsecureIsAbsentWhenSomethingProvesTheProxy(t *testing.T) {
	withSecret := newTestServer(t)
	withSecret.enableForwardOnLoopback(t, proxySecret)
	container := newTestServer(t)
	container.enableForward(t)
	basic := newTestServer(t)
	basic.enableBasic(t)

	for name, read := range map[string]*httptest.ResponseRecorder{
		"loopback with a secret":     callProxied(withSecret.Server, http.MethodGet, "/api/auth", loopbackPeer, testUsername, proxySecret),
		"container proxy, no secret": callForwarded(container.Server, http.MethodGet, "/api/auth", trustedPeer, testUsername),
		"basic mode":                 callAs(basic.Server, http.MethodGet, "/api/auth", testUsername, testPassword),
	} {
		if read.Code != http.StatusOK {
			t.Errorf("%s: %d %s", name, read.Code, read.Body.String())
			continue
		}
		if strings.Contains(read.Body.String(), "forward_insecure") {
			t.Errorf("%s: forward_insecure must be absent: %s", name, read.Body.String())
		}
	}
}

func TestTrustsLoopback(t *testing.T) {
	for cidr, want := range map[string]bool{
		"127.0.0.1/32":  true,
		"127.0.0.0/8":   true,
		"::1/128":       true,
		"0.0.0.0/0":     true,
		"::/0":          true,
		"64.0.0.0/2":    true,
		"10.0.0.0/8":    false,
		"172.18.0.2/32": false,
		"192.0.2.0/24":  false,
		"126.0.0.0/8":   false,
		"128.0.0.0/1":   false,
		"fd00::/8":      false,
		"::2/128":       false,
		// Le pair est démappé avant la comparaison : ce préfixe ne couvre
		// aucun pair, il n'y a rien à prouver.
		"::ffff:127.0.0.1/128": false,
	} {
		if got := TrustsLoopback(parsePrefixes(t, cidr)); got != want {
			t.Errorf("%s: %v, want %v", cidr, got, want)
		}
	}
	if TrustsLoopback(nil) {
		t.Error("no proxy at all trusts no loopback")
	}
	if !TrustsLoopback(parsePrefixes(t, "10.0.0.0/8", "127.0.0.1/32")) {
		t.Error("one loopback prefix among others must be seen")
	}
}
