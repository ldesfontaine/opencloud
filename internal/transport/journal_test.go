package transport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// Les fixtures de testdata/journal viennent de systemd 257 sur ce poste :
// systemd-run --user a produit les trois cas, journalctl --user -o json les a
// rendus. capture-user-manager.jsonl garde une capture brute ; les autres sont
// la même chose transposée au gestionnaire système, celui qui joue en vrai
// oc-action-<id>.service — UNIT au lieu de USER_UNIT, _PID 1, _SYSTEMD_UNIT
// égal à l'unité pour les lignes du script. L'entrée « Deactivated
// successfully » d'exit-0 est ajoutée à la main : le gestionnaire utilisateur
// la journalise en debug, jamais le gestionnaire système.

func readFixture(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile("testdata/journal/" + name)
	if err != nil {
		t.Fatalf("lire la fixture %s : %v", name, err)
	}
	return string(content)
}

func collectJournal(t *testing.T, fixture string) (Outcome, []Line) {
	t.Helper()
	var lines []Line
	outcome, err := ReadJournal(strings.NewReader(readFixture(t, fixture)),
		"oc-action-abc123.service", func(line Line) { lines = append(lines, line) })
	if err != nil {
		t.Fatalf("ReadJournal %s : %v", fixture, err)
	}
	return outcome, lines
}

func TestReadJournal_UneSortieNulleConclutSurZero(t *testing.T) {
	outcome, lines := collectJournal(t, "exit-0.jsonl")

	if outcome != (Outcome{ExitCode: 0}) {
		t.Errorf("issue %+v", outcome)
	}
	if len(lines) != 1 || lines[0].Text != "bonjour" {
		t.Fatalf("lignes du script : %+v", lines)
	}
	if lines[0].Cursor == "" {
		t.Error("la ligne ne porte pas son curseur")
	}
	if lines[0].At.IsZero() {
		t.Error("la ligne n'est pas horodatée")
	}
}

func TestReadJournal_UnEchecRendLeCodeDuScript(t *testing.T) {
	outcome, lines := collectJournal(t, "exit-2.jsonl")

	if outcome.ExitCode != 2 || outcome.Killed || outcome.TimedOut {
		t.Errorf("issue %+v", outcome)
	}
	if len(lines) != 1 || lines[0].Text != "avant" {
		t.Errorf("lignes du script : %+v", lines)
	}
}

func TestReadJournal_LeDelaiMaximumSeDistingueDUnEchec(t *testing.T) {
	outcome, _ := collectJournal(t, "timeout.jsonl")

	if !outcome.TimedOut {
		t.Errorf("issue %+v : RuntimeMaxSec n'est pas vu", outcome)
	}
}

func TestReadJournal_UnMessageNonUTF8ArriveQuandMeme(t *testing.T) {
	_, lines := collectJournal(t, "binary-message.jsonl")

	if len(lines) != 1 {
		t.Fatalf("lignes du script : %+v", lines)
	}
	if !strings.HasPrefix(lines[0].Text, "hors UTF-8 : ") {
		t.Errorf("texte %q", lines[0].Text)
	}
}

func TestReadJournal_LesLignesDuGestionnaireNeSontPasCellesDuScript(t *testing.T) {
	_, lines := collectJournal(t, "exit-2.jsonl")

	for _, line := range lines {
		if strings.Contains(line.Text, "Main process exited") || strings.Contains(line.Text, "Started") {
			t.Errorf("une ligne de systemd est passée pour une ligne du script : %q", line.Text)
		}
	}
}

func TestReadJournal_BorneLeNombreDeLignesRelayees(t *testing.T) {
	entries := &strings.Builder{}
	for index := 0; index < maxFollowedLines+50; index++ {
		fmt.Fprintf(entries, `{"__CURSOR":"c%d","__REALTIME_TIMESTAMP":"1788699294811945",`+
			`"MESSAGE":"ligne %d","_SYSTEMD_UNIT":"oc-action-abc123.service","_PID":"4242"}`+"\n", index, index)
	}
	entries.WriteString(readFixture(t, "exit-0.jsonl"))

	var relayed []Line
	if _, err := ReadJournal(strings.NewReader(entries.String()), "oc-action-abc123.service",
		func(line Line) { relayed = append(relayed, line) }); err != nil {
		t.Fatalf("ReadJournal : %v", err)
	}

	if len(relayed) != maxFollowedLines {
		t.Fatalf("%d lignes relayées, borne %d", len(relayed), maxFollowedLines)
	}
	if !strings.Contains(relayed[len(relayed)-1].Text, "tronqué") {
		t.Errorf("la troncature n'est pas dite : %q", relayed[len(relayed)-1].Text)
	}
}

