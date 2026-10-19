package main

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Un flux en direct ouvert n'empêche plus l'arrêt : il finit avec le
// contexte de base, et Shutdown revient vite, sans erreur.
func TestNewHTTPServer_OpenStream_ShutsDownQuickly(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	streamEnded := make(chan struct{})
	stream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(streamEnded)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(": open\n\n"))
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
	})
	httpServer := newHTTPServer(listener.Addr().String(), stream)
	served := make(chan error, 1)
	go func() { served <- httpServer.Serve(listener) }()

	response, err := http.Get("http://" + listener.Addr().String() + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := bufio.NewReader(response.Body).ReadString('\n'); err != nil {
		t.Fatalf("le flux ne s'est pas ouvert: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	startedAt := time.Now()
	if err := httpServer.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed > 2*time.Second {
		t.Fatalf("shutdown a attendu le flux: %s", elapsed)
	}
	<-streamEnded
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("serve: %v", err)
	}
}

// Les en-têtes sont bornés : un anonyme ne pose plus une URL d'un mégaoctet
// sur /ping, mais un en-tête de taille ordinaire passe.
func TestNewHTTPServer_OversizedHeaders_AreRefused(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	reached := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	httpServer := newHTTPServer(listener.Addr().String(), reached)
	go func() { _ = httpServer.Serve(listener) }()
	defer httpServer.Close()

	get := func(headerBytes int) int {
		request, err := http.NewRequest(http.MethodGet, "http://"+listener.Addr().String()+"/ping/hb_x", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-Padding", strings.Repeat("a", headerBytes))
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		return response.StatusCode
	}
	if got := get(8 << 10); got != http.StatusNoContent {
		t.Fatalf("8 KiB of headers: status %d", got)
	}
	if got := get(100 << 10); got != http.StatusRequestHeaderFieldsTooLarge {
		t.Fatalf("100 KiB of headers: status %d, want 431", got)
	}
}
