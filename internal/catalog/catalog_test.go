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
	if len(definitions) != 7 {
		t.Fatalf("le catalogue tient %d actions, la fixture en fige 7 : mettre la fixture à jour", len(definitions))
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

	dnsToken := definitions[1]
	if dnsToken.Kind != KindDNSToken {
		t.Errorf("Kind = %q", dnsToken.Kind)
	}
	if dnsToken.Label != "Poser le jeton DNS" {
		t.Errorf("Label = %q", dnsToken.Label)
	}
	if dnsToken.Scope != ScopeDomain {
		t.Errorf("Scope = %q, attendu %q", dnsToken.Scope, ScopeDomain)
	}
	if dnsToken.Place != PlaceTarget {
		t.Errorf("Place = %q, attendu %q", dnsToken.Place, PlaceTarget)
	}
	if !dnsToken.Reversible {
		t.Error("Poser le jeton DNS est réversible : on repose l'ancien jeton")
	}
	if !dnsToken.Interrupts {
		t.Error("Poser le jeton DNS coupe brièvement : Traefik redémarre si le fichier change")
	}
	if !dnsToken.NeedsConfirmation() {
		t.Error("une action qui coupe se confirme")
	}
	if dnsToken.Timeout != 5*time.Minute {
		t.Errorf("Timeout = %s, attendu 5m", dnsToken.Timeout)
	}
	if len(dnsToken.Params) != 1 || dnsToken.Params[0].Name != paramZone || !dnsToken.Params[0].Required {
		t.Errorf("Params = %v, attendu la seule zone, requise", dnsToken.Params)
	}
	if dnsToken.Params[0].Type != ParamDomain {
		t.Errorf("la zone est un nom de domaine, type = %q", dnsToken.Params[0].Type)
	}

	enroler := definitions[2]
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

	proxy := definitions[3]
	if proxy.Kind != KindProxy {
		t.Errorf("Kind = %q", proxy.Kind)
	}
	if proxy.Label != "Installer le proxy" {
		t.Errorf("Label = %q", proxy.Label)
	}
	if proxy.Scope != ScopeMachine {
		t.Errorf("Scope = %q, attendu %q", proxy.Scope, ScopeMachine)
	}
	if proxy.Place != PlaceTarget {
		t.Errorf("Place = %q, attendu %q", proxy.Place, PlaceTarget)
	}
	if !proxy.Reversible || proxy.Interrupts {
		t.Error("Installer le proxy est réversible et ne coupe rien : rien ne tourne encore derrière")
	}
	if proxy.Timeout != 10*time.Minute {
		t.Errorf("Timeout = %s, attendu 10m", proxy.Timeout)
	}
	if len(proxy.Params) != 0 {
		t.Errorf("Params = %v, attendu aucun", proxy.Params)
	}
	if proxy.NeedsConfirmation() {
		t.Error("une action réversible qui n'interrompt rien ne se confirme pas")
	}

	socle := definitions[4]
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

	vhost := definitions[5]
	if vhost.Kind != KindVhost {
		t.Errorf("Kind = %q", vhost.Kind)
	}
	if vhost.Label != "Créer un hôte virtuel" {
		t.Errorf("Label = %q", vhost.Label)
	}
	if vhost.Scope != ScopeDomain {
		t.Errorf("Scope = %q, attendu %q", vhost.Scope, ScopeDomain)
	}
	if vhost.Place != PlaceTarget {
		t.Errorf("Place = %q, attendu %q", vhost.Place, PlaceTarget)
	}
	if !vhost.Reversible || vhost.Interrupts {
		t.Error("Créer un hôte virtuel est réversible et ne coupe rien : elle ajoute une route")
	}
	if vhost.Timeout != 5*time.Minute {
		t.Errorf("Timeout = %s, attendu 5m", vhost.Timeout)
	}
	// Le port n'est pas un paramètre : il est lu sur la machine, dans la
	// définition du service (15-catalogue-actions.md §3).
	wantParams := []ParamSpec{
		{Name: paramDomain, Type: ParamDomain},
		{Name: paramEnvironment, Type: ParamSlug},
		{Name: paramService, Type: ParamSlug},
	}
	if len(vhost.Params) != len(wantParams) {
		t.Fatalf("Params = %v, attendu %d paramètres", vhost.Params, len(wantParams))
	}
	for index, want := range wantParams {
		got := vhost.Params[index]
		if got.Name != want.Name || got.Type != want.Type || !got.Required {
			t.Errorf("paramètre %d = %+v, attendu %q de type %q, requis", index, got, want.Name, want.Type)
		}
	}
	for _, spec := range vhost.Params {
		if spec.Name == "port" {
			t.Error("le port ne se saisit pas : il est lu dans la définition du service")
		}
	}

	removal := definitions[6]
	if removal.Kind != KindVhostRemove {
		t.Errorf("Kind = %q", removal.Kind)
	}
	if removal.Label != "Supprimer un hôte virtuel" {
		t.Errorf("Label = %q", removal.Label)
	}
	if removal.Scope != ScopeDomain {
		t.Errorf("Scope = %q, attendu %q", removal.Scope, ScopeDomain)
	}
	if !removal.Interrupts {
		t.Error("Supprimer un hôte virtuel coupe le nom : elle s'annonce comme telle")
	}
	if !removal.NeedsConfirmation() {
		t.Error("une action qui coupe se confirme")
	}
	if len(removal.Params) != 1 || removal.Params[0].Name != paramDomain {
		t.Errorf("Params = %v, attendu le seul nom de domaine", removal.Params)
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
	if _, found := Lookup(KindProxy); !found {
		t.Error("Installer le proxy doit être trouvée")
	}
	if _, found := Lookup(KindSocle); !found {
		t.Error("Poser le socle doit être trouvée")
	}
	if _, found := Lookup(KindVhost); !found {
		t.Error("Créer un hôte virtuel doit être trouvée")
	}
	if _, found := Lookup(KindVhostRemove); !found {
		t.Error("Supprimer un hôte virtuel doit être trouvée")
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
