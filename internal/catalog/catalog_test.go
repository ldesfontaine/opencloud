package catalog

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/scripts"
)

// La fixture du catalogue : toute action ajoutée casse ce test, et c'est ce
// qu'on veut — une action nouvelle se relit ici avant d'être visible.
func TestDefinitions_AreTheFrozenList(t *testing.T) {
	definitions := Definitions()
	if len(definitions) != 1 {
		t.Fatalf("le catalogue tient %d actions, la fixture en fige 1 : mettre la fixture à jour", len(definitions))
	}

	diagnostiquer := definitions[0]
	if diagnostiquer.Kind != KindDiagnostiquer {
		t.Errorf("Kind = %q", diagnostiquer.Kind)
	}
	if diagnostiquer.Label != "Diagnostiquer" {
		t.Errorf("Label = %q", diagnostiquer.Label)
	}
	if diagnostiquer.Scope != ScopeMachine {
		t.Errorf("Scope = %q, attendu %q", diagnostiquer.Scope, ScopeMachine)
	}
	if diagnostiquer.Place != PlaceTarget {
		t.Errorf("Place = %q, attendu %q", diagnostiquer.Place, PlaceTarget)
	}
	if !diagnostiquer.Reversible {
		t.Error("Diagnostiquer est réversible : elle ne change rien")
	}
	if diagnostiquer.Interrupts {
		t.Error("Diagnostiquer n'interrompt aucun service")
	}
	if diagnostiquer.Timeout != 5*time.Minute {
		t.Errorf("Timeout = %s, attendu 5m", diagnostiquer.Timeout)
	}
	if len(diagnostiquer.Params) != 0 {
		t.Errorf("Params = %v, attendu aucun", diagnostiquer.Params)
	}
	if diagnostiquer.NeedsConfirmation() {
		t.Error("une action réversible qui n'interrompt rien ne se confirme pas")
	}
}

func TestLookup_FindsWhatTheCatalogHoldsAndNothingElse(t *testing.T) {
	if _, found := Lookup(KindDiagnostiquer); !found {
		t.Error("Diagnostiquer doit être trouvée")
	}
	// Enroler existe dans les types, sans définition pour l'instant.
	if _, found := Lookup(KindEnroler); found {
		t.Error("Enroler n'a pas encore de définition")
	}
	if _, found := Lookup(Kind("inconnue")); found {
		t.Error("une action inconnue ne doit pas être trouvée")
	}
}

// La bijection : une définition sans script ne partirait jamais, un script
// sans définition ne serait jamais visible.
func TestDefinitions_AndScripts_MatchOneForOne(t *testing.T) {
	for _, definition := range Definitions() {
		if _, err := scripts.Script(string(definition.Kind)); err != nil {
			t.Errorf("l'action %q n'a pas de script : %v", definition.Kind, err)
		}
	}
	for _, kind := range scripts.Kinds() {
		if _, found := Lookup(Kind(kind)); !found {
			t.Errorf("le script %q n'a pas de définition au catalogue", kind)
		}
	}
}

// Les scripts et le catalogue disent la même chose : les préfixes que Go relit
// sont ceux que le shell écrit, et les codes de retour se répondent.
func TestScripts_WriteThePrefixesAndExitCodesTheCatalogDeclares(t *testing.T) {
	script, err := scripts.Script(string(KindDiagnostiquer))
	if err != nil {
		t.Fatalf("assembler le script : %v", err)
	}
	assembled := string(script)

	expected := []string{
		"printf '" + StepPrefix + " %s\\n'",
		"printf '" + ResultPrefix + " refusé — %s\\n'",
		"printf '" + ResultPrefix + " échoué — %s\\n'",
		"printf '" + ResultPrefix + " inchangé\\n'",
		"printf '" + ResultPrefix + " fait\\n'",
		fmt.Sprintf("exit %d", ExitDone),
		fmt.Sprintf("exit %d", ExitFailed),
		fmt.Sprintf("exit %d", ExitRefused),
	}
	for _, line := range expected {
		if !strings.Contains(assembled, line) {
			t.Errorf("le script ne dit pas %q", line)
		}
	}
}
