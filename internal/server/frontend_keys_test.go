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
	frontKey     = regexp.MustCompile(`\bt\("([a-z0-9_.]+)"`)
	catalogTable = regexp.MustCompile(`(?m)^\[([a-z0-9_]+)\]\s*$`)
)

// Les littéraux qui ont la forme d'une clé d'une section du catalogue sans
// en être une, chacun avec sa raison.
var literalsNotKeys = map[string]string{
	"agent.update":     "un droit d'agent (machine.CapabilityAgentUpdate), que le front envoie au serveur",
	"backup.produce":   "un droit d'agent (machine.CapabilityBackupProduce), que le front envoie au serveur",
	"proxmox.power":    "un droit d'agent (machine.CapabilityProxmoxPower), que le front envoie au serveur",
	"proxmox.snapshot": "un droit d'agent (machine.CapabilityProxmoxSnapshot), que le front envoie au serveur",
	"backup.receive":   "un droit d'agent (machine.CapabilityBackupReceive), que le front envoie au serveur",
	"probe.lan":        "un droit d'agent (machine.CapabilityProbeLAN), que le front envoie au serveur",
}

// frontKeys rend les clés que le texte d'un fichier du front demande en
// dur : celles de t("…"), et tout littéral « section.clé » d'une section du
// catalogue, où qu'il soit (une branche de ternaire, un champ `key:`). Un
// littéral qui finit par « _ » ou « . » est le début d'une clé composée à
// l'exécution : un autre test la couvre.
func frontKeys(content string, sections []string) []string {
	keys := []string{}
	for _, match := range frontKey.FindAllStringSubmatch(content, -1) {
		keys = append(keys, match[1])
	}
	literal := regexp.MustCompile(`"((?:` + strings.Join(sections, "|") + `)\.[a-z0-9_]+)"`)
	for _, match := range literal.FindAllStringSubmatch(content, -1) {
		keys = append(keys, match[1])
	}
	return slices.DeleteFunc(keys, func(key string) bool {
		_, notKey := literalsNotKeys[key]
		return notKey || strings.HasSuffix(key, "_") || strings.HasSuffix(key, ".")
	})
}

func catalogSections(t *testing.T) []string {
	t.Helper()
	text, err := os.ReadFile("../lang/fr.toml")
	if err != nil {
		t.Fatal(err)
	}
	sections := []string{}
	for _, match := range catalogTable.FindAllStringSubmatch(string(text), -1) {
		sections = append(sections, regexp.QuoteMeta(match[1]))
	}
	if len(sections) == 0 {
		t.Fatal("no catalog section found")
	}
	return sections
}

// Chaque clé que le front écrit en dur existe dans les deux catalogues :
// une clé absente s'afficherait entre crochets. Les clés composées à
// l'exécution (job.status_ + état) sont couvertes par
// TestCatalog_HasAKeyForEveryValueOfEveryClosedList.
func TestFrontend_UsesOnlyKnownKeys(t *testing.T) {
	server := newTestServer(t)
	sections := catalogSections(t)
	found := 0
	err := filepath.WalkDir("../../web/src", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || (!strings.HasSuffix(path, ".ts") && !strings.HasSuffix(path, ".tsx")) {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, key := range frontKeys(string(content), sections) {
			found++
			for _, code := range lang.Codes() {
				if strings.HasPrefix(server.catalogs.For(code).Get(key), "[") {
					t.Errorf("%s: la clé %q manque dans %s", path, key, code)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == 0 {
		t.Fatal("no key found: the guard no longer sees the front")
	}
}

func TestFrontKeys_SeesLiteralsInTernariesAndFields(t *testing.T) {
	content := "t(flag ? \"directory.units_help_compose\" : \"directory.units_help\")\n" +
		"{ href: \"/services\", key: \"services.tab_containers\" }\n" +
		"t(\"directory.nature_\" + nature)\n" +
		"import(\"./app.test\")\n"
	got := frontKeys(content, []string{"directory", "services"})
	want := []string{"directory.units_help_compose", "directory.units_help", "services.tab_containers"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
