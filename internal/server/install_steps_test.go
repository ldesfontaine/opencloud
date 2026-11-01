package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

func stepCommand(steps []installStepJSON, id string) string {
	for _, step := range steps {
		if step.ID == id {
			return step.Command
		}
	}
	return ""
}

func stepIDs(steps []installStepJSON) []string {
	ids := make([]string, 0, len(steps))
	for _, step := range steps {
		ids = append(ids, step.ID)
	}
	return ids
}

func TestInstallSteps_FollowTheOrderTheOperatorPastes(t *testing.T) {
	rights, err := machine.ParseCapabilities("docker.deploy,backup.receive=/srv/backups")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name         string
		capabilities []machine.Capability
		target       installTarget
		want         []string
	}{
		{"a new machine, observe only", nil, installTarget{}, []string{"binary", "prepare", "enroll", "unit"}},
		{"a new machine whose rights write elsewhere", rights, installTarget{}, []string{"binary", "prepare", "enroll", "unit", "rights"}},
		{"the openCloud machine has the binary already", nil, installTarget{local: true}, []string{"prepare", "enroll", "unit"}},
		{"a re-enrollment stops the agent first", rights, installTarget{reenroll: true}, []string{"stop", "binary", "prepare", "enroll", "unit", "rights"}},
	}
	for _, c := range cases {
		if got := stepIDs(installSteps("https://opencloud.example.org", "oc_abc", lang.French, c.capabilities, c.target)); !slices.Equal(got, c.want) {
			t.Errorf("%s: steps %v, want %v", c.name, got, c.want)
		}
	}
}

// Le jeton ne vit que dans l'enrôlement : aucune autre étape, copiée ailleurs
// ou lue par un autre compte, ne le porte.
func TestInstallSteps_TheTokenIsOnlyInTheEnrollment(t *testing.T) {
	rights, err := machine.ParseCapabilities("docker.deploy,backup.receive=/srv/backups,backup.produce=proxmox-vzdump")
	if err != nil {
		t.Fatal(err)
	}
	const token = "oc_jeton_secret_0123"
	for _, step := range installSteps("https://opencloud.example.org", token, lang.French, rights, installTarget{reenroll: true}) {
		if has := strings.Contains(step.Command, token); has != (step.ID == stepEnroll) {
			t.Errorf("step %s: token present = %v", step.ID, has)
		}
	}
}

func TestInstallBinary_CopiesTheServerBinaryFromTheServerAddress(t *testing.T) {
	cases := map[string]string{
		"https://opencloud.example.org":     "scp root@opencloud.example.org:/usr/local/bin/opencloud /tmp/opencloud && chmod 0755 /tmp/opencloud",
		"https://192.0.2.7:8443":            "scp root@192.0.2.7:/usr/local/bin/opencloud /tmp/opencloud && chmod 0755 /tmp/opencloud",
		"https://[2001:db8::1]:8443":        "scp root@[2001:db8::1]:/usr/local/bin/opencloud /tmp/opencloud && chmod 0755 /tmp/opencloud",
		"https://x.example;touch$IFS/tmp/p": "scp root@ADRESSE_DU_SERVEUR:/usr/local/bin/opencloud /tmp/opencloud && chmod 0755 /tmp/opencloud",
		"":                                  "scp root@ADRESSE_DU_SERVEUR:/usr/local/bin/opencloud /tmp/opencloud && chmod 0755 /tmp/opencloud",
	}
	for publicURL, want := range cases {
		if got := installBinary(publicURL); got != want {
			t.Errorf("%q: %q, want %q", publicURL, got, want)
		}
	}
}

// L'étape de l'unité pose exactement le fichier de machine.AgentUnitLines : le
// shell lit l'heredoc entre apostrophes sans rien en interpréter.
func TestInstallUnit_TheShellWritesExactlyTheUnit(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	target := filepath.Join(t.TempDir(), "agent.service")
	script := strings.Split(installUnit(), "\nEOF\n")[0]
	script = strings.Replace(script, machine.UnitFile, target, 1) + "\nEOF\n"
	if output, err := exec.Command(shell, "-c", script).CombinedOutput(); err != nil { // #nosec G204 -- le texte que le test juge, écrit dans un dossier temporaire.
		t.Fatalf("sh: %v %s", err, output)
	}
	written, err := os.ReadFile(target) // #nosec G304 -- un fichier de test.
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != strings.Join(machine.AgentUnitLines(), "\n")+"\n" {
		t.Fatalf("unit written:\n%s", written)
	}
	if unit := installUnit(); !strings.HasSuffix(unit, "systemctl daemon-reload\nsystemctl enable --now opencloud-agent.service") {
		t.Fatalf("the unit step must end by enabling the service:\n%s", unit)
	}
}

func TestInstallRights_AuthorizesTheFoldersThatDeployAndBackupsWrite(t *testing.T) {
	if got := installRights(nil); got != "" {
		t.Fatalf("observe alone writes nowhere else: %q", got)
	}
	rights, err := machine.ParseCapabilities("docker.update,backup.receive=/srv/backups,files.write=/srv/site")
	if err != nil {
		t.Fatal(err)
	}
	got := installRights(rights)
	want := `printf '%s\n' '[Service]' 'ReadWritePaths=/srv/workspace /srv/backups' | install -m 0644 /dev/stdin /etc/systemd/system/opencloud-agent.service.d/droits.conf`
	if !strings.Contains(got, want) || strings.Contains(got, "/srv/site") || !strings.HasSuffix(got, "systemctl restart opencloud-agent.service") {
		t.Fatalf("rights step:\n%s", got)
	}
}
