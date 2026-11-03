package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// fakeResticBinary écrit un faux restic qui note ce qu'il a reçu dans dir :
// ses arguments (un par ligne), son environnement, le contenu, le mode et le
// chemin du fichier de mot de passe. leak le fait recopier le mot de passe
// sur sa sortie d'erreur, comme une erreur bavarde ; exitCode est son code.
func fakeResticBinary(t *testing.T, leak bool, exitCode int) (string, string) {
	t.Helper()
	dir := t.TempDir()
	lines := []string{
		"#!/bin/sh",
		`printf '%s\n' "$@" > ` + dir + `/args`,
		`env > ` + dir + `/env`,
		`cp "$RESTIC_PASSWORD_FILE" ` + dir + `/password`,
		`stat -c %a "$RESTIC_PASSWORD_FILE" > ` + dir + `/mode`,
		`printf '%s' "$RESTIC_PASSWORD_FILE" > ` + dir + `/path`,
		`echo "removed snapshot 0123abcd"`,
	}
	if leak {
		lines = append(lines, `echo "Fatal: wrong password $(cat "$RESTIC_PASSWORD_FILE")" >&2`)
	}
	lines = append(lines, "exit "+strconv.Itoa(exitCode))
	binary := filepath.Join(dir, "restic")
	if err := writeProgram(binary, []byte(strings.Join(lines, "\n")+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary, dir
}

func readNoted(t *testing.T, dir, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("the fake restic did not note %s: %v", name, err)
	}
	return string(content)
}

// retainBench est une cible préparée, avec un faux restic lancé pour de vrai.
func retainBench(t *testing.T, leak bool, exitCode int) (*targetBench, string) {
	t.Helper()
	b := newTargetBench(t)
	expectResult(t, b.run(t, agentwire.KindResticTargetSetup, setupParams()), agentwire.ActionSucceeded, "")
	binary, noted := fakeResticBinary(t, leak, exitCode)
	b.runner.checks.findRestic = func() (string, error) { return binary, nil }
	return b, noted
}

func (b *targetBench) retain(t *testing.T, params agentwire.ResticRetainParams) []agentwire.ActionResult {
	t.Helper()
	return b.runWithSecret(t, agentwire.KindResticRetain, params, agentwire.ResticRetainSecret{RepoPassword: seededRepoPassword})
}

// restic.retain lance restic forget --prune sur le dossier local du dépôt,
// regroupé par hôte et chemins, avec les seuls comptes demandés ; le mot de
// passe du dépôt est un fichier 0600, jamais un argument ni une variable,
// effacé une fois la commande finie.
func TestResticRetain_RunsForgetPruneLocallyWithAPasswordFileThenRemovesIt(t *testing.T) {
	b, noted := retainBench(t, false, 0)
	params := agentwire.ResticRetainParams{User: resticUser, KeepLast: 2, KeepDaily: 7, KeepMonthly: 12}

	result := expectResult(t, b.retain(t, params), agentwire.ActionSucceeded, "")

	want := []string{
		"-r", filepath.Join(b.dir, resticUser), "--no-cache", "forget", "--prune", "--group-by", "host,paths",
		"--keep-last", "2", "--keep-daily", "7", "--keep-monthly", "12",
	}
	if args := strings.Split(strings.TrimSpace(readNoted(t, noted, "args")), "\n"); strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("args %q", args)
	}
	if readNoted(t, noted, "password") != seededRepoPassword || strings.TrimSpace(readNoted(t, noted, "mode")) != "600" {
		t.Fatal("restic did not read the repository password from a private file")
	}
	passwordFile := readNoted(t, noted, "path")
	if _, err := os.Stat(passwordFile); !os.IsNotExist(err) {
		t.Fatalf("the password file %s outlived the command: %v", passwordFile, err)
	}
	if env := readNoted(t, noted, "env"); strings.Contains(env, seededRepoPassword) || strings.Contains(env, "RESTIC_PASSWORD=") {
		t.Fatalf("the password reached the environment: %s", env)
	}
	if !hasLine(result.Output, "removed snapshot 0123abcd") || !hasLine(result.Output, "forget --prune "+resticUser) {
		t.Fatalf("output %+v", result.Output)
	}
}

