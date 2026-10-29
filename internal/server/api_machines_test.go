package server

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/version"
)

// Le jeton d'enrôlement est lisible de tous les comptes de la machine dès
// qu'il est dans les arguments d'un processus : la commande ne le met qu'après
// printf, un builtin qui ne crée pas de processus, et sudo ne reçoit rien.
func TestInstallCommand_KeepsTheTokenOutOfEveryExecutedArgv(t *testing.T) {
	safe := regexp.MustCompile(`^oc_[a-z0-9_]+$`)
	for range 200 {
		token, _, err := machine.NewToken("vps-paris-1", "")
		if err != nil {
			t.Fatal(err)
		}
		// Les guillemets simples ne se cassent que sur une apostrophe ; le
		// jeton n'a que des minuscules, des chiffres et « _ ».
		if !safe.MatchString(token) {
			t.Fatalf("token %q has characters that need escaping", token)
		}
		for _, code := range []lang.Code{lang.French, lang.English} {
			command := installCommand("https://opencloud.example.org", token, code, nil)
			builtin, executed, piped := strings.Cut(command, " | ")
			if !piped || builtin != "printf '%s\\n' '"+token+"'" {
				t.Fatalf("the token must only follow printf: %q", command)
			}
			if strings.Contains(executed, token) || strings.Contains(executed, "-token") || !strings.HasPrefix(executed, "sudo -u opencloud-agent /var/lib/opencloud-agent/bin/current agent -server https://opencloud.example.org") {
				t.Fatalf("the executed command must carry no token: %q", executed)
			}
		}
	}
	if command := installCommand("https://x", "oc_abc", lang.English, nil); !strings.HasSuffix(command, " -lang en") {
		t.Fatalf("language missing: %q", command)
	}
}

// Les droits cochés passent dans la ligne, lisibles et sans guillemets ;
// observe seul ne s'écrit pas, c'est le défaut de l'agent.
func TestInstallCommand_CarriesTheCheckedRights(t *testing.T) {
	observeOnly := []machine.Capability{{Name: machine.CapabilityObserve}}
	if command := installCommand("https://x", "oc_abc", lang.French, observeOnly); strings.Contains(command, "-rights") {
		t.Fatalf("observe alone must not be written: %q", command)
	}
	checked, err := machine.NormalizeCapabilities([]machine.Capability{
		{Name: machine.CapabilityFilesWrite, Paths: []string{"/srv/workspace", "/opt/site"}},
		{Name: machine.CapabilityAgentUpdate},
	})
	if err != nil {
		t.Fatal(err)
	}
	command := installCommand("https://x", "oc_abc", lang.French, checked)
	if !strings.HasSuffix(command, " -rights observe,agent.update,files.write=/opt/site:/srv/workspace") {
		t.Fatalf("rights missing or misplaced: %q", command)
	}
	// Le droit coché par défaut dans le formulaire s'écrit dans la ligne : seul
	// observe seul s'omet.
	local, err := machine.ParseCapabilities("probe.lan")
	if err != nil {
		t.Fatal(err)
	}
	if command := installCommand("https://x", "oc_abc", lang.French, local); !strings.HasSuffix(command, " -rights observe,probe.lan") {
		t.Fatalf("probe.lan missing: %q", command)
	}
}

// accountLines : les lignes qui précèdent toujours les droits, cinq pour le
// compte, son état et son binaire, deux pour le lanceur stable.
const accountLines = 7

