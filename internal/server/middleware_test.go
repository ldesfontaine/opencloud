package server

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var requestIDValue = regexp.MustCompile(`^[0-9a-f]{16}$`)

func TestRequestID_IsGeneratedNeverCopiedFromTheClient(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	request.Header.Set(requestIDHeader, "chosen-by-the-client")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if got := recorder.Header().Get(requestIDHeader); !requestIDValue.MatchString(got) {
		t.Fatalf("request id %q", got)
	}
}

// Une panique devient un 500 journalisé avec l'identifiant : JSON sous
// /api, texte ailleurs. Le processus continue.
func TestRecoverPanic_Answers500AndLogsTheRequestID(t *testing.T) {
	var journal bytes.Buffer
	server := newTestServer(t)
	server.logger = slog.New(slog.NewTextHandler(&journal, nil))
	boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })
	handler := server.chain(boom)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/machines", nil))
	if recorder.Code != http.StatusInternalServerError || errorCode(t, recorder) != codeInternal {
		t.Fatalf("api: %d %s", recorder.Code, recorder.Body.String())
	}
	id := recorder.Header().Get(requestIDHeader)
	if !strings.Contains(journal.String(), "panic") || !strings.Contains(journal.String(), id) {
		t.Fatalf("journal lacks the panic or its request id:\n%s", journal.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/machines", nil))
	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), panicResponseTitle) {
		t.Fatalf("page: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestLogRequest_DebugByDefaultWarnOn500(t *testing.T) {
	var journal bytes.Buffer
	server := newTestServer(t)
	server.logger = slog.New(slog.NewTextHandler(&journal, &slog.HandlerOptions{Level: slog.LevelDebug}))
	answer := http.StatusOK
	handler := server.chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(answer) }))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/session", nil))
	if !strings.Contains(journal.String(), "level=DEBUG") || !strings.Contains(journal.String(), "status=200") {
		t.Fatalf("journal:\n%s", journal.String())
	}
	journal.Reset()
	answer = http.StatusBadGateway
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/session", nil))
	if !strings.Contains(journal.String(), "level=WARN") || !strings.Contains(journal.String(), "status=502") {
		t.Fatalf("journal:\n%s", journal.String())
	}
}

func TestLimitBody_StopsAtOneMebibyte(t *testing.T) {
	server := newTestServer(t)
	var readErr error
	handler := server.chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	body := strings.NewReader(strings.Repeat("x", maxRequestBody+1))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/ping/x", body))
	var tooLarge *http.MaxBytesError
	if readErr == nil || !strings.Contains(readErr.Error(), "request body too large") {
		t.Fatalf("read error %v (want %T)", readErr, tooLarge)
	}
}

// Le piège classique : un middleware qui capture le statut cache le
// Flusher, et les deux flux SSE meurent en silence. Ici il le relaie.
func TestChain_KeepsTheFlusherForBothStreams(t *testing.T) {
	server := newTestServer(t)
	var flushErr error = io.ErrUnexpectedEOF
	handler := server.chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		flushErr = http.NewResponseController(w).Flush()
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/agent/stream", nil))
	if flushErr != nil {
		t.Fatalf("flush through the chain: %v", flushErr)
	}
	if _, ok := any(&statusWriter{}).(http.Flusher); !ok {
		t.Fatal("statusWriter must implement http.Flusher")
	}
}
