package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
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
	for _, reason := range []string{agentwire.ReasonDockerDenied, agentwire.ReasonDockerMissing, agentwire.ReasonPathDenied, agentwire.ReasonPathUnwritable, agentwire.ReasonNotSelfUpdatable} {
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
