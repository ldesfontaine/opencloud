package lifecycle

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/registry"
)

const (
	// Au-delà, ce qu'on sait de la source est trop vieux pour être affiché
	// sans réserve.
	StaleAfter = 30 * 24 * time.Hour
	// Ce qu'on montre des branches plus récentes : les plus proches.
	maxNewer      = 4
	productPageAt = "https://endoflife.date/"
	// Les jours de la source, « 2028-11-09 » : ils se comparent comme du
	// texte.
	dayLayout = "2006-01-02"
)

// verdictOf lit le support d'une image dans ce que la source a donné. Le
// dépôt se compare sans l'hôte du registre : « quay.io/keycloak/keycloak »
// est le même produit que sur le Hub.
func verdictOf(snapshot Snapshot, image string, now time.Time) Verdict {
	verdict := Verdict{Status: StatusPending, FetchedAt: snapshot.FetchedAt}
	if snapshot.FetchedAt.IsZero() {
		return verdict
	}
	verdict.IsStale = now.Sub(snapshot.FetchedAt) > StaleAfter
	reference, err := registry.Parse(image)
	if err != nil {
		verdict.Status = StatusUnknownProduct
		return verdict
	}
	name, known := snapshot.Repositories[reference.Repository]
	if !known {
		verdict.Status = StatusUnknownProduct
		return verdict
	}
	verdict.ProductURL = productPageAt + name
	product, fetched := snapshot.Products[name]
	if !fetched {
		verdict.Product = name
		return verdict
	}
	verdict.Product = product.Label
	verdict.FetchedAt = product.FetchedAt
	verdict.IsStale = now.Sub(product.FetchedAt) > StaleAfter
	cycle, found := cycleOf(product.Cycles, reference.Tag)
	if !found {
		verdict.Status = StatusUnknownVersion
		return verdict
	}
	today := now.UTC().Format(dayLayout)
	verdict.Status = StatusMaintained
	if isOver(cycle, today) {
		verdict.Status = StatusEOL
	}
	verdict.Cycle, verdict.EOLFrom, verdict.Latest = cycle.Name, cycle.EOLFrom, cycle.Latest
	verdict.Newer = newerMaintained(product.Cycles, cycle, today)
	return verdict
}

// isOver : la source le dit, ou la date de fin est passée. La date compte
// aussi parce qu'une lecture gardée vieillit, et la date non.
func isOver(cycle Cycle, today string) bool {
	return cycle.IsEOL || (cycle.EOLFrom != "" && cycle.EOLFrom <= today)
}

// cycleOf trouve la branche d'un tag : « 16.4 » est de la branche « 16 »,
// « v3.1.4-alpine » de la « 3.1 ». On essaie du plus précis au plus large.
// « latest » ou un tag sans version ne disent rien.
func cycleOf(cycles []Cycle, tag string) (Cycle, bool) {
	parts := versionParts(tag)
	for length := len(parts); length > 0; length-- {
		candidate := strings.Join(parts[:length], ".")
		for _, cycle := range cycles {
			if cycle.Name == candidate {
				return cycle, true
			}
		}
	}
	return Cycle{}, false
}

// versionParts rend les composants numériques en tête d'un tag, sans le
// « v » ni la variante : « v3.1.4-alpine » → 3, 1, 4.
func versionParts(tag string) []string {
	version := strings.TrimPrefix(tag, "v")
	if end := strings.IndexAny(version, "-+_"); end >= 0 {
		version = version[:end]
	}
	parts := strings.Split(version, ".")
	for _, part := range parts {
		if _, ok := number(part); !ok {
			return nil
		}
	}
	return parts
}

// number lit un composant de version : des chiffres seulement, neuf au
// plus, sinon c'est un identifiant de build.
func number(part string) (int, bool) {
	if part == "" || len(part) > 9 {
		return 0, false
	}
	for _, character := range part {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	value, err := strconv.Atoi(part)
	return value, err == nil
}

// newerMaintained rend les branches maintenues au-dessus de celle qui
// tourne, de la plus proche à la plus lointaine.
func newerMaintained(cycles []Cycle, current Cycle, today string) []Newer {
	var newer []Cycle
	for _, cycle := range cycles {
		if cycle.IsMaintained && !isOver(cycle, today) && isAbove(cycle.Name, current.Name) {
			newer = append(newer, cycle)
		}
	}
	sort.Slice(newer, func(i, j int) bool { return isAbove(newer[j].Name, newer[i].Name) })
	if len(newer) > maxNewer {
		newer = newer[:maxNewer]
	}
	found := make([]Newer, 0, len(newer))
	for _, cycle := range newer {
		found = append(found, Newer{Cycle: cycle.Name, Latest: cycle.Latest, EOLFrom: cycle.EOLFrom})
	}
	return found
}

// isAbove compare deux noms de branche composant par composant. Un nom
// qui n'est pas une version n'est au-dessus de rien.
func isAbove(name, reference string) bool {
	left, right := versionParts(name), versionParts(reference)
	if left == nil || right == nil {
		return false
	}
	for index := 0; index < len(left) && index < len(right); index++ {
		a, _ := number(left[index])
		b, _ := number(right[index])
		if a != b {
			return a > b
		}
	}
	return len(left) > len(right)
}