// La préparation : une ligne par dossier où un droit coché écrit, aucune pour
// observe seul ; le binaire vient de /tmp pour une machine ordinaire et du
// serveur pour la machine openCloud.
func TestInstallPreparation_PreparesTheAccountAndOneFolderPerCheckedRight(t *testing.T) {
	observeOnly := []machine.Capability{{Name: machine.CapabilityObserve}}
	plain := installPreparation(observeOnly, "v0.0.1", false)
	want := strings.Join([]string{
		"useradd --system --home-dir /var/lib/opencloud-agent --shell /usr/sbin/nologin opencloud-agent",
		"getent group docker >/dev/null && usermod -aG docker opencloud-agent",
		"install -d -m 0700 -o opencloud-agent -g opencloud-agent /var/lib/opencloud-agent",
		"runuser -u opencloud-agent -- install -d -m 0700 /var/lib/opencloud-agent/bin",
		"runuser -u opencloud-agent -- install -m 0755 /tmp/opencloud /var/lib/opencloud-agent/bin/opencloud-v0.0.1 && runuser -u opencloud-agent -- ln -sfn opencloud-v0.0.1 /var/lib/opencloud-agent/bin/current",
		// Le lanceur stable, que l'unité lance : une copie à root, hors de bin/.
		"install -d -m 0755 -o root -g root /usr/local/lib/opencloud-agent",
		"install -m 0755 -o root -g root /tmp/opencloud /usr/local/lib/opencloud-agent/opencloud-guard",
	}, "\n")
	if plain != want {
		t.Fatalf("observe alone prepares the account and the guard only:\n%s", plain)
	}

	rights, err := machine.ParseCapabilities("observe,docker.deploy,docker.update,backup.receive=/srv/backups,files.write=/opt/site:/srv/workspace,agent.update,probe.lan")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(installPreparation(rights, "v0.0.1", false), "\n")
	folders := lines[accountLines:]
	wantFolders := []string{
		"test ! -L /srv/workspace && install -d -m 0755 -o opencloud-agent -g opencloud-agent /srv/workspace # docker.deploy, docker.update, files.write",
		"test ! -L /srv/backups && install -d -m 0700 -o opencloud-agent -g opencloud-agent /srv/backups # backup.receive",
		"test ! -L /opt/site && install -d -m 0755 -o opencloud-agent -g opencloud-agent /opt/site # files.write",
	}
	// Un dossier demandé par deux droits n'est préparé qu'une fois.
	if len(folders) != len(wantFolders) {
		t.Fatalf("folders:\n%s", strings.Join(folders, "\n"))
	}
	for _, line := range wantFolders {
		if !slices.Contains(folders, line) {
			t.Errorf("missing %q in\n%s", line, strings.Join(folders, "\n"))
		}
	}

	if local := installPreparation(observeOnly, "v0.0.1", true); !strings.Contains(local, " /usr/local/bin/opencloud /var/lib/opencloud-agent/bin/opencloud-v0.0.1") || strings.Contains(local, "/tmp/") {
		t.Fatalf("the openCloud machine installs the binary of the server:\n%s", local)
	}
}

// La préparation se rejoue sur une machine dont le compte possède déjà son
// état (Changer les droits). install -d suit un lien et donne sa cible : si
// root l'appliquait à bin/, le compte, sans Docker, n'aurait qu'à remplacer
// bin/ par un lien vers /etc/cron.d pour se le faire donner. Root ne touche donc
// aucun nom que le compte contrôle : bin/, le binaire, le lien et un dossier de
// droit sous un dossier du compte se font sous le compte ; un dossier que root
// donne est d'abord vérifié n'être pas un lien.
func TestInstallPreparation_RootNeverActsOnWhatTheAccountControls(t *testing.T) {
	rights, err := machine.ParseCapabilities("docker.deploy,backup.receive=/srv/backups/nightly,files.write=/srv/workspace/blog/static:/opt/site:/var/lib/opencloud-agent/extra")
	if err != nil {
		t.Fatal(err)
	}
	accountFolders := []string{"/var/lib/opencloud-agent", "/srv/workspace", "/srv/backups/nightly", "/srv/workspace/blog/static", "/opt/site", "/var/lib/opencloud-agent/extra"}
	seenNested := false
	for number, line := range strings.Split(installPreparation(rights, "v0.0.1", false), "\n") {
		code, _, _ := strings.Cut(line, " # ")
		for _, command := range strings.Split(code, " && ") {
			if strings.HasPrefix(command, "runuser -u opencloud-agent -- ") || !strings.Contains(command, "/var/lib/opencloud-agent") && !strings.HasPrefix(command, "install ") {
				continue
			}
			target := command[strings.LastIndex(command, " ")+1:]
			// root ne crée ni ne donne que l'état lui-même (sous /var/lib, à
			// root) et des dossiers qui ne sont sous aucun dossier du compte.
			if target != "/var/lib/opencloud-agent" && insideAny(target, accountFolders) {
				t.Errorf("line %d: root acts on %s, under a folder of the account: %q", number, target, line)
			}
			if strings.Contains(command, "-o opencloud-agent") && target != "/var/lib/opencloud-agent" && !strings.HasPrefix(code, "test ! -L "+target+" && ") {
				t.Errorf("line %d: root gives %s without checking it is not a link: %q", number, target, line)
			}
		}
		if strings.HasPrefix(line, "runuser -u opencloud-agent -- install -d -m 0755 /srv/workspace/blog/static") {
			seenNested = true
		}
	}
	if !seenNested {
		t.Fatal("a right's folder under the services root is made by the account")
	}
}

