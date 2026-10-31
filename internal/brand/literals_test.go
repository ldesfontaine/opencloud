package brand

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Une exception : un littéral qui nomme le produit sans pouvoir passer par
// ce paquet, avec la raison.
type exception struct {
	file    string
	literal string
}

var exceptions = map[exception]string{
	{"internal/alert/payload.go", "alert.open_in_opencloud"}:          "clé de catalogue : les clés ne changent pas avec le nom",
	{"internal/server/api_tokens.go", `json:"opencloud_url"`}:         "clé JSON de l'API, lue par le front ; elle nomme la variable OPENCLOUD_URL du .env de mcp-maison",
	{"internal/server/api_settings_mcp.go", `json:"opencloud_tools"`}: "clé JSON de l'API, lue par le front ; elle compte les outils du groupe opencloud de mcp-maison",
}

// Les préfixes de jetons ne s'écrivent pas non plus ailleurs : un littéral
// « oc_ » hors de ce paquet est un schéma de jeton copié.
var tokenSchemes = []string{"oc_", "ock_"}

// Le nom du produit s'écrit dans ce paquet et nulle part ailleurs : aucun
// littéral du code Go de production ne le contient, ni ne vaut un schéma de
// jeton. Les commentaires et les identifiants Go ne sont pas des littéraux.
// Le front est tenu de même : les chaînes et le texte des composants .tsx ne
// nomment pas le produit, le mot-symbole se lit dans la page (Wordmark).
func TestNameOnlyInBrand(t *testing.T) {
	root := moduleRoot(t)
	used := make(map[exception]bool)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			switch relative {
			case ".git", "web/node_modules", "web/dist", "internal/brand":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".tsx") && strings.HasPrefix(relative, "web/src/") && !strings.HasSuffix(path, ".test.tsx") {
			checkFrontFile(t, root, path, relative, used)
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Errorf("%s: %v", relative, err)
			return nil
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.ImportSpec:
				return false
			case *ast.BasicLit:
				if typed.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(typed.Value)
				if err != nil {
					t.Errorf("%s: %s: %v", relative, typed.Value, err)
					return true
				}
				if !namesTheProduct(value) {
					return true
				}
				key := exception{relative, value}
				if _, ok := exceptions[key]; ok {
					used[key] = true
					return true
				}
				t.Errorf("%s: literal %q names the product: use internal/brand", relative, value)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for key, why := range exceptions {
		if !used[key] {
			t.Errorf("exception no longer used, remove it: %s %q (%s)", key.file, key.literal, why)
		}
	}
}

func checkFrontFile(t *testing.T, root, path, relative string, used map[exception]bool) {
	t.Helper()
	source, err := os.ReadFile(path) // #nosec G304 -- le parcours du dépôt par le test.
	if err != nil {
		t.Errorf("%s: %v", relative, err)
		return
	}
	for _, value := range frontLiterals(string(source)) {
		if !namesTheProduct(value) {
			continue
		}
		key := exception{relative, value}
		if _, ok := exceptions[key]; ok {
			used[key] = true
			continue
		}
		t.Errorf("%s: %q names the product: read it from the page (Wordmark, productName)", relative, excerpt(value))
	}
}

// excerpt garde l'entourage du nom : le texte des balises est mis bout à bout
// et peut être long.
func excerpt(value string) string {
	at := strings.Index(strings.ToLower(value), "opencloud")
	if at < 0 {
		return value
	}
	return value[max(0, at-20):min(len(value), at+29)]
}

var jsxText = regexp.MustCompile(`>([^<>{}]*)<`)

// frontLiterals rend ce qu'un composant .tsx écrit comme texte : le contenu
// de ses chaînes, et le texte de ses balises mis bout à bout (le mot-symbole
// « open »+« Cloud » est écrit en deux balises). Les commentaires et les
// identifiants n'en sont pas. Un balayage, pas un analyseur : il suffit à
// garder la règle.
func frontLiterals(source string) []string {
	literals := []string{}
	code := strings.Builder{}
	for i := 0; i < len(source); {
		switch c := source[i]; {
		case strings.HasPrefix(source[i:], "//"):
			for i < len(source) && source[i] != '\n' {
				i++
			}
		case strings.HasPrefix(source[i:], "/*"):
			end := strings.Index(source[i+2:], "*/")
			if end < 0 {
				i = len(source)
			} else {
				i += end + 4
			}
		case c == '"' || c == '\'' || c == '`':
			start := i + 1
			i = start
			for i < len(source) && source[i] != c {
				if source[i] == '\\' {
					i++
				}
				i++
			}
			if i > len(source) {
				i = len(source)
			}
			literals = append(literals, source[start:i])
			code.WriteString(`""`)
			i++
		default:
			code.WriteByte(c)
			i++
		}
	}
	texts := []string{}
	for _, match := range jsxText.FindAllStringSubmatch(code.String(), -1) {
		if text := strings.TrimSpace(match[1]); text != "" {
			texts = append(texts, text)
		}
	}
	return append(literals, strings.Join(texts, ""))
}

func namesTheProduct(value string) bool {
	if strings.Contains(strings.ToLower(value), "opencloud") {
		return true
	}
	for _, scheme := range tokenSchemes {
		if value == scheme {
			return true
		}
	}
	return false
}

// moduleRoot remonte du répertoire du test jusqu'à go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

func TestFrontLiterals_ReadsStringsAndTagTextButNotCommentsOrIdentifiers(t *testing.T) {
	source := "// openCloud en commentaire\n" +
		"/* openCloud */\n" +
		"const opencloud_tools = 3;\n" +
		"const a = \"http://x\"; const b = 'it\\'s'; const c = `tpl`;\n" +
		"<span class=\"wordmark\"><span>open</span><b>Cloud</b></span>"
	got := frontLiterals(source)
	want := []string{"http://x", "it\\'s", "tpl", "wordmark", "openCloud"}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("literal %d: got %q, want %q", index, got[index], want[index])
		}
	}
}

func TestFrontLiterals_FlagsAWordmarkWrittenInTags(t *testing.T) {
	if !slicesContainsProduct(frontLiterals(`<b>open</b><b>Cloud</b>`)) {
		t.Fatal("le mot-symbole en balises n'est pas vu")
	}
	if slicesContainsProduct(frontLiterals(`const opencloud_tools = 3; // openCloud`)) {
		t.Fatal("un identifiant ou un commentaire ne nomme pas le produit")
	}
}

func slicesContainsProduct(literals []string) bool {
	for _, literal := range literals {
		if namesTheProduct(literal) {
			return true
		}
	}
	return false
}
