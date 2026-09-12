package web

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/settings"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	server, err := New(Options{
		Logger:   slog.New(slog.NewTextHandler(os.Stderr, nil)),
		Version:  "v0.0.1",
		Settings: settings.New(root),
	})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

// L'arbre des routes est figé dans testdata/routes.txt : le modifier est
// une décision, pas un effet de bord.
func TestRoutes_MatchFrozenTree(t *testing.T) {
	server := newTestServer(t)
	var lines []string
	for _, route := range server.routes() {
		lines = append(lines, route.method+" "+route.pattern)
	}
	got := strings.Join(lines, "\n") + "\n"
	want, err := os.ReadFile("testdata/routes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("routes changed:\n%s\nwant:\n%s", got, want)
	}
}
