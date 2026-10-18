package main

import (
	"bytes"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

const exportedYAML = "# Annuaire\nversion: 1\nmachines:\n  - name: opencloud\n"

// shortDir crée un dossier au chemin court : le chemin d'une socket Unix ne
// dépasse pas 107 octets, et t.TempDir() porte le nom du test.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "oc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// writeShortConfig écrit une configuration dont state_dir est un dossier
// court, et rend le chemin du fichier et celui de state_dir.
func writeShortConfig(t *testing.T) (configPath, stateDir string) {
	t.Helper()
	dir := shortDir(t)
	stateDir = filepath.Join(dir, "state")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath = filepath.Join(dir, "config.toml")
	content := "listen = \"127.0.0.1:18081\"\nstate_dir = \"" + stateDir + "\"\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath, stateDir
}

// serveOnControlSocket fait tourner serve en petit : un serveur HTTP sur la
// socket de commande de state_dir.
func serveOnControlSocket(t *testing.T, stateDir string, handler http.Handler) {
	t.Helper()
	root, err := os.OpenRoot(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	control := startControlSocket(slog.New(slog.DiscardHandler), root, stateDir, handler)
	if control == nil {
		t.Fatal("the control socket did not open")
	}
	t.Cleanup(func() { _ = control.Close() })
}

func exportHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/manifest/export", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		_, _ = w.Write([]byte(exportedYAML))
	})
	return mux
}

func TestExportCommand_WritesTheFile(t *testing.T) {
	configPath, stateDir := writeShortConfig(t)
	serveOnControlSocket(t, stateDir, exportHandler())

	var out bytes.Buffer
	if err := exportCommand([]string{"-config", configPath}, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != exportedYAML {
		t.Fatalf("stdout: %q", out.String())
	}

	target := filepath.Join(shortDir(t), "opencloud.yaml")
	if err := os.WriteFile(target, []byte("ancien contenu"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := exportCommand([]string{"-config", configPath, "-o", target}, &out); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(target)
	if err != nil || string(written) != exportedYAML {
		t.Fatalf("file: %q, %v", written, err)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode: %v, %v", info, err)
	}
	if !strings.Contains(out.String(), target) || strings.Contains(out.String(), "version: 1") {
		t.Fatalf("with -o, stdout only says where: %q", out.String())
	}
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil || len(entries) != 1 {
		t.Fatalf("a temporary file stayed behind: %v, %v", entries, err)
	}
}

// Sans -propose, la commande demande le fichier tel qu'il est ; avec, elle le
// demande avec ses propositions (?propose=1), et rien d'autre ne change.
func TestExportCommand_ProposeAsksForProposals(t *testing.T) {
	configPath, stateDir := writeShortConfig(t)
	var queries []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/manifest/export", func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		if r.URL.Query().Get("propose") == "1" {
			_, _ = w.Write([]byte(exportedYAML + "  # Proposé\n  - name: front\n"))
			return
		}
		_, _ = w.Write([]byte(exportedYAML))
	})
	serveOnControlSocket(t, stateDir, mux)

	var plain, proposed bytes.Buffer
	if err := exportCommand([]string{"-config", configPath}, &plain); err != nil {
		t.Fatal(err)
	}
	if err := exportCommand([]string{"-config", configPath, "-propose"}, &proposed); err != nil {
		t.Fatal(err)
	}
	if plain.String() != exportedYAML {
		t.Fatalf("sans -propose : %q", plain.String())
	}
	if !strings.Contains(proposed.String(), "# Proposé") {
		t.Fatalf("avec -propose : %q", proposed.String())
	}
	if len(queries) != 2 || queries[0] != "" || queries[1] != "propose=1" {
		t.Fatalf("requêtes = %q", queries)
	}
}

func TestExportCommand_ServerDownSaysSo(t *testing.T) {
	configPath, stateDir := writeShortConfig(t)
	for name, prepare := range map[string]func(t *testing.T){
		"pas de socket": func(*testing.T) {},
		"socket d'un serveur arrêté": func(t *testing.T) {
			listener, err := net.Listen("unix", filepath.Join(stateDir, brand.ControlSocketFile))
			if err != nil {
				t.Fatal(err)
			}
			listener.(*net.UnixListener).SetUnlinkOnClose(false)
			_ = listener.Close()
		},
	} {
		t.Run(name, func(t *testing.T) {
			prepare(t)
			var out bytes.Buffer
			err := exportCommand([]string{"-config", configPath}, &out)
			if err == nil || !strings.Contains(err.Error(), "le serveur ne tourne pas") || !strings.Contains(err.Error(), stateDir) {
				t.Fatalf("got %v", err)
			}
			var refused *refusal
			if errors.As(err, &refused) {
				t.Fatal("a server that is down is an error (exit 1), not a refusal")
			}
			if out.Len() != 0 {
				t.Fatalf("stdout: %q", out.String())
			}
		})
	}
}