// Les lignes se collent dans un shell root : le shell doit y lire exactement
// les mots écrits, rien de plus. On les fait lire par un vrai sh, chaque
// commande remplacée par un témoin qui note ses arguments.
func TestInstallPreparation_TheShellReadsExactlyTheWrittenWords(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	rights, err := machine.ParseCapabilities("docker.deploy,backup.receive=/srv/back_ups-2.0,files.write=/a/b.c:/d_e/f-g:/srv/workspace/x")
	if err != nil {
		t.Fatal(err)
	}
	for _, local := range []bool{false, true} {
		preparation := installPreparation(rights, "v0.0.1", local)
		stubs := t.TempDir()
		record := filepath.Join(stubs, "record")
		witness := "#!" + shell + "\nfor word in \"${0##*/}\" \"$@\"; do printf '%s\\n' \"$word\" >> " + record + "; done\nprintf -- '---\\n' >> " + record + "\n"
		if err := os.WriteFile(filepath.Join(stubs, "witness"), []byte(witness), 0o700); err != nil { // #nosec G306 -- un témoin exécutable dans un dossier de test.
			t.Fatal(err)
		}
		for _, name := range []string{"useradd", "getent", "usermod", "install", "runuser", "ln"} {
			if err := os.Symlink("witness", filepath.Join(stubs, name)); err != nil {
				t.Fatal(err)
			}
		}
		run := exec.Command(shell, "-c", preparation) // #nosec G204 -- le texte que le test juge, lu par un sh dont chaque commande est un témoin.
		run.Env = []string{"PATH=" + stubs}
		if output, err := run.CombinedOutput(); err != nil {
			t.Fatalf("sh: %v %s", err, output)
		}
		recorded, err := os.ReadFile(record) // #nosec G304 G703 -- le fichier du témoin, dans le dossier du test.
		if err != nil {
			t.Fatal(err)
		}
		var want []string
		for line := range strings.SplitSeq(preparation, "\n") {
			code, _, _ := strings.Cut(line, " # ")
			for _, command := range strings.Split(code, " && ") {
				if strings.HasPrefix(command, "test ") {
					continue
				}
				want = append(want, strings.Fields(strings.ReplaceAll(command, ">/dev/null", ""))...)
				want = append(want, "---")
			}
		}
		if got := strings.Fields(string(recorded)); !slices.Equal(got, want) {
			t.Fatalf("the shell read other words than written:\n got %q\nwant %q", got, want)
		}
	}
}

// backup.produce ajoute six lignes, une par geste, après celles du compte et
// avant les dossiers ; sans lui, rien de tout cela n'est écrit. La règle sudo
// est un seul geste : écrite à côté, jugée par visudo, puis renommée.
func TestInstallPreparation_ProduceAddsOneLinePerGestureAfterTheAccount(t *testing.T) {
	rights, err := machine.ParseCapabilities("backup.receive=/srv/backups,backup.produce=proxmox-vzdump")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(installPreparation(rights, "v0.0.1", false), "\n")
	want := []string{
		"runuser -u opencloud-agent -- install -d -m 0700 /var/lib/opencloud-agent/produce",
		`printf '%s\n' '#!/bin/sh' '# openCloud : vzdump hors du bac à sable de l agent, sur ordre de l agent.' 'exec /usr/bin/systemd-run --pipe --wait --collect --quiet /usr/bin/vzdump "$@"' | install -m 0755 -o root -g root /dev/stdin /usr/local/sbin/opencloud-vzdump`,
		`printf '%s\n' 'opencloud-agent ALL=(root) NOPASSWD: /usr/local/sbin/opencloud-vzdump' | install -m 0440 -o root -g root /dev/stdin /etc/sudoers.d/.opencloud-agent.tmp && visudo -cf /etc/sudoers.d/.opencloud-agent.tmp && mv /etc/sudoers.d/.opencloud-agent.tmp /etc/sudoers.d/opencloud-agent || { rm -f /etc/sudoers.d/.opencloud-agent.tmp; false; }`,
		"install -d -m 0755 /etc/systemd/system/opencloud-agent.service.d",
		`printf '%s\n' '[Service]' 'NoNewPrivileges=false' | install -m 0644 /dev/stdin /etc/systemd/system/opencloud-agent.service.d/produce.conf`,
		"systemctl daemon-reload",
	}
	if len(lines) != accountLines+len(want)+1 || !slices.Equal(lines[accountLines:accountLines+len(want)], want) {
		t.Fatalf("the produce lines are not the six gestures right after the account:\n%s", strings.Join(lines, "\n"))
	}
	if folder := lines[len(lines)-1]; !strings.HasPrefix(folder, "test ! -L /srv/backups && ") {
		t.Errorf("the folders of the other rights come after: %q", folder)
	}

	others, err := machine.ParseCapabilities("docker.deploy,backup.receive=/srv/backups,files.write=/opt/site,agent.update,probe.lan")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"sudo", "visudo", "systemctl", "vzdump", "/etc/", "/usr/local/sbin", "produce", "mv "} {
		if preparation := installPreparation(others, "v0.0.1", false); strings.Contains(preparation, forbidden) {
			t.Errorf("without backup.produce the preparation mentions %q:\n%s", forbidden, preparation)
		}
	}
}

