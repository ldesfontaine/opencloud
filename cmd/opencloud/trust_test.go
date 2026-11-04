package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agent"
	"github.com/ldesfontaine/opencloud/internal/anchor"
	"github.com/ldesfontaine/opencloud/internal/lang"
)

// enrolledStateDir écrit un état d'agent enrôlé et rend son dossier.
func enrolledStateDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := agent.SaveIdentity(root, agent.Identity{MachineID: "m", Language: lang.French}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRunAgentTrust_AUsageErrorIsARefusalThatListsTheTypes(t *testing.T) {
	for _, arguments := range [][]string{{}, {"frobnicate"}, {"add"}, {"add", "backup.guest"}, {"list", "extra"}, {"remove", "a", "b", "c"}} {
		var out bytes.Buffer
		err := runAgentTrust(append([]string{"-state", enrolledStateDir(t)}, arguments...), &out)
		var refused *refusal
		if !errors.As(err, &refused) || !strings.Contains(refused.message, "backup.recipient, backup.guest, restic.target-cert") {
			t.Errorf("%v: %v", arguments, err)
		}
	}
}

// Sans être root, la commande refuse sans rien écrire : elle change ce que
// l'agent accepte d'exécuter.
func TestRunAgentTrust_RefusesAnAccountThatIsNotRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("le test joue un compte sans privilège")
	}
	dir := enrolledStateDir(t)
	var out bytes.Buffer
	err := runAgentTrust([]string{"-state", dir, "add", anchor.BackupGuest, "101"}, &out)
	var refused *refusal
	if !errors.As(err, &refused) || !strings.Contains(refused.message, "root") {
		t.Fatalf("err %v", err)
	}
	root, openErr := os.OpenRoot(dir)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer root.Close()
	if identity, loadErr := agent.LoadIdentity(root); loadErr != nil || len(identity.Anchors) != 0 {
		t.Fatalf("a refused command wrote: %+v %v", identity.Anchors, loadErr)
	}
}

func TestPrintAnchors_SeparatesNoneFromUnreadable(t *testing.T) {
	var out bytes.Buffer
	state := anchor.FromFile(map[string][]string{anchor.BackupGuest: {"101", "102"}, anchor.ResticTargetCert: {"nope"}})
	printAnchors(&out, testCatalog(t), agent.TrustResult{State: state})
	text := out.String()
	for _, want := range []string{"backup.recipient\taucune posée", "backup.guest\t101 102", "restic.target-cert\tillisible"} {
		if !strings.Contains(text, want) {
			t.Errorf("%q missing from:\n%s", want, text)
		}
	}
}

func TestSplitTrustArguments(t *testing.T) {
	cases := map[string]struct {
		arguments []string
		verb      string
		ok        bool
	}{
		"list":   {[]string{"list"}, "list", true},
		"add":    {[]string{"add", "backup.guest", "101"}, "add", true},
		"remove": {[]string{"remove", "backup.guest", "101"}, "remove", true},
		"short":  {[]string{"add", "backup.guest"}, "", false},
		"long":   {[]string{"list", "x"}, "", false},
		"none":   {nil, "", false},
		"verb":   {[]string{"drop", "a", "b"}, "", false},
	}
	for name, c := range cases {
		verb, _, _, ok := splitTrustArguments(c.arguments)
		if verb != c.verb || ok != c.ok {
			t.Errorf("%s: %q %v", name, verb, ok)
		}
	}
}

func TestParseAnchorFlags_JudgesEveryTypeOfTheRegistry(t *testing.T) {
	values := map[string]*string{}
	for _, kind := range anchor.Kinds() {
		empty := ""
		values[kind.Name] = &empty
	}
	if anchors, bad := parseAnchorFlags(values); bad != nil || len(anchors) != 0 {
		t.Fatalf("no flag: %v %v", anchors, bad)
	}
	good, wrong := "101,102", "0101"
	values[anchor.BackupGuest] = &good
	if anchors, bad := parseAnchorFlags(values); bad != nil || len(anchors[anchor.BackupGuest]) != 2 {
		t.Fatalf("good: %v %v", anchors, bad)
	}
	values[anchor.BackupGuest] = &wrong
	if _, bad := parseAnchorFlags(values); bad == nil || bad.Name != anchor.BackupGuest {
		t.Fatalf("a malformed value must name its type: %v", bad)
	}
}
