package systemdunits

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

const listing = `  ssh.service                       loaded    active   running OpenBSD Secure Shell server
  forgejo.service                   loaded    active   running Forgejo (Beyond coding. We Forge.)
  opencloud-agent.service           loaded    failed   failed  openCloud agent
  cron.service                      loaded    inactive dead    Regular background program processing daemon
● apt-daily.service                 not-found inactive dead    apt-daily.service
  masked-thing.service              masked    inactive dead    masked-thing.service
  odd.service                       loaded    active   cleaning-up-now Something new
  getty@tty1.service                loaded    active   running Getty on tty1
  systemd-fsck@dev-disk-by\x2duuid-0.service loaded active exited File System Check
  not-a-service.timer               loaded    active   waiting A timer
`

func TestParse_KeepsNameAndStatesOnly(t *testing.T) {
	report := Parse([]byte(listing))
	if !report.Available || report.Truncated {
		t.Fatalf("rapport : %+v", report)
	}
	var names []string
	for _, unit := range report.Units {
		names = append(names, unit.Name)
	}
	want := "cron.service,forgejo.service,getty@tty1.service,masked-thing.service,odd.service,opencloud-agent.service,ssh.service"
	if got := strings.Join(names, ","); got != want {
		t.Errorf("unités %q, attendu %q", got, want)
	}
	byName := map[string]Unit{}
	for _, unit := range report.Units {
		byName[unit.Name] = unit
	}
	if got := byName["forgejo.service"]; got != (Unit{Name: "forgejo.service", Load: LoadLoaded, Active: ActiveActive, Sub: SubRunning}) {
		t.Errorf("forgejo : %+v", got)
	}
	if got := byName["opencloud-agent.service"]; got.Active != ActiveFailed || got.Sub != SubFailed {
		t.Errorf("agent : %+v", got)
	}
	if got := byName["odd.service"]; got.Sub != Other {
		t.Errorf("un sous-état inconnu doit devenir %q, reçu %q", Other, got.Sub)
	}
	if got := byName["masked-thing.service"]; got.Load != LoadMasked {
		t.Errorf("masquée : %+v", got)
	}
	if err := report.Validate(); err != nil {
		t.Errorf("ce que l'agent relève doit passer la validation du serveur : %v", err)
	}
}

func TestParse_TooManyUnits_IsCutByNameAndSaysSo(t *testing.T) {
	var builder strings.Builder
	for index := range MaxUnits + 3 {
		builder.WriteString("u" + strings.Repeat("0", 4-len(itoa(index))) + itoa(index) + ".service loaded active running d\n")
	}
	report := Parse([]byte(builder.String()))
	if len(report.Units) != MaxUnits || !report.Truncated || report.Units[0].Name != "u0000.service" {
		t.Fatalf("%d unités, coupé=%v", len(report.Units), report.Truncated)
	}
	if err := report.Validate(); err != nil {
		t.Error(err)
	}
}

func itoa(number int) string {
	text := ""
	for number > 0 || text == "" {
		text = string(rune('0'+number%10)) + text
		number /= 10
	}
	return text
}

