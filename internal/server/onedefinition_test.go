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

// relayedWaits relève les fonctions `wait…` qui ne font que passer
// l'attente à waittest.Until : l'appelant nomme lui-même ce qu'il attend.
func relayedWaits(fileSet *token.FileSet, file *ast.File) []string {
	var found []string
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil || !strings.HasPrefix(strings.ToLower(function.Name.Name), "wait") {
			continue
		}
		if onlyCallsUntil(function) {
			found = append(found, fileSet.Position(function.Pos()).String()+": "+function.Name.Name+" relaie waittest.Until, l'appeler directement")
		}
	}
	return found
}

// onlyCallsUntil dit si le corps se réduit à `waittest.Until(…)`, avec ou
// sans `t.Helper()`, et si la condition est celle que la fonction a reçue :
// un helper qui bâtit sa propre condition (un compteur, un fichier) nomme
// une attente, il ne relaie rien.
func onlyCallsUntil(function *ast.FuncDecl) bool {
	calls := 0
	for _, statement := range function.Body.List {
		expression, ok := statement.(*ast.ExprStmt)
		if !ok {
			return false
		}
		call, ok := expression.X.(*ast.CallExpr)
		if !ok {
			return false
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		switch {
		case selector.Sel.Name == "Helper":
		case selector.Sel.Name == "Until" && isIdent(selector.X, "waittest") && len(call.Args) > 0 && isParameter(function, call.Args[len(call.Args)-1]):
			calls++
		default:
			return false
		}
	}
	return calls == 1
}

func isParameter(function *ast.FuncDecl, expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return false
	}
	for _, field := range function.Type.Params.List {
		for _, name := range field.Names {
			if name.Name == ident.Name {
				return true
			}
		}
	}
	return false
}

func isIdent(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name
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
		for _, at := range relayedWaits(fileSet, file) {
			t.Errorf("%s", at)
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

// machineFactKey reconnaît la clé d'un fait de machine (hors ligne, inconnue,
// sans agent) déclarée sous le nom d'un autre domaine : « backup.machine_offline ».
var machineFactKey = regexp.MustCompile(`^"([a-z_]+)\.(machine_offline|machine_unknown|machine_invalid|no_agent)"$`)

// machineFactKeyExceptions : les clés de même suffixe qui disent autre chose
// que le fait de machine, avec la raison.
var machineFactKeyExceptions = map[string]string{
	"manifest.machine_unknown": "une ligne du manifeste nomme une machine absente : la clé porte la ligne et la valeur, ce n'est pas le refus d'une machine visée",
}

// machineFactKeys relève les littéraux qui déclarent le refus d'un fait de
// machine hors de `machine`.
func machineFactKeys(fileSet *token.FileSet, file *ast.File) []string {
	var found []string
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		match := machineFactKey.FindStringSubmatch(literal.Value)
		if match == nil || match[1] == "machine" {
			return true
		}
		if _, excepted := machineFactKeyExceptions[strings.Trim(literal.Value, `"`)]; excepted {
			return true
		}
		found = append(found, fileSet.Position(literal.Pos()).String()+": "+literal.Value)
		return true
	})
	return found
}

// Un fait de machine (hors ligne, inconnue, sans agent) se refuse avec la
// sentinelle de `machine`, jamais avec une copie sous la clé d'un domaine.
func TestOneDefinition_MachineFacts(t *testing.T) {
	checked := 0
	forEachGoFile(t, moduleRoot(), false, func(path string, fileSet *token.FileSet, file *ast.File) {
		if isException(path) {
			return
		}
		checked++
		for _, at := range machineFactKeys(fileSet, file) {
			t.Errorf("%s : utiliser machine.ErrOffline, machine.ErrUnknown ou machine.ErrNoAgent", at)
		}
	})
	if checked == 0 {
		t.Fatal("no source file seen: the guard no longer sees the code")
	}
}

// Les gardes du lot « droits et capacités » : ce que l'agent d'une machine a le
// droit de faire, et sait faire, se juge dans `machine` (Agent.Fit, Supports).

// isAgentSelector dit si l'expression est « quelque chose.Agent » : l'agent
// d'un machine.Status.
func isAgentSelector(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "Agent"
}

