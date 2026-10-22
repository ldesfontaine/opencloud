package agent

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// lockedBuffer reçoit le journal de l'agent, écrit depuis ses goroutines.
type lockedBuffer struct {
	mu   sync.Mutex
	text strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text.String()
}

// node est une entrée de la chaîne factice : son propriétaire, son mode, et
// la cible d'un lien.
type node struct {
	uid    uint32
	mode   fs.FileMode
	target string
}

// fakeFS rend un stat et un readlink sur une chaîne qu'on écrit à la main :
// les tests de la règle ne dépendent ni du compte qui les lance ni du disque.
func fakeFS(chain map[string]node) (func(string) (uint32, fs.FileMode, error), func(string) (string, error)) {
	stat := func(path string) (uint32, fs.FileMode, error) {
		found, ok := chain[path]
		if !ok {
			return 0, 0, errors.New("stat " + path + ": no such file or directory")
		}
		return found.uid, found.mode, nil
	}
	readlink := func(path string) (string, error) {
		found, ok := chain[path]
		if !ok || found.mode&fs.ModeSymlink == 0 {
			return "", errors.New("readlink " + path + ": invalid argument")
		}
		return found.target, nil
	}
	return stat, readlink
}

// check juge path sur la chaîne factice.
func check(path string, chain map[string]node, euid uint32) (string, error) {
	stat, readlink := fakeFS(chain)
	return checkChain(path, stat, readlink, euid)
}

const (
	dir0755 = fs.ModeDir | 0o755
	dir0700 = fs.ModeDir | 0o700
	link    = fs.ModeSymlink | 0o777
)

// safeChain est la disposition voulue : tout à root, l'état en 0700.
func safeChain() map[string]node {
	return map[string]node{
		"/":                        {0, dir0755, ""},
		"/var":                     {0, dir0755, ""},
		"/var/lib":                 {0, dir0755, ""},
		"/var/lib/opencloud-agent": {0, dir0700, ""},
		"/var/lib/opencloud":       {999, dir0755, ""},
		"/var/lib/opencloud/agent": {0, dir0700, ""},
		"/tmp":                     {0, fs.ModeDir | fs.ModeSticky | 0o777, ""},
		"/tmp/state":               {1000, dir0700, ""},
		"/home":                    {0, dir0755, ""},
		"/home/agent":              {1000, dir0755, ""},
		"/home/agent/state":        {1000, dir0700, ""},
		"/srv":                     {0, dir0755, ""},
		"/srv/state":               {0, dir0700, ""},
	}
}

func requireUnsafe(t *testing.T, err error, wantPath string, wantUID uint32) {
	t.Helper()
	var unsafe *UnsafeStateError
	if !errors.As(err, &unsafe) {
		t.Fatalf("want an UnsafeStateError on %s, got %v", wantPath, err)
	}
	if unsafe.Path != wantPath || unsafe.UID != wantUID {
		t.Fatalf("refused %s (uid %d), want %s (uid %d)", unsafe.Path, unsafe.UID, wantPath, wantUID)
	}
}

func requireSafe(t *testing.T, path string, chain map[string]node, euid uint32, wantResolved string) {
	t.Helper()
	resolved, err := check(path, chain, euid)
	if err != nil || resolved != wantResolved {
		t.Fatalf("%s: resolved %q, %v; want %s", path, resolved, err, wantResolved)
	}
}

func TestCheckChain_RootOwnedStateUnderRootDirsIsAccepted(t *testing.T) {
	requireSafe(t, "/var/lib/opencloud-agent", safeChain(), 0, "/var/lib/opencloud-agent")
}

// Le cas qui a fait le chantier : l'état est à root, mais le dossier du
// serveur au-dessus appartient à son compte, qui peut y renommer un dossier.
func TestCheckChain_ParentOwnedByAnotherAccountIsRefusedAndNamed(t *testing.T) {
	_, err := check("/var/lib/opencloud/agent", safeChain(), 0)
	requireUnsafe(t, err, "/var/lib/opencloud", 999)
	if message := err.Error(); !strings.Contains(message, "/var/lib/opencloud is owned by uid 999") {
		t.Fatalf("message %q", message)
	}
}

