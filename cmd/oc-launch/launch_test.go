package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/actiondir"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

const testActionID = "diagnostiquer-1"

// Une action complète, telle qu'openCloud la dépose, dans un dossier temporaire
// appartenant à l'utilisateur qui joue les tests.
func depositAction(t *testing.T) launcher {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, testActionID)
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeActionFile(t, directory, actiondir.ScriptName, "#!/bin/bash\ntrue\n", 0o700)
	writeActionFile(t, directory, actiondir.ParamsName, "OC_EXEMPLE=\"1\"\n", 0o600)
	writeActionFile(t, directory, actiondir.TimeoutName, "300\n", 0o600)
	return launcher{actionsRoot: root, ownerUID: uint32(os.Getuid())}
}

func writeActionFile(t *testing.T, directory, name, content string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile passe par l'umask : le mode se pose explicitement.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func expectRefusal(t *testing.T, err error) refusal.Refusal {
	t.Helper()
	var refused refusal.Refusal
	if !errors.As(err, &refused) {
		t.Fatalf("attendu un refus nommé, reçu %v", err)
	}
	if refused.Cause == "" || refused.Remedy == "" {
		t.Fatalf("un refus dit la cause et le geste qui la lève : %+v", refused)
	}
	return refused
}

func TestPrepare_CompleteAction_BuildsTheVector(t *testing.T) {
	launch := depositAction(t)

	toRun, err := launch.prepare([]string{testActionID})
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	if toRun.Path != actiondir.SystemdRunPath {
		t.Errorf("Path = %q, attendu %q", toRun.Path, actiondir.SystemdRunPath)
	}
	if !slices.Contains(toRun.Args, "--property=RuntimeMaxSec=300") {
		t.Errorf("le délai lu dans le dossier doit devenir RuntimeMaxSec : %v", toRun.Args)
	}
	if !slices.Equal(toRun.Env, []string{fixedPath, fixedLocale}) {
		t.Errorf("Env = %v : l'environnement est remplacé, jamais hérité", toRun.Env)
	}
}

// La ligne de commande est figée : seuls l'identifiant et le délai y varient.
func TestLaunchVector_IsTheFrozenCommandLine(t *testing.T) {
	toRun := launchVector(actiondir.Dir("abc-1"), "abc-1", 1800)

	expected := []string{
		"systemd-run",
		"--unit=oc-action-abc-1",
		"--description=openCloud action abc-1",
		"--property=EnvironmentFile=/var/lib/opencloud/actions/abc-1/params.env",
		"--property=RuntimeMaxSec=1800",
		"--property=WorkingDirectory=/var/lib/opencloud/actions/abc-1",
		"--collect",
		"--quiet",
		"/var/lib/opencloud/actions/abc-1/run.sh",
	}
	if !slices.Equal(toRun.Args, expected) {
		t.Fatalf("Args =\n%v\nattendu\n%v", toRun.Args, expected)
	}
	if toRun.Path != "/usr/bin/systemd-run" {
		t.Fatalf("Path = %q", toRun.Path)
	}
}

func TestPrepare_ArgumentsOtherThanOneID_AreRefused(t *testing.T) {
	launch := depositAction(t)
	cases := map[string][]string{
		"aucun argument":      {},
		"deux arguments":      {testActionID, "--property=User=root"},
		"une option":          {"-t"},
		"un chemin":           {"/etc/shadow"},
		"une remontée":        {"../../etc"},
		"une majuscule":       {"Diagnostiquer"},
		"un identifiant vide": {""},
	}
	for name, arguments := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := launch.prepare(arguments)
			expectRefusal(t, err)
		})
	}
}

// Le critère du ticket : un identifiant valide dont le dossier n'existe pas.
func TestPrepare_MissingDirectory_IsRefusedByName(t *testing.T) {
	launch := depositAction(t)

	_, err := launch.prepare([]string{"explode"})

	refused := expectRefusal(t, err)
	if !strings.Contains(refused.Cause, "dossier") {
		t.Fatalf("le refus doit nommer le dossier absent : %q", refused.Cause)
	}
}

func TestPrepare_SymlinkedDirectory_IsRefused(t *testing.T) {
	launch := depositAction(t)
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(launch.actionsRoot, "lien")); err != nil {
		t.Fatal(err)
	}

	_, err := launch.prepare([]string{"lien"})

	expectRefusal(t, err)
}