func TestReadJournal_UneLigneTropLongueEstCoupee(t *testing.T) {
	entry := fmt.Sprintf(`{"__CURSOR":"c1","__REALTIME_TIMESTAMP":"1788699294811945",`+
		`"MESSAGE":%q,"_SYSTEMD_UNIT":"oc-action-abc123.service","_PID":"4242"}`+"\n",
		strings.Repeat("x", 3*maxTextBytes))

	var relayed []Line
	if _, err := ReadJournal(strings.NewReader(entry+readFixture(t, "exit-0.jsonl")),
		"oc-action-abc123.service", func(line Line) { relayed = append(relayed, line) }); err != nil {
		t.Fatalf("ReadJournal : %v", err)
	}
	if len(relayed[0].Text) > maxTextBytes+50 {
		t.Errorf("ligne de %d octets", len(relayed[0].Text))
	}
}

func TestReadJournal_UnFluxCoupeAvantLaFinNestPasUneIssue(t *testing.T) {
	_, err := ReadJournal(strings.NewReader(readFixture(t, "capture-user-manager.jsonl")),
		"oc-action-abc123.service", nil)
	if !errors.Is(err, errJournalEnded) {
		t.Fatalf("attendu errJournalEnded, obtenu %v", err)
	}
}

func TestFollowCommand_RevalideLIdentifiantEtLeCurseur(t *testing.T) {
	cases := []struct {
		actionID string
		cursor   string
		expected string
	}{
		{"abc123", "", "journalctl -o json -u oc-action-abc123 --lines=all -f"},
		{"abc123", "s=dbe;i=63249b;b=32cd;m=41524ac26;t=65ad00213c729;x=3ebb37b142e3fb55",
			"journalctl -o json -u oc-action-abc123 --after-cursor=s=dbe;i=63249b;b=32cd;m=41524ac26;t=65ad00213c729;x=3ebb37b142e3fb55 -f"},
	}
	for _, testCase := range cases {
		remote, err := followCommand(testCase.actionID, testCase.cursor)
		if err != nil {
			t.Fatalf("followCommand(%q, %q) : %v", testCase.actionID, testCase.cursor, err)
		}
		if remote != testCase.expected {
			t.Errorf("commande %q, attendue %q", remote, testCase.expected)
		}
	}

	for _, cursor := range []string{"c1 ; reboot", "c1'", "c1\n", strings.Repeat("c", 301), "c1$(id)"} {
		if _, err := followCommand("abc123", cursor); !errors.Is(err, ErrRefusedName) {
			t.Errorf("curseur %q accepté : %v", cursor, err)
		}
	}
	if _, err := followCommand("ABC", ""); !errors.Is(err, ErrRefusedName) {
		t.Errorf("identifiant accepté : %v", err)
	}
}

func TestFollow_SuitLUniteJusquAuConstatDeSystemd(t *testing.T) {
	fake := newFakeSSH(t)
	fake.replies(t, 0, readFixture(t, "exit-2.jsonl"))

	var lines []Line
	outcome, err := fake.client().Follow(context.Background(), "abc123", "", func(line Line) { lines = append(lines, line) })
	if err != nil {
		t.Fatalf("Follow : %v", err)
	}
	if outcome.ExitCode != 2 {
		t.Errorf("issue %+v", outcome)
	}
	if len(lines) != 1 || lines[0].Text != "avant" {
		t.Errorf("lignes relayées : %+v", lines)
	}
	if got, expected := fake.remoteCommand(t), "journalctl -o json -u oc-action-abc123 --lines=all -f"; got != expected {
		t.Errorf("commande distante %q, attendue %q", got, expected)
	}
}

func TestFollow_MachineInjoignablePendantLeSuivi(t *testing.T) {
	fake := newFakeSSH(t)
	fake.replies(t, 255, "")
	fake.write(t, "stderr", "ssh: connect to host 127.0.0.1 port 22: Connection refused")

	_, err := fake.client().Follow(context.Background(), "abc123", "", nil)
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("attendu ErrUnreachable, obtenu %v", err)
	}
}

func TestFollow_UnContexteExpireRendSonErreur(t *testing.T) {
	fake := newFakeSSH(t)
	fake.replies(t, 0, readFixture(t, "capture-user-manager.jsonl"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := fake.client().Follow(ctx, "abc123", "", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("attendu context.Canceled, obtenu %v", err)
	}
}