// Root ne pose, avec backup.produce, que ce qui est à lui : un script, une
// règle, un drop-in, sous /usr/local/sbin et /etc. Le seul dossier du compte
// (produce/) se fait sous le compte.
func TestInstallPreparation_ProduceRootOnlyWritesWhatIsRoots(t *testing.T) {
	rights, err := machine.ParseCapabilities("backup.produce=proxmox-vzdump")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(installPreparation(rights, "v0.0.1", false), "\n")[accountLines:] {
		if strings.Contains(line, "/var/lib/opencloud-agent") && !strings.HasPrefix(line, "runuser -u opencloud-agent -- ") {
			t.Errorf("root acts under the state of the account: %q", line)
		}
		if strings.Contains(line, "-o opencloud-agent") {
			t.Errorf("root gives something to the account: %q", line)
		}
		if strings.Contains(line, "install") && !strings.HasPrefix(line, "runuser") && !strings.Contains(line, "-d -m 0755 /etc/") && !strings.Contains(line, "-o root -g root") && !strings.Contains(line, "-m 0644 /dev/stdin /etc/systemd/") {
			t.Errorf("an install that is neither the account's nor root's own: %q", line)
		}
	}
}

// Les six lignes se collent dans un shell root : le shell doit y lire
// exactement les mots écrits, et ce que les tubes livrent à install est
// exactement le texte des constantes. Chaque commande de la machine est un
// témoin qui note ses arguments ; install note aussi ce qu'il reçoit sur son
// entrée. Rien ne lance sudo, visudo, mv, systemctl ni vzdump.
func TestInstallPreparation_ProduceTheShellWritesExactlyTheTexts(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	cat, err := exec.LookPath("cat")
	if err != nil {
		t.Skip("no cat")
	}
	rights, err := machine.ParseCapabilities("backup.produce=proxmox-vzdump")
	if err != nil {
		t.Fatal(err)
	}
	stubs := t.TempDir()
	received := t.TempDir()
	record := filepath.Join(stubs, "record")
	witness := "#!" + shell + "\nfor word in \"${0##*/}\" \"$@\"; do printf '%s\\n' \"$word\" >> " + record + "; done\nprintf -- '---\\n' >> " + record +
		"\nfor last; do :; done\ncase \" $* \" in *\" /dev/stdin \"*) " + cat + " > " + received + "/\"${last##*/}\" ;; esac\n"
	if err := os.WriteFile(filepath.Join(stubs, "witness"), []byte(witness), 0o700); err != nil { // #nosec G306 -- un témoin exécutable dans un dossier de test.
		t.Fatal(err)
	}
	for _, name := range []string{"useradd", "getent", "usermod", "install", "runuser", "ln", "visudo", "mv", "rm", "systemctl"} {
		if err := os.Symlink("witness", filepath.Join(stubs, name)); err != nil {
			t.Fatal(err)
		}
	}
	run := exec.Command(shell, "-c", installPreparation(rights, "v0.0.1", false)) // #nosec G204 -- le texte que le test juge, lu par un sh dont chaque commande est un témoin.
	run.Env = []string{"PATH=" + stubs}
	if output, err := run.CombinedOutput(); err != nil {
		t.Fatalf("sh: %v %s", err, output)
	}
	recorded, err := os.ReadFile(record) // #nosec G304 G703 -- le fichier du témoin, dans le dossier du test.
	if err != nil {
		t.Fatal(err)
	}
	var commands [][]string
	for block := range strings.SplitSeq(string(recorded), "---\n") {
		if words := strings.Fields(block); len(words) > 0 {
			commands = append(commands, words)
		}
	}
	// Les neuf commandes des lignes du compte et du lanceur stable (trois de ces
	// lignes en enchaînent deux), puis les six gestes, dont la règle sudo en
	// trois commandes enchaînées (écrire, juger, poser).
	const accountCommands = 9
	wantProduce := [][]string{
		{"runuser", "-u", "opencloud-agent", "--", "install", "-d", "-m", "0700", "/var/lib/opencloud-agent/produce"},
		{"install", "-m", "0755", "-o", "root", "-g", "root", "/dev/stdin", "/usr/local/sbin/opencloud-vzdump"},
		{"install", "-m", "0440", "-o", "root", "-g", "root", "/dev/stdin", "/etc/sudoers.d/.opencloud-agent.tmp"},
		{"visudo", "-cf", "/etc/sudoers.d/.opencloud-agent.tmp"},
		{"mv", "/etc/sudoers.d/.opencloud-agent.tmp", "/etc/sudoers.d/opencloud-agent"},
		{"install", "-d", "-m", "0755", "/etc/systemd/system/opencloud-agent.service.d"},
		{"install", "-m", "0644", "/dev/stdin", "/etc/systemd/system/opencloud-agent.service.d/produce.conf"},
		{"systemctl", "daemon-reload"},
	}
	if len(commands) != accountCommands+len(wantProduce) {
		t.Fatalf("the shell ran %d commands: %q", len(commands), commands)
	}
	for index, want := range wantProduce {
		if !slices.Equal(commands[accountCommands+index], want) {
			t.Errorf("command %d read as %q, want %q", index, commands[accountCommands+index], want)
		}
	}
	for file, lines := range map[string][]string{
		"opencloud-vzdump": machine.VzdumpWrapperLines(), ".opencloud-agent.tmp": machine.SudoersRuleLines(), "produce.conf": machine.ProduceDropInLines(),
	} {
		got, err := os.ReadFile(filepath.Join(received, file)) // #nosec G304 G703 -- ce que le témoin a reçu, dans le dossier du test.
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if want := strings.Join(lines, "\n") + "\n"; string(got) != want {
			t.Errorf("%s received %q, want %q", file, got, want)
		}
	}
}

