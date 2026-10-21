package server

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/lang"
)

var (
	// Une clé entière entre guillemets, apostrophes ou accents graves.
	quotedKey = regexp.MustCompile("[\"'`]([a-z0-9_]+\\.[a-z0-9_.]+)[\"'`]")
	// Le début d'une clé composée à l'exécution : un littéral qui finit par
	// « _ » ou « . », suivi d'une fin de chaîne ou d'un ${…}.
	composedPrefix = regexp.MustCompile("[\"'`]([a-z0-9_]+\\.[a-z0-9_.]*[_.])(?:[\"'`]|\\$\\{)")
)

// Les clés qu'aucun code ne nomme et qu'on garde quand même, chacune avec sa
// raison. Vide aujourd'hui : une clé sans lecteur se retire des deux
// catalogues.
var keysWithoutReader = map[string]string{}

// catalogReaders est ce que le code demande au catalogue : les clés écrites
// en entier, et les débuts de clés composées à l'exécution.
type catalogReaders struct {
	literals map[string]bool
	prefixes []string
}

// readCatalogReaders lit le front, les composants et les commandes. Les
// tests ne comptent pas : une clé lue par un test seul ne s'affiche nulle part.
func readCatalogReaders(t *testing.T) catalogReaders {
	t.Helper()
	readers := catalogReaders{literals: map[string]bool{}}
	for _, root := range []string{"../../web/src", "..", "../../cmd"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !isCatalogReader(path) {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, match := range quotedKey.FindAllStringSubmatch(string(content), -1) {
				readers.literals[match[1]] = true
			}
			for _, match := range composedPrefix.FindAllStringSubmatch(string(content), -1) {
				readers.prefixes = append(readers.prefixes, match[1])
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return readers
}

func isCatalogReader(path string) bool {
	if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".test.ts") {
		return false
	}
	return strings.HasSuffix(path, ".go") || strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".tsx")
}

// hasReader dit si une clé est lue : écrite en entier, composée d'une valeur
// d'une liste fermée du serveur, commencée par un préfixe composé que le
// code écrit, ou forme « _one » d'une clé lue (le serveur et le front la
// composent pour le singulier).
func (r catalogReaders) hasReader(key string, closed map[string]bool, closedPrefixes map[string]bool) bool {
	if r.literals[key] || closed[key] {
		return true
	}
	for _, prefix := range r.prefixes {
		// Le préfixe d'une liste fermée se juge valeur par valeur, et le nom
		// seul d'une section ne dit pas quelle clé est lue.
		if closedPrefixes[prefix] || isSectionName(prefix) {
			continue
		}
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	singular, isSingular := strings.CutSuffix(key, "_one")
	return isSingular && r.hasReader(singular, closed, closedPrefixes)
}

func isSectionName(prefix string) bool {
	return strings.Count(prefix, ".") == 1 && strings.HasSuffix(prefix, ".")
}

// Chaque clé du catalogue a un lecteur : le sens inverse de
// TestFrontend_UsesOnlyKnownKeys. Une clé que plus rien ne demande se
// traduirait pour rien et resterait à relire à chaque changement de ton. Il
// vit ici, à côté des listes fermées qu'il relit : lang n'importe pas les
// composants.
func TestCatalog_EveryKeyHasAReader(t *testing.T) {
	catalogs, err := lang.Load()
	if err != nil {
		t.Fatal(err)
	}
	closed := map[string]bool{}
	closedPrefixes := map[string]bool{}
	for _, list := range closedKeyLists() {
		closedPrefixes[list.prefix] = true
		for _, value := range list.values {
			closed[list.prefix+value+list.suffix] = true
		}
	}
	readers := readCatalogReaders(t)
	keys := catalogs[lang.Default].Keys()
	for _, key := range keys {
		_, kept := keysWithoutReader[key]
		isRead := readers.hasReader(key, closed, closedPrefixes)
		switch {
		case isRead && kept:
			t.Errorf("%s a un lecteur : retire-la de keysWithoutReader", key)
		case !isRead && !kept:
			t.Errorf("%s : aucun code ne la lit, retire-la de fr.toml et en.toml", key)
		}
	}
	for key := range keysWithoutReader {
		if !slices.Contains(keys, key) {
			t.Errorf("%s n'est plus au catalogue : retire-la de keysWithoutReader", key)
		}
	}
}

func TestCatalogReaders_SeesLiteralsPrefixesAndSingulars(t *testing.T) {
	readers := catalogReaders{
		literals: map[string]bool{"manifest.written": true},
		prefixes: []string{"container.state_", "probe.reason_", "nav."},
	}
	closed := map[string]bool{"probe.reason_timeout": true}
	closedPrefixes := map[string]bool{"probe.reason_": true}
	for key, want := range map[string]bool{
		"manifest.written":      true,
		"manifest.written_one":  true,
		"container.state_oom":   true,
		"probe.reason_timeout":  true,
		"probe.reason_vanished": false,
		"nav.backups":           false,
		"manifest.other_one":    false,
	} {
		if got := readers.hasReader(key, closed, closedPrefixes); got != want {
			t.Errorf("%s: got %v, want %v", key, got, want)
		}
	}
	content := "t(`job.snippet_${key}`) + \"channel.format_\" + 'x.y'"
	var found []string
	for _, match := range composedPrefix.FindAllStringSubmatch(content, -1) {
		found = append(found, match[1])
	}
	if !slices.Equal(found, []string{"job.snippet_", "channel.format_"}) {
		t.Fatalf("prefixes: got %v", found)
	}
}
