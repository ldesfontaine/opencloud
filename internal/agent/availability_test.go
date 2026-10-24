package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// fakeDisk juge un chemin sans toucher au disque : ce que stat en dit, ce
// qu'access répond pour un dossier qui existe.
type fakeDisk struct {
	entries map[string]fs.FileInfo
	errors  map[string]error
	denied  map[string]error
}

type fakeInfo struct {
	fs.FileInfo
	dir bool
}

func (f fakeInfo) IsDir() bool { return f.dir }

func (d fakeDisk) stat(path string) (fs.FileInfo, error) {
	if err, found := d.errors[path]; found {
		return nil, err
	}
	if info, found := d.entries[path]; found {
		return info, nil
	}
	return nil, &fs.PathError{Op: "stat", Path: path, Err: syscall.ENOENT}
}

func (d fakeDisk) access(path string) error {
	return d.denied[path]
}

func dirs(paths ...string) map[string]fs.FileInfo {
	entries := map[string]fs.FileInfo{}
	for _, path := range paths {
		entries[path] = fakeInfo{dir: true}
	}
	return entries
}

// Le jugement d'un dossier : la raison dit ce qui manque, et laisse faire ce
// qui peut l'être. Chaque cas est un refus que la machine donne vraiment.
func TestJudgeWritable_NamesWhatTheAccountLacks(t *testing.T) {
	cases := map[string]struct {
		disk fakeDisk
		path string
		want string
	}{
		"a folder the account writes": {
			disk: fakeDisk{entries: dirs("/srv/backups")}, path: "/srv/backups", want: "",
		},
		"a folder owned by another account": {
			disk: fakeDisk{entries: dirs("/srv/backups"), denied: map[string]error{"/srv/backups": syscall.EACCES}},
			path: "/srv/backups", want: agentwire.ReasonPathDenied,
		},
		"a folder refused by EPERM": {
			disk: fakeDisk{entries: dirs("/srv/backups"), denied: map[string]error{"/srv/backups": syscall.EPERM}},
			path: "/srv/backups", want: agentwire.ReasonPathDenied,
		},
		"a read-only mount is not the account's fault": {
			disk: fakeDisk{entries: dirs("/srv/backups"), denied: map[string]error{"/srv/backups": syscall.EROFS}},
			path: "/srv/backups", want: agentwire.ReasonPathUnwritable,
		},
		"a folder to create under a parent the account writes": {
			disk: fakeDisk{entries: dirs("/srv")}, path: "/srv/workspace", want: "",
		},
		"a folder to create under a parent it does not own": {
			disk: fakeDisk{entries: dirs("/srv"), denied: map[string]error{"/srv": syscall.EACCES}},
			path: "/srv/workspace", want: agentwire.ReasonPathDenied,
		},
		"the nearest parent is the one judged, deep below": {
			disk: fakeDisk{entries: dirs("/srv", "/srv/a"), denied: map[string]error{"/srv/a": syscall.EACCES}},
			path: "/srv/a/b/c/d", want: agentwire.ReasonPathDenied,
		},
		"the nearest parent on a read-only mount": {
			disk: fakeDisk{entries: dirs("/srv"), denied: map[string]error{"/srv": syscall.EROFS}},
			path: "/srv/a/b", want: agentwire.ReasonPathUnwritable,
		},
		"a parent that cannot even be crossed": {
			disk: fakeDisk{errors: map[string]error{"/srv/private/backups": &fs.PathError{Op: "stat", Err: syscall.EACCES}}},
			path: "/srv/private/backups", want: agentwire.ReasonPathDenied,
		},
		"a symbolic loop is not a permission": {
			disk: fakeDisk{errors: map[string]error{"/srv/loop": &fs.PathError{Op: "stat", Err: syscall.ELOOP}}},
			path: "/srv/loop", want: agentwire.ReasonPathUnwritable,
		},
		"a file where a folder is needed": {
			disk: fakeDisk{entries: map[string]fs.FileInfo{"/srv/backups": fakeInfo{dir: false}}},
			path: "/srv/backups", want: agentwire.ReasonPathUnwritable,
		},
		"nothing exists up to the root": {
			disk: fakeDisk{}, path: "/srv/backups", want: agentwire.ReasonPathUnwritable,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := judgeWritable(c.path, c.disk.stat, c.disk.access); got != c.want {
				t.Errorf("judged %q, want %q", got, c.want)
			}
		})
	}
}

