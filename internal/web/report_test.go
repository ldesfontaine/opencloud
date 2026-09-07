package web

import (
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/store"
)

// La sortie d'un vrai Diagnostiquer, relevée en jouant
// internal/scripts/diagnostiquer/run.sh sur une machine (lecture seule, sans
// root) : c'est ce que le rapport doit savoir lire.
const realDiagnosticOutput = `étape: préflight — de quoi lire la machine
étape: identité
info: hote=parrot
info: noyau=7.0.13+parrot7-amd64
info: systeme=Parrot Security 7.3 (echo)
info: duree_de_fonctionnement=127289 s
étape: horloge
info: horloge_synchronisee=no
étape: espace et mémoire
info: espace_libre_racine=242785 Mio
info: espace_libre_srv=242785 Mio
info: memoire_libre=20921 Mio
étape: unités systemd
info: unites_en_echec=snap-brave-621.mount
étape: docker
info: docker=29.8.0
info: plugin_compose=présent
étape: ports
info: port_80=libre
info: port_443=libre
étape: la norme d'implantation
info: norme_workspace=absent
info: norme_data=absent
étape: le lanceur
info: lanceur=absent
étape: vérifier — la lecture n'a rien écrit
résultat: inchangé`

// La même machine, une fois enrôlée et rangée : lanceur posé, /srv en place,
// horloge synchronisée, et un port publié sur toutes les interfaces.
const enrolledDiagnosticOutput = `étape: identité
info: hote=web-1
info: duree_de_fonctionnement=3600 s
étape: horloge
info: horloge_synchronisee=yes
étape: espace et mémoire
info: espace_libre_srv=absent
étape: unités systemd
info: unites_en_echec=aucune
étape: ports
info: port_80=tenu
info: port_443=libre
avertissement: port publié sur toutes les interfaces : 0.0.0.0:8080
étape: la norme d'implantation
info: norme_workspace=présent
info: norme_data=absent
étape: le lanceur
info: lanceur_proprietaire=root:root
info: lanceur_mode=755
info: lanceur_bits_speciaux=aucun
info: lanceur_empreinte=sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08
étape: vérifier — la lecture n'a rien écrit
résultat: inchangé`

// storedLines rend la sortie telle que le journal la garde : une ligne, un
// rang.
func storedLines(output string) []store.ActionLine {
	var lines []store.ActionLine
	for index, text := range strings.Split(output, "\n") {
		lines = append(lines, store.ActionLine{Seq: int64(index + 1), At: time.Now().UTC(), Text: text})
	}
	return lines
}

func newTestReport(t *testing.T, output string) *report {
	t.Helper()
	read := newReport(catalog.KindDiagnostiquer, storedLines(output))
	if read == nil {
		t.Fatal("cette sortie doit donner un rapport")
	}
	return read
}

// factOf trouve un constat par son libellé, dans toutes les sections.
func factOf(read *report, label string) (reportFact, bool) {
	for _, section := range read.Sections {
		for _, fact := range section.Facts {
			if fact.Label == label {
				return fact, true
			}
		}
	}
	return reportFact{}, false
}

func expectValue(t *testing.T, read *report, label, text, tone string) {
	t.Helper()
	fact, found := factOf(read, label)
	if !found {
		t.Fatalf("aucun constat « %s » dans le rapport", label)
	}
	if fact.Value.Text != text || fact.Value.Tone != tone {
		t.Fatalf("« %s » = %q en %q, attendu %q en %q", label, fact.Value.Text, fact.Value.Tone, text, tone)
	}
}

func TestReport_ARealOutput_BecomesOneSectionPerStep(t *testing.T) {
	read := newTestReport(t, realDiagnosticOutput)

	var titles []string
	for _, section := range read.Sections {
		titles = append(titles, section.Title)
	}
	want := []string{"Identité", "Horloge", "Espace et mémoire", "Unités systemd",
		"Docker", "Ports", "Norme d'implantation", "Lanceur"}
	if strings.Join(titles, ", ") != strings.Join(want, ", ") {
		t.Fatalf("sections = %v, attendu %v", titles, want)
	}
}

// Le préflight et la vérification n'écrivent aucun constat : une section vide
// ne se montre pas, le bandeau du résultat dit déjà que l'action est passée.
func TestReport_AStepWithoutFacts_MakesNoSection(t *testing.T) {
	read := newTestReport(t, realDiagnosticOutput)

	for _, section := range read.Sections {
		if section.Title == "Préflight" || section.Title == "Vérification" {
			t.Fatalf("une étape sans constat ne fait pas de section : %s", section.Title)
		}
	}
}

