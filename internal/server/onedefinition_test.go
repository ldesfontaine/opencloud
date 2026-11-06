package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Les gardes « une logique, une définition » (17-conventions-code.md) :
// elles lisent le source et refusent la deuxième écriture d'un geste qui a
// déjà sa maison. Une exception se déclare ici, avec sa raison.

// forEachGoFile parcourt le source Go du module (le front et .git exclus) ;
// withTests dit s'il faut aussi les _test.go.
func forEachGoFile(t *testing.T, root string, withTests bool, visit func(path string, fileSet *token.FileSet, file *ast.File)) {
	t.Helper()
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
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if !withTests && strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		visit(path, fileSet, file)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func moduleRoot() string { return filepath.Join("..", "..") }

// Où vit chaque outil de test : seuls ses fichiers peuvent porter le geste.
const (
	waittestDir   = "internal/wait/waittest/"
	goldentestDir = "internal/goldentest/"
	testkitDir    = "internal/testkit/"
)

// oneDefinitionExceptions : les endroits où le geste s'écrit encore à la
// main, avec la raison. Chemin du fichier (relatif au module) → raison.
var oneDefinitionExceptions = map[string]string{}

func isException(path string) bool {
	relative := filepath.ToSlash(strings.TrimPrefix(path, moduleRoot()+string(filepath.Separator)))
	_, excepted := oneDefinitionExceptions[relative]
	return excepted
}

// handWrittenDeadlines relève les `deadline := time.Now().Add(…)` : la
// boucle d'attente écrite à la main dans un test.
func handWrittenDeadlines(fileSet *token.FileSet, file *ast.File) []string {
	var found []string
	ast.Inspect(file, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		name, ok := assign.Lhs[0].(*ast.Ident)
		if !ok || name.Name != "deadline" || !isNowPlus(assign.Rhs[0]) {
			return true
		}
		found = append(found, fileSet.Position(assign.Pos()).String())
		return true
	})
	return found
}

// isNowPlus dit si l'expression est `time.Now().Add(…)`.
func isNowPlus(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	add, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || add.Sel.Name != "Add" {
		return false
	}
	now, ok := add.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	nowFunc, ok := now.Fun.(*ast.SelectorExpr)
	if !ok || nowFunc.Sel.Name != "Now" {
		return false
	}
	pkg, ok := nowFunc.X.(*ast.Ident)
	return ok && pkg.Name == "time"
}

// copiedTestHelpers : les petits gestes de test qui ont une maison. Nom →
// où il vit.
var copiedTestHelpers = map[string]string{
	"ptr":                "new(valeur) (Go 1.26)",
	"compareGolden":      "goldentest.Compare",
	"closedPort":         "testkit.ClosedPort",
	"closedLoopbackPort": "testkit.ClosedPort",
	"mustJSON":           "testkit.MustJSON",
	"sumOf":              "testkit.SHA256Hex",
	"sha256Hex":          "testkit.SHA256Hex",
	"sumHex":             "testkit.SHA256Hex",
	"newRoot":            "testkit.EmptyRoot",
	"openRoot":           "testkit.EmptyRoot",
	"openEmptyRoot":      "testkit.EmptyRoot",
	"emptyStateDir":      "testkit.EmptyRoot",
}

// copiedHelpers relève les fonctions libres (sans receveur) qui recopient
// un geste de testkit ou de goldentest.
func copiedHelpers(fileSet *token.FileSet, file *ast.File) []string {
	var found []string
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv != nil {
			continue
		}
		if home, copied := copiedTestHelpers[function.Name.Name]; copied {
			found = append(found, fileSet.Position(function.Pos()).String()+": "+function.Name.Name+" (utiliser "+home+")")
		}
	}
	return found
}

