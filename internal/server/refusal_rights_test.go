package server

import (
	"encoding/json"
	"go/ast"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/backup"
	"github.com/ldesfontaine/opencloud/internal/deploy"
	"github.com/ldesfontaine/opencloud/internal/goldentest"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

// rightRefusals : les refus qui nomment un droit de l'agent. Le front lit leur
// droit pour savoir quelle machine ré-enrôler (web/src/lib/rights.ts).
var rightRefusals = map[string]*refusal.Error{
	"deploy.right_missing":                deploy.ErrRightMissing,
	"backup.right_missing":                backup.ErrRightMissing,
	"backup.source_right_missing":         backup.ErrSourceRightMissing,
	"restic.target_right_missing":         restic.ErrTargetRightMissing,
	"restic.source_right_missing":         restic.ErrSourceRightMissing,
	"restic.source_vm_right_missing":      restic.ErrSourceVMRightMissing,
	"restic.source_docker_right_missing":  restic.ErrSourceDockerRightMissing,
	"restic.target_verify_right_missing":  restic.ErrTargetVerifyRightMissing,
	"restic.target_browse_right_missing":  restic.ErrTargetBrowseRightMissing,
	"restic.source_restore_right_missing": restic.ErrSourceRestoreRightMissing,
	"restic.restore_outside_right":        restic.ErrRestoreOutsideRight,
}

func TestRefusalRights_GoldenSaysWhichRightEachRefusalNames(t *testing.T) {
	rights := map[string]string{}
	for key, refused := range rightRefusals {
		if got, ok := refusal.KeyOf(refused); !ok || got != key {
			t.Errorf("%s: the sentinel carries key %q", key, got)
		}
		rights[key] = refused.Right()
	}
	encoded, err := json.MarshalIndent(rights, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	goldentest.Compare(t, filepath.Join("testdata", "refusal_rights.golden.json"), string(encoded)+"\n")
}

// needsRightKeys relève les clés des refus déclarés avec NeedsRight.
func needsRightKeys(fileSet *token.FileSet, file *ast.File) []string {
	var keys []string
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "NeedsRight" {
			return true
		}
		declaration, ok := selector.X.(*ast.CallExpr)
		if !ok || len(declaration.Args) == 0 {
			return true
		}
		if literal, ok := declaration.Args[0].(*ast.BasicLit); ok && literal.Kind == token.STRING {
			if key, err := strconv.Unquote(literal.Value); err == nil {
				keys = append(keys, key)
			}
		}
		return true
	})
	return keys
}

// Un refus qui nomme un droit s'ajoute à la liste ci-dessus, donc au golden
// que le front compare à sa table : l'oublier casse ce test.
func TestRefusalRights_EveryDeclarationIsListed(t *testing.T) {
	var declared []string
	forEachGoFile(t, moduleRoot(), false, func(path string, fileSet *token.FileSet, file *ast.File) {
		declared = append(declared, needsRightKeys(fileSet, file)...)
	})
	if len(declared) == 0 {
		t.Fatal("no refusal names a right: the guard no longer sees the code")
	}
	for _, key := range declared {
		if _, listed := rightRefusals[key]; !listed {
			t.Errorf("%s names a right but is not in rightRefusals", key)
		}
	}
	for key := range rightRefusals {
		if !slices.Contains(declared, key) {
			t.Errorf("%s is in rightRefusals but no declaration calls NeedsRight", key)
		}
	}
}
