package machine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuardInstallLines_PutRootsCopyOutsideTheStateOfTheAccount(t *testing.T) {
	got := GuardInstallLines("/tmp/opencloud")
	want := []string{
		"install -d -m 0755 -o root -g root /usr/local/lib/opencloud-agent",
		"install -m 0755 -o root -g root /tmp/opencloud /usr/local/lib/opencloud-agent/opencloud-guard",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s", strings.Join(got, "\n"))
	}
	for _, line := range got {
		// Ce que root pose n'est jamais sous l'état que le compte de l'agent écrit.
		if strings.Contains(line, "/var/lib/opencloud-agent") || strings.Contains(line, "runuser") {
			t.Errorf("root acts under the state of the account: %q", line)
		}
	}
}

func TestGuardMigrationLines_AreTheExactLinesToPasteWithSudo(t *testing.T) {
	got := strings.Join(GuardMigrationLines("/tmp/opencloud", "v0.0.2"), "\n")
	want := strings.Join([]string{
		"sudo systemctl stop opencloud-agent.service",
		"sudo install -d -m 0755 -o root -g root /usr/local/lib/opencloud-agent",
		"sudo install -m 0755 -o root -g root /tmp/opencloud /usr/local/lib/opencloud-agent/opencloud-guard",
		"sudo -u opencloud-agent install -m 0755 /tmp/opencloud /var/lib/opencloud-agent/bin/opencloud-v0.0.2",
		"sudo -u opencloud-agent ln -sfn opencloud-v0.0.2 /var/lib/opencloud-agent/bin/current",
		"sudo sed -i 's|^ExecStart=/var/lib/opencloud-agent/bin/current |ExecStart=/usr/local/lib/opencloud-agent/opencloud-guard guard -state /var/lib/opencloud-agent |' /etc/systemd/system/opencloud-agent.service",
		"systemctl cat opencloud-agent.service | grep '^ExecStart='",
		"sudo systemctl daemon-reload",
		"sudo systemctl start opencloud-agent.service",
	}, "\n")
	if got != want {
		t.Fatalf("got:\n%s", got)
	}
	if GuardMigrationText("/tmp/opencloud", "v0.0.2") != got {
		t.Fatal("the text is not the lines")
	}
	// Rien ne vient de l'opérateur ni du réseau, et rien ne lance l'agent en root.
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "sudo -u root") || strings.Contains(line, "su -") || strings.Contains(line, "eval") || strings.Contains(line, "sh -c") {
			t.Errorf("a line hands a shell an input: %q", line)
		}
	}
}

// Migrer l'unité installée par la ligne de sed donne exactement l'unité que la
// documentation livre ; la rejouer ne change rien de plus.
func TestGuardMigration_RewritesTheOldUnitLineIntoTheShippedOne(t *testing.T) {
	sed, err := exec.LookPath("sed")
	if err != nil {
		t.Skip("no sed")
	}
	var script string
	for _, line := range GuardMigrationLines("/tmp/opencloud", "v0.0.2") {
		if strings.HasPrefix(line, "sudo sed -i ") {
			parts := strings.Split(line, "'")
			if len(parts) != 3 {
				t.Fatalf("the sed script is not one quoted word: %q", line)
			}
			script = parts[1]
		}
	}
	if script == "" {
		t.Fatal("no sed line")
	}
	unit := filepath.Join(t.TempDir(), "opencloud-agent.service")
	old := "[Service]\nUser=opencloud-agent\nExecStart=/var/lib/opencloud-agent/bin/current agent -state /var/lib/opencloud-agent -ca-file /etc/ssl/ca-interne.pem\nRestart=on-failure\n"
	if err := os.WriteFile(unit, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if output, err := exec.Command(sed, "-i", script, unit).CombinedOutput(); err != nil { // #nosec G204 -- le script que le test juge, sur un fichier de test.
			t.Fatalf("sed: %v %s", err, output)
		}
	}
	migrated, err := os.ReadFile(unit) // #nosec G304 -- un fichier de test.
	if err != nil {
		t.Fatal(err)
	}
	const wantLine = "ExecStart=/usr/local/lib/opencloud-agent/opencloud-guard guard -state /var/lib/opencloud-agent agent -state /var/lib/opencloud-agent -ca-file /etc/ssl/ca-interne.pem"
	if !strings.Contains(string(migrated), wantLine+"\n") || strings.Count(string(migrated), "ExecStart=") != 1 {
		t.Fatalf("migrated unit:\n%s", migrated)
	}

	shipped, err := os.ReadFile("../../docs/installation/opencloud-agent.service") // #nosec G304 -- le fichier livré, lu par un test.
	if err != nil {
		t.Fatal(err)
	}
	var shippedLine string
	for line := range strings.SplitSeq(string(shipped), "\n") {
		if strings.HasPrefix(line, "ExecStart=") {
			shippedLine = line
		}
	}
	if want := strings.TrimSuffix(wantLine, " -ca-file /etc/ssl/ca-interne.pem"); shippedLine != want {
		t.Fatalf("the shipped unit runs %q, the migration produces %q", shippedLine, want)
	}
	// Le reste de l'unité livrée ne change pas ce que les autres textes disent :
	// Restart=on-failure relance un code 3, RestartPreventExitStatus=2 ne retient que le 2.
	for _, line := range []string{"Restart=on-failure", "RestartPreventExitStatus=2", "NoNewPrivileges=true", "User=opencloud-agent"} {
		if !strings.Contains(string(shipped), "\n"+line+"\n") {
			t.Errorf("the shipped unit lost %q", line)
		}
	}
}
