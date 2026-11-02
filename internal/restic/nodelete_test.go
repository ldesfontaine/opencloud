package restic

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// La non-suppression, gardée par le code lui-même : si un chemin vers la
// purge apparaît (une route, un verbe, un autre composant qui envoie
// restic.retain), ce test échoue et c'est à Lucas de trancher.
//
// Un chemin vers la purge est une mention de restic.retain dans du code
// hors test. Seuls y ont droit : la définition du genre et sa liste
// (agentwire), son exécution (agent), son suivi (action), et son envoi, ici,
// par la nuit du serveur.

var retainMentions = []string{"KindResticRetain", "sendRetain", "retainRepo", "retainAfterNight", "RunIfDue"}

var retainAllowed = map[string][]string{
	"KindResticRetain": {"internal/agentwire/restic.go", "internal/agentwire/command.go", "internal/action/action.go", "internal/agent/orders.go", "internal/restic/retain.go"},
	"sendRetain":       {"internal/restic/retain.go"},
	"retainRepo":       {"internal/restic/retain.go"},
	"retainAfterNight": {"internal/restic/retain.go", "internal/restic/watch.go"},
	// La nuit : la boucle du service la lance, aucune route ne la nomme.
	"RunIfDue": {"internal/restic/watch.go", "internal/backup/watch.go"},
}

func TestRetain_OnlyTheNightOfTheServerReachesThePurge(t *testing.T) {
	root := filepath.Join("..", "..")
	found := map[string][]string{}
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return walkErr
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, path)
		ast.Inspect(parsed, func(node ast.Node) bool {
			var name string
			switch typed := node.(type) {
			case *ast.Ident:
				name = typed.Name
			case *ast.SelectorExpr:
				name = typed.Sel.Name
			default:
				return true
			}
			if slices.Contains(retainMentions, name) && !slices.Contains(found[name], filepath.ToSlash(relative)) {
				found[name] = append(found[name], filepath.ToSlash(relative))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, files := range found {
		for _, file := range files {
			if !slices.Contains(retainAllowed[name], file) {
				t.Errorf("%s est mentionné par %s : un chemin vers la purge de plus", name, file)
			}
		}
	}
	if len(found["KindResticRetain"]) == 0 {
		t.Fatal("aucune mention de KindResticRetain : le test ne voit plus rien")
	}
}

// retainAfterNight n'est appelé que par la fin de la nuit (RunIfDue, que
// seule la boucle du service lance, voir ci-dessus), jamais par une autre
// méthode exportée du service.
func TestRetain_NoExportedMethodOfTheServiceCallsIt(t *testing.T) {
	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	callers := map[string]bool{}
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range parsed.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction || function.Body == nil {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, isCall := node.(*ast.CallExpr)
				if !isCall {
					return true
				}
				if selector, isSelector := call.Fun.(*ast.SelectorExpr); isSelector && selector.Sel.Name == "retainAfterNight" {
					callers[function.Name.Name] = true
				}
				return true
			})
		}
	}
	for caller := range callers {
		if ast.IsExported(caller) && caller != "RunIfDue" {
			t.Errorf("%s (exportée) appelle retainAfterNight : une route pourrait purger", caller)
		}
	}
	if len(callers) == 0 {
		t.Fatal("retainAfterNight n'a aucun appelant : le test ne voit plus rien")
	}
}
