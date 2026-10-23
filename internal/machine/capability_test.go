package machine

import (
	"errors"
	"testing"
)

// La forme texte se relit telle qu'elle s'écrit, observe toujours en tête,
// dans l'ordre de la liste fermée, chemins triés et sans doublon.
func TestCapabilities_ParseNormalizesAndFormatsBack(t *testing.T) {
	parsed, err := ParseCapabilities(" files.write=/srv/workspace:/opt/site:/opt/site , agent.update,docker.update")
	if err != nil {
		t.Fatal(err)
	}
	const want = "observe,agent.update,docker.update,files.write=/opt/site:/srv/workspace"
	if got := FormatCapabilities(parsed); got != want {
		t.Fatalf("formatted %q, want %q", got, want)
	}
	again, err := ParseCapabilities(want)
	if err != nil || !SameCapabilities(parsed, again) {
		t.Fatalf("the text form does not read back: %+v %v", again, err)
	}
	empty, err := ParseCapabilities("")
	if err != nil || FormatCapabilities(empty) != CapabilityObserve {
		t.Fatalf("an empty list is observe alone: %+v %v", empty, err)
	}
}

// probe.lan se range juste après observe, quel que soit l'ordre où on l'écrit,
// et ne prend aucun chemin.
func TestCapabilities_ProbeLANComesRightAfterObserveAndTakesNoPath(t *testing.T) {
	parsed, err := ParseCapabilities("docker.update, probe.lan,agent.update")
	if err != nil {
		t.Fatal(err)
	}
	if got := FormatCapabilities(parsed); got != "observe,probe.lan,agent.update,docker.update" {
		t.Fatalf("formatted %q", got)
	}
	if !HasCapability(parsed, CapabilityProbeLAN) {
		t.Fatal("probe.lan is lost")
	}
	if names := CapabilityNames(); names[0] != CapabilityObserve || names[1] != CapabilityProbeLAN {
		t.Fatalf("display order %v", names)
	}
	if _, err := ParseCapabilities("probe.lan=/x"); !errors.Is(err, ErrCapabilityScope) {
		t.Fatalf("a path on probe.lan: %v", err)
	}
	without, err := ParseCapabilities("agent.update")
	if err != nil || HasCapability(without, CapabilityProbeLAN) {
		t.Fatalf("probe.lan must be opt-in at the parser: %+v %v", without, err)
	}
}

func TestCapabilities_RefuseUnknownRightsBadPathsAndWrongScopes(t *testing.T) {
	cases := map[string]error{
		"shell.exec":                             ErrCapabilityUnknown,
		"files.write=relative/path":              ErrCapabilityPath,
		"files.write=/":                          ErrCapabilityPath,
		"files.write=/srv/../etc":                ErrCapabilityPath,
		"files.write=/srv/a b":                   ErrCapabilityPath,
		"files.write":                            ErrCapabilityScope,
		"backup.receive":                         ErrCapabilityScope,
		"backup.receive=/srv/a:/srv/b":           ErrCapabilityScope,
		"docker.deploy=/srv/a":                   ErrCapabilityScope,
		"files.write=/a:/b:/c:/d:/e:/f:/g:/h:/i": ErrCapabilityScope,
	}
	for text, want := range cases {
		if _, err := ParseCapabilities(text); !errors.Is(err, want) {
			t.Errorf("%q: %v, want %v", text, err, want)
		}
	}
}

// L'écart ne se dit que s'il y a deux choses à comparer : ce que le jeton
// demandait et ce que l'agent a rapporté.
func TestAgent_CapabilitiesDifferOnlyWhenBothSidesAreKnown(t *testing.T) {
	requested, _ := ParseCapabilities("agent.update")
	observeOnly, _ := ParseCapabilities("")
	reported := Agent{ID: "agt_1", Requested: requested, Capabilities: observeOnly, ReportedAt: testReportedAt}
	if !reported.CapabilitiesDiffer() {
		t.Fatal("a file without agent.update differs from the token")
	}
	same := Agent{ID: "agt_1", Requested: requested, Capabilities: requested, ReportedAt: testReportedAt}
	if same.CapabilitiesDiffer() {
		t.Fatal("the same list does not differ")
	}
	if (Agent{ID: "agt_1", Requested: requested}).CapabilitiesDiffer() {
		t.Fatal("nothing reported yet: nothing to compare")
	}
	if (Agent{ID: "agt_1", Capabilities: observeOnly, ReportedAt: testReportedAt}).CapabilitiesDiffer() {
		t.Fatal("an agent from before the rights: nothing requested, nothing to compare")
	}
}

// Un dossier est couvert par le chemin du droit ou dessous ; un voisin dont
// le nom commence pareil, un chemin non propre ou le droit d'un autre nom ne
// le sont pas.
func TestCoversPath(t *testing.T) {
	capabilities, err := ParseCapabilities("backup.receive=/srv/backups,files.write=/srv/site")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, path string
		want       bool
	}{
		{CapabilityBackupReceive, "/srv/backups", true},
		{CapabilityBackupReceive, "/srv/backups/opencloud", true},
		{CapabilityBackupReceive, "/srv/backups2", false},
		{CapabilityBackupReceive, "/srv", false},
		{CapabilityBackupReceive, "/srv/backups/../etc", false},
		{CapabilityBackupReceive, "/srv/backups/", false},
		{CapabilityBackupReceive, "srv/backups", false},
		{CapabilityBackupReceive, "/srv/site", false},
		{CapabilityFilesWrite, "/srv/site/a", true},
		{CapabilityDockerDeploy, "/srv/backups", false},
	}
	for _, c := range cases {
		if got := CoversPath(capabilities, c.name, c.path); got != c.want {
			t.Errorf("CoversPath(%s, %q) = %t, want %t", c.name, c.path, got, c.want)
		}
	}
	if CoversPath(nil, CapabilityBackupReceive, "/srv/backups") {
		t.Error("no rights cover nothing")
	}
	if granted, ok := CoveringPath(capabilities, CapabilityBackupReceive, "/srv/backups/opencloud/nuit"); !ok || granted != "/srv/backups" {
		t.Errorf("covering path %q %t", granted, ok)
	}
}