func TestReport_EachKnownKey_IsTranslated(t *testing.T) {
	read := newTestReport(t, realDiagnosticOutput)

	expectValue(t, read, "Hôte", "parrot", "")
	expectValue(t, read, "Noyau", "7.0.13+parrot7-amd64", "")
	expectValue(t, read, "Système", "Parrot Security 7.3 (echo)", "")
	expectValue(t, read, "Allumée depuis", "1 j 11 h", "")
	expectValue(t, read, "Horloge synchronisée", "non", toneWarn)
	expectValue(t, read, "Espace libre sur /", "242785 Mio", "")
	expectValue(t, read, "Mémoire libre", "20921 Mio", "")
	expectValue(t, read, "Unités en échec", "snap-brave-621.mount", toneDanger)
	expectValue(t, read, "Docker", "29.8.0", "")
	expectValue(t, read, "Plugin compose", "présent", "")
	expectValue(t, read, "Port 80", "libre", "")
	expectValue(t, read, "/srv/workspace", "absent", toneWarn)
	expectValue(t, read, "Lanceur", "absent", toneDanger)
}

func TestReport_AMachineInOrder_ReadsTheGoodNewsInGreen(t *testing.T) {
	read := newTestReport(t, enrolledDiagnosticOutput)

	expectValue(t, read, "Allumée depuis", "1 h 0 min", "")
	expectValue(t, read, "Horloge synchronisée", "oui", toneOK)
	expectValue(t, read, "Espace libre sur /srv", "/srv absent", toneWarn)
	expectValue(t, read, "Unités en échec", "aucune", toneOK)
	expectValue(t, read, "Port 80", "tenu", toneAccent)
	expectValue(t, read, "/srv/workspace", "présent", toneOK)
	expectValue(t, read, "Propriétaire du lanceur", "root:root", "")
	expectValue(t, read, "Mode du lanceur", "755", "")
	expectValue(t, read, "Bits spéciaux", "aucun", "")
}

func TestReport_TheDigest_IsShortenedAndKeepsTheWhole(t *testing.T) {
	read := newTestReport(t, enrolledDiagnosticOutput)

	fingerprint, found := factOf(read, "Empreinte")
	if !found {
		t.Fatal("le lanceur porte son empreinte")
	}
	if !strings.HasSuffix(fingerprint.Value.Text, "…") || !fingerprint.Value.Mono {
		t.Fatalf("empreinte affichée = %q", fingerprint.Value.Text)
	}
	if !strings.HasPrefix(fingerprint.Value.Title, "sha256:9f86d0") || len(fingerprint.Value.Title) != 71 {
		t.Fatalf("le title doit porter l'empreinte entière, reçu %q", fingerprint.Value.Title)
	}
}

func TestReport_TheWarnings_ComeFirst(t *testing.T) {
	read := newTestReport(t, enrolledDiagnosticOutput)

	if len(read.Warnings) != 1 {
		t.Fatalf("avertissements = %v", read.Warnings)
	}
	if read.Warnings[0] != "port publié sur toutes les interfaces : 0.0.0.0:8080" {
		t.Fatalf("l'avertissement perd son texte : %q", read.Warnings[0])
	}
}

func TestReport_AnUnknownStepAndKey_KeepWhatTheScriptWrote(t *testing.T) {
	read := newTestReport(t, "étape: une étape nouvelle\ninfo: charge_moyenne=0.42")

	if len(read.Sections) != 1 || read.Sections[0].Title != "une étape nouvelle" {
		t.Fatalf("sections = %+v", read.Sections)
	}
	expectValue(t, read, "charge moyenne", "0.42", "")
}

// Le rapport s'ajoute par une entrée dans la table des vocabulaires : une
// action qui n'y est pas garde son journal.
func TestReport_AnActionWithoutVocabulary_HasNoReport(t *testing.T) {
	if read := newReport(catalog.KindEnroler, storedLines(realDiagnosticOutput)); read != nil {
		t.Fatalf("Enrôler n'a pas de lecture, reçu %+v", read)
	}
}

// Un refus au préflight n'écrit aucun constat : il n'y a rien à mettre en
// rapport, le journal suffit.
func TestReport_ARefusalBeforeAnyFact_HasNoReport(t *testing.T) {
	refused := "étape: préflight — de quoi lire la machine\nrésultat: refusé — cette machine ne tourne pas sous systemd\n→ installer systemd"

	if read := newReport(catalog.KindDiagnostiquer, storedLines(refused)); read != nil {
		t.Fatalf("un refus n'est pas un rapport, reçu %+v", read)
	}
}

func TestUptimeValue_ReadsSecondsAsDaysAndHours(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"127289 s", "1 j 11 h"},
		{"271234 s", "3 j 3 h"},
		{"7200 s", "2 h 0 min"},
		{"90 s", "1 min"},
		{"inconnu", "inconnu"},
	}

	for _, testCase := range cases {
		if got := uptimeValue(testCase.raw).Text; got != testCase.want {
			t.Fatalf("uptimeValue(%q) = %q, attendu %q", testCase.raw, got, testCase.want)
		}
	}
}