// Un restic qui échoue en recopiant le mot de passe : l'ordre échoue, la
// sortie le masque, le fichier est effacé quand même.
func TestResticRetain_FailureMasksThePasswordAndStillRemovesTheFile(t *testing.T) {
	b, noted := retainBench(t, true, 1)
	result := expectResult(t, b.retain(t, agentwire.ResticRetainParams{User: resticUser, KeepDaily: 7}), agentwire.ActionFailed, agentwire.ReasonResticFailed)
	for _, line := range result.Output {
		if strings.Contains(line.Line, seededRepoPassword) {
			t.Fatalf("the password reached the output: %q", line.Line)
		}
	}
	if !hasLine(result.Output, redactedSecret) {
		t.Fatalf("output %+v", result.Output)
	}
	if _, err := os.Stat(readNoted(t, noted, "path")); !os.IsNotExist(err) {
		t.Fatal("the password file outlived a failed command")
	}
	if strings.Contains(b.logs.String(), seededRepoPassword) {
		t.Fatal("the password reached the journal")
	}
}

// Le plancher de la cible : par défaut sept quotidiens. Une rétention qui
// garderait moins, sur un seul compte, est refusée sans rien lancer, quoi que
// le serveur demande ; une rétention hors bornes ou vide aussi.
func TestResticRetain_PolicyUnderTheFloorIsRefusedByTheTarget(t *testing.T) {
	b, noted := retainBench(t, false, 0)
	below := []agentwire.ResticRetainParams{
		{User: resticUser, KeepDaily: 6, KeepWeekly: 52},
		{User: resticUser, KeepLast: 1000},
	}
	for _, params := range below {
		expectResult(t, b.retain(t, params), agentwire.ActionRefused, agentwire.ReasonRetentionBelowFloor)
	}
	invalid := []agentwire.ResticRetainParams{
		{User: resticUser},
		{User: resticUser, KeepDaily: 7, KeepYearly: 1001},
		{User: resticUser, KeepDaily: 7, KeepWeekly: -1},
	}
	for _, params := range invalid {
		expectResult(t, b.retain(t, params), agentwire.ActionRefused, agentwire.ReasonRetentionInvalid)
	}
	withoutSecret := b.run(t, agentwire.KindResticRetain, agentwire.ResticRetainParams{User: resticUser, KeepDaily: 7})
	expectResult(t, withoutSecret, agentwire.ActionFailed, agentwire.ReasonBadOrder)
	if _, err := os.Stat(filepath.Join(noted, "args")); !os.IsNotExist(err) {
		t.Fatal("restic ran for a refused retention")
	}
}

// Le plancher se pose par restic.target.setup, se relève, se garde quand
// l'ordre n'en dit rien, et ne s'abaisse jamais par un ordre : il faut
// effacer son fichier sur la machine.
func TestResticTargetSetup_RaisesTheFloorButRefusesToLowerIt(t *testing.T) {
	b, _ := retainBench(t, false, 0)
	floorPath := filepath.Join(b.stateDir.Name(), resticDir, retentionFloorFile)
	withFloor := func(floor agentwire.ResticRetentionFloor) agentwire.ResticTargetSetupParams {
		params := setupParams()
		params.RetentionFloor = &floor
		return params
	}

	expectResult(t, b.run(t, agentwire.KindResticTargetSetup, withFloor(agentwire.ResticRetentionFloor{KeepDaily: 14, KeepWeekly: 4})), agentwire.ActionSucceeded, "")
	raised, _ := os.ReadFile(floorPath)
	for _, lower := range []agentwire.ResticRetentionFloor{{KeepDaily: 7, KeepWeekly: 4}, {KeepDaily: 30}} {
		expectResult(t, b.run(t, agentwire.KindResticTargetSetup, withFloor(lower)), agentwire.ActionRefused, agentwire.ReasonRetentionBelowFloor)
	}
	if kept, _ := os.ReadFile(floorPath); string(kept) != string(raised) {
		t.Fatalf("a refused setup rewrote the floor: %s", kept)
	}
	expectResult(t, b.run(t, agentwire.KindResticTargetSetup, setupParams()), agentwire.ActionSucceeded, "")
	if kept, _ := os.ReadFile(floorPath); string(kept) != string(raised) {
		t.Fatalf("a setup without floor changed it: %s", kept)
	}
	expectResult(t, b.retain(t, agentwire.ResticRetainParams{User: resticUser, KeepDaily: 30, KeepWeekly: 3}), agentwire.ActionRefused, agentwire.ReasonRetentionBelowFloor)
	expectResult(t, b.retain(t, agentwire.ResticRetainParams{User: resticUser, KeepDaily: 14, KeepWeekly: 4}), agentwire.ActionSucceeded, "")
	expectResult(t, b.run(t, agentwire.KindResticTargetSetup, withFloor(agentwire.ResticRetentionFloor{KeepDaily: 14, KeepWeekly: 4, KeepMonthly: 6})), agentwire.ActionSucceeded, "")
	expectResult(t, b.run(t, agentwire.KindResticTargetSetup, withFloor(agentwire.ResticRetentionFloor{KeepDaily: -1})), agentwire.ActionRefused, agentwire.ReasonRetentionInvalid)

	// L'opérateur efface le fichier sur la machine : le plancher redevient
	// libre.
	if err := os.Remove(floorPath); err != nil {
		t.Fatal(err)
	}
	expectResult(t, b.run(t, agentwire.KindResticTargetSetup, withFloor(agentwire.ResticRetentionFloor{KeepDaily: 3})), agentwire.ActionSucceeded, "")
}