func TestRead_ReasonWhenSystemdCannotBeRead(t *testing.T) {
	cases := map[string]struct {
		err  error
		want Reason
	}{
		"commande absente": {exec.ErrNotFound, ReasonNoSystemctl},
		"pas systemd":      {errNotBooted, ReasonNotBooted},
		"autre échec":      {errors.New("exit status 1"), ReasonUnreadable},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			report := Read(context.Background(), func(context.Context, string, ...string) ([]byte, error) { return nil, test.err })
			if report.Available || report.Reason != test.want || len(report.Units) != 0 {
				t.Errorf("rapport %+v", report)
			}
			if err := report.Validate(); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestRead_AsksOnlyForTheUnitList(t *testing.T) {
	var asked []string
	Read(context.Background(), func(_ context.Context, name string, args ...string) ([]byte, error) {
		asked = append([]string{name}, args...)
		return nil, nil
	})
	if asked[0] != "systemctl" || asked[1] != "list-units" {
		t.Errorf("commande lancée : %v", asked)
	}
	for _, argument := range asked {
		if argument == "show" || strings.HasPrefix(argument, "-p") || argument == "cat" {
			t.Errorf("l'agent ne lit ni les propriétés ni le fichier d'une unité : %v", asked)
		}
	}
}

// Le banc de non-fuite : une valeur secrète semée dans tout ce que systemctl
// peut écrire en texte libre (description, champs en trop, nom hors modèle,
// état inconnu) ne figure dans aucune sortie du paquet. Le nom d'une unité
// valide, lui, est un champ rapporté : un nom de fichier n'est pas un secret.
func TestRead_SeededSecret_NeverInTheReport(t *testing.T) {
	const secret = "hunter2-S3CR3T-token"
	seeded := strings.Join([]string{
		"app.service loaded active running Mon appli PASSWORD=" + secret,
		"api.service loaded active running --token=" + secret + " Environment=KEY=" + secret,
		"weird.service loaded active state-" + secret + " d",
		"other.service loaded activ-" + secret + " running d",
		"third.service load-" + secret + " active running d",
		"" + secret + " loaded active running d",
		"url.service loaded active running https://user:" + secret + "@db/x?token=" + secret,
	}, "\n")
	report := Read(context.Background(), func(context.Context, string, ...string) ([]byte, error) { return []byte(seeded), nil })

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Errorf("le secret est dans le rapport : %s", encoded)
	}
	if strings.Contains(string(encoded), "PASSWORD") || strings.Contains(string(encoded), "Environment") {
		t.Errorf("un mot de la description est dans le rapport : %s", encoded)
	}
	// Le nom contenant le secret est refusé par le serveur s'il arrivait quand même.
	forged := Report{Available: true, Units: []Unit{{Name: "x" + secret + "!.service", Load: LoadLoaded, Active: ActiveActive, Sub: SubRunning}}}
	if forged.Validate() == nil {
		t.Error("un nom hors modèle doit être refusé")
	}
}

func TestExec_PassesNoEnvironmentOfTheAgent(t *testing.T) {
	const secret = "hunter2-S3CR3T-token"
	t.Setenv("OPENCLOUD_TEST_SECRET", secret)
	output, err := Exec(context.Background(), "env")
	if err != nil {
		t.Skip("env indisponible :", err)
	}
	if strings.Contains(string(output), secret) || strings.Contains(string(output), "OPENCLOUD_TEST_SECRET") {
		t.Errorf("l'environnement de l'agent est passé à la commande : %s", output)
	}
}

func TestValidate_RefusesWhatAnAgentCannotHaveRead(t *testing.T) {
	good := Unit{Name: "a.service", Load: LoadLoaded, Active: ActiveActive, Sub: SubRunning}
	cases := map[string]Report{
		"unité en double":       {Available: true, Units: []Unit{good, good}},
		"état hors liste":       {Available: true, Units: []Unit{{Name: "a.service", Load: "x", Active: ActiveActive, Sub: SubRunning}}},
		"nom sans .service":     {Available: true, Units: []Unit{{Name: "a.timer", Load: LoadLoaded, Active: ActiveActive, Sub: SubRunning}}},
		"raison sans échec":     {Available: true, Reason: ReasonUnreadable, Units: []Unit{good}},
		"échec sans raison":     {Available: false, Units: []Unit{}},
		"échec avec des unités": {Available: false, Reason: ReasonUnreadable, Units: []Unit{good}},
	}
	for name, report := range cases {
		if report.Validate() == nil {
			t.Errorf("%s : doit être refusé", name)
		}
	}
	if (Report{Available: true, Units: []Unit{good}}).Validate() != nil {
		t.Error("un rapport correct est refusé")
	}
}
