package probe

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func validDefinition() Definition {
	return Definition{
		Name:      "site nextcloud",
		Kind:      KindHTTP,
		Target:    "https://cloud.exemple.fr/",
		MachineID: "machine-1",
	}
}

func TestDefinition_Complete_FillsDefaultsForHTTP(t *testing.T) {
	completed := Definition{Name: " site ", Kind: KindHTTP, Target: " https://a.fr ", MachineID: "m"}.Complete()
	if completed.Name != "site" || completed.Target != "https://a.fr" {
		t.Fatalf("espaces non retirés: %+v", completed)
	}
	if completed.Interval != DefaultInterval || completed.Timeout != DefaultTimeout {
		t.Fatalf("cadence par défaut absente: %+v", completed)
	}
	if completed.Method != "GET" || completed.ExpectedStatus != "2xx" {
		t.Fatalf("défauts HTTP absents: %+v", completed)
	}
	if completed.FailureThreshold != DefaultFailureThreshold || completed.RecoveryThreshold != DefaultRecoveryThreshold {
		t.Fatalf("seuils par défaut absents: %+v", completed)
	}
}

func TestDefinition_Complete_LeavesTCPWithoutHTTPFields(t *testing.T) {
	completed := Definition{Name: "base", Kind: KindTCP, Target: "10.8.0.2:5432", MachineID: "m", Method: "POST", ExpectedStatus: "500", ExpectedBody: "x", FollowRedirects: true}.Complete()
	if completed.Method != "" || completed.ExpectedStatus != "" || completed.ExpectedBody != "" || completed.FollowRedirects {
		t.Fatalf("une sonde TCP a gardé des champs HTTP: %+v", completed)
	}
}

func TestDefinition_Validate_AcceptsWhatTheFormSends(t *testing.T) {
	if err := validDefinition().Complete().Validate(); err != nil {
		t.Fatalf("définition valable refusée: %v", err)
	}
	tcp := Definition{Name: "base", Kind: KindTCP, Target: "db.local:5432", MachineID: "m"}.Complete()
	if err := tcp.Validate(); err != nil {
		t.Fatalf("sonde TCP valable refusée: %v", err)
	}
}

func TestDefinition_Validate_RefusesWhatCannotBeProbed(t *testing.T) {
	cases := []struct {
		name  string
		build func(Definition) Definition
		want  error
	}{
		{"nom vide", func(d Definition) Definition { d.Name = "  "; return d }, ErrNameInvalid},
		{"nom trop long", func(d Definition) Definition { d.Name = strings.Repeat("a", 81); return d }, ErrNameInvalid},
		{"type inconnu", func(d Definition) Definition { d.Kind = "ping"; return d }, ErrKindInvalid},
		{"sans machine", func(d Definition) Definition { d.MachineID = ""; return d }, ErrMachineInvalid},
		{"cible sans protocole", func(d Definition) Definition { d.Target = "cloud.exemple.fr"; return d }, ErrTargetInvalid},
		{"cible ftp", func(d Definition) Definition { d.Target = "ftp://cloud.exemple.fr/"; return d }, ErrTargetInvalid},
		{"intervalle trop court", func(d Definition) Definition { d.Interval = 5 * time.Second; return d }, ErrIntervalInvalid},
		{"intervalle trop long", func(d Definition) Definition { d.Interval = 48 * time.Hour; return d }, ErrIntervalInvalid},
		{"délai plus long que l'intervalle", func(d Definition) Definition {
			d.Interval = 30 * time.Second
			d.Timeout = 40 * time.Second
			return d
		}, ErrTimeoutInvalid},
		{"seuil nul", func(d Definition) Definition { d.FailureThreshold = -1; return d }, ErrThresholdInvalid},
		{"méthode qui écrit", func(d Definition) Definition { d.Method = "DELETE"; return d }, ErrMethodInvalid},
		{"code attendu illisible", func(d Definition) Definition { d.ExpectedStatus = "deux-cents"; return d }, ErrStatusInvalid},
		{"texte attendu trop long", func(d Definition) Definition { d.ExpectedBody = strings.Repeat("a", 201); return d }, ErrBodyInvalid},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.build(validDefinition()).Complete().Validate()
			if !errors.Is(err, testCase.want) {
				t.Fatalf("refus attendu %v, obtenu %v", testCase.want, err)
			}
		})
	}
}

// L'adresse de métadonnées des hébergeurs livre les identifiants de
// l'instance : elle ne se sonde pas, et le lien-local avec elle.
func TestDefinition_Validate_RefusesLinkLocalTargets(t *testing.T) {
	for _, target := range []string{"http://169.254.169.254/latest/meta-data/", "http://[fe80::1]/", "http://169.254.1.2:8080/"} {
		definition := validDefinition()
		definition.Target = target
		if err := definition.Complete().Validate(); !errors.Is(err, ErrTargetForbidden) {
			t.Fatalf("%s: refus attendu, obtenu %v", target, err)
		}
	}
}

// Sonder un service par l'agent de sa propre machine est l'usage voulu :
// la boucle locale et le privé restent ouverts.
func TestDefinition_Validate_AcceptsLoopbackAndPrivateTargets(t *testing.T) {
	for _, target := range []string{"http://127.0.0.1:8080/health", "http://10.8.0.2/", "http://[::1]:3000/"} {
		definition := validDefinition()
		definition.Target = target
		if err := definition.Complete().Validate(); err != nil {
			t.Fatalf("%s: accepté attendu, refusé par %v", target, err)
		}
	}
}

func TestParseTarget_GivesThePortToDial(t *testing.T) {
	cases := map[string]Address{
		"https://cloud.exemple.fr/":      {Host: "cloud.exemple.fr", Port: "443"},
		"http://cloud.exemple.fr/health": {Host: "cloud.exemple.fr", Port: "80"},
		"https://cloud.exemple.fr:8443/": {Host: "cloud.exemple.fr", Port: "8443"},
	}
	for target, want := range cases {
		got, err := ParseTarget(KindHTTP, target)
		if err != nil || got != want {
			t.Fatalf("%s: attendu %+v, obtenu %+v (%v)", target, want, got, err)
		}
	}
}

func TestMatchStatus_ReadsFamiliesAndCodes(t *testing.T) {
	cases := []struct {
		pattern string
		code    int
		want    bool
	}{
		{"2xx", 204, true},
		{"2xx", 301, false},
		{"", 200, true},
		{"200,301", 301, true},
		{"200,301", 302, false},
		{"418", 418, true},
	}
	for _, testCase := range cases {
		if got := matchStatus(testCase.pattern, testCase.code); got != testCase.want {
			t.Fatalf("%q avec %d: attendu %v", testCase.pattern, testCase.code, testCase.want)
		}
	}
}