// La règle sudo ne se pose que si elle est écrite et que visudo l'accepte : si
// l'une des trois commandes échoue, le fichier provisoire est retiré, la règle
// n'est pas posée (ou, si c'est mv qui échoue, retirée avec lui), et la ligne
// rend une erreur. Chaque commande est un témoin ; celle qui doit échouer sort 1.
func TestInstallPreparation_ProduceTheSudoersRuleIsNeverPlacedWithoutVisudo(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	rights, err := machine.ParseCapabilities("backup.produce=proxmox-vzdump")
	if err != nil {
		t.Fatal(err)
	}
	var line string
	for _, candidate := range strings.Split(installPreparation(rights, "v0.0.1", false), "\n") {
		if strings.Contains(candidate, machine.SudoersFile) {
			line = candidate
		}
	}
	if line == "" {
		t.Fatal("no sudoers line in the preparation")
	}
	temp, final := machine.SudoersTempFile, machine.SudoersFile
	cases := []struct {
		failing string
		want    []string
	}{
		{"", []string{"install", "visudo", "mv"}},
		{"install", []string{"install", "rm"}},
		{"visudo", []string{"install", "visudo", "rm"}},
		{"mv", []string{"install", "visudo", "mv", "rm"}},
	}
	for _, c := range cases {
		t.Run("failing "+c.failing, func(t *testing.T) {
			stubs := t.TempDir()
			record := filepath.Join(stubs, "record")
			for _, name := range []string{"install", "visudo", "mv", "rm"} {
				exit := "0"
				if name == c.failing {
					exit = "1"
				}
				witness := "#!" + shell + "\nprintf '%s %s\\n' \"${0##*/}\" \"$*\" >> " + record + "\ncat > /dev/null\nexit " + exit + "\n"
				if err := os.WriteFile(filepath.Join(stubs, name), []byte(witness), 0o700); err != nil { // #nosec G306 -- un témoin exécutable dans un dossier de test.
					t.Fatal(err)
				}
			}
			run := exec.Command(shell, "-c", line) // #nosec G204 -- le texte que le test juge, lu par un sh dont chaque commande est un témoin.
			run.Env = []string{"PATH=" + stubs + ":/usr/bin:/bin"}
			output, runErr := run.CombinedOutput()
			recorded, err := os.ReadFile(record) // #nosec G304 G703 -- le fichier du témoin, dans le dossier du test.
			if err != nil {
				t.Fatalf("%v %s", err, output)
			}
			var got []string
			for entry := range strings.SplitSeq(strings.TrimSpace(string(recorded)), "\n") {
				name, args, _ := strings.Cut(entry, " ")
				got = append(got, name)
				if name == "rm" && args != "-f "+temp {
					t.Errorf("rm touched %q, not the provisional file", args)
				}
				if name == "mv" && args != temp+" "+final {
					t.Errorf("mv moved %q", args)
				}
				if name == "visudo" && args != "-cf "+temp {
					t.Errorf("visudo judged %q, not the provisional file", args)
				}
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("ran %v, want %v", got, c.want)
			}
			if failed := runErr != nil; failed != (c.failing != "") {
				t.Errorf("the line failed = %t, want %t (%v)", failed, c.failing != "", runErr)
			}
		})
	}
}

