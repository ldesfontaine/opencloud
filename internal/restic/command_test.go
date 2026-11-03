package restic

import (
	"slices"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

const (
	seededRESTPassword = "seeded-rest-password-7f3a"
	seededRepoPassword = "seeded-repository-password-91c2"
)

func directRepository() Repository {
	return Repository{
		Path:           agentwire.ResticPathDirect,
		RepositoryFile: "/var/lib/agent/restic/nas.repository",
		CACertFile:     "/var/lib/agent/restic/nas.pem",
		PasswordFile:   "/var/lib/agent/restic/nas.password",
		CacheDir:       "/var/lib/agent/restic/cache",
	}
}

func TestBackupDirectory_DirectNamesFilesNeverSecrets(t *testing.T) {
	command, err := BackupDirectory("/usr/bin/restic", directRepository(), "/srv/services/wiki")
	if err != nil {
		t.Fatal(err)
	}

	wantArgs := []string{"backup", "--json", "--quiet", "--cache-dir", "/var/lib/agent/restic/cache", "--", "/srv/services/wiki"}
	if !slices.Equal(command.Args, wantArgs) {
		t.Errorf("args %q", command.Args)
	}
	wantEnv := []string{
		"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C",
		"RESTIC_PASSWORD_FILE=/var/lib/agent/restic/nas.password",
		"RESTIC_REPOSITORY_FILE=/var/lib/agent/restic/nas.repository",
		"RESTIC_CACERT=/var/lib/agent/restic/nas.pem",
	}
	if !slices.Equal(command.Env, wantEnv) {
		t.Errorf("env %q", command.Env)
	}
}

func TestBackupStream_RelayPointsAtTheLocalProxy(t *testing.T) {
	repository := Repository{
		Path:         agentwire.ResticPathRelay,
		ProxyURL:     "rest:http://127.0.0.1:40123/0123abcd/",
		PasswordFile: "/var/lib/agent/restic/nas.password",
	}
	producer := []string{"/usr/bin/vzdump", "105", "--stdout", "--mode", "snapshot"}

	command, err := BackupStream("/usr/bin/restic", repository, "vm-105.vma", producer)
	if err != nil {
		t.Fatal(err)
	}

	wantArgs := []string{"backup", "--json", "--quiet", "--no-cache", "--stdin-from-command", "--stdin-filename", "vm-105.vma", "--",
		"/usr/bin/vzdump", "105", "--stdout", "--mode", "snapshot"}
	if !slices.Equal(command.Args, wantArgs) {
		t.Errorf("args %q", command.Args)
	}
	if !slices.Contains(command.Env, "RESTIC_REPOSITORY=rest:http://127.0.0.1:40123/0123abcd/") || slices.ContainsFunc(command.Env, func(variable string) bool {
		return strings.HasPrefix(variable, "RESTIC_CACERT=") || strings.HasPrefix(variable, "RESTIC_REPOSITORY_FILE=")
	}) {
		t.Errorf("env %q", command.Env)
	}
}

func TestBackup_RefusesWhatWouldEscapeOrRunAShell(t *testing.T) {
	relay := Repository{Path: agentwire.ResticPathRelay, ProxyURL: "rest:http://127.0.0.1:1/x/", PasswordFile: "/p"}
	if _, err := BackupDirectory("restic", relay, "/srv"); err == nil {
		t.Error("relative binary")
	}
	for _, directory := range []string{"srv", "/srv/../etc", "/srv/", ""} {
		if _, err := BackupDirectory("/usr/bin/restic", relay, directory); err == nil {
			t.Errorf("directory %q", directory)
		}
	}
	for _, name := range []string{"", "../x", "a/b", "-x", strings.Repeat("a", 161)} {
		if _, err := BackupStream("/usr/bin/restic", relay, name, []string{"/usr/bin/vzdump"}); err == nil {
			t.Errorf("name %q", name)
		}
	}
	for _, producer := range [][]string{nil, {"vzdump"}, {"/usr/bin/vzdump", "a\x00b"}} {
		if _, err := BackupStream("/usr/bin/restic", relay, "vm.vma", producer); err == nil {
			t.Errorf("producer %q", producer)
		}
	}
	incomplete := []Repository{
		{Path: agentwire.ResticPathDirect, PasswordFile: "/p", RepositoryFile: "/r"},
		{Path: agentwire.ResticPathRelay, PasswordFile: "/p", ProxyURL: "rest:https://nas:8000/"},
		{Path: agentwire.ResticPathRelay, ProxyURL: "rest:http://127.0.0.1:1/x/"},
		{Path: "tunnel", PasswordFile: "/p"},
	}
	for _, repository := range incomplete {
		if _, err := BackupDirectory("/usr/bin/restic", repository, "/srv"); err == nil {
			t.Errorf("repository %+v", repository)
		}
	}
}

// Banc de non-fuite : le mot de passe REST et celui du dépôt, semés, ne
// figurent dans aucun argument, aucune variable, aucun rendu de journal ni
// aucune erreur. Seule l'URL écrite dans le fichier du dépôt porte le mot de
// passe REST.
func TestCommand_SeededSecretsAppearNowhere(t *testing.T) {
	repositoryURL, err := DirectRepositoryURL("192.0.2.10", 8443, "vps", seededRESTPassword)
	if err != nil || repositoryURL != "rest:https://vps:"+seededRESTPassword+"@192.0.2.10:8443/vps/" {
		t.Fatalf("url %q %v", repositoryURL, err)
	}
	_, refused := DirectRepositoryURL("nas/evil", 8443, "vps", seededRESTPassword)

	command, _ := BackupDirectory("/usr/bin/restic", directRepository(), "/srv/services/wiki")
	_, commandErr := BackupDirectory("/usr/bin/restic", Repository{Path: agentwire.ResticPathDirect}, "/srv")

	outputs := append([]string{command.String(), refused.Error(), commandErr.Error()}, command.Args...)
	outputs = append(outputs, command.Env...)
	for _, output := range outputs {
		for _, secret := range []string{seededRESTPassword, seededRepoPassword} {
			if strings.Contains(output, secret) {
				t.Errorf("%q leaks a secret", output)
			}
		}
	}
	if strings.Contains(command.String(), "/var/lib/agent/restic/nas.password") {
		t.Errorf("the log line names env values: %s", command.String())
	}
}

func TestParseBackupSummary_ReadsOnlyTheSummaryLine(t *testing.T) {
	snapshot := strings.Repeat("c", 64)
	line := `{"message_type":"summary","files_new":3,"data_added":1048576,"total_duration":7.3,"snapshot_id":"` + snapshot + `"}`

	summary, isSummary := ParseBackupSummary([]byte(line))

	if !isSummary || summary.SnapshotID != snapshot || summary.DataAdded != 1048576 || summary.DurationSeconds != 7.3 {
		t.Fatalf("summary %+v %v", summary, isSummary)
	}
	for _, other := range []string{`{"message_type":"status","percent_done":0.5}`, `not json`, ``} {
		if _, isSummary := ParseBackupSummary([]byte(other)); isSummary {
			t.Errorf("%q read as a summary", other)
		}
	}
}

// Le dépôt se crée avec le même environnement que la sauvegarde, sans mot
// de passe dans un argument.
func TestInitRepository_UsesTheSameFilesAsTheBackup(t *testing.T) {
	repository := Repository{Path: agentwire.ResticPathDirect, RepositoryFile: "/state/repository", CACertFile: "/state/ca.crt", PasswordFile: "/state/repo-password"}
	command, err := InitRepository("/usr/bin/restic", repository)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(command.Args, " ") != "init --json --quiet" {
		t.Fatalf("args %v", command.Args)
	}
	if !slices.Contains(command.Env, "RESTIC_REPOSITORY_FILE=/state/repository") || !slices.Contains(command.Env, "RESTIC_PASSWORD_FILE=/state/repo-password") {
		t.Fatalf("env %v", command.Env)
	}
	if _, err := InitRepository("restic", repository); err == nil {
		t.Fatal("a relative binary is accepted")
	}
}

// La rétention se lance sur le dossier local du dépôt, regroupée par hôte et
// chemins, avec les seuls comptes demandés ; le mot de passe est un fichier.
func TestForgetRepository_PassesOnlyTheRequestedCountsAndAFile(t *testing.T) {
	retention := agentwire.ResticRetainParams{User: "vps", KeepDaily: 7, KeepMonthly: 12}
	command, err := ForgetRepository("/usr/bin/restic", "/srv/restic/vps", "/state/restic/retain-vps", retention)
	if err != nil {
		t.Fatal(err)
	}
	want := "-r /srv/restic/vps --no-cache forget --prune --group-by host,paths --keep-daily 7 --keep-monthly 12"
	if strings.Join(command.Args, " ") != want {
		t.Fatalf("args %q", command.Args)
	}
	if !slices.Equal(command.Env, []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C", "RESTIC_PASSWORD_FILE=/state/restic/retain-vps"}) {
		t.Fatalf("env %v", command.Env)
	}
	refused := map[string]func() (Command, error){
		"relative binary": func() (Command, error) { return ForgetRepository("restic", "/srv/restic/vps", "/p", retention) },
		"dirty repository": func() (Command, error) {
			return ForgetRepository("/usr/bin/restic", "/srv/restic/../etc", "/p", retention)
		},
		"relative password file": func() (Command, error) { return ForgetRepository("/usr/bin/restic", "/srv/restic/vps", "p", retention) },
		"empty policy": func() (Command, error) {
			return ForgetRepository("/usr/bin/restic", "/srv/restic/vps", "/p", agentwire.ResticRetainParams{User: "vps"})
		},
	}
	for name, build := range refused {
		if _, err := build(); err == nil {
			t.Errorf("%s passed", name)
		}
	}
}