// Sur le vrai disque : ce que le compte du test peut écrire, et, hors root,
// ce qu'il ne peut pas.
func TestWritableOrCreatable_OnTheRealDisk(t *testing.T) {
	dir := t.TempDir()
	if got := writableOrCreatable(dir); got != "" {
		t.Errorf("a folder of the account: %q", got)
	}
	if got := writableOrCreatable(filepath.Join(dir, "a", "b")); got != "" {
		t.Errorf("a folder to create: %q", got)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := writableOrCreatable(file); got != agentwire.ReasonPathUnwritable {
		t.Errorf("a file: %q", got)
	}
	if got := writableOrCreatable(filepath.Join(file, "below")); got != agentwire.ReasonPathUnwritable {
		t.Errorf("below a file: %q", got)
	}
	// Un lien vers un dossier du compte se suit, comme os.OpenRoot le suit.
	link := filepath.Join(dir, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if got := writableOrCreatable(link); got != "" {
		t.Errorf("a link to a folder of the account: %q", got)
	}
}

func TestWritableOrCreatable_ARealFolderTheAccountDoesNotOwn(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere: the refusal cannot be produced by the disk")
	}
	dir := t.TempDir()
	closed := filepath.Join(dir, "closed")
	if err := os.Mkdir(closed, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(closed, 0o700) })
	if got := writableOrCreatable(closed); got != agentwire.ReasonPathDenied {
		t.Errorf("a read-only folder: %q", got)
	}
	if got := writableOrCreatable(filepath.Join(closed, "new", "deep")); got != agentwire.ReasonPathDenied {
		t.Errorf("a folder to create inside it: %q", got)
	}
	shut := filepath.Join(dir, "shut")
	if err := os.Mkdir(shut, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(shut, 0o700) })
	if got := writableOrCreatable(filepath.Join(shut, "below")); got != agentwire.ReasonPathDenied {
		t.Errorf("below a folder that cannot be crossed: %q", got)
	}
}

// listenOn ouvre une vraie socket unix sous un chemin court (la limite d'une
// socket est d'une centaine d'octets, plus court que t.TempDir()).
func listenOn(t *testing.T) (string, *net.UnixListener) {
	t.Helper()
	dir, err := os.MkdirTemp("", "dk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700); _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "d.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			_ = connection.Close()
		}
	}()
	return path, listener
}

func TestDockerReason_SaysWhyTheAccountCannotUseDocker(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	socket, listener := listenOn(t)

	if got := dockerReason(context.Background(), binary, socket); got != "" {
		t.Errorf("a socket that answers: %q", got)
	}
	if got := dockerReason(context.Background(), filepath.Join(t.TempDir(), "absent"), socket); got != agentwire.ReasonDockerMissing {
		t.Errorf("no program: %q", got)
	}
	if got := dockerReason(context.Background(), binary, filepath.Join(t.TempDir(), "absent.sock")); got != agentwire.ReasonDockerMissing {
		t.Errorf("no socket: %q", got)
	}
	// Docker arrêté : la socket est là, plus personne n'écoute. Ce n'est pas un
	// droit qui manque, compose up le dira.
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if got := dockerReason(context.Background(), binary, socket); got != "" {
		t.Errorf("a stopped daemon is not a missing right: %q", got)
	}
}

