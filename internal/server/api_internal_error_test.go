package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

// errorLog est le journal du serveur pendant un test : une ligne JSON par
// enregistrement, tous niveaux confondus.
type errorLog struct{ buffer bytes.Buffer }

// captureLog remplace le journal du serveur par un tampon. Les composants
// gardent le leur : seul ce que le serveur écrit lui-même est observé.
func captureLog(server *Server) *errorLog {
	log := &errorLog{}
	server.logger = slog.New(slog.NewJSONHandler(&log.buffer, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return log
}

// errors rend les enregistrements de niveau ERROR.
func (l *errorLog) errors(t *testing.T) []map[string]any {
	t.Helper()
	var found []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(l.buffer.String()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		if record["level"] == "ERROR" {
			found = append(found, record)
		}
	}
	return found
}

// La base fermée fait échouer toute lecture : l'erreur n'est prévue par aucun
// refus, donc 500. Le corps ne dit qu'« internal » ; le détail va au journal.
func TestAPI_UnforeseenFailure_Is500WithOnlyInternalAndOneErrorLogLine(t *testing.T) {
	server := newTestServer(t)
	log := captureLog(server.Server)
	if err := server.db.Close(); err != nil {
		t.Fatal(err)
	}
	recorder := callAPI(server.Server, http.MethodGet, "/api/status/incidents", nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	if body := strings.TrimSpace(recorder.Body.String()); body != `{"error":"internal"}` {
		t.Fatalf("body %s, want only the code", body)
	}
	records := log.errors(t)
	if len(records) != 1 {
		t.Fatalf("%d ERROR lines, want one: %s", len(records), log.buffer.String())
	}
	record := records[0]
	if record["msg"] != "internal error" || record["path"] != "/api/status/incidents" {
		t.Fatalf("record %v", record)
	}
	if detail, _ := record["error"].(string); detail == "" {
		t.Fatalf("the log must keep the cause: %v", record)
	}
}

// Un refus prévu ne passe pas par le chemin 500 : pas de ligne ERROR.
func TestAPI_ForeseenRefusal_WritesNoErrorLog(t *testing.T) {
	server := newTestServer(t)
	log := captureLog(server.Server)
	recorder := callAPI(server.Server, http.MethodGet, "/api/status/incidents/nope", nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status %d", recorder.Code)
	}
	if records := log.errors(t); len(records) != 0 {
		t.Fatalf("a 404 is not an error: %v", records)
	}
}

// Même chemin côté MCP : l'outil rend « internal » sans le détail, et le
// journal garde la cause avec le nom de l'outil.
func TestMCP_UnforeseenFailure_ReturnsInternalAndLogsTheTool(t *testing.T) {
	server := newTestServer(t)
	log := captureLog(server.Server)
	session := connectMCP(t, server.mcpServer)
	if err := server.db.Close(); err != nil {
		t.Fatal(err)
	}
	text, isError := callTool(t, session, "list_incidents", map[string]any{})
	if !isError || toolErrorCode(t, text) != codeInternal {
		t.Fatalf("%v %s", isError, text)
	}
	if strings.TrimSpace(text) != `{"error":"internal"}` {
		t.Fatalf("result %s, want only the code", text)
	}
	records := log.errors(t)
	if len(records) != 1 || records[0]["msg"] != "mcp tool failed" || records[0]["tool"] != "list_incidents" {
		t.Fatalf("ERROR lines %v", records)
	}
}

// toolFailed mène au même résultat quand l'outil n'a pas de refus à rendre.
func TestMCP_UnforeseenFailure_OnALookupToo(t *testing.T) {
	server := newTestServer(t)
	log := captureLog(server.Server)
	session := connectMCP(t, server.mcpServer)
	if err := server.db.Close(); err != nil {
		t.Fatal(err)
	}
	text, isError := callTool(t, session, "get_machine", map[string]any{"machine_id": remoteID})
	if !isError || toolErrorCode(t, text) != codeInternal {
		t.Fatalf("%v %s", isError, text)
	}
	if records := log.errors(t); len(records) != 1 || records[0]["tool"] != "get_machine" {
		t.Fatalf("ERROR lines %v", records)
	}
}