// Une sauvegarde relayée tient un verrou sur le dépôt : la rétention est
// refusée (busy) tant qu'il est posé, et passe une fois qu'il est retiré.
func TestResticRetain_IsRefusedWhileARelayedBackupHoldsALock(t *testing.T) {
	b, noted := retainBench(t, false, 0)
	lock := agentwire.ResticRelayRequest{ID: strings.Repeat("4", 32), Repo: resticUser, Method: "POST", Path: "/locks/" + strings.Repeat("b", 64), ContentLength: 4}
	b.runner.restic.target.noteRelayed(lock)

	expectResult(t, b.retain(t, agentwire.ResticRetainParams{User: resticUser, KeepDaily: 7}), agentwire.ActionRefused, agentwire.ReasonBusy)
	if _, err := os.Stat(filepath.Join(noted, "args")); !os.IsNotExist(err) {
		t.Fatal("restic ran on a busy repository")
	}

	lock.Method = "DELETE"
	b.runner.restic.target.noteRelayed(lock)
	expectResult(t, b.retain(t, agentwire.ResticRetainParams{User: resticUser, KeepDaily: 7}), agentwire.ActionSucceeded, "")
}

// Une source ne lance jamais forget : restic.retain demande restic.target,
// et aucune commande de la source n'est autre que backup ou init.
func TestResticRetain_ASourceCannotForget(t *testing.T) {
	b := newResticBench(t, "restic.produce")
	retain := agentwire.ResticRetainParams{User: resticUser, KeepDaily: 7}
	secret := agentwire.ResticRetainSecret{RepoPassword: seededRepoPassword}
	expectResult(t, b.runWithSecret(t, agentwire.KindResticRetain, retain, secret), agentwire.ActionRefused, agentwire.RightMissing(machine.CapabilityResticTarget))

	b.runner.restic.source.dialer = &refusingDialer{}
	b.provision(t, "nas", 8000, mustCertificate(t))
	b.service(t, "forgejo")
	expectResult(t, b.run(t, agentwire.KindResticBackup, serviceBackup("forgejo")), agentwire.ActionSucceeded, "")
	for _, call := range b.restic.callsMade() {
		if call.args[0] != "backup" && call.args[0] != "init" {
			t.Errorf("the source ran restic %v", call.args)
		}
		for _, arg := range call.args {
			if arg == "forget" || arg == "prune" || arg == "--prune" {
				t.Errorf("the source ran restic %v", call.args)
			}
		}
	}
}

// restic absent de la cible : la rétention est refusée sans rien lancer.
func TestResticRetain_IsRefusedWhenResticIsMissing(t *testing.T) {
	b, _ := retainBench(t, false, 0)
	b.runner.checks.findRestic = func() (string, error) { return "", os.ErrNotExist }
	expectResult(t, b.retain(t, agentwire.ResticRetainParams{User: resticUser, KeepDaily: 7}), agentwire.ActionRefused, agentwire.ReasonResticMissing)
}