func TestExportCommand_DoesNotCreateAMissingStateDir(t *testing.T) {
	dir := shortDir(t)
	missing := filepath.Join(dir, "absent")
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte("state_dir = \""+missing+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := exportCommand([]string{"-config", configPath}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "le serveur ne tourne pas") {
		t.Fatalf("got %v", err)
	}
	if _, statErr := os.Stat(missing); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("export must not create state_dir: %v", statErr)
	}
}

func TestExportCommand_ServerRefusalIsReported(t *testing.T) {
	configPath, stateDir := writeShortConfig(t)
	serveOnControlSocket(t, stateDir, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal"}`))
	}))
	var out bytes.Buffer
	err := exportCommand([]string{"-config", configPath}, &out)
	if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "internal") || out.Len() != 0 {
		t.Fatalf("got %v, stdout %q", err, out.String())
	}
}

func TestExportCommand_RefusesStrayArguments(t *testing.T) {
	configPath, _ := writeShortConfig(t)
	var refused *refusal
	if err := exportCommand([]string{"-config", configPath, "fichier.yaml"}, &bytes.Buffer{}); !errors.As(err, &refused) {
		t.Fatalf("got %v", err)
	}
}

func TestControlSocket_IsPrivateAndGoneAfterShutdown(t *testing.T) {
	_, stateDir := writeShortConfig(t)
	root, err := os.OpenRoot(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	control := startControlSocket(slog.New(slog.DiscardHandler), root, stateDir, exportHandler())
	if control == nil {
		t.Fatal("the control socket did not open")
	}
	path := filepath.Join(stateDir, brand.ControlSocketFile)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 || info.Mode()&fs.ModeSocket == 0 {
		t.Fatalf("mode %v, want a socket in 0600", info.Mode())
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}
	waitGone(t, path)
}

// waitGone attend que le fichier ait disparu : http.Server ferme son écoute
// depuis la goroutine de Serve, un instant après Close.
func waitGone(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the socket file stays after the shutdown: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestControlSocket_ReplacesAStaleSocketButNeverAnotherFile(t *testing.T) {
	_, stateDir := writeShortConfig(t)
	root, err := os.OpenRoot(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	path := filepath.Join(stateDir, brand.ControlSocketFile)

	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()
	control := startControlSocket(slog.New(slog.DiscardHandler), root, stateDir, exportHandler())
	if control == nil {
		t.Fatal("a stale socket from a killed server must be replaced")
	}
	_ = control.Close()
	waitGone(t, path)

	if err := os.WriteFile(path, []byte("à moi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if control := startControlSocket(slog.New(slog.DiscardHandler), root, stateDir, exportHandler()); control != nil {
		t.Fatal("a file that is not a socket must stop the socket from opening")
	}
	if kept, err := os.ReadFile(path); err != nil || string(kept) != "à moi" {
		t.Fatalf("the file was touched: %q, %v", kept, err)
	}
}

func TestControlSocket_TooLongAPathLeavesServeRunning(t *testing.T) {
	dir := shortDir(t)
	long := filepath.Join(dir, strings.Repeat("d", 120))
	if err := os.Mkdir(long, 0o700); err != nil {
		t.Skipf("cannot create a long directory: %v", err)
	}
	root, err := os.OpenRoot(long)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if control := startControlSocket(slog.New(slog.DiscardHandler), root, long, exportHandler()); control != nil {
		_ = control.Close()
		t.Fatal("a path over 107 bytes cannot hold a socket")
	}
}

func TestControlSocket_ASecondServeNeverTakesTheLiveSocket(t *testing.T) {
	configPath, stateDir := writeShortConfig(t)
	root, err := os.OpenRoot(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	first := startControlSocket(slog.New(slog.DiscardHandler), root, stateDir, exportHandler())
	if first == nil {
		t.Fatal("the first serve must get its socket")
	}
	defer first.Close()

	// Un second serve sur le même state_dir : il n'ouvre rien et ne retire rien.
	if second := startControlSocket(slog.New(slog.DiscardHandler), root, stateDir, exportHandler()); second != nil {
		_ = second.Close()
		t.Fatal("a live socket must not be replaced")
	}
	var out bytes.Buffer
	if err := exportCommand([]string{"-config", configPath}, &out); err != nil || out.String() != exportedYAML {
		t.Fatalf("the first server lost its socket: %q, %v", out.String(), err)
	}
}

func TestControlSocket_RefusesAStateDirOpenToOthers(t *testing.T) {
	_, stateDir := writeShortConfig(t)
	root, err := os.OpenRoot(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, mode := range []os.FileMode{0o750, 0o705, 0o755} {
		if err := os.Chmod(stateDir, mode); err != nil {
			t.Fatal(err)
		}
		if control := startControlSocket(slog.New(slog.DiscardHandler), root, stateDir, exportHandler()); control != nil {
			_ = control.Close()
			t.Fatalf("a state_dir in %v must not hold the socket", mode)
		}
		if _, err := os.Lstat(filepath.Join(stateDir, brand.ControlSocketFile)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("a socket was created in a %v directory: %v", mode, err)
		}
	}
}
