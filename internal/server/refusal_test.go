package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const refusalImportPath = "github.com/ldesfontaine/opencloud/internal/refusal"

// Chaque refus déclaré dans le code (refusal.Invalid, refusal.Conflict) a sa
// clé dans chaque catalogue, et deux refus ne partagent jamais une clé : une
// clé recopiée de travers se voit ici avant que le front n'affiche « [clé] ».
func TestRefusals_KeysExistInEveryCatalog(t *testing.T) {
	server := newTestServer(t)
	declared := declaredRefusalKeys(t, filepath.Join("..", ".."))
	if len(declared) == 0 {
		t.Fatal("no refusal declared: the guard no longer sees the code")
	}
	seen := map[string]string{}
	for _, refusal := range declared {
		if other, taken := seen[refusal.key]; taken {
			t.Errorf("%s: key %q already declared at %s", refusal.at, refusal.key, other)
		}
		seen[refusal.key] = refusal.at
		requireKeyInEveryCatalog(t, server, refusal.key)
	}
}

// declaredKey est la clé d'un refus et l'endroit du source qui la déclare.
type declaredKey struct {
	key string
	at  string
}

// declaredRefusalKeys relève la clé de chaque refusal.Invalid et
// refusal.Conflict du code de production du module. Une clé qui n'est pas un
// littéral échoue : le test ne saurait pas la vérifier.
func declaredRefusalKeys(t *testing.T, root string) []declaredKey {
	t.Helper()
	var keys []declaredKey
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "web" || entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		keys = append(keys, refusalKeysIn(t, fileSet, file)...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

// refusalKeysIn relève les clés des refus déclarés dans un fichier, sous le
// nom que le fichier donne au paquet refusal.
func refusalKeysIn(t *testing.T, fileSet *token.FileSet, file *ast.File) []declaredKey {
	t.Helper()
	local, imported := refusalImportName(file)
	if !imported {
		return nil
	}
	var keys []declaredKey
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !isRefusalDeclaration(call, local) {
			return true
		}
		at := fileSet.Position(call.Pos()).String()
		literal, ok := call.Args[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			t.Errorf("%s: key must be a string literal", at)
			return true
		}
		key, err := strconv.Unquote(literal.Value)
		if err != nil {
			t.Errorf("%s: %v", at, err)
			return true
		}
		keys = append(keys, declaredKey{key: key, at: at})
		return true
	})
	return keys
}

// refusalImportName rend le nom local du paquet refusal dans le fichier, s'il
// l'importe.
func refusalImportName(file *ast.File) (string, bool) {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != refusalImportPath {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name, true
		}
		return "refusal", true
	}
	return "", false
}

// isRefusalDeclaration dit si l'appel est local.Invalid(…) ou
// local.Conflict(…).
func isRefusalDeclaration(call *ast.CallExpr, local string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (selector.Sel.Name != "Invalid" && selector.Sel.Name != "Conflict") {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == local && len(call.Args) > 0
}
