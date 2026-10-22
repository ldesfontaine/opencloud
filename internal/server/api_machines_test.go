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
	}, "\n")
	if plain != want {
		t.Fatalf("observe alone prepares the account only:\n%s", plain)
	}

	rights, err := machine.ParseCapabilities("observe,docker.deploy,docker.update,backup.receive=/srv/backups,files.write=/opt/site:/srv/workspace,agent.update,probe.lan")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(installPreparation(rights, "v0.0.1", false), "\n")
	folders := lines[5:]
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
				// Cinq lignes : le compte et son état, aucun dossier de droit.
				if preparation := installPreparation(raw, "v0.0.1", local); strings.Count(preparation, "\n") != 4 || path != "/" && strings.Contains(preparation, path) {
					t.Errorf("%s=%q reaches the preparation:\n%s", name, path, preparation)
				}
			}
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