// agentRightJudgments relève ce qui juge un droit de l'agent à la main :
// « .Agent.ReportedAt.IsZero() », « .Agent.Capabilities » lu et
// « .Agent.UnavailableReason(…) ».
func agentRightJudgments(fileSet *token.FileSet, file *ast.File) []string {
	var found []string
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		reportedZero := selector.Sel.Name == "IsZero" && isReportedAtOfAgent(selector.X)
		readCapabilities := selector.Sel.Name == "Capabilities" && isAgentSelector(selector.X)
		readReason := selector.Sel.Name == "UnavailableReason" && isAgentSelector(selector.X)
		if reportedZero || readCapabilities || readReason {
			found = append(found, fileSet.Position(selector.Pos()).String()+": "+selector.Sel.Name)
		}
		return true
	})
	return found
}

func isReportedAtOfAgent(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "ReportedAt" && isAgentSelector(selector.X)
}

// redeclaredRightStates relève « type RightState » et « type RightFit »
// déclarés hors de `machine`.
func redeclaredRightStates(fileSet *token.FileSet, file *ast.File) []string {
	var found []string
	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.TypeSpec)
		if ok && (spec.Name.Name == "RightState" || spec.Name.Name == "RightFit") {
			found = append(found, fileSet.Position(spec.Pos()).String()+": "+spec.Name.Name)
		}
		return true
	})
	return found
}

// versionJudgments relève release.ParseVersion appelé sur la version d'un agent
// ou sur une constante d'agentwire : savoir ce que la version d'un agent sait
// faire est Agent.Supports.
func versionJudgments(fileSet *token.FileSet, file *ast.File) []string {
	var found []string
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		callee, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || callee.Sel.Name != "ParseVersion" {
			return true
		}
		switch argument := call.Args[0].(type) {
		case *ast.SelectorExpr:
			_, fromPackage := argument.X.(*ast.Ident)
			if isAgentSelector(argument.X) || (fromPackage && argument.X.(*ast.Ident).Name == "agentwire") {
				found = append(found, fileSet.Position(call.Pos()).String()+": ParseVersion")
			}
		}
		return true
	})
	return found
}

func TestOneDefinition_AgentRightsAreJudgedInMachine(t *testing.T) {
	checked := 0
	forEachGoFile(t, moduleRoot(), false, func(path string, fileSet *token.FileSet, file *ast.File) {
		relative := filepath.ToSlash(strings.TrimPrefix(path, moduleRoot()+string(filepath.Separator)))
		if isException(path) || strings.HasPrefix(relative, "internal/machine/") {
			return
		}
		checked++
		for _, at := range agentRightJudgments(fileSet, file) {
			t.Errorf("%s : juger un droit de l'agent par machine.Agent.Fit", at)
		}
		for _, at := range redeclaredRightStates(fileSet, file) {
			t.Errorf("%s : l'état d'un droit est machine.RightState", at)
		}
		for _, at := range versionJudgments(fileSet, file) {
			t.Errorf("%s : ce que la version d'un agent sait faire est machine.Agent.Supports", at)
		}
	})
	if checked == 0 {
		t.Fatal("no source file seen: the guard no longer sees the code")
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

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	waittest.Until(t, "condition", condition)
}

var ErrOffline = refusal.Conflict("backup.machine_offline", "offline")

type RightState string

func judge(target machine.Status) bool {
	_ = release.ParseVersion(target.Agent.Version)
	_ = target.Agent.UnavailableReason("docker.deploy")
	return !target.Agent.ReportedAt.IsZero()
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
	if got := relayedWaits(fileSet, file); len(got) != 1 {
		t.Errorf("relayed wait not caught: %v", got)
	}
	if got := copiedHelpers(fileSet, file); len(got) != 1 {
		t.Errorf("copied helper not caught: %v", got)
	}
	if got := redefinedUpdateFlags(fileSet, file); len(got) != 1 {
		t.Errorf("redefined -update flag not caught: %v", got)
	}
	if got := machineFactKeys(fileSet, file); len(got) != 1 {
		t.Errorf("machine fact key outside machine not caught: %v", got)
	}
	if got := agentRightJudgments(fileSet, file); len(got) != 2 {
		t.Errorf("hand-made right judgment not caught: %v", got)
	}
	if got := redeclaredRightStates(fileSet, file); len(got) != 1 {
		t.Errorf("redeclared right state not caught: %v", got)
	}
	if got := versionJudgments(fileSet, file); len(got) != 1 {
		t.Errorf("agent version judged outside machine not caught: %v", got)
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