func TestCheckChain_ParentWritableByGroupOrOthersIsRefused(t *testing.T) {
	for name, mode := range map[string]fs.FileMode{
		"group writable":  dir0755 | 0o020,
		"others writable": dir0755 | 0o002,
		"world writable":  fs.ModeDir | 0o777,
	} {
		t.Run(name, func(t *testing.T) {
			chain := safeChain()
			chain["/var/lib"] = node{0, mode, ""}
			_, err := check("/var/lib/opencloud-agent", chain, 0)
			requireUnsafe(t, err, "/var/lib", 0)
		})
	}
}

func TestCheckChain_StickyParentIsAccepted_ButNotTheStateDirItself(t *testing.T) {
	// /tmp en 1777 : personne n'y renomme le dossier d'un autre.
	requireSafe(t, "/tmp/state", safeChain(), 1000, "/tmp/state")
	// Un dossier d'état 1777 laisserait d'autres comptes y poser des fichiers.
	chain := safeChain()
	chain["/tmp/state"] = node{1000, fs.ModeDir | fs.ModeSticky | 0o777, ""}
	_, err := check("/tmp/state", chain, 1000)
	requireUnsafe(t, err, "/tmp/state", 1000)
	// Pareil quand l'arrivée n'est atteinte que par « .. ».
	_, err = check("/tmp/state/..", safeChain(), 1000)
	requireUnsafe(t, err, "/tmp", 0)
}

// Le bit collant ne remplace pas le propriétaire : un /tmp à un autre compte
// est refusé.
func TestCheckChain_StickyParentOfAnotherAccountIsStillRefused(t *testing.T) {
	chain := safeChain()
	chain["/tmp"] = node{999, fs.ModeDir | fs.ModeSticky | 0o777, ""}
	_, err := check("/tmp/state", chain, 1000)
	requireUnsafe(t, err, "/tmp", 999)
}

func TestCheckChain_StateOwnedByTheAgentAccountUnderRootDirsIsAccepted(t *testing.T) {
	requireSafe(t, "/home/agent/state", safeChain(), 1000, "/home/agent/state")
	// Le même état, vu par un agent d'un autre compte : refusé dès le
	// premier dossier qui n'est pas à lui.
	_, err := check("/home/agent/state", safeChain(), 1001)
	requireUnsafe(t, err, "/home/agent", 1000)
}

func TestCheckChain_StateDirWithGroupAccessIsRefused(t *testing.T) {
	chain := safeChain()
	chain["/var/lib/opencloud-agent"] = node{0, fs.ModeDir | 0o770, ""}
	_, err := check("/var/lib/opencloud-agent", chain, 0)
	requireUnsafe(t, err, "/var/lib/opencloud-agent", 0)
}

func TestCheckChain_StatFailureOnAParentIsARefusal(t *testing.T) {
	chain := safeChain()
	delete(chain, "/var/lib")
	_, err := check("/var/lib/opencloud-agent", chain, 0)
	var unsafe *UnsafeStateError
	if err == nil || errors.As(err, &unsafe) || !strings.Contains(err.Error(), "/var/lib") {
		t.Fatalf("got %v", err)
	}
}

func TestCheckChain_RelativePathIsRefused(t *testing.T) {
	for _, path := range []string{"var/lib/opencloud-agent", "./state", ""} {
		if _, err := check(path, safeChain(), 0); err == nil {
			t.Fatalf("%q accepted", path)
		}
	}
	// Un chemin absolu mais pas propre se suit comme le noyau le suit.
	requireSafe(t, "/var/./lib//opencloud-agent/", safeChain(), 0, "/var/lib/opencloud-agent")
}

// Un lien symbolique a toujours le mode 0777 : seul son propriétaire compte,
// sa cible est suivie et jugée.
func TestCheckChain_SymlinkIsJudgedByItsOwnerAndFollowed(t *testing.T) {
	chain := safeChain()
	chain["/var/lib/opencloud-agent"] = node{0, link, "/srv/state"}
	requireSafe(t, "/var/lib/opencloud-agent", chain, 0, "/srv/state")
	chain["/var/lib/opencloud-agent"] = node{999, link, "/srv/state"}
	_, err := check("/var/lib/opencloud-agent", chain, 0)
	requireUnsafe(t, err, "/var/lib/opencloud-agent", 999)
	// Un lien relatif part du dossier qui le contient.
	chain["/var/lib/opencloud-agent"] = node{0, link, "../../srv/state"}
	requireSafe(t, "/var/lib/opencloud-agent", chain, 0, "/srv/state")
}