func TestDockerReason_ASocketClosedToTheAccount(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens any socket: see TestDockerReasonOf_TranslatesTheDialErrors")
	}
	binary := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(binary, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	socket, _ := listenOn(t)
	if err := os.Chmod(socket, 0o000); err != nil {
		t.Fatal(err)
	}
	if got := dockerReason(context.Background(), binary, socket); got != agentwire.ReasonDockerDenied {
		t.Errorf("a socket closed to the account: %q", got)
	}
}

// Les erreurs d'ouverture fabriquées, pour ne pas dépendre du compte qui lance
// les tests : root ouvre toute socket, la CI tourne sans privilège.
func TestDockerReasonOf_TranslatesTheDialErrors(t *testing.T) {
	denied := &net.OpError{Op: "dial", Net: "unix", Err: &os.SyscallError{Syscall: "connect", Err: syscall.EACCES}}
	cases := map[string]struct {
		err  error
		want string
	}{
		"opened":          {nil, ""},
		"permission":      {denied, agentwire.ReasonDockerDenied},
		"wrapped denied":  {fmt.Errorf("dial: %w", denied), agentwire.ReasonDockerDenied},
		"fs permission":   {fs.ErrPermission, agentwire.ReasonDockerDenied},
		"no such socket":  {&net.OpError{Op: "dial", Err: &os.SyscallError{Err: syscall.ENOENT}}, agentwire.ReasonDockerMissing},
		"refused":         {&net.OpError{Op: "dial", Err: &os.SyscallError{Err: syscall.ECONNREFUSED}}, ""},
		"timeout":         {context.DeadlineExceeded, ""},
		"another failure": {errors.New("boom"), ""},
		"EPERM too":       {&net.OpError{Op: "dial", Err: &os.SyscallError{Err: syscall.EPERM}}, agentwire.ReasonDockerDenied},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := dockerReasonOf(c.err); got != c.want {
				t.Errorf("judged %q, want %q", got, c.want)
			}
		})
	}
}

// reasonsOf rend ce que le rapport des droits dit, avec ces vérifications.
func reasonsOf(t *testing.T, rights string, checks hostChecks) map[string]string {
	t.Helper()
	capabilities, err := machine.ParseCapabilities(rights)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := NewIdentity("http://127.0.0.1:1", "", "fr", capabilities)
	if err != nil {
		t.Fatal(err)
	}
	identity.RootDir = "/srv/workspace"
	return judgeRights(context.Background(), identity, checks)
}

// Le rapport pose la question de chaque droit au bon endroit, et ne dit rien
// de ce que le compte peut faire.
func TestJudgeRights_AsksEachRightItsOwnQuestion(t *testing.T) {
	var asked []string
	checks := hostChecks{
		docker: func(context.Context) string { return agentwire.ReasonDockerDenied },
		writable: func(path string) string {
			asked = append(asked, path)
			if path == "/srv/b" {
				return agentwire.ReasonPathDenied
			}
			return ""
		},
		agentUpdate: func() string { return agentwire.ReasonNotSelfUpdatable },
	}
	got := reasonsOf(t, "agent.update,docker.deploy,docker.update,backup.receive=/srv/a,files.write=/srv/a:/srv/b", checks)
	want := map[string]string{
		"agent.update":  agentwire.ReasonNotSelfUpdatable,
		"docker.deploy": agentwire.ReasonDockerDenied,
		"docker.update": agentwire.ReasonDockerDenied,
		"files.write":   agentwire.ReasonPathDenied,
	}
	if !mapsEqual(got, want) {
		t.Errorf("reported %v, want %v", got, want)
	}
	if _, judged := got["observe"]; judged {
		t.Error("observe is never judged")
	}
	if !slices.Contains(asked, "/srv/a") || !slices.Contains(asked, "/srv/b") {
		t.Errorf("the granted folders were not all asked: %v", asked)
	}

	// Tout va bien : rien n'est dit.
	if got := reasonsOf(t, "agent.update,docker.deploy,backup.receive=/srv/a", allowAll()); len(got) != 0 {
		t.Errorf("an account that can do everything reported %v", got)
	}
}

