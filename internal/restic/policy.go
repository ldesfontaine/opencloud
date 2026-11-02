package restic

import (
	"net/http"
	"regexp"
	"slices"
	"strings"
)

// Refusal est le code fermé d'une requête REST refusée ; il part tel quel à
// restic dans un 403 {"error": code}.
type Refusal string

func (r Refusal) Error() string {
	return string(r)
}

const (
	DeleteRefused           Refusal = "delete_refused"
	DeleteRepositoryRefused Refusal = "delete_repository_refused"
	PathRefused             Refusal = "path_refused"
	QueryRefused            Refusal = "query_refused"
	MethodRefused           Refusal = "method_refused"
)

// La seule requête de restic qui porte un paramètre : restic init.
const createQuery = "create=true"

// Les types de fichiers d'un dépôt (« REST Backend », restic 0.19.1) et la
// forme d'un nom : 64 hexadécimaux minuscules, à plat (pas de data/xx/).
var (
	fileTypes   = []string{"data", "keys", "locks", "snapshots", "index"}
	fileNameHex = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// La forme d'un chemin, une fois reconnu.
type route int

const (
	routeRepository route = iota // « / »
	routeConfig                  // « /config »
	routeList                    // « /{type}/ »
	routeFile                    // « /{type}/{nom} »
)

// Allow juge une requête de restic, chemin relatif au dépôt et tel qu'il est
// arrivé (encodé) : nil si elle passe, sinon le Refusal. Tout ce qui n'est
// pas dans le tableau de l'essai est refusé ; un DELETE ne passe que sur un
// verrou, que restic libère à chaque fin de commande.
func Allow(method, escapedPath, rawQuery string) error {
	matched, fileType, isKnown := matchRoute(escapedPath)
	if !isKnown {
		return PathRefused
	}
	if method == http.MethodDelete {
		return judgeDelete(matched, fileType, rawQuery)
	}
	if !isMethodAllowed(matched, method) {
		return MethodRefused
	}
	if !isQueryAllowed(matched, method, rawQuery) {
		return QueryRefused
	}
	return nil
}

// matchRoute reconnaît les quatre formes et rien d'autre. Un « % » (chemin
// ré-encodé), un « .. », un « // » ou un type inconnu ne correspondent à
// aucune.
func matchRoute(escapedPath string) (route, string, bool) {
	switch escapedPath {
	case "/":
		return routeRepository, "", true
	case "/config":
		return routeConfig, "", true
	}
	rest, hasSlash := strings.CutPrefix(escapedPath, "/")
	if !hasSlash {
		return 0, "", false
	}
	fileType, name, hasName := strings.Cut(rest, "/")
	if !hasName || !slices.Contains(fileTypes, fileType) {
		return 0, "", false
	}
	if name == "" {
		return routeList, fileType, true
	}
	if !fileNameHex.MatchString(name) {
		return 0, "", false
	}
	return routeFile, fileType, true
}

// judgeDelete : seul un verrou nommé se supprime, sans paramètre.
func judgeDelete(matched route, fileType, rawQuery string) error {
	switch {
	case matched == routeRepository:
		return DeleteRepositoryRefused
	case matched != routeFile || fileType != "locks":
		return DeleteRefused
	case rawQuery != "":
		return QueryRefused
	}
	return nil
}

func isMethodAllowed(matched route, method string) bool {
	switch matched {
	case routeRepository:
		return method == http.MethodPost
	case routeList:
		return method == http.MethodGet
	}
	return method == http.MethodHead || method == http.MethodGet || method == http.MethodPost
}

// isQueryAllowed : « create=true » sur POST / exactement, aucun paramètre
// ailleurs.
func isQueryAllowed(matched route, method, rawQuery string) bool {
	if matched == routeRepository && method == http.MethodPost {
		return rawQuery == createQuery
	}
	return rawQuery == ""
}