// redefinedUpdateFlags relève les `flag.Bool("update", …)` : le drapeau
// -update appartient à goldentest.
func redefinedUpdateFlags(fileSet *token.FileSet, file *ast.File) []string {
	var found []string
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Bool" {
			return true
		}
		if pkg, ok := selector.X.(*ast.Ident); !ok || pkg.Name != "flag" {
			return true
		}
		if literal, ok := call.Args[0].(*ast.BasicLit); ok && literal.Value == `"update"` {
			found = append(found, fileSet.Position(call.Pos()).String())
		}
		return true
	})
	return found
}

// Une attente de test se dit `waittest.Until`, jamais une boucle à délai
// écrite à la main : les bornes courtes cassent sous une CI chargée.
func TestOneDefinition_TestWaitsGoThroughWaittest(t *testing.T) {
	checked := 0
	forEachGoFile(t, moduleRoot(), true, func(path string, fileSet *token.FileSet, file *ast.File) {
		if !strings.HasSuffix(path, "_test.go") || strings.Contains(filepath.ToSlash(path), waittestDir) || isException(path) {
			return
		}
		checked++
		for _, at := range handWrittenDeadlines(fileSet, file) {
			t.Errorf("%s: boucle d'attente écrite à la main, utiliser waittest.Until", at)
		}
	})
	if checked == 0 {
		t.Fatal("no test file seen: the guard no longer sees the code")
	}
}

// Un petit geste de test (pointeur, golden, port fermé, JSON, empreinte,
// racine vide) a une seule définition.
func TestOneDefinition_TestHelpersLiveInTheirKit(t *testing.T) {
	checked := 0
	forEachGoFile(t, moduleRoot(), true, func(path string, fileSet *token.FileSet, file *ast.File) {
		slashed := filepath.ToSlash(path)
		if !strings.HasSuffix(path, "_test.go") || strings.Contains(slashed, testkitDir) || strings.Contains(slashed, goldentestDir) || isException(path) {
			return
		}
		checked++
		for _, at := range copiedHelpers(fileSet, file) {
			t.Errorf("%s", at)
		}
		for _, at := range redefinedUpdateFlags(fileSet, file) {
			t.Errorf("%s: le drapeau -update est déclaré par goldentest", at)
		}
	})
	if checked == 0 {
		t.Fatal("no test file seen: the guard no longer sees the code")
	}
}

// Les gardes ne valent que si elles échouent sur la faute : on leur donne
// un source fautif.
func TestOneDefinition_GuardsCatchTheirFault(t *testing.T) {
	const faulty = `package faulty

import (
	"flag"
	"time"
)

var update = flag.Bool("update", false, "")

func ptr(days int) *int { return &days }

func wait() {
	deadline := time.Now().Add(2 * time.Second)
	_ = deadline
}
`
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "faulty_test.go", faulty, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	if got := handWrittenDeadlines(fileSet, file); len(got) != 1 {
		t.Errorf("hand-written deadline not caught: %v", got)
	}
	if got := copiedHelpers(fileSet, file); len(got) != 1 {
		t.Errorf("copied helper not caught: %v", got)
	}
	if got := redefinedUpdateFlags(fileSet, file); len(got) != 1 {
		t.Errorf("redefined -update flag not caught: %v", got)
	}
}

var orderCount = regexp.MustCompile(`(?i)\b(un|deux|trois|quatre|cinq|six|sept|huit|neuf|dix|onze|douze|treize|\d+) ordres existent\b`)

// La liste des ordres vit dans agentwire.OrderKinds : la documentation y
// renvoie, elle ne la compte pas (le compte périme à chaque ordre ajouté).
func TestOneDefinition_DocDoesNotCountTheOrders(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join(moduleRoot(), "docs", "projet", "22-fonctionnement.md"))
	if err != nil {
		t.Fatal(err)
	}
	if found := orderCount.FindString(string(doc)); found != "" {
		t.Errorf("22-fonctionnement.md compte les ordres (%q) : renvoyer à agentwire.OrderKinds", found)
	}
	if !orderCount.MatchString("Huit ordres existent") {
		t.Error("the order-count pattern no longer matches what it must refuse")
	}
}
