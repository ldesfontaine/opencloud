package server

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Un jeton MCP tiré avant la commande de secours tombe à l'activation qui
// suit. Un appel d'outil dont le corps a été retenu à travers elle ne
// s'applique pas non plus : le Bearer se rejuge une fois le corps arrivé.
func TestAuthTransition_AHeldMCPCallDoesNotOutliveTheActivation(t *testing.T) {
	server := newTestServer(t)
	server.enableMCP(t)
	token := server.createAPIToken(t, "ouvert")

	reading := make(chan struct{})
	listener := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-Held") != "" {
			r.Body = &signalingBody{ReadCloser: r.Body, reading: reading}
		}
		server.ServeHTTP(w, r)
	}))
	listener.Config.ReadHeaderTimeout = 10 * time.Second
	listener.Start()
	t.Cleanup(listener.Close)

	session := openMCPSession(t, listener.URL+mcpPath, token)

	conn, err := net.Dial("tcp", listener.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	body := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_jobs","arguments":{}}}`
	headers := fmt.Sprintf("POST %s HTTP/1.1\r\nHost: example.com\r\nAuthorization: Bearer %s\r\nMcp-Session-Id: %s\r\nAccept: application/json, text/event-stream\r\nContent-Type: application/json\r\nX-Test-Held: 1\r\nContent-Length: %d\r\n\r\n", mcpPath, token, session, len(body))
	if _, err := conn.Write([]byte(headers)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reading:
	case <-time.After(5 * time.Second):
		t.Fatal("the server never started reading the held body")
	}

	server.rescue(t)
	enableBasicByAPI(t, server)

	if _, err := conn.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("held tool call: status %d, want 401", response.StatusCode)
	}
}

// openMCPSession fait l'initialisation MCP à la main et rend l'identifiant de
// session.
func openMCPSession(t *testing.T, endpoint, token string) string {
	t.Helper()
	post := func(session, payload string) *http.Response {
		request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewBufferString(payload))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("Content-Type", "application/json")
		if session != "" {
			request.Header.Set("Mcp-Session-Id", session)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	initialized := post("", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`)
	initialized.Body.Close()
	session := initialized.Header.Get("Mcp-Session-Id")
	if initialized.StatusCode != http.StatusOK || session == "" {
		t.Fatalf("initialize: %d, session %q", initialized.StatusCode, session)
	}
	notified := post(session, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	notified.Body.Close()
	if notified.StatusCode >= 300 {
		t.Fatalf("initialized: %d", notified.StatusCode)
	}
	return session
}

// Sans Bearer valide, /mcp refuse sur les en-têtes : le corps n'est pas lu,
// un anonyme ne fait pas tenir un Mio par connexion au serveur.
func TestServeMCP_DoesNotReadTheBodyWithoutAValidBearer(t *testing.T) {
	server := newTestServer(t)
	server.enableMCP(t)
	for name, bearer := range map[string]string{"absent": "", "inconnu": "Bearer oc_inconnu"} {
		reading := make(chan struct{})
		body := &signalingBody{ReadCloser: io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)), reading: reading}
		request := httptest.NewRequest(http.MethodPost, mcpPath, body)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		if bearer != "" {
			request.Header.Set("Authorization", bearer)
		}
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status %d, want 401", name, recorder.Code)
		}
		select {
		case <-reading:
			t.Fatalf("%s: the body of an anonymous request was read", name)
		default:
		}
	}
}