func TestPrepare_SymlinkedScript_IsRefused(t *testing.T) {
	launch := depositAction(t)
	directory := filepath.Join(launch.actionsRoot, testActionID)
	if err := os.Remove(filepath.Join(directory, actiondir.ScriptName)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/bin/sh", filepath.Join(directory, actiondir.ScriptName)); err != nil {
		t.Fatal(err)
	}

	_, err := launch.prepare([]string{testActionID})

	expectRefusal(t, err)
}

func TestPrepare_AnotherOwner_IsRefused(t *testing.T) {
	launch := depositAction(t)
	// L'uid attendu est injecté : le test n'a pas besoin d'être root pour
	// prouver que le lanceur n'exécute que ce qui appartient à opencloud.
	launch.ownerUID++

	_, err := launch.prepare([]string{testActionID})

	refused := expectRefusal(t, err)
	if !strings.Contains(refused.Cause, ownerAccount) {
		t.Fatalf("le refus doit nommer le compte attendu : %q", refused.Cause)
	}
}

func TestPrepare_WritableByGroupOrOthers_IsRefused(t *testing.T) {
	cases := map[string]struct {
		name string
		mode os.FileMode
	}{
		"dossier ouvert au groupe": {"", 0o770},
		"script ouvert aux autres": {actiondir.ScriptName, 0o707},
		"params ouvert au groupe":  {actiondir.ParamsName, 0o660},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			launch := depositAction(t)
			target := filepath.Join(launch.actionsRoot, testActionID, tc.name)
			if err := os.Chmod(target, tc.mode); err != nil {
				t.Fatal(err)
			}

			_, err := launch.prepare([]string{testActionID})

			refused := expectRefusal(t, err)
			if !strings.Contains(refused.Cause, "modifiable") {
				t.Fatalf("le refus doit dire ce qui est modifiable : %q", refused.Cause)
			}
		})
	}
}

func TestPrepare_ScriptNotExecutable_IsRefused(t *testing.T) {
	launch := depositAction(t)
	if err := os.Chmod(filepath.Join(launch.actionsRoot, testActionID, actiondir.ScriptName), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := launch.prepare([]string{testActionID})

	refused := expectRefusal(t, err)
	if !strings.Contains(refused.Cause, "exécutable") {
		t.Fatalf("le refus doit dire que le script n'est pas exécutable : %q", refused.Cause)
	}
}

func TestPrepare_MissingOrOddFiles_AreRefused(t *testing.T) {
	cases := map[string]func(t *testing.T, directory string){
		"script absent": func(t *testing.T, directory string) {
			removeActionFile(t, directory, actiondir.ScriptName)
		},
		"params absent": func(t *testing.T, directory string) {
			removeActionFile(t, directory, actiondir.ParamsName)
		},
		"délai absent": func(t *testing.T, directory string) {
			removeActionFile(t, directory, actiondir.TimeoutName)
		},
		"script en dossier": func(t *testing.T, directory string) {
			removeActionFile(t, directory, actiondir.ScriptName)
			if err := os.Mkdir(filepath.Join(directory, actiondir.ScriptName), 0o700); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			launch := depositAction(t)
			breakIt(t, filepath.Join(launch.actionsRoot, testActionID))

			_, err := launch.prepare([]string{testActionID})

			expectRefusal(t, err)
		})
	}
}

func TestPrepare_TimeoutOutOfBounds_IsRefused(t *testing.T) {
	for _, content := range []string{"", "0", "86401", "-30", "300 ; rm -rf /", "1e3"} {
		t.Run(content, func(t *testing.T) {
			launch := depositAction(t)
			directory := filepath.Join(launch.actionsRoot, testActionID)
			writeActionFile(t, directory, actiondir.TimeoutName, content, 0o600)

			_, err := launch.prepare([]string{testActionID})

			refused := expectRefusal(t, err)
			if !strings.Contains(refused.Cause, "délai") {
				t.Fatalf("le refus doit nommer le délai : %q", refused.Cause)
			}
		})
	}
}

func removeActionFile(t *testing.T, directory, name string) {
	t.Helper()
	if err := os.Remove(filepath.Join(directory, name)); err != nil {
		t.Fatal(err)
	}
}