// Docker joignable mais la racine des services refusée : la raison est celle du
// dossier. Docker d'abord quand les deux manquent.
func TestJudgeRights_DockerThenTheServicesRoot(t *testing.T) {
	checks := allowAll()
	checks.writable = func(path string) string {
		if path == "/srv/workspace" {
			return agentwire.ReasonPathDenied
		}
		return ""
	}
	got := reasonsOf(t, "docker.deploy", checks)
	if got["docker.deploy"] != agentwire.ReasonPathDenied {
		t.Errorf("reported %v", got)
	}
	checks.docker = func(context.Context) string { return agentwire.ReasonDockerMissing }
	if got := reasonsOf(t, "docker.deploy", checks); got["docker.deploy"] != agentwire.ReasonDockerMissing {
		t.Errorf("docker should come first: %v", got)
	}
}

// Un état mal gardé ne laisse que observe : il n'y a rien à juger, et rien n'est
// dit d'un droit que l'agent n'exerce de toute façon pas.
func TestJudgeRights_ANarrowedAgentSaysNothing(t *testing.T) {
	capabilities, err := machine.ParseCapabilities("docker.deploy,backup.receive=/srv/a")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := NewIdentity("http://127.0.0.1:1", "", "fr", capabilities)
	if err != nil {
		t.Fatal(err)
	}
	identity.narrowed = true
	denyAll := hostChecks{
		docker:      func(context.Context) string { return agentwire.ReasonDockerDenied },
		writable:    func(string) string { return agentwire.ReasonPathDenied },
		agentUpdate: func() string { return agentwire.ReasonPathDenied },
	}
	if got := judgeRights(context.Background(), identity, denyAll); len(got) != 0 {
		t.Errorf("a narrowed agent reported %v", got)
	}
}

