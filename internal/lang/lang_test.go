package lang

import (
	"slices"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

func TestLoad_EveryLanguageHasTheSameKeys(t *testing.T) {
	catalogs, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	reference := catalogs[Default].Keys()
	if len(reference) == 0 {
		t.Fatal("default language has no key")
	}
	for _, code := range Codes() {
		keys := catalogs[code].Keys()
		if !slices.Equal(keys, reference) {
			t.Errorf("%s: keys differ from %s\n%s", code, Default, diff(reference, keys))
		}
	}
}

func TestLoad_FormatStringsUseTheSameVerbs(t *testing.T) {
	catalogs, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	reference := catalogs[Default]
	for _, key := range reference.Keys() {
		want := strings.Count(reference.Get(key), "%")
		for _, code := range Codes() {
			if got := strings.Count(catalogs[code].Get(key), "%"); got != want {
				t.Errorf("%s: %s has %d %% verbs, %s has %d", code, key, got, Default, want)
			}
		}
	}
}

// Les catalogues sont des formats Go lus aussi par le navigateur : un « % »
// se double, sans quoi fmt.Sprintf y voit un verbe (« % u ») et le front l'ignore.
func TestLoad_EveryPercentIsAVerbOrEscaped(t *testing.T) {
	catalogs, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range Codes() {
		catalog := catalogs[code]
		for _, key := range catalog.Keys() {
			value := catalog.Get(key)
			for i := 0; i < len(value); i++ {
				if value[i] != '%' {
					continue
				}
				if i+1 >= len(value) || !strings.ContainsRune("sd%", rune(value[i+1])) {
					t.Errorf("%s: %s has a lone %% in %q", code, key, value)
					break
				}
				i++
			}
		}
	}
}

func TestGet_MissingKey_IsVisibleNotSilent(t *testing.T) {
	catalog := Catalog{code: French, strings: map[string]string{}}
	if got := catalog.Get("nav.nowhere"); got != "[nav.nowhere]" {
		t.Fatalf("got %q", got)
	}
}

func TestParse_RejectsEmptyAndNonStringValues(t *testing.T) {
	if _, err := parse([]byte("a = \"\"\n")); err == nil {
		t.Error("empty value accepted")
	}
	if _, err := parse([]byte("a = 3\n")); err == nil {
		t.Error("number accepted")
	}
	flat, err := parse([]byte("[nav]\nmachines = \"Machines\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if flat["nav.machines"] != "Machines" {
		t.Fatalf("got %v", flat)
	}
}

func TestFor_UnknownCode_FallsBackToDefault(t *testing.T) {
	catalogs, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := catalogs.For("xx").Code(); got != Default {
		t.Fatalf("got %s", got)
	}
}

func diff(want, got []string) string {
	var lines []string
	for _, key := range want {
		if !slices.Contains(got, key) {
			lines = append(lines, "  missing "+key)
		}
	}
	for _, key := range got {
		if !slices.Contains(want, key) {
			lines = append(lines, "  extra   "+key)
		}
	}
	return strings.Join(lines, "\n")
}

// La carte MCP de Paramètres ne promet pas « jamais un secret » : les
// journaux de conteneur sont bruts.
func TestLoad_MCPCardAdmitsRawLogs(t *testing.T) {
	catalogs, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for code, catalog := range catalogs {
		for _, key := range []string{"mcp.intro", "mcp.tools", "mcp.never"} {
			text := strings.ToLower(catalog.Get(key))
			if strings.Contains(text, "jamais un secret") || strings.Contains(text, "never a secret") {
				t.Errorf("%s %s: promises no secret: %s", code, key, text)
			}
		}
		if intro := catalog.Get("mcp.intro"); !strings.Contains(strings.ToLower(intro), map[Code]string{"fr": "journaux", "en": "logs"}[code]) {
			t.Errorf("%s mcp.intro does not mention container logs: %s", code, intro)
		}
	}
}

// Le nom du produit s'écrit dans brand, pas dans les catalogues : une valeur
// brute le nomme par un marqueur. Les clés, elles, gardent leur nom.
func TestFiles_NameOnlyThroughPlaceholders(t *testing.T) {
	for _, code := range Codes() {
		content, err := files.ReadFile(string(code) + ".toml")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := parse(content)
		if err != nil {
			t.Fatal(err)
		}
		for key, value := range raw {
			if strings.Contains(strings.ToLower(value), "opencloud") {
				t.Errorf("%s: %s names the product instead of a placeholder: %q", code, key, value)
			}
		}
	}
}

func TestLoad_ExpandsPlaceholders(t *testing.T) {
	catalogs, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range Codes() {
		catalog := catalogs[code]
		if got := catalog.Get("app.name"); got != brand.ProductName {
			t.Errorf("%s: app.name = %q, want %q", code, got, brand.ProductName)
		}
		for _, key := range []string{"auth.unauthorized", "auth.forward_unauthorized"} {
			if got := catalog.Get(key); !strings.Contains(got, brand.Command+" auth disable") {
				t.Errorf("%s: %s = %q, want the command %q", code, key, got, brand.Command+" auth disable")
			}
		}
		if got := catalog.Get("channel.secret_help"); !strings.Contains(got, brand.WebhookSignatureHeader) {
			t.Errorf("%s: channel.secret_help = %q, want the header %q", code, got, brand.WebhookSignatureHeader)
		}
	}
}

func TestLoad_NoPlaceholderLeft(t *testing.T) {
	catalogs, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for code, catalog := range catalogs {
		for _, key := range catalog.Keys() {
			if value := catalog.Get(key); strings.ContainsAny(value, "{}") {
				t.Errorf("%s: %s keeps a brace: %q", code, key, value)
			}
		}
	}
}

func TestExpand_RejectsUnknownPlaceholder(t *testing.T) {
	for _, value := range []string{"{produt} est là", "ouvert {", "fermé }"} {
		if _, err := expand(value); err == nil {
			t.Errorf("expand(%q) accepted a stray brace", value)
		}
	}
	got, err := expand("{product} : {command}")
	if err != nil || got != brand.ProductName+" : "+brand.Command {
		t.Errorf("expand known = %q, %v", got, err)
	}
	got, err = expand("{label_prefix}service")
	if err != nil || got != brand.LabelPrefix+"service" {
		t.Errorf("expand label prefix = %q, %v", got, err)
	}
}
