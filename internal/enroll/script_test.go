package enroll

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// playTestScript joue un petit script par la vraie mise en œuvre : c'est elle
// qui pose le fichier, remplace l'environnement et lit la sortie.
func playTestScript(t *testing.T, script LocalScript) ([]string, int, error) {
	t.Helper()
	if _, err := os.Stat(bashPath); err != nil {
		t.Skipf("%s absent de ce poste", bashPath)
	}
	if script.Timeout == 0 {
		script.Timeout = 5 * time.Second
	}

	var written []string
	exitCode, err := SystemScripts{}.Run(context.Background(), script, func(line string) {
		written = append(written, line)
	})
	return written, exitCode, err
}

func TestSystemScripts_RelaieChaqueLigneEtRendLeCodeDeRetour(t *testing.T) {
	written, exitCode, err := playTestScript(t, LocalScript{
		Content: []byte("#!/bin/bash\nprintf 'étape: une\\n'\nprintf 'résultat: refusé\\n' >&2\nexit 2\n"),
	})
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if exitCode != 2 {
		t.Errorf("code %d, attendu 2", exitCode)
	}
	// La sortie d'erreur compte autant que l'autre : l'opérateur lit les deux.
	if expected := []string{"étape: une", "résultat: refusé"}; !slices.Equal(written, expected) {
		t.Errorf("lignes %q, attendues %q", written, expected)
	}
}

// Aucune valeur saisie ne passe par une ligne de commande : elles entrent par
// l'environnement, comme params.env pour une action lancée par le lanceur.
func TestSystemScripts_LesValeursEntrentParLEnvironnementEtNonParLaLigneDeCommande(t *testing.T) {
	written, exitCode, err := playTestScript(t, LocalScript{
		Content:     []byte("#!/bin/bash\nprintf 'clé=%s\\n' \"$OC_PUBLIC_KEY\"\nprintf 'arguments=%s\\n' \"$*\"\n"),
		Environment: []string{"OC_PUBLIC_KEY=ssh-ed25519 AAAA exemple"},
	})
	if err != nil || exitCode != 0 {
		t.Fatalf("code %d, erreur %v", exitCode, err)
	}
	if expected := []string{"clé=ssh-ed25519 AAAA exemple", "arguments="}; !slices.Equal(written, expected) {
		t.Errorf("lignes %q, attendues %q", written, expected)
	}
}

func TestSystemScripts_UnScriptQuiSEmballeEstArrete(t *testing.T) {
	_, _, err := playTestScript(t, LocalScript{
		Content: []byte("#!/bin/bash\nwhile :; do :; done\n"),
		Timeout: 50 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("erreur : %v", err)
	}
}