// Les raisons qu'un agent peut dire sont toutes des raisons de refus connues du
// serveur : une raison nouvelle dite par l'agent sans l'être par le serveur
// serait ignorée en bloc à la lecture de l'en-tête.
func TestJudgeRights_EveryReasonIsOneTheServerReads(t *testing.T) {
	for _, reason := range []string{agentwire.ReasonDockerDenied, agentwire.ReasonDockerMissing, agentwire.ReasonPathDenied, agentwire.ReasonPathUnwritable, agentwire.ReasonNotSelfUpdatable, agentwire.ReasonSudoMissing, agentwire.ReasonProducerMissing, agentwire.ReasonSudoDenied} {
		text := machine.FormatUnavailable(map[string]string{machine.CapabilityDockerDeploy: reason})
		if _, err := machine.ParseUnavailable(text); err != nil {
			t.Errorf("%s: the server cannot read %q: %v", reason, text, err)
		}
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

// produceBench est une machine de test pour backup.produce : ce que lookPath et
// sudoList répondent, et ce qu'on leur a demandé. Aucune commande ne se lance.
type produceBench struct {
	missing  map[string]bool
	sudoErr  error
	looked   []string
	listed   []string
	sudoRuns int
}

func (b *produceBench) checks() hostChecks {
	checks := allowAll()
	checks.lookPath = func(name string) (string, error) {
		b.looked = append(b.looked, name)
		if b.missing[name] {
			return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
		}
		return name, nil
	}
	checks.sudoList = func(_ context.Context, program string) error {
		b.sudoRuns++
		b.listed = append(b.listed, program)
		return b.sudoErr
	}
	return checks
}

// Chaque manque a sa raison, dans l'ordre où l'agent les pose : sudo, puis les
// deux programmes, puis la règle. Tout est là : rien n'est dit.
func TestJudgeRights_ProduceSaysWhatIsMissingInTheOrderItAsks(t *testing.T) {
	cases := map[string]struct {
		missing map[string]bool
		sudoErr error
		want    string
	}{
		"everything is there": {nil, nil, ""},
		"no sudo":             {map[string]bool{"sudo": true}, nil, agentwire.ReasonSudoMissing},
		"no wrapper":          {map[string]bool{brand.VzdumpWrapperPath: true}, nil, agentwire.ReasonProducerMissing},
		"no vzdump":           {map[string]bool{machine.VzdumpBinary: true}, nil, agentwire.ReasonProducerMissing},
		"no rule":             {nil, errors.New("exit status 1"), agentwire.ReasonSudoDenied},
		// Sans sudo, la règle ne se pose même pas la question ; sans script, non plus.
		"no sudo and no rule": {map[string]bool{"sudo": true}, errors.New("exit status 1"), agentwire.ReasonSudoMissing},
		"no wrapper, no rule": {map[string]bool{brand.VzdumpWrapperPath: true}, errors.New("exit status 1"), agentwire.ReasonProducerMissing},
	}
	for label, c := range cases {
		t.Run(label, func(t *testing.T) {
			bench := &produceBench{missing: c.missing, sudoErr: c.sudoErr}
			got := reasonsOf(t, "backup.produce=proxmox-vzdump", bench.checks())
			if got["backup.produce"] != c.want {
				t.Errorf("reported %v, want %q", got, c.want)
			}
			if c.want == "" && len(got) != 0 {
				t.Errorf("an agent that can produce reported %v", got)
			}
			if c.want == agentwire.ReasonSudoDenied && !slices.Equal(bench.listed, []string{brand.VzdumpWrapperPath}) {
				t.Errorf("sudo was asked about %v, want the wrapper alone", bench.listed)
			}
			if (c.want == agentwire.ReasonSudoMissing || c.want == agentwire.ReasonProducerMissing) && bench.sudoRuns != 0 {
				t.Error("sudo was asked a question although a program is missing")
			}
		})
	}
}

// Le droit ne pose de question qu'à lui-même : ni Docker ni les dossiers, et un
// agent sans le droit n'est pas interrogé sur sudo.
func TestJudgeRights_ProduceAsksNothingElseAndOnlyWhenGranted(t *testing.T) {
	bench := &produceBench{}
	checks := bench.checks()
	checks.docker = func(context.Context) string { return agentwire.ReasonDockerDenied }
	checks.writable = func(string) string { return agentwire.ReasonPathDenied }
	if got := reasonsOf(t, "backup.produce=proxmox-vzdump", checks); len(got) != 0 {
		t.Errorf("produce reported %v although only Docker and the folders are refused", got)
	}
	bench = &produceBench{missing: map[string]bool{"sudo": true}, sudoErr: errors.New("no")}
	if got := reasonsOf(t, "agent.update,backup.receive=/srv/a", bench.checks()); len(got) != 0 || len(bench.looked) != 0 || bench.sudoRuns != 0 {
		t.Errorf("an agent without the right was asked about sudo: %v %v", got, bench.looked)
	}
}

// Un état mal gardé ne laisse que observe : le droit n'est pas jugé non plus.
func TestJudgeRights_ProduceIsNotJudgedByANarrowedAgent(t *testing.T) {
	capabilities, err := machine.ParseCapabilities("backup.produce=proxmox-vzdump")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := NewIdentity("http://127.0.0.1:1", "", "fr", capabilities)
	if err != nil {
		t.Fatal(err)
	}
	identity.narrowed = true
	bench := &produceBench{missing: map[string]bool{"sudo": true}}
	if got := judgeRights(context.Background(), identity, bench.checks()); len(got) != 0 || len(bench.looked) != 0 {
		t.Errorf("a narrowed agent reported %v", got)
	}
}

// Ce que l'agent dit part dans l'en-tête et le serveur le relit.
func TestJudgeRights_ProduceReasonsTravelInTheHeader(t *testing.T) {
	bench := &produceBench{sudoErr: errors.New("exit status 1")}
	got := reasonsOf(t, "backup.receive=/srv/a,backup.produce=proxmox-vzdump", bench.checks())
	parsed, err := machine.ParseUnavailable(machine.FormatUnavailable(got))
	if err != nil || parsed["backup.produce"] != agentwire.ReasonSudoDenied {
		t.Fatalf("the server reads %v %v from %v", parsed, err, got)
	}
}

// sudoListsProgram lance « sudo -n -l <script> » avec un environnement
// remplacé, et ne garde que le code de sortie. Le sudo du test est un témoin
// sur un PATH de test : le vrai sudo ne se lance jamais.
func TestSudoListsProgram_RunsSudoNonInteractivelyAndReadsTheExitCode(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	for _, exitCode := range []int{0, 1} {
		stubs := t.TempDir()
		record := filepath.Join(stubs, "record")
		witness := "#!" + shell + "\nprintf '%s\\n' \"$@\" > " + record + "\nenv >> " + record + "\nexit " + fmt.Sprint(exitCode) + "\n"
		if err := os.WriteFile(filepath.Join(stubs, "sudo"), []byte(witness), 0o700); err != nil { // #nosec G306 -- un témoin exécutable dans un dossier de test.
			t.Fatal(err)
		}
		t.Setenv("PATH", stubs)
		t.Setenv("OPENCLOUD_TEST_SECRET", "leak")
		err := sudoListsProgram(context.Background(), brand.VzdumpWrapperPath)
		if (err == nil) != (exitCode == 0) {
			t.Errorf("exit %d gave %v", exitCode, err)
		}
		recorded, readErr := os.ReadFile(record) // #nosec G304 G703 -- le fichier du témoin, dans le dossier du test.
		if readErr != nil {
			t.Fatal(readErr)
		}
		lines := strings.Split(strings.TrimSpace(string(recorded)), "\n")
		if !slices.Equal(lines[:3], []string{"-n", "-l", brand.VzdumpWrapperPath}) {
			t.Errorf("sudo was called with %q", lines[:3])
		}
		if strings.Contains(string(recorded), "OPENCLOUD_TEST_SECRET") {
			t.Error("the environment of the agent reached sudo")
		}
	}
}

// Un producteur que l'agent ne connaît pas n'est jamais dit disponible, et rien
// ne se lance pour lui : la liste fermée l'empêche d'arriver jusque-là, le
// jugement ferme quand même.
func TestProducerReason_AnUnknownProducerIsMissingWithoutAsking(t *testing.T) {
	bench := &produceBench{}
	for _, producer := range []string{"", "borg", "proxmox-vzdump;id", "/usr/bin/vzdump"} {
		if got := producerReason(context.Background(), bench.checks(), producer); got != agentwire.ReasonProducerMissing {
			t.Errorf("%q judged %q", producer, got)
		}
	}
	if len(bench.looked) != 0 || bench.sudoRuns != 0 {
		t.Errorf("an unknown producer asked %v and ran sudo %d times", bench.looked, bench.sudoRuns)
	}
}

// Sans sudo sur le PATH, la question ne se pose pas : une erreur, pas un lancement.
func TestSudoListsProgram_NoSudoOnThePathIsAnError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := sudoListsProgram(context.Background(), brand.VzdumpWrapperPath); err == nil {
		t.Fatal("sudo found in an empty directory")
	}
}

// Les vérifications de la vraie machine passent par exec.LookPath : sans sudo
// sur le PATH, le droit est « sudo_missing » sans rien lancer.
func TestNewHostChecks_ProduceFindsNoSudoOnAnEmptyPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	checks := newHostChecks("/nonexistent.sock", nil)
	if got := producerReason(context.Background(), checks, machine.ProducerProxmoxVzdump); got != agentwire.ReasonSudoMissing {
		t.Errorf("judged %q", got)
	}
}
