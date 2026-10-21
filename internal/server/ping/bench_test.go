package ping

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
	"github.com/ldesfontaine/opencloud/internal/server/servertest"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

// Le banc des pings : le vrai composant heartbeat sur une vraie base SQLite
// temporaire, et les routes montées sur un mux comme la racine les monte,
// sans la chaîne de middlewares (server la teste de bout en bout).
type bench struct {
	http.Handler
	handler    *Handler
	heartbeats *heartbeat.Service
	db         *store.DB
}

func newBench(t *testing.T) *bench {
	t.Helper()
	db := storetest.Open(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	clock := servertest.Now
	heartbeats := heartbeat.New(db, logger)
	heartbeats.SetClock(clock)
	handler := New(Options{Logger: logger, Heartbeats: heartbeats, ClientAddress: httpx.PeerHost, Clock: clock})
	mux := http.NewServeMux()
	handler.Routes(func(method, pattern string, route http.HandlerFunc) {
		mux.HandleFunc(method+" "+pattern, route)
	})
	return &bench{Handler: mux, handler: handler, heartbeats: heartbeats, db: db}
}

func (b *bench) createJob(t *testing.T, name string) heartbeat.Heartbeat {
	t.Helper()
	created, err := b.heartbeats.Create(context.Background(), heartbeat.Definition{Name: name, Interval: 5 * time.Minute, Grace: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

// send joue un ping ; peer vide, l'adresse par défaut des requêtes de test.
func (b *bench) send(method, path, body, peer string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if peer != "" {
		request.RemoteAddr = peer
	}
	recorder := httptest.NewRecorder()
	b.ServeHTTP(recorder, request)
	return recorder
}

// sendFrom joue un ping depuis une adresse de pair donnée et rend son statut.
func (b *bench) sendFrom(peer, path string) int {
	return b.send(http.MethodGet, path, "", peer).Code
}

// source donne une adresse de pair différente par i.
func source(i int) string {
	return "203.0.113." + strconv.Itoa(i+1) + ":4000"
}
