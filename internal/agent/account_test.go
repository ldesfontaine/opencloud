package agent

import (
	"errors"
	"os"
	"os/user"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/machine"
)

func lookupAs(name, home string) lookupUser {
	return func(uid string) (*user.User, error) {
		return &user.User{Uid: uid, Username: name, HomeDir: home}, nil
	}
}

func lookupFails(string) (*user.User, error) {
	return nil, errors.New("unknown account")
}

// Le compte dit au serveur : l'uid effectif, et le nom seulement s'il passe
// dans un en-tête.
func TestAccountOf_NamesTheAccountOnlyWhenTheNameCanTravel(t *testing.T) {
	cases := map[string]struct {
		euid   int
		lookup lookupUser
		want   machine.Account
	}{
		"root":                    {0, lookupAs("root", "/root"), machine.Account{UID: 0, Name: "root"}},
		"a service account":       {998, lookupAs("opencloud-agent", "/var/lib/opencloud-agent"), machine.Account{UID: 998, Name: "opencloud-agent"}},
		"an unknown account":      {4000, lookupFails, machine.Account{UID: 4000}},
		"a capitalised name":      {1001, lookupAs("Lucas", "/home/Lucas"), machine.Account{UID: 1001}},
		"a dotted name":           {1002, lookupAs("jean.dupont", "/home/jean"), machine.Account{UID: 1002}},
		"a name with a colon":     {1003, lookupAs("a:b", "/home/a"), machine.Account{UID: 1003}},
		"a name that is too long": {1004, lookupAs("abcdefghijklmnopqrstuvwxyzabcdefg", "/home/x"), machine.Account{UID: 1004}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := accountOf(c.euid, c.lookup); got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
			// Ce que le serveur lit est bien ce que l'agent écrit.
			parsed, ok := machine.ParseAccount(machine.FormatAccount(accountOf(c.euid, c.lookup)))
			if !ok || parsed != c.want {
				t.Errorf("the server reads %+v (%v)", parsed, ok)
			}
		})
	}
}

func TestCurrentAccount_IsTheEffectiveUser(t *testing.T) {
	if got := CurrentAccount(); got.UID != int64(os.Geteuid()) {
		t.Errorf("uid %d, effective uid %d", got.UID, os.Geteuid())
	}
}

func TestAccountLabel_IsTheNameOrTheUID(t *testing.T) {
	if got := AccountLabel(machine.Account{UID: 998, Name: "svc"}); got != "svc" {
		t.Errorf("named: %q", got)
	}
	if got := AccountLabel(machine.Account{UID: 4000}); got != "4000" {
		t.Errorf("unnamed: %q", got)
	}
	if got := AccountLabel(machine.Account{UID: 0}); got != "0" {
		t.Errorf("root without a name: %q", got)
	}
}

// Le dossier personnel sert au trousseau de Docker et à Compose : celui du
// compte de l'agent, jamais celui de root par défaut.
func TestHomeDirOf_IsTheHomeOfTheAccountThatRuns(t *testing.T) {
	cases := map[string]struct {
		euid        int
		environment string
		lookup      lookupUser
		want        string
	}{
		"root keeps /root even if HOME says otherwise": {0, "/home/x", lookupAs("root", "/elsewhere"), "/root"},
		"root without any HOME":                        {0, "", lookupFails, "/root"},
		"HOME of a service account":                    {998, "/var/lib/svc", lookupAs("svc", "/other"), "/var/lib/svc"},
		"relative HOME falls back on the account base": {998, "relative/dir", lookupAs("svc", "/var/lib/svc"), "/var/lib/svc"},
		"empty HOME falls back on the account base":    {998, "", lookupAs("svc", "/var/lib/svc"), "/var/lib/svc"},
		"nothing absolute anywhere":                    {998, "", lookupAs("svc", "relative"), ""},
		"unknown account, no HOME":                     {998, "", lookupFails, ""},
		"unknown account, relative HOME":               {998, "~", lookupFails, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := homeDirOf(c.euid, c.environment, c.lookup); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// Compose reçoit le HOME du compte, et aucun s'il est inconnu : il ne va pas
// chercher les identifiants de root pour un autre compte.
func TestComposeEnvironment_CarriesTheHomeOfTheAccount(t *testing.T) {
	has := func(environment []string, entry string) bool {
		for _, item := range environment {
			if item == entry {
				return true
			}
		}
		return false
	}
	if environment := composeEnvironment("/var/lib/svc", ""); !has(environment, "HOME=/var/lib/svc") || has(environment, "HOME=/root") {
		t.Errorf("environment %v", environment)
	}
	for _, item := range composeEnvironment("", "") {
		if len(item) >= 5 && item[:5] == "HOME=" {
			t.Errorf("a HOME without a home: %v", item)
		}
	}
	// Le trousseau de Compose est celui de la vérification des images.
	if environment := composeEnvironment("/var/lib/svc", "/etc/opencloud/docker"); !has(environment, "DOCKER_CONFIG=/etc/opencloud/docker") {
		t.Errorf("DOCKER_CONFIG did not reach Compose: %v", environment)
	}
	for _, item := range composeEnvironment("/var/lib/svc", "") {
		if strings.HasPrefix(item, "DOCKER_CONFIG=") {
			t.Errorf("a DOCKER_CONFIG without one: %v", item)
		}
	}
}
