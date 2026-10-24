package machine

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
)

func TestAccount_ReadsBackWhatItWrites(t *testing.T) {
	cases := []Account{{UID: 0, Name: "root"}, {UID: 998, Name: "opencloud-agent"}, {UID: 1000}, {UID: 4294967295, Name: "_svc"}}
	for _, want := range cases {
		got, ok := ParseAccount(FormatAccount(want))
		if !ok || got != want {
			t.Errorf("%+v -> %q -> %+v %v", want, FormatAccount(want), got, ok)
		}
	}
	if !(Account{UID: 0}).IsRoot() || (Account{UID: 998}).IsRoot() {
		t.Error("only uid 0 is root")
	}
}

// Le texte vient d'un pair : tout ce qui n'est pas exactement « uid:nom » est
// refusé, et rien n'est gardé à moitié.
func TestAccount_RefusesWhatAPeerCouldForge(t *testing.T) {
	cases := map[string]string{
		"empty":                    "",
		"no separator":             "998",
		"no uid":                   ":root",
		"negative uid":             "-1:root",
		"signed uid":               "+5:root",
		"leading zero":             "007:root",
		"leading zeros on zero":    "00:root",
		"above 32 bits":            "4294967296:root",
		"far above":                "99999999999999999999:root",
		"hexadecimal":              "0x10:root",
		"underscored":              "1_000:root",
		"decimal point":            "1.5:root",
		"space in uid":             " 5:root",
		"unicode digit":            "٣:root",
		"capitalised name":         "0:Root",
		"dotted name":              "33:www.data",
		"name with a space":        "998:open cloud",
		"name with a colon":        "998:open:cloud",
		"name starting with digit": "998:1agent",
		"trailing space":           "998:agent ",
		"newline":                  "998:agent\n",
		"33-character name":        "998:" + strings.Repeat("a", 33),
	}
	for label, text := range cases {
		if account, ok := ParseAccount(text); ok {
			t.Errorf("%s: %q accepted as %+v", label, text, account)
		}
	}
	// La borne haute des deux côtés : 32 caractères passent, 33 non.
	if _, ok := ParseAccount("998:" + strings.Repeat("a", 32)); !ok {
		t.Error("a 32-character name is allowed")
	}
}

func TestUnavailable_ReadsBackWhatItWritesInTheOrderOfTheRights(t *testing.T) {
	unavailable := map[string]string{
		CapabilityFilesWrite: "path_unwritable", CapabilityDockerDeploy: "docker_denied", CapabilityAgentUpdate: "not_self_updatable",
	}
	text := FormatUnavailable(unavailable)
	const want = "agent.update=not_self_updatable,docker.deploy=docker_denied,files.write=path_unwritable"
	if text != want {
		t.Fatalf("formatted %q, want %q", text, want)
	}
	parsed, err := ParseUnavailable(text)
	if err != nil || !maps.Equal(parsed, unavailable) {
		t.Fatalf("read back %v %v", parsed, err)
	}
	if FormatUnavailable(nil) != "" || FormatUnavailable(map[string]string{CapabilityDockerDeploy: ""}) != "" {
		t.Error("nothing unavailable writes nothing")
	}
	if parsed, err := ParseUnavailable(""); parsed != nil || err != nil {
		t.Errorf("an empty header says nothing: %v %v", parsed, err)
	}
}

// Les trois raisons de backup.produce passent l'en-tête, pour ce droit seul : un
// agent qui dit sudo_denied d'un droit de Docker est un pair qui se trompe.
func TestUnavailable_TheProduceRightCarriesItsOwnReasons(t *testing.T) {
	unavailable := map[string]string{CapabilityBackupProduce: "sudo_denied", CapabilityBackupReceive: "path_denied"}
	text := FormatUnavailable(unavailable)
	if text != "backup.receive=path_denied,backup.produce=sudo_denied" {
		t.Fatalf("formatted %q", text)
	}
	parsed, err := ParseUnavailable(text)
	if err != nil || !maps.Equal(parsed, unavailable) {
		t.Fatalf("read back %v %v", parsed, err)
	}
	for _, reason := range []string{"sudo_missing", "sudo_denied", "producer_missing"} {
		if _, err := ParseUnavailable("backup.produce=" + reason); err != nil {
			t.Errorf("%s refused: %v", reason, err)
		}
	}
}

// Un seul élément illisible refuse l'en-tête entier : on ne garde pas la moitié
// d'un rapport qu'on ne comprend pas.
func TestUnavailable_RefusesTheWholeHeaderOnOneBadItem(t *testing.T) {
	cases := map[string]string{
		"unknown right":     "shell.exec=docker_denied",
		"observe":           "observe=docker_denied",
		"unknown reason":    "docker.deploy=because",
		"a refusal reason":  "docker.deploy=bad_signature",
		"empty reason":      "docker.deploy=",
		"no separator":      "docker.deploy",
		"duplicate":         "docker.deploy=docker_denied,docker.deploy=docker_missing",
		"empty item":        "docker.deploy=docker_denied,,files.write=path_denied",
		"trailing comma":    "docker.deploy=docker_denied,",
		"one good one bad":  "docker.deploy=docker_denied,files.write=nope",
		"path in a reason":  "files.write=path_denied:/etc",
		"2049 bytes":        "docker.deploy=docker_denied" + strings.Repeat(" ", 2049-len("docker.deploy=docker_denied")),
		"nul":               "docker.deploy=docker_denied\x00",
		"reason with space": "docker.deploy=docker denied",
	}
	for label, text := range cases {
		parsed, err := ParseUnavailable(text)
		if !errors.Is(err, ErrUnavailableUnreadable) || parsed != nil {
			t.Errorf("%s: %q -> %v %v", label, text, parsed, err)
		}
	}
}

func TestUnavailableReasons_AreTheClosedListTheAgentJudgesWith(t *testing.T) {
	if len(UnavailableReasons()) != 8 {
		t.Fatalf("eight reasons, got %v", UnavailableReasons())
	}
	// Les droits qui écrivent, dans l'ordre des droits ; une liste écrite : un
	// droit ajouté plus tard (probe.lan, ssh.keys) n'y entre pas tout seul.
	want := []string{CapabilityAgentUpdate, CapabilityDockerDeploy, CapabilityDockerUpdate, CapabilityBackupReceive, CapabilityBackupProduce, CapabilityFilesWrite}
	if !slices.Equal(JudgedCapabilities(), want) || slices.Contains(JudgedCapabilities(), CapabilityObserve) {
		t.Errorf("judged rights %v, want %v", JudgedCapabilities(), want)
	}
	order := CapabilityNames()
	if !slices.IsSortedFunc(JudgedCapabilities(), func(a, b string) int { return slices.Index(order, a) - slices.Index(order, b) }) {
		t.Errorf("judged rights out of the order of the rights: %v", JudgedCapabilities())
	}
	for _, name := range JudgedCapabilities() {
		if !slices.Contains(order, name) {
			t.Errorf("%q is not a right", name)
		}
	}
}
