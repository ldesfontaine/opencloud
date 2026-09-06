package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/store"
)

func TestStatus_MigrationLockLeftBehind_NamesItAndRefuses(t *testing.T) {
	configPath := writeTestConfig(t, "127.0.0.1:1")
	stateDir := filepath.Join(filepath.Dir(configPath), "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(stateDir, store.MigrationLockFileName)
	if err := os.WriteFile(lockPath, []byte("4321\n2026-01-02T03:04:05Z\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := Run(context.Background(), []string{"status", "--config", configPath}, "dev", &out, &out)

	var refused refusal.Refusal
	if !errors.As(err, &refused) {
		t.Fatalf("attendu un refus, reçu %v", err)
	}
	for _, expected := range []string{"migration     :", "4321", "2026-01-02T03:04:05Z", lockPath} {
		if !strings.Contains(out.String(), expected) {
			t.Fatalf("status doit dire %q :\n%s", expected, out.String())
		}
	}
	if strings.Contains(out.String(), "service       :") {
		t.Fatalf("status sort en refus sans sonder le service :\n%s", out.String())
	}
}
