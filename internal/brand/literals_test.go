package brand

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
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
	{"internal/alert/payload.go", "alert.open_in_opencloud"}:         "clé de catalogue : les clés ne changent pas avec le nom",
	{"internal/server/mcp_tools_read.go", mcpMachineIDSchemaLiteral}: "étiquette jsonschema : une étiquette de champ ne se construit pas",
}

const mcpMachineIDSchemaLiteral = "json:\"machine_id\" jsonschema:\"Machine id, or 'local' for the openCloud host itself\""

// Les préfixes de jetons ne s'écrivent pas non plus ailleurs : un littéral
// « oc_ » hors de ce paquet est un schéma de jeton copié.
var tokenSchemes = []string{"oc_", "ocs_", "occ_", "oca_", "ocr_", "ock_"}

// Le nom du produit s'écrit dans ce paquet et nulle part ailleurs : aucun
// littéral du code Go de production ne le contient, ni ne vaut un schéma de
// jeton. Les commentaires et les identifiants Go ne sont pas des littéraux.
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
