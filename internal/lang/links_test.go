package lang

import (
	"encoding/json"
	"flag"
	"os"
	"regexp"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "réécrit testdata/links.golden.json")

// Le banc du front (menuLinks.test.ts) relit ce fichier : chaque adresse doit
// être une route de l'interface, chaque ancre un élément qui existe.
func TestLinks_GoldenFile(t *testing.T) {
	catalogs, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	type entry struct {
		ID    string `json:"id"`
		Path  string `json:"path"`
		Label string `json:"label"`
	}
	var entries []entry
	for _, link := range Links() {
		rendered, err := renderLink(link.ID, catalogs[French].strings)
		if err != nil {
			t.Fatal(err)
		}
		label := strings.TrimSuffix(strings.TrimPrefix(rendered, "["), "]("+link.Path+")")
		entries = append(entries, entry{link.ID, link.Path, label})
	}
	got, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	const golden = "testdata/links.golden.json"
	if *updateGolden {
		if err := os.WriteFile(golden, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (lancer go test ./internal/lang -update)", err)
	}
	if string(got) != string(want) {
		t.Fatalf("renvois différents de %s :\n%s\n(lancer go test ./internal/lang -update après vérification)", golden, got)
	}
}

// Un renvoi de menu s'écrit {link:id}. Un chemin « X › Y » ou « Paramètres →
// Y » écrit à la main n'est pas un lien, et dérive dès qu'un menu change.
func TestLoad_NoMenuPathWrittenByHand(t *testing.T) {
	catalogs, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	handWritten := regexp.MustCompile(`›|(Paramètres|Settings|Alertes|Alerts|Services|Machines) → `)
	// Les chemins d'un logiciel tiers (Element, OPNsense) ne sont pas ceux d'openCloud.
	thirdParty := regexp.MustCompile(`Element|OPNsense|Proxmox`)
	for _, code := range Codes() {
		catalog := catalogs[code]
		for _, key := range catalog.Keys() {
			value := catalog.Get(key)
			if i := strings.Index(value, "]("); i >= 0 {
				value = value[:strings.LastIndex(value[:i], "[")] + value[i:]
			}
			if handWritten.MatchString(value) && !thirdParty.MatchString(value) {
				t.Errorf("%s: %s écrit un chemin de menu à la main : %q (utiliser {link:id})", code, key, value)
			}
		}
	}
}

func TestExpandLinks_RendersMarkdownAndRefusesUnknown(t *testing.T) {
	catalog := map[string]string{"nav.settings": "Paramètres", "settings.section_mail": "E-mails"}
	got, err := expandLinks("Voir {link:mail}.", catalog)
	if err != nil || got != "Voir [Paramètres → E-mails](/parametres#e-mails)." {
		t.Errorf("expandLinks = %q, %v", got, err)
	}
	for _, value := range []string{"{link:nulle part}", "{link:mail"} {
		if _, err := expandLinks(value, catalog); err == nil {
			t.Errorf("expandLinks(%q) accepted a bad marker", value)
		}
	}
}
