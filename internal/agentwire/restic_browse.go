package agentwire

import (
	"errors"
	"log/slog"
	"strings"
)

// KindResticBrowse : la cible lit un dépôt qu'elle reçoit, pour l'explorateur
// de la page Sauvegardes : la liste des instantanés, ou le contenu d'UN dossier
// d'un instantané. Le mot de passe du dépôt vient du serveur, dans le secret
// signé de l'ordre. Rien n'est écrit sur le disque, rien n'est restauré.
const KindResticBrowse Kind = "restic.browse"

// Les bornes de ce que la cible rapporte d'un parcours. Un dossier plus gros
// est coupé, et le rapport le dit (Truncated).
const (
	MaxBrowseSnapshots = 200
	MaxBrowseEntries   = 2000
	maxBrowsePaths     = 8
	maxBrowsePathLen   = 4 << 10
	maxBrowseNameLen   = 255
)

var (
	errResticBrowse     = errors.New("restic browse: request outside its form")
	errResticBrowseSnap = errors.New("restic browse report: outside its bounds")
)

// ResticBrowseParams : le dépôt visé, et ce qu'on y lit. Snapshot vide : la
// liste des instantanés. Sinon, le contenu du dossier Path de cet instantané.
type ResticBrowseParams struct {
	User     string `json:"user"`
	Snapshot string `json:"snapshot,omitempty"`
	Path     string `json:"path,omitempty"`
}

func (p ResticBrowseParams) Validate() error {
	if !IsResticUser(p.User) {
		return errResticUser
	}
	if p.Snapshot == "" {
		if p.Path != "" {
			return errResticBrowse
		}
		return nil
	}
	if !IsSnapshotID(p.Snapshot) || !IsBrowsePath(p.Path) {
		return errResticBrowse
	}
	return nil
}

// IsSnapshotID : l'identifiant complet d'un instantané restic, 64 chiffres
// hexadécimaux minuscules.
func IsSnapshotID(id string) bool {
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

// IsBrowsePath : un chemin absolu normalisé, sans « .. », sans octet nul.
func IsBrowsePath(path string) bool {
	if path == "/" {
		return true
	}
	if !strings.HasPrefix(path, "/") || len(path) > maxBrowsePathLen || strings.HasSuffix(path, "/") ||
		strings.ContainsRune(path, 0) || strings.Contains(path, "//") {
		return false
	}
	for _, part := range strings.Split(path[1:], "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}

// ResticBrowseSecret : le mot de passe du dépôt, écrit par la cible le temps de
// la commande puis effacé.
type ResticBrowseSecret struct {
	RepoPassword string `json:"repo_password"` // #nosec G117 -- le champ existe pour porter un secret, le temps d'un ordre.
}

func (s ResticBrowseSecret) Validate() error {
	if !IsResticPassword(s.RepoPassword) {
		return errResticPassword
	}
	return nil
}

func (ResticBrowseSecret) String() string { return redactedResticSecret }

func (ResticBrowseSecret) LogValue() slog.Value { return slog.StringValue(redactedResticSecret) }

// BrowseSnapshot : un instantané, réduit à ce que l'explorateur montre.
type BrowseSnapshot struct {
	ID    string   `json:"id"`
	Time  string   `json:"time"`
	Paths []string `json:"paths"`
}

// BrowseEntry : un fils du dossier lu. Kind : « dir », « file », « link » ou
// « other ». Name est un nom, jamais un chemin.
type BrowseEntry struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Size    int64  `json:"size"`
	ModTime string `json:"mtime"`
}

// ResticBrowseReport est tout ce que le serveur apprend d'un parcours réussi :
// des noms, des tailles et des dates. Jamais un contenu, jamais la sortie
// brute de restic. Le serveur ne le garde pas : il le rend à l'écran qui l'a
// demandé.
type ResticBrowseReport struct {
	Snapshots []BrowseSnapshot `json:"snapshots,omitempty"`
	Entries   []BrowseEntry    `json:"entries,omitempty"`
	Truncated bool             `json:"truncated,omitempty"`
}

func (r ResticBrowseReport) Validate() error {
	if len(r.Snapshots) > MaxBrowseSnapshots || len(r.Entries) > MaxBrowseEntries {
		return errResticBrowseSnap
	}
	for _, snapshot := range r.Snapshots {
		if !IsSnapshotID(snapshot.ID) || len(snapshot.Time) > 40 || len(snapshot.Paths) > maxBrowsePaths {
			return errResticBrowseSnap
		}
		for _, path := range snapshot.Paths {
			if !IsBrowsePath(path) {
				return errResticBrowseSnap
			}
		}
	}
	for _, entry := range r.Entries {
		if !IsBrowseName(entry.Name) || !isBrowseKind(entry.Kind) || entry.Size < 0 || len(entry.ModTime) > 40 {
			return errResticBrowseSnap
		}
	}
	return nil
}

// IsBrowseName : un nom de fichier, pas un chemin.
func IsBrowseName(name string) bool {
	return name != "" && name != "." && name != ".." && len(name) <= maxBrowseNameLen &&
		!strings.ContainsAny(name, "/\x00")
}

func isBrowseKind(kind string) bool {
	return kind == "dir" || kind == "file" || kind == "link" || kind == "other"
}