// Un chemin hostile ne passe pas la normalisation des droits ; et même donné
// tel quel, sans elle, il n'entre pas dans des lignes collées en root. Le jeton
// n'y paraît jamais.
func TestInstallPreparation_HostilePathsNeverReachTheLines(t *testing.T) {
	hostile := []string{"/srv/a;id", "/srv/$(id)", "/srv/a b", "/srv/`id`", "/srv/a\nid", "/srv/a'b", "/srv/a\"b", "/srv/a|id", "/srv/a&id", "/srv/a>b", "/srv/*", "/srv/../etc", "/", "srv/x", "/srv/a\\b", "/srv/~"}
	for _, path := range hostile {
		for _, name := range []string{machine.CapabilityBackupReceive, machine.CapabilityFilesWrite} {
			raw := []machine.Capability{{Name: name, Paths: []string{path}}}
			if _, err := machine.NormalizeCapabilities(raw); err == nil {
				t.Errorf("%s=%q passes the normalization", name, path)
			}
			for _, local := range []bool{false, true} {
				// Sept lignes : le compte, son état et le lanceur stable, aucun dossier de droit.
				if preparation := installPreparation(raw, "v0.0.1", local); strings.Count(preparation, "\n") != accountLines-1 || path != "/" && strings.Contains(preparation, path) {
					t.Errorf("%s=%q reaches the preparation:\n%s", name, path, preparation)
				}
			}
		}
	}
	// Un producteur hostile non plus : refusé à la normalisation, et donné tel
	// quel, il n'ajoute aucune ligne ; à côté du vrai producteur, il ajoute les
	// six gestes, dont aucun ne le nomme.
	for _, producer := range []string{"proxmox-vzdump;id", "$(id)", "`id`", "proxmox-vzdump\nid", "proxmox-vzdump'", "proxmox-vzdump id", "/usr/bin/sh", "../etc"} {
		raw := []machine.Capability{{Name: machine.CapabilityBackupProduce, Paths: []string{producer}}}
		if _, err := machine.NormalizeCapabilities(raw); err == nil {
			t.Errorf("backup.produce=%q passes the normalization", producer)
		}
		if preparation := installPreparation(raw, "v0.0.1", false); strings.Count(preparation, "\n") != accountLines-1 || strings.Contains(preparation, producer) {
			t.Errorf("backup.produce=%q reaches the preparation:\n%s", producer, preparation)
		}
		mixed := []machine.Capability{{Name: machine.CapabilityBackupProduce, Paths: []string{machine.ProducerProxmoxVzdump, producer}}}
		if preparation := installPreparation(mixed, "v0.0.1", false); strings.Count(preparation, "\n") != accountLines-1+6 || strings.Contains(preparation, producer) {
			t.Errorf("backup.produce=proxmox-vzdump:%q reaches the preparation:\n%s", producer, preparation)
		}
	}
	token, _, err := machine.NewToken("vps", "")
	if err != nil {
		t.Fatal(err)
	}
	rights, err := machine.ParseCapabilities("docker.deploy,backup.receive=/srv/backups,files.write=/opt/site")
	if err != nil {
		t.Fatal(err)
	}
	if preparation := installPreparation(rights, "v0.0.1", false); strings.Contains(preparation, token) || strings.Contains(preparation, "oc_") {
		t.Fatalf("the token must never be in the preparation:\n%s", preparation)
	}
}

