package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/actiondir"
)

// Un purgeur qui ne demande rien à systemd : les unités nommées tournent
// encore, les autres non.
func testPurger(t *testing.T, actionsRoot string, runningIDs ...string) purger {
	t.Helper()
	return purger{
		actionsRoot: actionsRoot,
		isUnitRunning: func(unitName string) bool {
			return slices.Contains(runningIDs, strings.TrimPrefix(unitName, actiondir.UnitPrefix))
		},
		out:    io.Discard,
		errOut: io.Discard,
	}
}

// depositDirectories pose des dossiers d'action datés d'une minute d'écart,
// du plus ancien au plus récent, et rend leurs identifiants dans cet ordre.
func depositDirectories(t *testing.T, actionsRoot string, count int) []string {
	t.Helper()
	base := time.Now().Add(-time.Duration(count) * time.Minute)

	var ids []string
	for index := range count {
		id := fmt.Sprintf("action-%02d", index)
		directory := filepath.Join(actionsRoot, id)
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		writeActionFile(t, directory, actiondir.ScriptName, "#!/bin/bash\ntrue\n", 0o700)
		depositedAt := base.Add(time.Duration(index) * time.Minute)
		if err := os.Chtimes(directory, depositedAt, depositedAt); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func remainingNames(t *testing.T, actionsRoot string) []string {
	t.Helper()
	entries, err := os.ReadDir(actionsRoot)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	return names
}

func TestRemoveOldDirectories_KeepsTheThirtyMostRecentlyDeposited(t *testing.T) {
	actionsRoot := t.TempDir()
	ids := depositDirectories(t, actionsRoot, 35)
	// L'action qu'on lance est la plus récente, comme sur une machine.
	launchedID := ids[len(ids)-1]

	purged, err := testPurger(t, actionsRoot).removeOldDirectories(launchedID)
	if err != nil {
		t.Fatalf("purge : %v", err)
	}

	if purged != 5 {
		t.Errorf("dossiers purgés = %d, attendu 5", purged)
	}
	expected := ids[5:]
	slices.Sort(expected)
	if got := remainingNames(t, actionsRoot); !slices.Equal(got, expected) {
		t.Errorf("restent %v\nattendu %v", got, expected)
	}
}

// Le tri se fait sur la date de dépôt, pas sur le nom : un identifiant ne dit
// pas son âge.
func TestRemoveOldDirectories_SortsByDepositDateNotByName(t *testing.T) {
	actionsRoot := t.TempDir()
	ids := depositDirectories(t, actionsRoot, 31)
	// Le plus ancien par son nom devient le plus récent par sa date.
	oldestByName := filepath.Join(actionsRoot, ids[0])
	rejuvenated := time.Now().Add(time.Hour)
	if err := os.Chtimes(oldestByName, rejuvenated, rejuvenated); err != nil {
		t.Fatal(err)
	}

	purged, err := testPurger(t, actionsRoot).removeOldDirectories(ids[len(ids)-1])
	if err != nil {
		t.Fatalf("purge : %v", err)
	}

	if purged != 1 {
		t.Fatalf("dossiers purgés = %d, attendu 1", purged)
	}
	if _, err := os.Stat(oldestByName); err != nil {
		t.Errorf("le dossier redaté est le plus récent : il devait rester (%v)", err)
	}
	if _, err := os.Stat(filepath.Join(actionsRoot, ids[1])); !os.IsNotExist(err) {
		t.Errorf("le plus ancien par sa date devait partir (%v)", err)
	}
}

func TestRemoveOldDirectories_KeepsARunningUnitEvenWhenItIsOld(t *testing.T) {
	actionsRoot := t.TempDir()
	ids := depositDirectories(t, actionsRoot, 35)
	stillRunning := ids[0] // le plus ancien de tous

	purged, err := testPurger(t, actionsRoot, stillRunning).removeOldDirectories(ids[len(ids)-1])
	if err != nil {
		t.Fatalf("purge : %v", err)
	}

	if purged != 4 {
		t.Errorf("dossiers purgés = %d, attendu 4", purged)
	}
	if _, err := os.Stat(filepath.Join(actionsRoot, stillRunning)); err != nil {
		t.Errorf("un dossier dont l'unité tourne encore ne se purge jamais (%v)", err)
	}
}

// Un lien symbolique n'est ni suivi ni retiré : la purge ne touche qu'à des
// sous-dossiers directs de /var/lib/opencloud/actions.
func TestRemoveOldDirectories_NeverFollowsNorRemovesASymlink(t *testing.T) {
	actionsRoot := t.TempDir()
	elsewhere := t.TempDir()
	witness := filepath.Join(elsewhere, "temoin")
	if err := os.WriteFile(witness, []byte("à ne pas toucher\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(actionsRoot, "ailleurs")
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Fatal(err)
	}
	ids := depositDirectories(t, actionsRoot, 35)

	purged, err := testPurger(t, actionsRoot).removeOldDirectories(ids[len(ids)-1])
	if err != nil {
		t.Fatalf("purge : %v", err)
	}

	// Le lien ne compte pas parmi les dossiers gardés : les 35 vrais dossiers
	// se purgent comme s'il n'était pas là.
	if purged != 5 {
		t.Errorf("dossiers purgés = %d, attendu 5", purged)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Errorf("le lien symbolique devait rester (%v)", err)
	}
	if _, err := os.Stat(witness); err != nil {
		t.Errorf("la purge a suivi le lien hors du dossier des actions (%v)", err)
	}
}

func TestRemoveOldDirectories_LeavesWhatIsNotAnActionDirectory(t *testing.T) {
	actionsRoot := t.TempDir()
	ids := depositDirectories(t, actionsRoot, 35)
	intruder := filepath.Join(actionsRoot, "action-99")
	if err := os.WriteFile(intruder, []byte("ni dossier ni action\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	shouted := filepath.Join(actionsRoot, "PasUnIdentifiant")
	if err := os.Mkdir(shouted, 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := testPurger(t, actionsRoot).removeOldDirectories(ids[len(ids)-1]); err != nil {
		t.Fatalf("purge : %v", err)
	}

	for _, kept := range []string{intruder, shouted} {
		if _, err := os.Lstat(kept); err != nil {
			t.Errorf("%s devait rester (%v)", kept, err)
		}
	}
}

func TestRun_SaysOnItsOutputHowManyDirectoriesWerePurged(t *testing.T) {
	actionsRoot := t.TempDir()
	ids := depositDirectories(t, actionsRoot, 32)

	said := &strings.Builder{}
	purge := testPurger(t, actionsRoot)
	purge.out = said
	purge.run(ids[len(ids)-1])

	if got := said.String(); got != actiondir.FormatPurged(2) {
		t.Errorf("sortie = %q, attendue %q", got, actiondir.FormatPurged(2))
	}
}

func TestRun_SaysNothingWhenNothingWasPurged(t *testing.T) {
	actionsRoot := t.TempDir()
	ids := depositDirectories(t, actionsRoot, actiondir.KeptDirectories)

	said := &strings.Builder{}
	purge := testPurger(t, actionsRoot)
	purge.out = said
	purge.run(ids[len(ids)-1])

	if said.String() != "" {
		t.Errorf("sortie = %q, attendue vide", said.String())
	}
}
