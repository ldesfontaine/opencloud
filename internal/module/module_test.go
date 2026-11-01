package module

import (
	"slices"
	"testing"
)

func TestIn_EachScopeListsAModuleOnce(t *testing.T) {
	for _, scope := range []Scope{ScopeMachine, ScopeInstallation} {
		ids := In(scope)
		if len(ids) == 0 {
			t.Errorf("la portée %s est vide", scope)
		}
		for _, id := range ids {
			if countOf(ids, id) != 1 {
				t.Errorf("%s listé plus d'une fois dans %s", id, scope)
			}
		}
	}
}

func TestIn_ProbesLiveInBothScopes(t *testing.T) {
	if !slices.Contains(In(ScopeMachine), Probes) || !slices.Contains(In(ScopeInstallation), Probes) {
		t.Fatal("les sondes se montrent sur une machine et dans l'installation")
	}
}

func countOf(ids []ID, wanted ID) int {
	count := 0
	for _, id := range ids {
		if id == wanted {
			count++
		}
	}
	return count
}
