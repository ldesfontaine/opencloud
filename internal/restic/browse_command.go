package restic

import (
	"bytes"
	"encoding/json"
	"errors"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

// Les commandes du parcours d'un dépôt local de la cible, sur le même patron
// que la vérification : chemin absolu, mot de passe par fichier, environnement
// remplacé.
var errBrowsePath = errors.New("restic browse: path invalid")

// AllSnapshots : tous les instantanés du dépôt, en JSON.
func AllSnapshots(binary, repositoryDir, passwordFile string) (Command, error) {
	return verifyCommand(binary, repositoryDir, passwordFile, "snapshots", "--json")
}

// ListDirectory : les nœuds d'un dossier d'un instantané, en lignes JSON. Selon
// la version de restic, la liste est celle du dossier ou de tout son sous-arbre :
// ParseDirectory ne garde que les fils directs.
func ListDirectory(binary, repositoryDir, passwordFile, snapshotID, directory string) (Command, error) {
	if !snapshotIDPattern(snapshotID) {
		return Command{}, errVerifySnapshot
	}
	if !agentwire.IsBrowsePath(directory) {
		return Command{}, errBrowsePath
	}
	return verifyCommand(binary, repositoryDir, passwordFile, "ls", "--json", "--", snapshotID, directory)
}

// ParseBrowseSnapshots lit la sortie d'AllSnapshots : les plus récents d'abord,
// au plus limit, ceux dont l'identifiant a la forme d'un instantané.
func ParseBrowseSnapshots(output []byte, limit int) ([]agentwire.BrowseSnapshot, error) {
	var listed []struct {
		ID    string    `json:"id"`
		Time  time.Time `json:"time"`
		Paths []string  `json:"paths"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &listed); err != nil {
		return nil, err
	}
	slices.SortStableFunc(listed, func(a, b struct {
		ID    string    `json:"id"`
		Time  time.Time `json:"time"`
		Paths []string  `json:"paths"`
	}) int {
		return b.Time.Compare(a.Time)
	})
	kept := []agentwire.BrowseSnapshot{}
	for _, snapshot := range listed {
		if !agentwire.IsSnapshotID(snapshot.ID) || len(kept) >= limit {
			continue
		}
		paths := []string{}
		for _, p := range snapshot.Paths {
			if agentwire.IsBrowsePath(p) && len(paths) < 8 {
				paths = append(paths, p)
			}
		}
		kept = append(kept, agentwire.BrowseSnapshot{ID: snapshot.ID, Time: snapshot.Time.UTC().Format(time.RFC3339), Paths: paths})
	}
	return kept, nil
}

// ParseDirectory lit une ligne de la sortie de ListDirectory : le fils direct
// de directory qu'elle décrit, ou faux (ligne d'instantané, le dossier
// lui-même, un descendant plus profond, un nom hors forme).
func ParseDirectory(line []byte, directory string) (agentwire.BrowseEntry, bool) {
	var node struct {
		StructType string    `json:"struct_type"`
		Type       string    `json:"type"`
		Path       string    `json:"path"`
		Size       int64     `json:"size"`
		ModTime    time.Time `json:"mtime"`
	}
	if err := json.Unmarshal(line, &node); err != nil || node.StructType != "node" || !strings.HasPrefix(node.Path, "/") {
		return agentwire.BrowseEntry{}, false
	}
	if path.Dir(node.Path) != directory || node.Path == directory {
		return agentwire.BrowseEntry{}, false
	}
	name := path.Base(node.Path)
	if !agentwire.IsBrowseName(name) {
		return agentwire.BrowseEntry{}, false
	}
	kind := "other"
	switch node.Type {
	case "dir", "file":
		kind = node.Type
	case "symlink":
		kind = "link"
	}
	entry := agentwire.BrowseEntry{Name: name, Kind: kind, Size: max(node.Size, 0)}
	if !node.ModTime.IsZero() {
		entry.ModTime = node.ModTime.UTC().Format(time.RFC3339)
	}
	return entry, true
}
