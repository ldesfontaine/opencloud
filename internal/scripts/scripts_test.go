package scripts

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestKinds_ListsEveryScriptDirectory(t *testing.T) {
	kinds := Kinds()

	if len(kinds) == 0 {
		t.Fatal("aucun script embarqué")
	}
	if !slices.Contains(kinds, "diagnostiquer") {
		t.Fatalf("Kinds = %v, sans diagnostiquer", kinds)
	}
	for _, kind := range kinds {
		if _, err := Script(kind); err != nil {
			t.Errorf("%s : %v", kind, err)
		}
	}
}

func TestScript_PutsTheCommonHeaderBeforeTheBody(t *testing.T) {
	script, err := Script("diagnostiquer")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	if !bytes.HasPrefix(script, []byte("#!/bin/bash\n")) {
		t.Fatalf("le fichier déposé doit commencer par le shebang : %.20q", script)
	}
	header := bytes.Index(script, []byte("set -euo pipefail"))
	body := bytes.Index(script, []byte("LAUNCHER=/usr/local/sbin/oc-launch"))
	if header < 0 || body < 0 || header > body {
		t.Fatalf("en-tête à %d, corps à %d : l'en-tête doit venir en premier", header, body)
	}
}

// Ajouter une ligne à packages.txt doit suffire : le script assemblé porte la
// liste, et rien de ce qui n'est pas un paquet.
func TestScript_CarriesEveryPackageOfTheListAndNoComment(t *testing.T) {
	script, err := Script("socle")
	if err != nil {
		t.Fatalf("assembler le socle : %v", err)
	}
	assembled := string(script)

	packages, err := Packages("socle")
	if err != nil {
		t.Fatalf("lire la liste de paquets : %v", err)
	}
	if len(packages) == 0 {
		t.Fatal("la liste de paquets du socle est vide")
	}
	// La variable porte la liste entière, une ligne par paquet.
	block := "PACKAGES='" + strings.Join(packages, "\n") + "'\n"
	if !strings.Contains(assembled, block) {
		t.Errorf("le script assemblé ne porte pas la liste :\n%s", block)
	}
	for _, name := range packages {
		if !strings.Contains(assembled, name) {
			t.Errorf("le script assemblé ne nomme pas le paquet %q", name)
		}
	}

	// La variable arrive avant le corps qui la lit, et les commentaires du
	// fichier restent dans le dépôt.
	variable := strings.Index(assembled, "PACKAGES='")
	body := strings.Index(assembled, "GUARD_FILE=")
	if variable < 0 || body < 0 || variable > body {
		t.Fatalf("PACKAGES à %d, corps à %d : la liste doit venir avant le corps", variable, body)
	}
	if strings.Contains(assembled, "Ajouter une ligne ici suffit") {
		t.Error("un commentaire de packages.txt s'est retrouvé dans le script assemblé")
	}
}

func TestPackages_AnActionWithoutAList_HasNone(t *testing.T) {
	packages, err := Packages("diagnostiquer")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if len(packages) != 0 {
		t.Errorf("Packages = %v, attendu aucune liste", packages)
	}
}

func TestParsePackages_RefusesWhatIsNotAPackageList(t *testing.T) {
	for name, content := range map[string]string{
		"un doublon":         "curl\njq\ncurl\n",
		"une espace":         "curl jq\n",
		"un guillemet":       "curl'\n",
		"une majuscule":      "Curl\n",
		"un chemin absolu":   "/usr/bin/curl\n",
		"une substitution":   "$(id)\n",
		"un nom trop court":  "c\n",
		"un point d'entrée ": "../lib\n",
	} {
		if _, err := parsePackages("socle", []byte(content)); err == nil {
			t.Errorf("%s devrait être refusé : %q", name, content)
		}
	}
}

func TestScript_UnknownKind_Fails(t *testing.T) {
	for _, kind := range []string{"inconnue", "", "../lib"} {
		if _, err := Script(kind); err == nil {
			t.Errorf("%q devrait être refusé", kind)
		}
	}
}

func TestScripts_ParseUnderBash(t *testing.T) {
	bash := lookPath(t, "bash")

	for _, kind := range Kinds() {
		t.Run(kind, func(t *testing.T) {
			path := writeAssembledScript(t, kind)
			// bash -n analyse sans exécuter : une faute de syntaxe se voit ici,
			// pas sur une machine à mi-parcours.
			output, err := exec.Command(bash, "-n", "--", path).CombinedOutput()
			if err != nil {
				t.Fatalf("bash -n : %v\n%s", err, output)
			}
		})
	}
}

func TestScripts_PassShellcheck(t *testing.T) {
	shellcheck, err := exec.LookPath("shellcheck")
	if err != nil {
		t.Skip("shellcheck absent de ce poste ; la CI le joue")
	}

	for _, kind := range Kinds() {
		t.Run(kind, func(t *testing.T) {
			path := writeAssembledScript(t, kind)
			output, err := exec.Command(shellcheck, "--", path).CombinedOutput()
			if err != nil {
				t.Fatalf("shellcheck : %v\n%s", err, output)
			}
		})
	}
}

func TestDiagnostiquer_PlayedTwice_SaysUnchangedBothTimes(t *testing.T) {
	bash := lookPath(t, "bash")
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		t.Skip("pas de systemd ici : le préflight du script refuserait, à raison")
	}
	path := writeAssembledScript(t, "diagnostiquer")

	for pass := 1; pass <= 2; pass++ {
		output, err := exec.Command(bash, "--", path).CombinedOutput()
		if err != nil {
			t.Fatalf("passage %d : %v\n%s", pass, err, output)
		}
		if !strings.Contains(string(output), "résultat: inchangé") {
			t.Fatalf("passage %d ne conclut pas « inchangé » :\n%s", pass, output)
		}
		if !strings.Contains(string(output), "étape: le lanceur") {
			t.Fatalf("passage %d n'écrit pas ses étapes :\n%s", pass, output)
		}
	}
}

func writeAssembledScript(t *testing.T, kind string) string {
	t.Helper()
	script, err := Script(kind)
	if err != nil {
		t.Fatalf("assembler %s : %v", kind, err)
	}
	path := filepath.Join(t.TempDir(), kind+".sh")
	if err := os.WriteFile(path, script, 0o700); err != nil {
		t.Fatalf("écrire %s : %v", path, err)
	}
	return path
}

func lookPath(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s absent de ce poste", name)
	}
	return path
}
