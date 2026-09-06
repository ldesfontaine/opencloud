package selfupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validToken = "github_pat_0123456789abcdef"

// Le test n'est pas root : l'uid attendu est celui qui écrit les fichiers.
func currentUID(t *testing.T) uint32 {
	t.Helper()
	return uint32(os.Getuid())
}

func writeTokenFile(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "github-token")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("écrire le fichier : %v", err)
	}
	// WriteFile passe par umask : le mode se pose ensuite.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("poser le mode : %v", err)
	}
	return path
}

func TestReadToken_MissingFile_IsNoTokenAndNoError(t *testing.T) {
	token, err := readToken(filepath.Join(t.TempDir(), "github-token"), currentUID(t))

	if err != nil {
		t.Fatalf("un fichier absent n'est pas une erreur, reçu %v", err)
	}
	if token != "" {
		t.Fatalf("token = %q, attendu vide", token)
	}
}

func TestReadToken_PresentFile_IsReadFromTheFirstLineTrimmed(t *testing.T) {
	path := writeTokenFile(t, "  "+validToken+"  \n# une note\n", 0o600)

	token, err := readToken(path, currentUID(t))

	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if token != validToken {
		t.Fatalf("token = %q, attendu %q", token, validToken)
	}
}

func TestReadToken_ReadableByOthers_IsRefusedWithTheChmod(t *testing.T) {
	path := writeTokenFile(t, validToken+"\n", 0o644)

	_, err := readToken(path, currentUID(t))

	expectRefusal(t, err, "chmod 0600")
	expectSecretKept(t, err)
}

func TestReadToken_OwnedBySomeoneElse_IsRefusedWithTheChown(t *testing.T) {
	path := writeTokenFile(t, validToken+"\n", 0o600)

	_, err := readToken(path, currentUID(t)+1)

	expectRefusal(t, err, "chown root:root")
	expectSecretKept(t, err)
}

func TestReadToken_ContentThatIsNotAToken_IsRefusedWithoutCopyingIt(t *testing.T) {
	for name, content := range map[string]string{
		"vide":          "\n",
		"trop court":    "abc\n",
		"avec espace":   "gh pat 0123456789abcdef\n",
		"avec un tiret": "github-pat-0123456789abcdef\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := writeTokenFile(t, content, 0o600)

			_, err := readToken(path, currentUID(t))

			expectRefusal(t, err, "forme d'un jeton GitHub")
			expectSecretKept(t, err)
		})
	}
}

func TestReadToken_FileLargerThanAToken_IsRefusedWithoutReadingItAll(t *testing.T) {
	path := writeTokenFile(t, strings.Repeat("a", maxTokenFileBytes+1), 0o600)

	_, err := readToken(path, currentUID(t))

	expectRefusal(t, err, "octets")
	expectSecretKept(t, err)
}

// Un refus qui recopie le secret le sortirait du fichier vers le journal.
func expectSecretKept(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), validToken) {
		t.Fatalf("le refus recopie le jeton : %v", err)
	}
}