// Un lien sûr vers un état rangé sous le dossier d'un autre compte : le
// chemin donné est irréprochable, pas la cible.
func TestCheckChain_TargetUnderAnotherAccountIsRefused(t *testing.T) {
	chain := safeChain()
	chain["/var/lib/opencloud-agent"] = node{0, link, "/var/lib/opencloud/agent"}
	_, err := check("/var/lib/opencloud-agent", chain, 0)
	requireUnsafe(t, err, "/var/lib/opencloud", 999)
}

// Le trou d'un jugement « chemin donné + chemin résolu » : un dossier qu'on
// ne traverse qu'entre deux liens. Son propriétaire choisit où mène le
// second ; aujourd'hui vers un état sûr, demain vers le sien.
func TestCheckChain_DirCrossedBetweenTwoLinksIsJudged(t *testing.T) {
	chain := safeChain()
	chain["/var/lib/opencloud-agent"] = node{0, link, "/var/lib/opencloud/hop/state"}
	chain["/var/lib/opencloud/hop"] = node{999, link, "/srv"}
	_, err := check("/var/lib/opencloud-agent", chain, 0)
	requireUnsafe(t, err, "/var/lib/opencloud", 999)
}

// « .. » après un lien remonte depuis sa cible, comme le noyau ; un
// nettoyage à la lettre jugerait un autre dossier que celui ouvert.
func TestCheckChain_DotDotAfterALinkClimbsFromItsTarget(t *testing.T) {
	chain := safeChain()
	chain["/var/lib/up"] = node{0, link, "/var/lib/opencloud/agent"}
	chain["/var/lib/agent"] = node{0, dir0700, ""}
	_, err := check("/var/lib/up/../agent", chain, 0)
	requireUnsafe(t, err, "/var/lib/opencloud", 999)
}

func TestCheckChain_LinkLoopIsARefusal(t *testing.T) {
	chain := safeChain()
	chain["/srv/a"] = node{0, link, "b"}
	chain["/srv/b"] = node{0, link, "a"}
	if _, err := check("/srv/a", chain, 0); err == nil || !strings.Contains(err.Error(), "too many links") {
		t.Fatalf("got %v", err)
	}
}

