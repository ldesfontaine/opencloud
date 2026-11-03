package restic

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

var errRestorePath = errors.New("restic restore: path invalid")

// RestoreSnapshot : restic restore d'UN chemin d'un instantané, dans un dossier
// temporaire que l'agent a créé ; il le déplace ensuite à côté de l'original.
// --no-lock : restic ne pose aucun verrou, donc n'écrit rien dans le dépôt et
// n'envoie aucun DELETE (la règle du dépôt ne les laisse passer que sur les
// verrous). Le chemin est un motif pour restic : ses caractères spéciaux sont
// protégés, un nom comme « a*b » ne restaure pas ses voisins.
func RestoreSnapshot(binary string, repository Repository, snapshotID, targetDir, includePath string) (Command, error) {
	if !filepath.IsAbs(binary) {
		return Command{}, errCommandBinary
	}
	if !snapshotIDPattern(snapshotID) {
		return Command{}, errVerifySnapshot
	}
	if !filepath.IsAbs(targetDir) || includePath == "/" || !agentwire.IsBrowsePath(includePath) {
		return Command{}, errRestorePath
	}
	env, err := repository.environment()
	if err != nil {
		return Command{}, err
	}
	args := []string{"restore", "--no-lock"}
	if repository.CacheDir != "" {
		args = append(args, "--cache-dir", repository.CacheDir)
	} else {
		args = append(args, "--no-cache")
	}
	args = append(args, "--target", targetDir, "--include", escapePattern(includePath), "--", snapshotID)
	return Command{Binary: binary, Args: args, Env: env}, nil
}

// escapePattern protège ce que restic lit comme un motif : * ? [ et la barre
// inverse.
func escapePattern(path string) string {
	return strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`).Replace(path)
}
