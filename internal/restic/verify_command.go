package restic

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Les commandes de la vérification d'un dépôt local de la cible. Comme
// ForgetRepository : le dépôt est un chemin absolu, le mot de passe un fichier
// que restic lit lui-même, l'environnement remplacé.
const (
	// Une part des données relue par restic check, en plus de la structure :
	// chaque vérification en lit une autre tranche au hasard.
	checkReadSubset = "5%"
	maxDumpPath     = 4 << 10
)

var (
	errVerifySnapshot = errors.New("restic verify: snapshot id invalid")
	errVerifyPath     = errors.New("restic verify: file path invalid")
)

func verifyCommand(binary, repositoryDir, passwordFile string, tail ...string) (Command, error) {
	if !filepath.IsAbs(binary) {
		return Command{}, errCommandBinary
	}
	if !filepath.IsAbs(repositoryDir) || filepath.Clean(repositoryDir) != repositoryDir {
		return Command{}, errCommandDirectory
	}
	if !filepath.IsAbs(passwordFile) {
		return Command{}, errCommandRepository
	}
	args := append([]string{"-r", repositoryDir, "--no-cache"}, tail...)
	env := []string{commandPath, commandLocale, envPasswordFile + "=" + passwordFile}
	return Command{Binary: binary, Args: args, Env: env}, nil
}

// CheckRepository : restic check, la structure puis une tranche des données.
func CheckRepository(binary, repositoryDir, passwordFile string) (Command, error) {
	return verifyCommand(binary, repositoryDir, passwordFile, "check", "--read-data-subset", checkReadSubset)
}

// LatestSnapshots : le plus récent instantané de chaque hôte et chemins, en
// JSON.
func LatestSnapshots(binary, repositoryDir, passwordFile string) (Command, error) {
	return verifyCommand(binary, repositoryDir, passwordFile, "snapshots", "--json", "--latest", "1")
}

// ListSnapshot : les nœuds d'un instantané, en lignes JSON.
func ListSnapshot(binary, repositoryDir, passwordFile, snapshotID string) (Command, error) {
	if !snapshotIDPattern(snapshotID) {
		return Command{}, errVerifySnapshot
	}
	return verifyCommand(binary, repositoryDir, passwordFile, "ls", "--json", snapshotID)
}

// DumpFile : le contenu d'un fichier d'un instantané sur la sortie, sans rien
// écrire sur le disque. Le chemin est celui de l'instantané, après « -- ».
func DumpFile(binary, repositoryDir, passwordFile, snapshotID, path string) (Command, error) {
	if !snapshotIDPattern(snapshotID) {
		return Command{}, errVerifySnapshot
	}
	if !strings.HasPrefix(path, "/") || len(path) > maxDumpPath || strings.ContainsRune(path, 0) {
		return Command{}, errVerifyPath
	}
	return verifyCommand(binary, repositoryDir, passwordFile, "dump", "--", snapshotID, path)
}

func snapshotIDPattern(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, r := range id {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// Snapshot est ce que restic snapshots --json dit d'un instantané, réduit à
// ce que la vérification emploie.
type Snapshot struct {
	ID   string    `json:"id"`
	Time time.Time `json:"time"`
}

// ParseSnapshots lit la sortie de LatestSnapshots, les plus récents d'abord,
// au plus limit, ceux dont l'identifiant a la forme d'un instantané.
func ParseSnapshots(output []byte, limit int) ([]Snapshot, error) {
	var listed []Snapshot
	if err := json.Unmarshal(bytes.TrimSpace(output), &listed); err != nil {
		return nil, err
	}
	kept := listed[:0]
	for _, snapshot := range listed {
		if snapshotIDPattern(snapshot.ID) {
			kept = append(kept, snapshot)
		}
	}
	slices.SortFunc(kept, func(a, b Snapshot) int { return b.Time.Compare(a.Time) })
	if len(kept) > limit {
		kept = kept[:limit]
	}
	return kept, nil
}

// Node est une ligne de restic ls --json, réduite à un fichier.
type Node struct {
	Path string
	Size int64
}

// ParseNode lit une ligne de la sortie de ListSnapshot ; faux si ce n'est pas
// un fichier ordinaire.
func ParseNode(line []byte) (Node, bool) {
	var message struct {
		Type string `json:"type"`
		Path string `json:"path"`
		Size int64  `json:"size"`
	}
	if err := json.Unmarshal(line, &message); err != nil || message.Type != "file" || !strings.HasPrefix(message.Path, "/") {
		return Node{}, false
	}
	return Node{Path: message.Path, Size: message.Size}, true
}
