package lang

import (
	"slices"
	"strings"
	"testing"
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
