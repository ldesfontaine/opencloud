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
	if len(definitions) != 3 {
		t.Fatalf("le catalogue tient %d actions, la fixture en fige 3 : mettre la fixture à jour", len(definitions))
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

	enroler := definitions[1]
	if enroler.Kind != KindEnroler {
		t.Errorf("Kind = %q", enroler.Kind)
	}
	if enroler.Label != "Enrôler" {
		t.Errorf("Label = %q", enroler.Label)
	}
	if enroler.Scope != ScopeMachine {
		t.Errorf("Scope = %q, attendu %q", enroler.Scope, ScopeMachine)
	}
	if enroler.Place != PlaceTarget {
		t.Errorf("Place = %q, attendu %q", enroler.Place, PlaceTarget)
	}
	if !enroler.Reversible || enroler.Interrupts {
		t.Error("Enrôler est réversible et n'interrompt aucun service")
	}
	if enroler.Timeout != 5*time.Minute {
		t.Errorf("Timeout = %s, attendu 5m", enroler.Timeout)
	}
	if len(enroler.Params) != 1 {
		t.Fatalf("Params = %v, attendu la seule clé publique", enroler.Params)
	}
	publicKey := enroler.Params[0]
	if publicKey.Name != "public_key" || publicKey.Type != ParamPublicKey || !publicKey.Required {
		t.Errorf("paramètre = %+v", publicKey)
	}

	socle := definitions[2]
	if socle.Kind != KindSocle {
		t.Errorf("Kind = %q", socle.Kind)
	}
	if socle.Label != "Poser le socle" {
		t.Errorf("Label = %q", socle.Label)
	}
	if socle.Scope != ScopeMachine {
		t.Errorf("Scope = %q, attendu %q", socle.Scope, ScopeMachine)
	}
	if socle.Place != PlaceTarget {
		t.Errorf("Place = %q, attendu %q", socle.Place, PlaceTarget)
	}
	if !socle.Reversible || socle.Interrupts {
		t.Error("Poser le socle est réversible et ne coupe aucun service")
	}
	if socle.Timeout != 15*time.Minute {
		t.Errorf("Timeout = %s, attendu 15m : apt peut être lent", socle.Timeout)
	}
	if len(socle.Params) != 0 {
		t.Errorf("Params = %v, attendu aucun", socle.Params)
	}
	if socle.NeedsConfirmation() {
		t.Error("une action réversible qui n'interrompt rien ne se confirme pas")
	}
}

// La liste de paquets du socle est la seule source : elle est embarquée, elle
// n'est pas vide, et elle ne répète aucun paquet.
func TestSocle_PackageList_IsEmbeddedWithoutRepeats(t *testing.T) {
	packages, err := scripts.Packages(string(KindSocle))
	if err != nil {
		t.Fatalf("lire la liste de paquets : %v", err)
	}
	if len(packages) == 0 {
		t.Fatal("la liste de paquets du socle est vide")
	}

	seen := map[string]bool{}
	for _, name := range packages {
		if seen[name] {
			t.Errorf("le paquet %q apparaît deux fois", name)
		}
		seen[name] = true
	}
	// Les paquets du dépôt officiel de Docker : le socle pose Docker et le
	// plugin compose, jamais docker.io.
	for _, name := range []string{"docker-ce", "docker-ce-cli", "containerd.io", "docker-compose-plugin"} {
		if !seen[name] {
			t.Errorf("la liste = %v, sans %s", packages, name)
		}
	}
	if seen["docker.io"] {
		t.Errorf("la liste = %v, avec docker.io : Docker vient de son dépôt officiel", packages)
	}
}

func TestLookup_FindsWhatTheCatalogHoldsAndNothingElse(t *testing.T) {
	if _, found := Lookup(KindDiagnostiquer); !found {
		t.Error("Diagnostiquer doit être trouvée")
	}
	if _, found := Lookup(KindEnroler); !found {
		t.Error("Enroler doit être trouvée")
	}
	if _, found := Lookup(KindSocle); !found {
		t.Error("Poser le socle doit être trouvée")
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
