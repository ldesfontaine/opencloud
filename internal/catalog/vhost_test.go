package catalog

import (
	"errors"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/scripts"
	"github.com/ldesfontaine/opencloud/internal/validate"
)

func vhostParams() map[string]string {
	return map[string]string{
		paramDomain:      "temoin.exemple.fr",
		paramEnvironment: "prod",
		paramService:     "temoin",
	}
}

// fragmentOf rend le seul fichier que l'action dépose.
func fragmentOf(t *testing.T, params map[string]string) string {
	t.Helper()

	prepared, err := Prepare(KindVhost, params)
	if err != nil {
		t.Fatalf("préparer Créer un hôte virtuel : %v", err)
	}
	if len(prepared.Files) != 1 || prepared.Files[0].Path != vhostFragmentName {
		t.Fatalf("l'action dépose %v, attendu le seul %s", prepared.Files, vhostFragmentName)
	}
	return string(prepared.Files[0].Content)
}

// Le fragment route le nom vers le conteneur du service, sur l'entrée
// sécurisée, sans résolveur de certificat : le certificat est une action à
// part.
func TestPrepareVhost_RendersTheFragmentTheProxyReads(t *testing.T) {
	fragment := fragmentOf(t, vhostParams())

	for _, expected := range []string{
		"rule: \"Host(`temoin.exemple.fr`)\"",
		"- websecure",
		"tls: {}",
		"url: \"http://prod-temoin:" + vhostPortPlaceholder + "\"",
	} {
		if !strings.Contains(fragment, expected) {
			t.Errorf("le fragment ne porte pas %q :\n%s", expected, fragment)
		}
	}

	// Le certificat vit dans l'action « Demander un certificat » : un
	// résolveur ici le demanderait tout seul, sans préflight ni budget.
	if strings.Contains(fragment, "certResolver") {
		t.Errorf("le fragment nomme un certResolver :\n%s", fragment)
	}
}

// Le port n'est pas dans le fragment rendu : openCloud ne le connaît pas. Le
// script le lit sur la machine et remplace ce marqueur-là, et lui seul.
func TestPrepareVhost_LeavesThePortToTheMachine(t *testing.T) {
	fragment := fragmentOf(t, vhostParams())

	if strings.Count(fragment, vhostPortPlaceholder) != 1 {
		t.Errorf("le fragment porte %d marqueurs de port, attendu un seul :\n%s",
			strings.Count(fragment, vhostPortPlaceholder), fragment)
	}

	script, err := scripts.Script(string(KindVhost))
	if err != nil {
		t.Fatalf("assembler le script : %v", err)
	}
	if !strings.Contains(string(script), "PORT_PLACEHOLDER='"+vhostPortPlaceholder+"'") {
		t.Error("le script ne connaît pas le marqueur que Go laisse dans le fragment")
	}
}

// Les chemins et les noms sont dérivés des paramètres, jamais saisis : Go et
// le shell doivent les construire pareil, sinon la pose écrirait ailleurs que
// ce que l'écran a montré.
func TestVhostNames_AreBuiltTheSameWayInGoAndInTheScript(t *testing.T) {
	if got := VhostContainerName("prod", "temoin"); got != "prod-temoin" {
		t.Errorf("VhostContainerName = %q, attendu prod-temoin", got)
	}
	if got := VhostFragmentPath("temoin.exemple.fr"); got != "/srv/data/traefik/temoin.exemple.fr.yml" {
		t.Errorf("VhostFragmentPath = %q", got)
	}

	for _, kind := range []Kind{KindVhost, KindVhostRemove} {
		script, err := scripts.Script(string(kind))
		if err != nil {
			t.Fatalf("assembler le script %s : %v", kind, err)
		}
		assembled := string(script)
		for _, expected := range []string{
			`FRAGMENTS_DIR=` + proxyFragmentsDir,
			`$OC_DOMAIN.yml`,
		} {
			if !strings.Contains(assembled, expected) {
				t.Errorf("le script %s ne dit pas %q", kind, expected)
			}
		}
	}

	// Le conteneur, le réseau partagé et le dossier du proxy : le script de
	// création les construit comme Go.
	script, err := scripts.Script(string(KindVhost))
	if err != nil {
		t.Fatalf("assembler le script : %v", err)
	}
	for _, expected := range []string{
		`CONTAINER_NAME="$OC_ENVIRONMENT-$OC_SERVICE"`,
		`SHARED_NETWORK=` + SharedNetwork,
		`PROXY_SERVICE_DIR=` + proxyServiceDir,
	} {
		if !strings.Contains(string(script), expected) {
			t.Errorf("le script ne dit pas %q", expected)
		}
	}
}

// Un nom trop long ferait un nom de fichier que le système refuse : le refus
// se dit avant tout effet, et il compte les octets (15-catalogue-actions.md §5).
func TestPrepareVhost_RefusesANameTooLongForItsFragment(t *testing.T) {
	// 253 octets, la borne d'un nom de domaine : son fragment en demanderait
	// 257, plus que les 255 d'un nom de fichier.
	long := strings.Repeat("a", validate.MaxDomainLength-len(".fr")) + ".fr"
	if err := validate.Domain(long); err != nil {
		t.Fatalf("le nom de test n'est pas un domaine valide : %v", err)
	}

	params := vhostParams()
	params[paramDomain] = long
	_, err := Prepare(KindVhost, params)

	var refused refusal.Refusal
	if !errors.As(err, &refused) {
		t.Fatalf("erreur = %v, attendu un refus", err)
	}
	if !strings.Contains(refused.Cause, "255") {
		t.Errorf("le refus ne dit pas la borne : %q", refused.Cause)
	}
}

// Supprimer un hôte virtuel ne dépose aucun fichier : elle en retire un.
func TestPrepareVhostRemove_DepositsNothing(t *testing.T) {
	prepared, err := Prepare(KindVhostRemove, map[string]string{paramDomain: "temoin.exemple.fr"})
	if err != nil {
		t.Fatalf("préparer Supprimer un hôte virtuel : %v", err)
	}
	if len(prepared.Files) != 0 {
		t.Errorf("l'action dépose %v, attendu aucun fichier", prepared.Files)
	}
}

// Le proxy crée le réseau que les services publiés rejoignent : sans lui, le
// fragment routerait vers un nom que Docker ne résout pas.
func TestProxy_CreatesTheSharedNetworkTheFragmentRoutesOn(t *testing.T) {
	compose := filesOfProxy(t)[proxyComposeName]
	if !strings.Contains(compose, "  "+SharedNetwork+":\n    external: true") {
		t.Errorf("le compose du proxy ne déclare pas le réseau partagé en externe :\n%s", compose)
	}

	script, err := scripts.Script(string(KindProxy))
	if err != nil {
		t.Fatalf("assembler le script : %v", err)
	}
	if !strings.Contains(string(script), `network create -- "$SHARED_NETWORK"`) {
		t.Error("le script du proxy ne crée pas le réseau partagé")
	}
}