// Sur disque, sans root : des dossiers du compte qui lance le test, dont on
// joue seulement le mode. Ce qui est au-dessus de t.TempDir() (le /tmp de la
// machine) ne regarde pas ces tests : ils ne jugent que l'endroit refusé.
func mkdir(t *testing.T, path string, mode fs.FileMode) string {
	t.Helper()
	if err := os.Mkdir(path, mode); err != nil {
		t.Fatal(err)
	}
	// L'umask a pu retirer des bits : le mode se pose après coup.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// base rend un dossier de test 0700, chemin résolu.
func base(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(resolved, 0o700); err != nil {
		t.Fatal(err)
	}
	return resolved
}

// refusedAt rend le chemin que l'erreur nomme, "" sans erreur.
func refusedAt(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	var unsafe *UnsafeStateError
	if !errors.As(err, &unsafe) {
		t.Fatalf("not an UnsafeStateError: %v", err)
	}
	return unsafe.Path
}

// insideBase : l'erreur, s'il y en a une, ne vient pas d'un dossier fait ici.
func insideBase(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// checkOpened ouvre l'état comme la commande, puis le juge.
func checkOpened(t *testing.T, path string) error {
	t.Helper()
	opened, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	return CheckStateDir(opened, path)
}

func TestCheckStateDir_OnDisk(t *testing.T) {
	root := base(t)
	safe := mkdir(t, filepath.Join(root, "safe"), 0o700)
	if refused := refusedAt(t, checkOpened(t, safe)); insideBase(refused, root) {
		t.Fatalf("a private dir under a private dir is refused at %s", refused)
	}
	open := mkdir(t, filepath.Join(root, "open"), 0o777)
	inOpen := mkdir(t, filepath.Join(open, "state"), 0o700)
	if refused := refusedAt(t, checkOpened(t, inOpen)); refused != open {
		t.Fatalf("state under a 0777 dir: refused at %q, want %s", refused, open)
	}
	shared := mkdir(t, filepath.Join(root, "shared"), 0o770)
	if refused := refusedAt(t, checkOpened(t, shared)); refused != shared {
		t.Fatalf("state with group write: refused at %q, want %s", refused, shared)
	}
	opened, err := os.OpenRoot(safe)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if err := CheckStateDir(opened, filepath.Join(root, "missing")); err == nil {
		t.Fatal("a missing state dir is not safe")
	}
}

// Un chemin relatif se juge comme l'absolu qu'il désigne.
func TestCheckStateDir_RelativePathIsMadeAbsolute(t *testing.T) {
	root := base(t)
	open := mkdir(t, filepath.Join(root, "open"), 0o777)
	mkdir(t, filepath.Join(open, "state"), 0o700)
	mkdir(t, filepath.Join(root, "safe"), 0o700)
	t.Chdir(root)
	if refused := refusedAt(t, checkOpened(t, "open/state")); refused != open {
		t.Fatalf("refused at %q, want %s", refused, open)
	}
	if refused := refusedAt(t, checkOpened(t, "safe")); insideBase(refused, root) {
		t.Fatalf("refused at %s", refused)
	}
}

// Le chemin donné et le chemin résolu sont jugés tous les deux.
func TestCheckStateDir_ResolvedPathIsJudgedToo(t *testing.T) {
	root := base(t)
	private := mkdir(t, filepath.Join(root, "private"), 0o700)
	open := mkdir(t, filepath.Join(root, "open"), 0o777)

	// Donné : un lien rangé dans un dossier 0777, vers un dossier privé. La
	// cible est sûre, mais celui qui peut écrire dans open/ remplace le lien.
	if err := os.Symlink(private, filepath.Join(open, "state")); err != nil {
		t.Fatal(err)
	}
	if refused := refusedAt(t, checkOpened(t, filepath.Join(open, "state"))); refused != open {
		t.Fatalf("given path: refused at %q, want %s", refused, open)
	}

	// Résolu : un lien sûr (dans un dossier privé) vers un état rangé dans un
	// dossier 0777. Le chemin donné est irréprochable, la cible ne l'est pas.
	inOpen := mkdir(t, filepath.Join(open, "real"), 0o700)
	link := filepath.Join(root, "link")
	if err := os.Symlink(inOpen, link); err != nil {
		t.Fatal(err)
	}
	if refused := refusedAt(t, checkOpened(t, link)); refused != open {
		t.Fatalf("resolved path: refused at %q, want %s", refused, open)
	}
}

// Ouvert, puis remplacé avant d'être jugé : le chemin désigne maintenant un
// dossier sûr, mais l'agent garderait celui qu'il a ouvert.
func TestCheckStateDir_SwappedAfterOpeningIsRefused(t *testing.T) {
	root := base(t)
	state := mkdir(t, filepath.Join(root, "state"), 0o700)
	opened, err := os.OpenRoot(state)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if refused := refusedAt(t, CheckStateDir(opened, state)); insideBase(refused, root) {
		t.Fatalf("before the swap: refused at %s", refused)
	}
	if err := os.Rename(state, filepath.Join(root, "planted")); err != nil {
		t.Fatal(err)
	}
	mkdir(t, state, 0o700)
	err = CheckStateDir(opened, state)
	if err == nil || !strings.Contains(err.Error(), "changed between its opening and its check") {
		t.Fatalf("swapped state: %v", err)
	}
}

// Un dossier sûr ne suffit pas : identity.json, qui porte les droits, est
// jugé aussi (un état déplacé à la main garde les fichiers de qui l'avait
// remplacé).
func TestCheckStateDir_IdentityFileIsJudged(t *testing.T) {
	root := base(t)
	state := mkdir(t, filepath.Join(root, "state"), 0o700)
	identity := filepath.Join(state, identityFile)
	if err := os.WriteFile(identity, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if refused := refusedAt(t, checkOpened(t, state)); insideBase(refused, root) {
		t.Fatalf("a private identity is refused at %s", refused)
	}
	if err := os.Chmod(identity, 0o620); err != nil {
		t.Fatal(err)
	}
	if refused := refusedAt(t, checkOpened(t, state)); refused != identity {
		t.Fatalf("group-writable identity: refused at %q, want %s", refused, identity)
	}
	if err := os.Remove(identity); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "elsewhere.json"), identity); err != nil {
		t.Fatal(err)
	}
	if refused := refusedAt(t, checkOpened(t, state)); refused != identity {
		t.Fatalf("identity as a link: refused at %q, want %s", refused, identity)
	}
}

// Lancé avec un état mal gardé, l'agent rapporte observe seul et refuse un
// ordre qui écrit, mais n'abîme pas le fichier : l'état corrigé, un
// redémarrage rend les droits.
func TestRun_UnsafeStateKeepsObserveOnly(t *testing.T) {
	b := newBench(t)
	rights, err := machine.ParseCapabilities("docker.deploy")
	if err != nil {
		t.Fatal(err)
	}
	token, _, _ := b.machines.CreateToken(context.Background(), "vps-brest-1", rights)
	enrolling := b.options(token)
	enrolling.Rights = rights
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, enrolling) }()
	granted := b.waitFor(t, "vps-brest-1", func(s machine.Status) bool {
		return s.Online && machine.HasCapability(s.Agent.Capabilities, machine.CapabilityDockerDeploy)
	})
	cancel()
	<-done
	b.waitFor(t, "vps-brest-1", func(s machine.Status) bool { return !s.Online })

	// Redémarré sur le même état, que la commande a jugé mal gardé.
	var logs lockedBuffer
	narrowed := b.options("")
	narrowed.UnsafeState = &UnsafeStateError{Path: "/var/lib/opencloud", UID: 999, Mode: dir0755}
	narrowed.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	go func() { done <- Run(ctx, narrowed) }()
	reported := b.waitFor(t, "vps-brest-1", func(s machine.Status) bool {
		return s.Online && !s.Agent.ReportedAt.IsZero() && !machine.HasCapability(s.Agent.Capabilities, machine.CapabilityDockerDeploy)
	})
	if machine.FormatCapabilities(reported.Agent.Capabilities) != "observe" || !reported.Agent.CapabilitiesDiffer() {
		t.Fatalf("reported %+v", reported.Agent)
	}
	if !strings.Contains(logs.String(), "state dir is not safe, writing rights are off") || !strings.Contains(logs.String(), "/var/lib/opencloud is owned by uid 999") {
		t.Fatalf("journal %q", logs.String())
	}
	// Le serveur, qui sait le droit absent, ne pousse pas l'ordre.
	operator := audit.WithActor(context.Background(), audit.Operator("lucas"))
	if _, err := b.actions.Send(operator, granted.ID, agentwire.KindDockerDeploy, nil); !errors.Is(err, action.ErrRightMissing) {
		t.Fatalf("deploy sent anyway: %v", err)
	}
	cancel()
	<-done

	identity, err := LoadIdentity(b.stateDir)
	if err != nil || strings.Join(identity.Rights, ",") != "observe,docker.deploy" {
		t.Fatalf("rights on disk %+v %v", identity.Rights, err)
	}
}

