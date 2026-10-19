package proxysecret

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Un faux secret de 64 caractères, la forme de openssl rand -hex 32.
const testSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" // gitleaks:allow -- faux secret de test

func writeSecret(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "forward.secret")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile subit l'umask : le mode voulu se pose ensuite.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad_WithoutPath_MeansNoSecret(t *testing.T) {
	secret, err := Load("")
	if err != nil || secret != "" {
		t.Fatalf("empty path: %q, %v", secret, err)
	}
}

func TestLoad_ReadsTheLineWithoutItsNewline(t *testing.T) {
	for name, content := range map[string]string{
		"with newline":    testSecret + "\n",
		"without newline": testSecret,
		"with CRLF":       testSecret + "\r\n",
	} {
		secret, err := Load(writeSecret(t, content, 0o600))
		if err != nil || secret != testSecret {
			t.Errorf("%s: %q, %v", name, secret, err)
		}
	}
}

func TestLoad_AcceptsGroupReadableFileForTheServiceAccount(t *testing.T) {
	if _, err := Load(writeSecret(t, testSecret+"\n", 0o640)); err != nil {
		t.Fatalf("0640 must pass: %v", err)
	}
}

func TestLoad_RefusesAFileOtherAccountsCanRead(t *testing.T) {
	for _, mode := range []os.FileMode{0o644, 0o604, 0o601, 0o666} {
		_, err := Load(writeSecret(t, testSecret+"\n", mode))
		if !errors.Is(err, ErrFileMode) {
			t.Errorf("mode %04o: got %v, want ErrFileMode", mode, err)
		}
	}
}

func TestLoad_RefusesAMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("a missing file must stop the start")
	}
	if _, err := Load(filepath.Join(t.TempDir(), "no", "such", "directory")); err == nil {
		t.Fatal("a missing directory must stop the start")
	}
}

func TestLoad_RefusesWhatIsNotAStrongSingleLineSecret(t *testing.T) {
	for name, content := range map[string]string{
		"empty":         "",
		"too short":     strings.Repeat("a", MinLength-1) + "\n",
		"two lines":     testSecret + "\n" + testSecret + "\n",
		"with a space":  strings.Repeat("a", MinLength) + " b\n",
		"with a tab":    strings.Repeat("a", MinLength) + "\tb\n",
		"non ASCII":     strings.Repeat("é", MinLength) + "\n",
		"too long file": strings.Repeat("a", maxFileBytes+1),
	} {
		_, err := Load(writeSecret(t, content, 0o600))
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

func TestLoad_AcceptsExactlyTheMinimumLength(t *testing.T) {
	if _, err := Load(writeSecret(t, strings.Repeat("a", MinLength)+"\n", 0o600)); err != nil {
		t.Fatalf("%d characters must pass: %v", MinLength, err)
	}
}

func TestLoad_ErrorsNeverQuoteTheSecret(t *testing.T) {
	short := strings.Repeat("s3cr3t-", 3)
	_, err := Load(writeSecret(t, short+"\n", 0o600))
	if err == nil || strings.Contains(err.Error(), short) {
		t.Fatalf("the error must exist and not hold the content: %v", err)
	}
	_, err = Load(writeSecret(t, testSecret+" x\n", 0o600))
	if err == nil || strings.Contains(err.Error(), testSecret) {
		t.Fatalf("the error must exist and not hold the content: %v", err)
	}
}