func TestAPI_CreateMachineToken_ShowsTheTokenOnceAndListsItAsPending(t *testing.T) {
	server := newTestServer(t)
	recorder := callAPI(server.Server, http.MethodPost, "/api/machines/tokens", tokenRequest{Name: "vps-paris-1"})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var response tokenResponse
	decodeAPI(t, recorder, &response)
	if !tokenValue.MatchString(response.Token) || response.Command != "printf '%s\\n' '"+response.Token+"' | sudo -u opencloud-agent /var/lib/opencloud-agent/bin/current agent -server http://example.com" || response.URLLocal {
		t.Fatalf("token response %+v", response)
	}
	if !strings.Contains(response.Preparation, " /tmp/opencloud /var/lib/opencloud-agent/bin/opencloud-"+version.Number()) || strings.Contains(response.Preparation, response.Token) {
		t.Fatalf("preparation %q", response.Preparation)
	}
	pending, err := server.machines.PendingTokens(context.Background())
	if err != nil || len(pending) != 1 || pending[0].Name != "vps-paris-1" {
		t.Fatalf("pending %+v %v", pending, err)
	}
	list := callAPI(server.Server, http.MethodGet, "/api/machines", nil)
	if !strings.Contains(list.Body.String(), pending[0].Masked()) || tokenValue.MatchString(list.Body.String()) {
		t.Fatal("the list must show the masked token and never the cleartext")
	}

	if got := callAPI(server.Server, http.MethodDelete, "/api/machines/tokens/"+pending[0].ID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("cancel: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodDelete, "/api/machines/tokens/"+pending[0].ID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("cancel again: %d", got.Code)
	}
	if pending, _ := server.machines.PendingTokens(context.Background()); len(pending) != 0 {
		t.Fatalf("still pending %+v", pending)
	}
}

func TestAPI_CreateMachineToken_RefusesABadNameWithItsKey(t *testing.T) {
	server := newTestServer(t)
	got := callAPI(server.Server, http.MethodPost, "/api/machines/tokens", tokenRequest{Name: "VPS Paris"})
	if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "machine.name_invalid" {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/tokens", map[string]any{"name": "x", "extra": 1}); got.Code != http.StatusBadRequest || errorCode(t, got) != codeBadJSON {
		t.Fatalf("unknown field: %d %s", got.Code, got.Body.String())
	}
}

func TestAPI_RemoveMachine_ForgetsItTheOpenCloudOneToo(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	if got := callAPI(server.Server, http.MethodDelete, "/api/machines/"+remoteID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("remove: %d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID, nil); got.Code != http.StatusNotFound {
		t.Fatalf("after remove: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodDelete, "/api/machines/"+remoteID, nil); got.Code != http.StatusNotFound {
		t.Fatalf("remove again: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodDelete, "/api/machines/local", nil); got.Code != http.StatusNoContent {
		t.Fatalf("remove local: %d %s", got.Code, got.Body.String())
	}
}

func TestAPI_Reenroll_IssuesATokenForTheSameMachine(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	recorder := callAPI(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/reenroll", nil)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var response tokenResponse
	decodeAPI(t, recorder, &response)
	if response.Name != "vps-paris-1" || !tokenValue.MatchString(response.Token) {
		t.Fatalf("token response %+v", response)
	}
	pending, _ := server.machines.PendingTokens(context.Background())
	if len(pending) != 1 || pending[0].MachineID != remoteID {
		t.Fatalf("pending %+v", pending)
	}
	got := callAPI(server.Server, http.MethodPost, "/api/machines/local/actions/reenroll", nil)
	if got.Code != http.StatusCreated {
		t.Fatalf("reenroll local: %d", got.Code)
	}
	// La machine openCloud prend le binaire du serveur, une autre celui de /tmp.
	var local tokenResponse
	decodeAPI(t, got, &local)
	if !strings.Contains(local.Preparation, " /usr/local/bin/opencloud ") || !strings.Contains(response.Preparation, " /tmp/opencloud ") {
		t.Fatalf("binaries: %q / %q", local.Preparation, response.Preparation)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/nope/actions/reenroll", nil); got.Code != http.StatusNotFound {
		t.Fatalf("reenroll unknown: %d", got.Code)
	}
}

// Changer les droits d'un agent, c'est le ré-enrôler avec d'autres droits
// (décision 10) : la ligne les porte, le jeton les garde ; sans droits dans
// la demande, ceux d'avant. Un droit faux se refuse avant tout jeton.
func TestAPI_Reenroll_ChangesTheRightsOfTheAgent(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	path := "/api/machines/" + remoteID + "/actions/reenroll"
	recorder := callAPI(server.Server, http.MethodPost, path, map[string]any{"capabilities": []map[string]any{{"name": "agent.update"}}})
	var response tokenResponse
	decodeAPI(t, recorder, &response)
	if recorder.Code != http.StatusCreated || !strings.HasSuffix(response.Command, " -rights observe,agent.update") {
		t.Fatalf("%d %+v", recorder.Code, response)
	}
	pending, _ := server.machines.PendingTokens(context.Background())
	if len(pending) != 1 || machine.FormatCapabilities(pending[0].Capabilities) != "observe,agent.update" {
		t.Fatalf("pending %+v", pending)
	}
	kept := callAPI(server.Server, http.MethodPost, path, map[string]any{})
	decodeAPI(t, kept, &response)
	if kept.Code != http.StatusCreated || strings.Contains(response.Command, "-rights") {
		t.Fatalf("without rights: %d %+v", kept.Code, response)
	}
	refused := callAPI(server.Server, http.MethodPost, path, map[string]any{"capabilities": []map[string]any{{"name": "shell.exec"}}})
	if refused.Code != http.StatusUnprocessableEntity || errorCode(t, refused) != "machine.capability_unknown" {
		t.Fatalf("unknown right: %d %s", refused.Code, refused.Body.String())
	}
}

// Les droits cochés passent du formulaire à la ligne d'installation ; un
// droit inconnu ou un chemin faux se refuse avec sa clé.
func TestAPI_CreateMachineToken_PutsTheCheckedRightsInTheCommand(t *testing.T) {
	server := newTestServer(t)
	request := tokenRequest{Name: "vps-paris-1", Capabilities: []capabilityJSON{
		{Name: machine.CapabilityBackupReceive, Paths: []string{"/srv/backups"}}, {Name: machine.CapabilityDockerUpdate},
	}}
	recorder := callAPI(server.Server, http.MethodPost, "/api/machines/tokens", request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var response tokenResponse
	decodeAPI(t, recorder, &response)
	if !strings.HasSuffix(response.Command, " -rights observe,docker.update,backup.receive=/srv/backups") {
		t.Fatalf("command %q", response.Command)
	}
	for _, refused := range []struct {
		capability capabilityJSON
		key        string
	}{
		{capabilityJSON{Name: "shell.exec"}, "machine.capability_unknown"},
		{capabilityJSON{Name: machine.CapabilityFilesWrite, Paths: []string{"relative"}}, "machine.capability_path"},
		{capabilityJSON{Name: machine.CapabilityBackupReceive}, "machine.capability_scope"},
	} {
		got := callAPI(server.Server, http.MethodPost, "/api/machines/tokens", tokenRequest{Name: "vps-lyon-2", Capabilities: []capabilityJSON{refused.capability}})
		if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != refused.key {
			t.Errorf("%+v: %d %s", refused.capability, got.Code, got.Body.String())
		}
	}
}

// Le droit passe par l'API : la ligne le porte, la préparation pose ses six
// gestes, un producteur inconnu ou une portée de chemin se refusent avec leur clé.
func TestAPI_CreateMachineToken_ProduceRightReachesTheCommandAndThePreparation(t *testing.T) {
	server := newTestServer(t)
	request := tokenRequest{Name: "pve", Capabilities: []capabilityJSON{{Name: machine.CapabilityBackupProduce, Paths: []string{machine.ProducerProxmoxVzdump}}}}
	recorder := callAPI(server.Server, http.MethodPost, "/api/machines/tokens", request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var response tokenResponse
	decodeAPI(t, recorder, &response)
	if !strings.HasSuffix(response.Command, " -rights observe,backup.produce=proxmox-vzdump") {
		t.Errorf("command %q", response.Command)
	}
	for _, line := range []string{"visudo -cf /etc/sudoers.d/.opencloud-agent.tmp", "mv /etc/sudoers.d/.opencloud-agent.tmp /etc/sudoers.d/opencloud-agent", "systemctl daemon-reload", "install -d -m 0700 /var/lib/opencloud-agent/produce"} {
		if !strings.Contains(response.Preparation, line) {
			t.Errorf("the preparation lacks %q:\n%s", line, response.Preparation)
		}
	}
	for _, refused := range []struct {
		capability capabilityJSON
		key        string
	}{
		{capabilityJSON{Name: machine.CapabilityBackupProduce, Paths: []string{"borg"}}, "machine.capability_producer"},
		{capabilityJSON{Name: machine.CapabilityBackupProduce, Paths: []string{"/srv/backups"}}, "machine.capability_producer"},
		{capabilityJSON{Name: machine.CapabilityBackupProduce}, "machine.capability_scope"},
	} {
		got := callAPI(server.Server, http.MethodPost, "/api/machines/tokens", tokenRequest{Name: "vps-lyon-2", Capabilities: []capabilityJSON{refused.capability}})
		if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != refused.key {
			t.Errorf("%+v: %d %s", refused.capability, got.Code, got.Body.String())
		}
	}
}

// Révoquer garde la machine : 204, la fiche dit l'agent révoqué ; une
// seconde fois, 409 machine.no_agent ; la machine openCloud ne se révoque
// pas.
func TestAPI_RevokeAgent_KeepsTheMachineAndSaysTheAgentIsRevoked(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/revoke", nil); got.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", got.Code, got.Body.String())
	}
	var revoked machineJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID, nil), &revoked)
	if revoked.Agent == nil || revoked.Agent.RevokedAt == nil {
		t.Fatalf("machine after revoke %+v", revoked)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/revoke", nil); got.Code != http.StatusConflict || errorCode(t, got) != "machine.no_agent" {
		t.Fatalf("revoke twice: %d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/nobody/actions/revoke", nil); got.Code != http.StatusNotFound {
		t.Fatalf("unknown machine: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/local/actions/revoke", nil); got.Code != http.StatusNoContent {
		t.Fatalf("revoke local: %d %s", got.Code, got.Body.String())
	}
}

// La ligne de l'agent de la machine openCloud s'offre tant qu'elle n'a
// jamais eu d'agent : la même tant qu'elle attend, avec observe et probe.lan ; 409 une
// fois qu'un agent l'a eue.
func TestAPI_OfferLocalEnrollment_UntilTheOpenCloudMachineHasAnAgent(t *testing.T) {
	server := newTestServer(t)
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/local-enrollment", nil); got.Code != http.StatusConflict || errorCode(t, got) != "machine.local_enrolled" {
		t.Fatalf("with its agent: %d %s", got.Code, got.Body.String())
	}
	// Retirée, la machine openCloud n'a plus d'agent : la ligne s'offre de
	// nouveau, au nom du nom d'hôte.
	if got := callAPI(server.Server, http.MethodDelete, "/api/machines/"+machine.LocalID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("remove: %d %s", got.Code, got.Body.String())
	}
	var first, second tokenResponse
	decodeAPI(t, callAPI(server.Server, http.MethodPost, "/api/machines/local-enrollment", nil), &first)
	decodeAPI(t, callAPI(server.Server, http.MethodPost, "/api/machines/local-enrollment", nil), &second)
	if first.Name != "opencloud-host" || !tokenValue.MatchString(first.Token) || !strings.HasSuffix(first.Command, " -rights observe,probe.lan") || first.Command != second.Command {
		t.Fatalf("offers %+v %+v", first, second)
	}
	if !strings.Contains(first.Preparation, " /usr/local/bin/opencloud ") || first.Preparation != second.Preparation {
		t.Fatalf("the openCloud machine installs the binary of the server: %q", first.Preparation)
	}
}