// Côté agent : même si un ordre signé arrivait, il est refusé right_missing,
// et le rang qu'il écrit ne touche pas aux droits du fichier.
func TestOrderRunner_UnsafeStateRefusesWritingOrdersAndKeepsRightsOnDisk(t *testing.T) {
	b := newOrderBenchWithRights(t, []string{"observe", "docker.deploy"})
	b.runner.identity.narrowed = true

	deploy := order(1)
	deploy.Kind = agentwire.KindDockerDeploy
	result := b.send(t, deploy, b.private)
	if result.State != agentwire.ActionRefused || result.Reason != agentwire.RightMissing(machine.CapabilityDockerDeploy) {
		t.Fatalf("deploy %+v", result)
	}
	// observe reste : un ping passe, et son rang est réécrit dans le fichier.
	if ping := b.send(t, order(2), b.private); ping.State != agentwire.ActionSucceeded {
		t.Fatalf("ping %+v", ping)
	}
	saved, err := LoadIdentity(b.stateDir)
	if err != nil || saved.LastOrderSeq != 2 || strings.Join(saved.Rights, ",") != "observe,docker.deploy" {
		t.Fatalf("saved %+v %v", saved, err)
	}
	if !b.runner.identity.narrowed || len(b.runner.identity.Capabilities()) != 1 {
		t.Fatalf("narrowing lost after a write: %+v", b.runner.identity.Capabilities())
	}
}
