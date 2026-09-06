package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

func TestRun_Version_PrintsVersion(t *testing.T) {
	var out bytes.Buffer

	if err := Run(context.Background(), []string{"version"}, "1.2.3", &out, &out); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if got, want := out.String(), "opencloud 1.2.3\n"; got != want {
		t.Fatalf("sortie = %q, attendu %q", got, want)
	}
}

func TestRun_UnknownCommand_ReturnsError(t *testing.T) {
	var out bytes.Buffer

	err := Run(context.Background(), []string{"explode"}, "dev", &out, &out)
	if !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("erreur = %v, attendu ErrUnknownCommand", err)
	}
}

func TestServe_MissingConfig_ReturnsError(t *testing.T) {
	var out bytes.Buffer
	missing := filepath.Join(t.TempDir(), "absent.toml")

	err := Run(context.Background(), []string{"serve", "--config", missing}, "dev", &out, &out)
	if err == nil || !strings.Contains(err.Error(), "absent.toml") {
		t.Fatalf("attendu une erreur nommant le fichier, reçu %v", err)
	}
}

// safeBuffer : le journal de serve est écrit depuis une autre goroutine que le test.
type safeBuffer struct {
	mutex  sync.Mutex
	buffer bytes.Buffer
}

func (b *safeBuffer) Write(content []byte) (int, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.buffer.Write(content)
}

func (b *safeBuffer) String() string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.buffer.String()
}

// freePort réserve un port puis le libère : serve doit le reprendre aussitôt.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func writeTestConfig(t *testing.T, listen string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	// state_dir relatif : résolu contre le fichier, donc dans dir.
	content := fmt.Sprintf("listen = %q\nstate_dir = \"state\"\nunknown_key = 1\n", listen)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("délai dépassé : %s", what)
}

func TestSelfUpdate_DevelopmentBuild_IsRefusedWithoutTouchingTheNetwork(t *testing.T) {
	var out bytes.Buffer
	configPath := writeTestConfig(t, "127.0.0.1:1")

	err := Run(context.Background(), []string{"self-update", "--config", configPath}, "dev", &out, &out)

	var refused refusal.Refusal
	if !errors.As(err, &refused) || !strings.Contains(refused.Cause, "n'est pas une release") {
		t.Fatalf("attendu un refus nommant la version de développement, reçu %v", err)
	}
}

func TestUsage_ListsEveryCommand(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), nil, "dev", &out, &out); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"serve", "status", "self-update", "version"} {
		if !strings.Contains(out.String(), command) {
			t.Fatalf("l'usage doit lister %q :\n%s", command, out.String())
		}
	}
}

func TestServeAndStatus_EndToEnd(t *testing.T) {
	listen := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	configPath := writeTestConfig(t, listen)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	var serveLog safeBuffer
	served := make(chan error, 1)
	go func() {
		served <- Run(ctx, []string{"serve", "--config", configPath}, "0.0.0-test", &serveLog, &serveLog)
	}()
	waitFor(t, "serve écoute", func() bool { return strings.Contains(serveLog.String(), `"msg":"listening"`) })

	if !strings.Contains(serveLog.String(), "unknown_key") {
		t.Fatalf("la clé inconnue doit être nommée dans le journal :\n%s", serveLog.String())
	}
	if !strings.Contains(serveLog.String(), "migration applied") {
		t.Fatalf("les migrations doivent être appliquées :\n%s", serveLog.String())
	}

	var statusOut bytes.Buffer
	if err := Run(ctx, []string{"status", "--config", configPath}, "0.0.0-test", &statusOut, &statusOut); err != nil {
		t.Fatalf("status : %v\n%s", err, statusOut.String())
	}
	for _, expected := range []string{"valide, 1 avertissement", "base présente", "répond, version 0.0.0-test"} {
		if !strings.Contains(statusOut.String(), expected) {
			t.Fatalf("status doit dire %q :\n%s", expected, statusOut.String())
		}
	}

	stop()
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("serve doit s'arrêter proprement : %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve ne s'est pas arrêté")
	}

	var downOut bytes.Buffer
	err := Run(context.Background(), []string{"status", "--config", configPath}, "x", &downOut, &downOut)
	if !errors.Is(err, ErrServiceDown) {
		t.Fatalf("service arrêté : attendu ErrServiceDown, reçu %v", err)
	}
}
